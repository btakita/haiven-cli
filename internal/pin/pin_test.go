package pin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type fakeFetcher struct {
	calls int
	err   error
}

func (f *fakeFetcher) Fetch(_ context.Context, v, goos, goarch, dest string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dest, []byte("binary "+v+" "+goos+"/"+goarch), 0o755)
}

func resolver(t *testing.T, running string, f Fetcher) Resolver {
	t.Helper()
	return Resolver{Cache: Cache{Dir: t.TempDir()}, Fetcher: f, GOOS: "linux", GOARCH: "amd64", Running: running}
}

func TestResolveServesItself(t *testing.T) {
	cases := []struct {
		name, running, pinned, active string
		want                          Reason
	}{
		{"unpinned", "1.0.0", "", "", ReasonUnpinned},
		{"matches", "1.0.0", "1.0.0", "", ReasonMatches},
		{"already re-executed", "1.0.0", "2.0.0", "2.0.0", ReasonActive},
		{"dev build", "0.0.0-dev", "2.0.0", "", ReasonDevBuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFetcher{}
			d, err := resolver(t, tc.running, f).Resolve(context.Background(), tc.pinned, tc.active)
			if err != nil {
				t.Fatal(err)
			}
			if d.Reexec || d.Reason != tc.want || f.calls != 0 {
				t.Fatalf("got %+v calls=%d, want reason %q and no fetch", d, f.calls, tc.want)
			}
		})
	}
}

func TestResolveFetchesOnceThenUsesCache(t *testing.T) {
	f := &fakeFetcher{}
	r := resolver(t, "1.0.0", f)
	for i := 0; i < 2; i++ {
		d, err := r.Resolve(context.Background(), "2.0.0", "")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(r.Cache.Dir, "versions", "2.0.0", "linux-amd64", "haiven")
		if !d.Reexec || d.Binary != want || d.Reason != ReasonPinned {
			t.Fatalf("got %+v, want re-exec of %s", d, want)
		}
	}
	if f.calls != 1 {
		t.Fatalf("fetched %d times, want 1", f.calls)
	}
}

func TestResolvePropagatesFetchError(t *testing.T) {
	f := &fakeFetcher{err: fmt.Errorf("x: %w", ErrChecksumMismatch)}
	_, err := resolver(t, "1.0.0", f).Resolve(context.Background(), "2.0.0", "")
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestCachePathRejectsInvalidVersion(t *testing.T) {
	if _, err := (Cache{Dir: "c"}).Path("../../etc", "linux", "amd64"); err == nil {
		t.Fatal("a version with path syntax must be rejected")
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("1.2.3", "linux", "arm64"); got != "haiven_1.2.3_linux_arm64.tar.gz" {
		t.Fatal(got)
	}
	if got := AssetName("1.2.3", "windows", "amd64"); got != "haiven_1.2.3_windows_amd64.zip" {
		t.Fatal(got)
	}
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// A decoy with path syntax must never be written anywhere.
	for _, f := range []struct {
		name string
		body []byte
	}{{"../README.md", []byte("readme")}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipped(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves one release; corrupt flips a byte of the archive
// after its checksum is computed.
func releaseServer(t *testing.T, v, goos, goarch string, archive []byte, corrupt bool) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(archive)
	asset := AssetName(v, goos, goarch)
	served := append([]byte(nil), archive...)
	if corrupt {
		served[len(served)/2] ^= 0xff
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+v+"/"+ChecksumsAsset, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", hex.EncodeToString(sum[:]), asset, hex.EncodeToString(make([]byte, 32)))
	})
	mux.HandleFunc("/v"+v+"/"+asset, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(served) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestReleaseFetcher(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			body := []byte("#!haiven " + goos)
			archive := tarGz(t, "haiven_1.2.3/"+BinaryName(goos), body)
			if goos == "windows" {
				archive = zipped(t, "haiven_1.2.3/"+BinaryName(goos), body)
			}
			srv := releaseServer(t, "1.2.3", goos, "amd64", archive, false)
			dest := filepath.Join(t.TempDir(), BinaryName(goos))
			if err := (ReleaseFetcher{Base: srv.URL}).Fetch(context.Background(), "1.2.3", goos, "amd64", dest); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("got %q, %v", got, err)
			}
			if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 1 {
				t.Fatalf("download left %d entries, want only the binary", len(entries))
			}
		})
	}
}

func TestReleaseFetcherRejectsChecksumMismatch(t *testing.T) {
	archive := tarGz(t, "haiven", []byte("binary"))
	srv := releaseServer(t, "1.2.3", "linux", "amd64", archive, true)
	dest := filepath.Join(t.TempDir(), "haiven")
	err := (ReleaseFetcher{Base: srv.URL}).Fetch(context.Background(), "1.2.3", "linux", "amd64", dest)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want checksum mismatch", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a mismatched download must not be written")
	}
}

func TestReleaseFetcherMissingRelease(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	err := (ReleaseFetcher{Base: srv.URL}).Fetch(context.Background(), "9.9.9", "linux", "amd64", filepath.Join(t.TempDir(), "haiven"))
	if err == nil || errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want an unavailable error", err)
	}
}

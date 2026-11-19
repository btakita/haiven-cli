package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/haiven-dev/haiven-cli/internal/pin"
)

const testVersion = "1.2.3"

var fakeNotices = []archiveFile{
	{name: "LICENSE", data: []byte("license fixture"), mode: 0o644},
	{name: "NOTICE", data: []byte("notice fixture"), mode: 0o644},
	{name: "THIRD_PARTY_NOTICES.txt", data: []byte("third-party notice fixture"), mode: 0o644},
}

func fakeBinaries() map[Target][]byte {
	bins := make(map[Target][]byte, len(Targets))
	for _, t := range Targets {
		bins[t] = []byte("haiven " + t.GOOS + "/" + t.GOARCH)
	}
	return bins
}

// release packages fake binaries and serves them in the GitHub releases
// layout (<base>/v<version>/<asset>) that pin and the install scripts use.
func release(t *testing.T) (dir, base string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "v"+testVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(testVersion, fakeBinaries(), fakeNotices, defaultModTime, dir); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)
	return dir, srv.URL
}

func TestPackageAssetsAndChecksums(t *testing.T) {
	dir, _ := release(t)
	f, err := os.Open(filepath.Join(dir, pin.ChecksumsAsset))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var listed []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("checksums.txt line %q is not \"<hex>  <name>\"", sc.Text())
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s: checksum does not match the archive", name)
		}
		listed = append(listed, name)
	}
	var want []string
	for _, tg := range Targets {
		want = append(want, pin.AssetName(testVersion, tg.GOOS, tg.GOARCH))
	}
	if len(listed) != len(want) {
		t.Fatalf("checksums.txt lists %v, want %v", listed, want)
	}
	for i := 1; i < len(listed); i++ {
		if listed[i-1] >= listed[i] {
			t.Fatalf("checksums.txt is not sorted: %v", listed)
		}
	}
	for _, target := range Targets {
		name := pin.AssetName(testVersion, target.GOOS, target.GOARCH)
		if !contains(listed, name) {
			t.Errorf("checksums.txt lists no %s", name)
		}
		files := readArchive(t, filepath.Join(dir, name), target.GOOS)
		if len(files) != 1+len(fakeNotices) || !bytes.Equal(files[pin.BinaryName(target.GOOS)], fakeBinaries()[target]) {
			t.Errorf("%s does not contain the expected binary and notice files", name)
		}
		for _, notice := range fakeNotices {
			if !bytes.Equal(files[notice.name], notice.data) {
				t.Errorf("%s contents differ in %s", notice.name, name)
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want)+1 {
		t.Errorf("release dir holds %d files, want %d archives + checksums.txt", len(entries), len(want))
	}
}

func readArchive(t *testing.T, path, goos string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if goos == "windows" {
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Close() }()
		for _, file := range reader.File {
			data, err := fs.ReadFile(reader, file.Name)
			if err != nil {
				t.Fatal(err)
			}
			files[file.Name] = data
		}
		return files
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = data
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The version pin downloads these exact archives for every platform.
func TestPinFetchesEveryTarget(t *testing.T) {
	_, base := release(t)
	bins := fakeBinaries()
	for _, tg := range Targets {
		dest := filepath.Join(t.TempDir(), pin.BinaryName(tg.GOOS))
		err := pin.ReleaseFetcher{Base: base}.Fetch(context.Background(), testVersion, tg.GOOS, tg.GOARCH, dest)
		if err != nil {
			t.Fatalf("%s/%s: %v", tg.GOOS, tg.GOARCH, err)
		}
		got, err := os.ReadFile(dest)
		if err != nil || !bytes.Equal(got, bins[tg]) {
			t.Fatalf("%s/%s: fetched %q, %v", tg.GOOS, tg.GOARCH, got, err)
		}
	}
}

// install.sh (Linux and macOS) installs the host archive. install.ps1 shares
// the layout; scripts/install_test.go runs it on Windows.
func TestInstallScriptInstallsPackagedArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for Linux and macOS")
	}
	host := Target{runtime.GOOS, runtime.GOARCH}
	if !contains(targetNames(), host.GOOS+"/"+host.GOARCH) {
		t.Skipf("no release asset for %s/%s", host.GOOS, host.GOARCH)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	_, base := release(t)
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command(sh, filepath.Join("..", "..", "scripts", "install.sh"), "--version", testVersion, "--dir", dir)
	cmd.Env = append(os.Environ(), "HAIVEN_DOWNLOAD_BASE="+base)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "haiven"))
	if err != nil || !bytes.Equal(got, fakeBinaries()[host]) {
		t.Fatalf("installed %q, %v", got, err)
	}
}

func targetNames() []string {
	var names []string
	for _, t := range Targets {
		names = append(names, t.GOOS+"/"+t.GOARCH)
	}
	return names
}

func TestPackageIsReproducible(t *testing.T) {
	mtime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	a, b := t.TempDir(), t.TempDir()
	if _, err := Package(testVersion, fakeBinaries(), fakeNotices, mtime, a); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(testVersion, fakeBinaries(), fakeNotices, mtime, b); err != nil {
		t.Fatal(err)
	}
	x, _ := os.ReadFile(filepath.Join(a, pin.ChecksumsAsset))
	y, _ := os.ReadFile(filepath.Join(b, pin.ChecksumsAsset))
	if len(x) == 0 || !bytes.Equal(x, y) {
		t.Fatalf("two packagings of the same binaries differ:\n%s\n%s", x, y)
	}
}

func TestModTime(t *testing.T) {
	if got, _ := modTime(""); !got.Equal(defaultModTime) {
		t.Errorf("unset: %v", got)
	}
	if got, _ := modTime("1793491200"); !got.Equal(time.Unix(1793491200, 0)) {
		t.Errorf("epoch: %v", got)
	}
	if got, _ := modTime("0"); !got.Equal(defaultModTime) {
		t.Errorf("pre-1980 epoch: %v", got)
	}
	if _, err := modTime("soon"); err == nil {
		t.Error("a non-numeric SOURCE_DATE_EPOCH was accepted")
	}
}

func TestRejectsInvalidVersion(t *testing.T) {
	for _, v := range []string{"", "1.2", "v1.2.3/../x", "latest"} {
		if err := run([]string{"-version", v, "-out", t.TempDir()}, &bytes.Buffer{}); err == nil {
			t.Errorf("-version %q was accepted", v)
		}
	}
}

// A release build reports its stamped version and keeps checkout metadata
// out of the binary.
func TestBuildStampsVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	host := Target{runtime.GOOS, runtime.GOARCH}
	bin, err := build(testVersion, host, "../../cmd/haiven", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), pin.BinaryName(host.GOOS))
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs" || strings.HasPrefix(setting.Key, "vcs.") {
			t.Errorf("release binary embeds checkout metadata %s", setting.Key)
		}
	}
	out, err := exec.Command(path, "version", "--json").Output()
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var res struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.Data.Version != testVersion {
		t.Fatalf("version --json reported %q (%v):\n%s", res.Data.Version, err, out)
	}
}

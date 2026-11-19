package pin

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DefaultDownloadBase is where release assets are published. Asset URLs are
// <base>/v<version>/<asset>, the GitHub releases layout.
const DefaultDownloadBase = "https://github.com/haiven-dev/haiven-cli/releases/download"

// ChecksumsAsset is the sha256sum-format checksum file published with every
// release.
const ChecksumsAsset = "checksums.txt"

// maxBinary bounds a release binary, so a hostile or broken server cannot
// fill the disk.
const maxBinary = 256 << 20

// AssetName is the archive name for one platform. install.sh and install.ps1
// use the same pattern; release_test.go pins it.
func AssetName(v, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("haiven_%s_%s_%s%s", v, goos, goarch, ext)
}

// ReleaseFetcher downloads release archives over HTTPS and verifies them
// against the release's checksums.txt before extracting the binary.
type ReleaseFetcher struct {
	Base   string // defaults to DefaultDownloadBase
	Client *http.Client
}

// Fetch implements Fetcher.
func (f ReleaseFetcher) Fetch(ctx context.Context, v, goos, goarch, dest string) error {
	base := f.Base
	if base == "" {
		base = DefaultDownloadBase
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	asset := AssetName(v, goos, goarch)
	dir := strings.TrimRight(base, "/") + "/v" + v + "/"

	sums, err := get(ctx, client, dir+ChecksumsAsset, 1<<20)
	if err != nil {
		return err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	archive, err := get(ctx, client, dir+asset, maxBinary)
	if err != nil {
		return err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: %w", asset, ErrChecksumMismatch)
	}
	bin, err := extract(archive, goos)
	if err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	return writeAtomic(dest, bin)
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: response exceeds %d bytes", url, limit)
	}
	return body, nil
}

// checksumFor finds asset in a sha256sum-format file ("<hex>  <name>", an
// optional "*" binary marker before the name).
func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			sum := strings.ToLower(fields[0])
			if len(sum) != 64 {
				return "", fmt.Errorf("%s: malformed checksum for %s", ChecksumsAsset, asset)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s lists no checksum for %s", ChecksumsAsset, asset)
}

// extract returns the haiven binary from a release archive. Only the entry
// whose base name is the binary is read; no path from the archive is ever
// used on disk.
func extract(archive []byte, goos string) ([]byte, error) {
	name := BinaryName(goos)
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != name || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return readBounded(rc)
		}
		return nil, fmt.Errorf("archive has no %s", name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no %s", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
			return readBounded(tr)
		}
	}
}

func readBounded(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBinary {
		return nil, errors.New("binary exceeds size bound")
	}
	return b, nil
}

// writeAtomic writes an executable via a temp file in the same directory,
// so a concurrent reader sees either nothing or the whole binary.
func writeAtomic(dest string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".haiven-download-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}
	return os.Rename(name, dest)
}

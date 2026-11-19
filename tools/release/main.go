// Command release builds the haiven release assets: one archive per platform
// and the checksums.txt that scripts/install.sh, scripts/install.ps1 and the
// haiven.toml version pin (internal/pin) verify downloads against.
//
//	go run ./tools/release -version 1.2.3 -out dist/release
//
// The archive names, archive formats and checksum file come from internal/pin,
// so the published layout and the code that downloads it cannot drift apart.
// The release pipeline uploads the contents of -out as release assets.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haiven-dev/haiven-cli/internal/pin"
	"github.com/haiven-dev/haiven-cli/internal/version"
)

// Target is one release platform.
type Target struct{ GOOS, GOARCH string }

// Targets are the release platforms: the same six that install.sh,
// install.ps1 and the Taskfile cross task cover.
var Targets = []Target{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// versionVar is the variable a release build sets through -ldflags -X
// (internal/version, and the Taskfile's LDFLAGS).
const versionVar = "github.com/haiven-dev/haiven-cli/internal/version.Version"

// defaultModTime stamps archive entries when SOURCE_DATE_EPOCH is unset, so
// two builds of one commit produce byte-identical archives either way.
var defaultModTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, log io.Writer) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(log)
	v := fs.String("version", "", "release version, MAJOR.MINOR.PATCH (a leading v is dropped)")
	out := fs.String("out", filepath.Join("dist", "release"), "directory for the archives and checksums.txt")
	pkg := fs.String("pkg", "./cmd/haiven", "package to build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ver := strings.TrimPrefix(*v, "v")
	if !version.Valid(ver) {
		return fmt.Errorf("-version %q is not a semantic version", *v)
	}
	mtime, err := modTime(os.Getenv("SOURCE_DATE_EPOCH"))
	if err != nil {
		return err
	}
	notices, err := releaseNotices(*pkg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := clean(*out); err != nil {
		return err
	}
	bins, err := os.MkdirTemp("", "haiven-release-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(bins) }()

	binaries := make(map[Target][]byte, len(Targets))
	for _, t := range Targets {
		_, _ = fmt.Fprintf(log, "build %s/%s\n", t.GOOS, t.GOARCH)
		b, err := build(ver, t, *pkg, filepath.Join(bins, t.GOOS+"_"+t.GOARCH))
		if err != nil {
			return err
		}
		binaries[t] = b
	}
	assets, err := Package(ver, binaries, notices, mtime, *out)
	if err != nil {
		return err
	}
	for _, a := range assets {
		_, _ = fmt.Fprintf(log, "wrote %s\n", filepath.Join(*out, a))
	}
	return nil
}

// clean removes the assets of an earlier run from out, so an upload of out
// never carries another version's archives. Other files are left alone.
func clean(out string) error {
	stale, err := filepath.Glob(filepath.Join(out, "haiven_*"))
	if err != nil {
		return err
	}
	stale = append(stale, filepath.Join(out, pin.ChecksumsAsset))
	for _, f := range stale {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// modTime parses SOURCE_DATE_EPOCH (https://reproducible-builds.org/specs/source-date-epoch/).
func modTime(epoch string) (time.Time, error) {
	if epoch == "" {
		return defaultModTime, nil
	}
	n, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH %q: %w", epoch, err)
	}
	t := time.Unix(n, 0).UTC()
	// A zip entry cannot carry a time before 1980 (MS-DOS date).
	if t.Year() < 1980 {
		return defaultModTime, nil
	}
	return t, nil
}

// Ldflags are the linker flags of a release build: stripped, with the
// version stamped in.
func Ldflags(ver string) string {
	return "-s -w -X " + versionVar + "=" + ver
}

// build cross-compiles one static binary and returns its bytes.
func build(ver string, t Target, pkg, dir string) ([]byte, error) {
	dest := filepath.Join(dir, pin.BinaryName(t.GOOS))
	// The private build receipt records the producing checkout's Git revision.
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", Ldflags(ver), "-o", dest, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.GOOS, "GOARCH="+t.GOARCH)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build %s/%s: %w", t.GOOS, t.GOARCH, err)
	}
	return os.ReadFile(dest)
}

// Package writes one archive per binary into out, named by pin.AssetName and
// holding the binary and license files at its root, plus pin.ChecksumsAsset
// in sha256sum format ("<hex>  <name>", sorted by name). It returns the
// written file names, checksums.txt last.
func Package(ver string, binaries map[Target][]byte, notices []archiveFile, mtime time.Time, out string) ([]string, error) {
	if len(binaries) == 0 {
		return nil, errors.New("no binaries to package")
	}
	sums := make(map[string]string, len(binaries))
	for t, bin := range binaries {
		files := append([]archiveFile{{name: pin.BinaryName(t.GOOS), data: bin, mode: 0o755}}, notices...)
		var (
			data []byte
			err  error
		)
		if t.GOOS == "windows" {
			data, err = zipArchive(files, mtime)
		} else {
			data, err = tarGz(files, mtime)
		}
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", t.GOOS, t.GOARCH, err)
		}
		name := pin.AssetName(ver, t.GOOS, t.GOARCH)
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		sums[name] = hex.EncodeToString(sum[:])
	}
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	if err := os.WriteFile(filepath.Join(out, pin.ChecksumsAsset), []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return append(names, pin.ChecksumsAsset), nil
}

type archiveFile struct {
	name string
	data []byte
	mode os.FileMode
}

func tarGz(files []archiveFile, mtime time.Time) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gz.ModTime = mtime
	tw := tar.NewWriter(gz)
	for _, file := range files {
		hdr := &tar.Header{
			Name:     file.name,
			Mode:     int64(file.mode),
			Size:     int64(len(file.data)),
			ModTime:  mtime,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func zipArchive(files []archiveFile, mtime time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		hdr := &zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: mtime}
		hdr.SetMode(file.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

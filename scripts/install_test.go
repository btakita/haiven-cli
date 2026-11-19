// Package scripts_test runs the install scripts against a local release
// server: install.sh on Linux and macOS, install.ps1 on Windows.
package scripts_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/haiven-dev/haiven-cli/internal/pin"
)

const version = "1.2.3"

func archive(t *testing.T, goos string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	name := pin.BinaryName(goos)
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(body)
		}
		if err == nil {
			err = zw.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve publishes one release at /v<version>/ in the layout pin.AssetName
// and the scripts share. corrupt breaks the archive after checksumming.
func serve(t *testing.T, goos, goarch string, body []byte, corrupt bool) string {
	t.Helper()
	data := archive(t, goos, body)
	sum := sha256.Sum256(data)
	if corrupt {
		data = append([]byte(nil), data...)
		data[len(data)/2] ^= 0xff
	}
	asset := pin.AssetName(version, goos, goarch)
	mux := http.NewServeMux()
	mux.HandleFunc("/v"+version+"/"+pin.ChecksumsAsset, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
	})
	mux.HandleFunc("/v"+version+"/"+asset, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func installer(t *testing.T, base, dir string) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		shell, err := exec.LookPath("pwsh")
		if err != nil {
			shell, err = exec.LookPath("powershell")
		}
		if err != nil {
			t.Skip("no PowerShell on PATH")
		}
		cmd = exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", "install.ps1")
	} else {
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Skip("no sh on PATH")
		}
		cmd = exec.Command(sh, "install.sh", "--version", version)
	}
	cmd.Env = append(os.Environ(),
		"HAIVEN_VERSION="+version,
		"HAIVEN_DOWNLOAD_BASE="+base,
		"HAIVEN_INSTALL_DIR="+dir,
		"HAIVEN_NO_PATH=1",
	)
	return cmd
}

func goarch(t *testing.T) string {
	t.Helper()
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH
	}
	t.Skipf("no release asset for %s", runtime.GOARCH)
	return ""
}

func TestInstall(t *testing.T) {
	body := []byte("haiven test binary")
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := installer(t, serve(t, runtime.GOOS, goarch(t), body, false), dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, pin.BinaryName(runtime.GOOS)))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("installed %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "haiven"))
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("binary is not executable: %v", err)
		}
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := installer(t, serve(t, runtime.GOOS, goarch(t), []byte("x"), true), dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install succeeded on a corrupt archive:\n%s", out)
	}
	if !bytes.Contains(out, []byte("checksum mismatch")) {
		t.Fatalf("output does not name the checksum mismatch:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, pin.BinaryName(runtime.GOOS))); err == nil {
		t.Fatal("a corrupt archive was installed")
	}
}

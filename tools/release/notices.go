package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var noticeName = regexp.MustCompile(`(?i)^(license|licence|copying|notice|patents|copyright|unlicense)([.-].*)?$`)

type goModule struct {
	Path, Version, Dir string
	Main               bool
	Replace            *goModule
}

type goPackage struct {
	ImportPath, Dir string
	Standard        bool
	Module          *goModule
}

// releaseNotices records the union of the dependency graphs of the shipped
// binaries. Host-only build tools and test dependencies are outside that union.
func releaseNotices(pkg string) ([]archiveFile, error) {
	files := make([]archiveFile, 0, 3)
	for _, name := range []string{"LICENSE", "NOTICE"} {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		files = append(files, archiveFile{name: name, data: data, mode: 0o644})
	}
	cmd := exec.Command("go", "env", "-json", "GOROOT", "GOVERSION")
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read Go toolchain for notices: %w", err)
	}
	var toolchain struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(raw, &toolchain); err != nil {
		return nil, err
	}
	roots := map[string]string{"Go toolchain " + toolchain.GOVERSION: toolchain.GOROOT}
	vendor := filepath.Join(toolchain.GOROOT, "src", "vendor")
	for _, target := range Targets {
		if err := addNoticeRoots(pkg, target, vendor, roots); err != nil {
			return nil, err
		}
	}
	data, err := renderNotices(roots)
	if err != nil {
		return nil, err
	}
	return append(files, archiveFile{name: "THIRD_PARTY_NOTICES.txt", data: data, mode: 0o644}), nil
}

func addNoticeRoots(pkg string, target Target, vendor string, roots map[string]string) error {
	cmd := exec.Command("go", "list", "-deps", "-json", pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.GOOS, "GOARCH="+target.GOARCH)
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list notice inputs for %s/%s: %w", target.GOOS, target.GOARCH, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var dep goPackage
		if err := decoder.Decode(&dep); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if mod := dep.Module; mod != nil && !mod.Main {
			if mod.Replace != nil {
				mod = mod.Replace
			}
			roots[mod.Path+" "+mod.Version] = mod.Dir
		} else if dep.Standard && strings.HasPrefix(dep.ImportPath, "vendor/") {
			root, err := vendorNoticeRoot(vendor, dep.Dir)
			if err != nil {
				return fmt.Errorf("%s: %w", dep.ImportPath, err)
			}
			relative, err := filepath.Rel(vendor, root)
			if err != nil {
				return err
			}
			roots["Go toolchain vendor: "+filepath.ToSlash(relative)] = root
		}
	}
}

func vendorNoticeRoot(vendor, dir string) (string, error) {
	relative, err := filepath.Rel(vendor, dir)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("standard-library dependency is outside the Go vendor directory")
	}
	for root := dir; root != vendor; root = filepath.Dir(root) {
		files, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			if file.Type().IsRegular() && noticeName.MatchString(file.Name()) {
				return root, nil
			}
		}
	}
	return "", errors.New("missing vendored license or notice")
}

func renderNotices(roots map[string]string) ([]byte, error) {
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	slices.Sort(names)
	var out bytes.Buffer
	out.WriteString("Third-party notices for dependencies in the Haiven CLI release binaries.\n")
	for _, name := range names {
		root := roots[name]
		files, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read notices for %s: %w", name, err)
		}
		found := false
		for _, file := range files {
			if !file.Type().IsRegular() || !noticeName.MatchString(file.Name()) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, file.Name()))
			if err != nil {
				return nil, fmt.Errorf("read %s for %s: %w", file.Name(), name, err)
			}
			if len(bytes.TrimSpace(data)) == 0 {
				return nil, fmt.Errorf("empty %s for %s", file.Name(), name)
			}
			fmt.Fprintf(&out, "\n--- %s / %s ---\n\n", name, file.Name())
			out.Write(data)
			out.WriteByte('\n')
			found = true
		}
		if !found {
			return nil, fmt.Errorf("missing license or notice for %s", name)
		}
	}
	return out.Bytes(), nil
}

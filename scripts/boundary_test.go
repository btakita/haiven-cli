package scripts_test

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The public haiven CLI is built only from published artifacts: released SDK
// packages, a public API description and the SDK's published fixtures. The
// The CLI stays independent of development repositories so the projected
// source and release binary can be used with public GitHub access.
//
// The forbidden names are assembled from parts so this guard does not match
// itself; boundaryExempt still lists it in case that changes.
var forbidden = []*regexp.Regexp{
	// Match the aggregate path without matching standalone SDK repositories.
	regexp.MustCompile(`(?i)haiven-dev/haiven-` + `sdk(?:$|[^\w-])`),
	// The repository name in any spelling: kebab, snake, a module path or URL.
	regexp.MustCompile(`(?i)haiven[-_ ]?` + `contracts`),
	// An environment variable or flag that points at a contracts checkout.
	regexp.MustCompile(`(?i)contracts[-_]` + `root`),
	// A flag that takes a contracts checkout.
	regexp.MustCompile(`(^|\s|")--` + `contracts\b`),
	// A submodule or checkout path of the contracts repository.
	regexp.MustCompile(`(?i)(^|[\s"'/=])` + `contracts/` + `(descriptions|conformance|generated|releases|catalog)\b`),
}

// boundaryExempt lists the only files that may name the boundary itself:
// this guard. Documentation explains the boundary without naming the private
// repository.
var boundaryExempt = map[string]bool{
	"scripts/boundary_test.go": true,
}

// skippedDirs are not part of the source tree.
var skippedDirs = map[string]bool{".git": true, "dist": true, "node_modules": true}

func TestOnlyPublicDependencies(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skippedDirs[d.Name()] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if boundaryExempt[rel] || !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		// The path itself counts: a vendored copy under a contracts-named
		// directory is a dependency even when its bytes never say so.
		for _, re := range forbidden {
			if re.MatchString(rel) {
				t.Errorf("%s: path matches %s", rel, re)
			}
		}
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			for _, re := range forbidden {
				if loc := re.FindStringIndex(text); loc != nil {
					t.Errorf("%s:%d: references a private repository dependency (%q)", rel, line, strings.TrimSpace(text[loc[0]:loc[1]]))
				}
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("scanned no files; the guard is not looking at the repository")
	}
}

// The guard must catch every spelling it exists for.
func TestBoundaryPatternsMatch(t *testing.T) {
	for _, s := range []string{
		"github.com/haiven-dev/haiven-" + "sdk/releases",
		"github.com/haiven-dev/haiven-" + "contracts",
		"HAIVEN_" + "CONTRACTS_ROOT",
		`path = "haiven-` + `contracts"`,
		"read ../sdk/" + "contracts/descriptions/gaming/v1.json",
		"import haiven_" + "contracts",
		"--" + "contracts-root",
		"haiven api list --" + "contracts ../sdk",
	} {
		hit := false
		for _, re := range forbidden {
			hit = hit || re.MatchString(s)
		}
		if !hit {
			t.Errorf("no forbidden pattern matches %q", s)
		}
	}
	for _, s := range []string{"haiven-dev/haiven-sdk-unity", "haiven-dev/haiven-sdk-godot-demo"} {
		for _, re := range forbidden {
			if re.MatchString(s) {
				t.Errorf("forbidden pattern matches public repository %q", s)
			}
		}
	}
}

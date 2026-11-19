// Package project reads haiven.toml, the project file that pins the haiven
// CLI release a game project uses.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/haiven-dev/haiven-cli/internal/version"
)

// FileName is the project file the CLI looks for, walking up from the
// working directory.
const FileName = "haiven.toml"

// tableName is the one top-level table of a project file.
const tableName = "haiven"

// File is the decoded haiven.toml. Unknown keys are rejected, so a typo never
// silently drops a pin.
type File struct {
	Haiven Haiven `toml:"haiven" json:"haiven,omitempty"`
}

// Haiven pins the CLI release.
type Haiven struct {
	// Version is the haiven release every developer and CI job runs in this
	// project. An installed haiven of another version re-executes this one
	// from its cache.
	Version string `toml:"version" json:"version,omitempty" jsonschema:"pattern=^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\\+[0-9A-Za-z.-]+)?$" jsonschema_description:"Pinned haiven CLI release, MAJOR.MINOR.PATCH without a leading v."`
}

// Project is a located, validated project file.
type Project struct {
	// Path is the haiven.toml path; Dir is its directory.
	Path string
	Dir  string
	File File
}

// ErrNotFound means no haiven.toml exists at or above the start directory.
var ErrNotFound = errors.New("no " + FileName + " found")

// Problem is one validation finding. Field is the dotted TOML key.
type Problem struct {
	Field   string
	Message string
	// Pin marks a problem with [haiven].version, which has its own
	// diagnostic code.
	Pin bool
}

// InvalidError lists every problem in a project file.
type InvalidError struct {
	Path     string
	Problems []Problem
}

func (e *InvalidError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Field + ": " + p.Message
	}
	return e.Path + ": " + strings.Join(parts, "; ")
}

// Find walks up from start to the nearest haiven.toml that is a CLI project
// file. A haiven.toml that belongs to another tool (see IsForeign) is not a
// project file: Find skips it and keeps walking, so another tool's
// configuration never reads as a broken project.
func Find(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, FileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				return "", err
			}
			if !IsForeign(raw) {
				return candidate, nil
			}
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// IsForeign reports whether haiven.toml bytes are another tool's file rather
// than a CLI project file: valid TOML whose top-level keys are all foreign.
// An SDK generator configuration is one such file.
//
// Anything that could be a CLI project file stays a project, so its problems
// are still reported: a file that is not valid TOML, an empty file, and a file
// with a [haiven] table or a top-level key that looks like a mistyped one (a
// near miss of "haiven", or a bare "version" missing its table header). A
// typo therefore never silently drops a pin.
func IsForeign(raw []byte) bool {
	var top map[string]any
	if _, err := toml.NewDecoder(bytes.NewReader(raw)).Decode(&top); err != nil || len(top) == 0 {
		return false
	}
	for key := range top {
		if looksLikeCLIKey(key) {
			return false
		}
	}
	return true
}

// looksLikeCLIKey matches the [haiven] table name, near misses of it, and
// the bare version key of a file missing its [haiven] header.
func looksLikeCLIKey(key string) bool {
	k := strings.ToLower(key)
	return k == "version" || editDistance(k, tableName) <= 2
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// Load finds and validates the project file at or above start.
func Load(start string) (*Project, error) {
	path, err := Find(start)
	if err != nil {
		return nil, err
	}
	return Read(path)
}

// Read decodes and validates one project file.
func Read(path string) (*Project, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := Parse(path, raw)
	if err != nil {
		return nil, err
	}
	return &Project{Path: path, Dir: filepath.Dir(path), File: *file}, nil
}

// Parse decodes and validates project-file bytes. path is used only in
// messages.
func Parse(path string, raw []byte) (*File, error) {
	var f File
	meta, err := toml.NewDecoder(bytes.NewReader(raw)).Decode(&f)
	if err != nil {
		return nil, &InvalidError{Path: path, Problems: []Problem{{Field: "(file)", Message: err.Error()}}}
	}
	var problems []Problem
	for _, key := range unknownRoots(meta.Undecoded()) {
		problems = append(problems, Problem{Field: key, Message: "unknown key"})
	}
	if v := f.Haiven.Version; v != "" && !version.Valid(v) {
		problems = append(problems, Problem{Field: "haiven.version", Message: fmt.Sprintf("%q is not MAJOR.MINOR.PATCH", v), Pin: true})
	}
	if len(problems) > 0 {
		return nil, &InvalidError{Path: path, Problems: problems}
	}
	return &f, nil
}

// unknownRoots reports each unknown key once, at its outermost unknown
// ancestor: an unknown table is one problem, not one per key inside it. Every
// top-level key other than [haiven] is unknown, including the implicit parent
// of a dotted table header such as [outputs.rest].
func unknownRoots(keys []toml.Key) []string {
	sorted := make([]toml.Key, 0, len(keys))
	for _, key := range keys {
		if len(key) > 1 && key[0] != tableName {
			key = key[:1]
		}
		sorted = append(sorted, key)
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) < len(sorted[j]) })
	seen := map[string]bool{}
	var roots []string
	for _, key := range sorted {
		covered := seen[key.String()]
		for n := 1; n < len(key) && !covered; n++ {
			covered = seen[key[:n].String()]
		}
		if covered {
			continue
		}
		seen[key.String()] = true
		roots = append(roots, key.String())
	}
	sort.Strings(roots)
	return roots
}

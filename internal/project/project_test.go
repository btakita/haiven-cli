package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	f, err := Parse("haiven.toml", []byte(`
[haiven]
version = "1.2.3"
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Haiven.Version != "1.2.3" {
		t.Fatalf("unexpected decode: %+v", f)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		toml  string
		field string
		pin   bool
	}{
		"unknown key":             {"[haiven]\npinned = \"1.0.0\"\n", "haiven.pinned", false},
		"bad version":             {"[haiven]\nversion = \"v1.0\"\n", "haiven.version", true},
		"path in version":         {"[haiven]\nversion = \"1.0.0/../../x\"\n", "haiven.version", true},
		"removed generator table": {"[generator]\ncommit = \"abc123\"\n", "generator", false},
		"not toml":                {"[haiven\n", "(file)", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("haiven.toml", []byte(tc.toml))
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("want InvalidError, got %v", err)
			}
			p := invalid.Problems[0]
			if p.Field != tc.field || p.Pin != tc.pin {
				t.Fatalf("got %+v, want field %q pin %v", p, tc.field, tc.pin)
			}
		})
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("[haiven]\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(nested)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != root || p.File.Haiven.Version != "1.0.0" {
		t.Fatalf("got %+v", p)
	}
}

func TestFindNotFound(t *testing.T) {
	_, err := Find(t.TempDir())
	if !errors.Is(err, ErrNotFound) && !strings.Contains(err.Error(), "haiven.toml") {
		t.Fatalf("got %v", err)
	}
}

// generatorConfig mirrors the shape of the SDK generator's root haiven.toml, the
// generator configuration that shares the project file's name.
const generatorConfig = `[contracts]
rest_description = "descriptions/http/public/gaming/v0/openapi.json"

[outputs.rest]
python = "python/haiven/rest.py"

[vocabularies.published_names]
ws-error-code = "WsErrorCode"

[[parity.known_gaps]]
sdk = "godot"
reason = "x"

[[parity.known_gaps]]
sdk = "luau"
reason = "y"
`

func TestIsForeign(t *testing.T) {
	cases := map[string]struct {
		toml    string
		foreign bool
	}{
		"generator config":        {generatorConfig, true},
		"removed generator table": {"[generator]\ncommit = \"abc123\"\n", true},
		"project":                 {"[haiven]\nversion = \"1.0.0\"\n", false},
		"project with foreign":    {"[haiven]\nversion = \"1.0.0\"\n" + generatorConfig, false},
		"empty":                   {"", false},
		"comments only":           {"# nothing pinned yet\n", false},
		"mistyped table":          {"[haivn]\nversion = \"1.0.0\"\n", false},
		"mistyped table case":     {"[Haiven]\nversion = \"1.0.0\"\n", false},
		"missing table header":    {"version = \"1.0.0\"\n", false},
		"not toml":                {"[haiven\n", false},
		"not toml foreign-ish":    {"[contracts\n", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsForeign([]byte(tc.toml)); got != tc.foreign {
				t.Fatalf("IsForeign = %v, want %v", got, tc.foreign)
			}
		})
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindSkipsForeignFile(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "vendor", "sdk-generator")
	nested := filepath.Join(sdk, "codegen")
	write(t, filepath.Join(sdk, FileName), generatorConfig)
	write(t, filepath.Join(nested, "x.py"), "")

	if _, err := Find(nested); !errors.Is(err, ErrNotFound) {
		t.Fatalf("only a foreign file above: got %v, want ErrNotFound", err)
	}

	write(t, filepath.Join(root, FileName), "[haiven]\nversion = \"1.0.0\"\n")
	p, err := Load(nested)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != root || p.File.Haiven.Version != "1.0.0" {
		t.Fatalf("want the enclosing project, got %+v", p)
	}
}

func TestFindStopsAtMalformedProject(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "game")
	write(t, filepath.Join(root, FileName), "[haiven]\nversion = \"1.0.0\"\n")
	write(t, filepath.Join(nested, FileName), "[haivn]\nversion = \"1.0.0\"\n")
	_, err := Load(nested)
	var invalid *InvalidError
	if !errors.As(err, &invalid) || invalid.Path != filepath.Join(nested, FileName) {
		t.Fatalf("want the mistyped nested file reported, got %v", err)
	}
}

func TestParseReportsUnknownTableOnce(t *testing.T) {
	_, err := Parse("haiven.toml", []byte("[haiven]\nversion = \"1.0.0\"\n"+generatorConfig))
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError, got %v", err)
	}
	var fields []string
	for _, p := range invalid.Problems {
		fields = append(fields, p.Field)
	}
	want := "contracts outputs parity vocabularies"
	if got := strings.Join(fields, " "); got != want {
		t.Fatalf("fields = %q, want %q", got, want)
	}
}

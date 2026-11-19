package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/haiven-dev/haiven-cli/internal/demo"
	"github.com/haiven-dev/haiven-cli/internal/pin"
	"github.com/haiven-dev/haiven-cli/internal/result"
	"github.com/haiven-dev/haiven-cli/internal/starter"
)

var update = flag.Bool("update", false, "rewrite golden files and published schemas")

const testVersion = "1.2.3"

// fixture is a temp project layout: files maps slash paths to contents.
type fixture map[string]string

func (f fixture) write(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range f {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// pinnedProject is a game project that pins this CLI's own release.
const pinnedProject = "[haiven]\nversion = \"1.2.3\"\n"

// foreignProjectFile has the shape of an SDK generator configuration,
// which shares the project file's name but is not a CLI project file.
const foreignProjectFile = "[contracts]\nrest_description = \"openapi.json\"\n\n[outputs.rest]\npython = \"rest.py\"\n\n[[parity.known_gaps]]\nsdk = \"godot\"\n"

type invocation struct {
	args            []string
	files           fixture
	cwd             string // relative to the fixture root
	env             map[string]string
	pin             func(t *testing.T) *pin.Resolver
	reexec          func(binary string, args []string, pinned string) (int, error)
	http            http.RoundTripper
	stdin           string
	starterCatalog  starter.CatalogSource
	starterRunner   starter.Runner
	lifecycleRunner starter.LifecycleRunner
	gitDirty        func(context.Context, string) (bool, error)
	demoOpener      demo.Opener
	demoBuilder     demo.Builder
	demoRegistrar   demo.Registrar
}

type ran struct {
	exit           int
	stdout, stderr string
}

func (in invocation) run(t *testing.T) ran {
	t.Helper()
	root := in.files.write(t)
	var stdout, stderr bytes.Buffer
	e := Env{
		Stdin:                  strings.NewReader(in.stdin),
		Stdout:                 &stdout,
		Stderr:                 &stderr,
		Getenv:                 func(k string) string { return in.env[k] },
		Cwd:                    filepath.Join(root, filepath.FromSlash(in.cwd)),
		Version:                testVersion,
		GOOS:                   "linux",
		GOARCH:                 "amd64",
		Reexec:                 in.reexec,
		HTTP:                   in.http,
		StarterCatalog:         in.starterCatalog,
		StarterRunner:          in.starterRunner,
		StarterLifecycleRunner: in.lifecycleRunner,
		GitDirty:               in.gitDirty,
		DemoOpener:             in.demoOpener,
		DemoBuilder:            in.demoBuilder,
		DemoRegistrar:          in.demoRegistrar,
	}
	if in.pin != nil {
		e.Pin = in.pin(t)
	}
	exit := Run(context.Background(), e, in.args)
	return ran{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

// goldenCases each print one envelope. Their stdout is pinned under
// testdata/golden and validated against the strict result schema and the
// command's data schema.
var goldenCases = map[string]invocation{
	"version":                   {args: []string{"version", "--json"}},
	"help-one":                  {args: []string{"help", "explain", "--json"}},
	"help-group":                {args: []string{"help", "sdk", "--json"}},
	"sdk-group":                 {args: []string{"sdk", "--json"}},
	"sdk-unknown-command":       {args: []string{"sdk", "frobnicate", "--json"}},
	"sdk-starter-group":         {args: []string{"sdk", "starter", "--json"}},
	"starter-list":              {args: []string{"sdk", "starter", "list", "--engine", "unity", "--json"}},
	"starter-describe":          {args: []string{"sdk", "starter", "describe", "unity-bossroom", "--json"}},
	"starter-describe-existing": {args: []string{"sdk", "starter", "describe", "unity-existing", "--json"}},
	"starter-create-preview":    {args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", "new-game", "--preview", "--json"}, starterRunner: &fakeStarterRunner{}},
	"starter-integrate-preview": {args: []string{"sdk", "starter", "integrate", "unity-bossroom", "--project", "game", "--preview", "--json"}, files: fixture{"game/project.txt": "owned\n"}, starterRunner: &fakeStarterRunner{}, gitDirty: func(context.Context, string) (bool, error) { return false, nil }},
	"starter-integrate-existing-preview": {
		args:  []string{"sdk", "starter", "integrate", "unity-existing", "--project", "game", "--param", "sdk_revision=28193c29b4115fdf37bd431e027400f56e340629", "--preview", "--json"},
		files: fixture{"game/project.txt": "owned\n"}, starterRunner: &fakeStarterRunner{},
		gitDirty: func(context.Context, string) (bool, error) { return false, nil },
	},
	"starter-verify-no-lock":   {args: []string{"sdk", "starter", "verify", "--project", "game", "--json"}, files: fixture{"game/project.txt": "owned\n"}},
	"generate-top-level":       {args: []string{"generate", "--json"}},
	"capabilities":             {args: []string{"capabilities", "--json"}},
	"capabilities-project":     {args: []string{"--json", "capabilities"}, files: fixture{"game/haiven.toml": pinnedProject}, cwd: "game"},
	"capabilities-bad-project": {args: []string{"capabilities", "--json"}, files: fixture{"haiven.toml": "[haiven]\nversion = \"one\"\n"}},
	// Another tool's haiven.toml (the SDK generator's config) is not a
	// project file: no project, no warnings, and an enclosing project wins.
	"capabilities-foreign-project-file": {args: []string{"capabilities", "--json"}, files: fixture{"haiven.toml": foreignProjectFile}},
	"capabilities-foreign-in-project":   {args: []string{"capabilities", "--json"}, files: fixture{"game/haiven.toml": pinnedProject, "game/sdk-generator/haiven.toml": foreignProjectFile}, cwd: "game/sdk-generator"},
	"schema-exit-codes":                 {args: []string{"schema", "exit-codes", "--json"}},
	"schema-unknown":                    {args: []string{"schema", "nope", "--json"}},
	"schema-missing-arg":                {args: []string{"schema", "--json"}},
	"explain-diagnostic":                {args: []string{"explain", "invalid-project-file", "--json"}},
	"explain-outcome":                   {args: []string{"explain", "lock-mismatch", "--json"}},
	"explain-unknown":                   {args: []string{"explain", "no-such-code", "--json"}},
	"unknown-command":                   {args: []string{"frobnicate", "--json"}},
	"invalid-flag":                      {args: []string{"version", "--bogus", "--json"}},
	"env-json":                          {args: []string{"version"}, env: map[string]string{OutputEnv: "json"}},
	"pin-unavailable": {
		args: []string{"version", "--json"}, files: fixture{"haiven.toml": "[haiven]\nversion = \"9.9.9\"\n"},
		pin: func(t *testing.T) *pin.Resolver {
			return &pin.Resolver{Cache: pin.Cache{Dir: t.TempDir()}, Fetcher: failingFetcher{errors.New("GET https://example.invalid/v9.9.9/checksums.txt: 404 Not Found")}, GOOS: "linux", GOARCH: "amd64", Running: testVersion}
		},
		reexec: func(string, []string, string) (int, error) { return 0, errors.New("must not run") },
	},
	"pin-checksum-mismatch": {
		args: []string{"version", "--json"}, files: fixture{"haiven.toml": "[haiven]\nversion = \"9.9.9\"\n"},
		pin: func(t *testing.T) *pin.Resolver {
			return &pin.Resolver{Cache: pin.Cache{Dir: t.TempDir()}, Fetcher: failingFetcher{pin.ErrChecksumMismatch}, GOOS: "linux", GOARCH: "amd64", Running: testVersion}
		},
		reexec: func(string, []string, string) (int, error) { return 0, errors.New("must not run") },
	},
}

type failingFetcher struct{ err error }

func (f failingFetcher) Fetch(context.Context, string, string, string, string) error { return f.err }

func compile(t *testing.T, name string) *sjs.Schema {
	t.Helper()
	s, ok := schemaFor(name, true)
	if !ok {
		t.Fatalf("no schema %q", name)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sjs.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := sjs.NewCompiler()
	url := string(s.ID)
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return compiled
}

func TestGolden(t *testing.T) {
	resultSchema := compile(t, SchemaResult)
	for name, in := range goldenCases {
		t.Run(name, func(t *testing.T) {
			got := in.run(t)

			var env result.Envelope
			dec := json.NewDecoder(strings.NewReader(got.stdout))
			if err := dec.Decode(&env); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, got.stdout)
			}
			if dec.More() {
				t.Fatal("stdout holds more than one JSON document")
			}
			if got.stderr != "" {
				t.Fatalf("--json wrote to stderr: %q", got.stderr)
			}
			if want := int(env.Code.Exit()); got.exit != want {
				t.Fatalf("exit %d, envelope code %q maps to %d", got.exit, env.Code, want)
			}
			if env.OK != (env.Code == result.OutcomeOK) {
				t.Fatal("ok disagrees with code")
			}

			inst, err := sjs.UnmarshalJSON(strings.NewReader(got.stdout))
			if err != nil {
				t.Fatal(err)
			}
			if err := resultSchema.Validate(inst); err != nil {
				t.Fatalf("envelope does not match the strict result schema: %v", err)
			}
			if cmd := lookup(env.Command); cmd != nil && cmd.data != nil {
				data, ok := inst.(map[string]any)["data"].(map[string]any)
				if !ok {
					t.Fatal("data is not an object")
				}
				if err := compile(t, env.Command).Validate(data); err != nil {
					t.Fatalf("data does not match the strict %s schema: %v", env.Command, err)
				}
			}

			golden := filepath.Join("testdata", "golden", name+".json")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got.stdout), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/cli -update)", err)
			}
			if string(want) != got.stdout {
				t.Fatalf("golden mismatch for %s (run go test ./internal/cli -update)\n--- want\n%s\n--- got\n%s", name, want, got.stdout)
			}
		})
	}
}

// TestCommandSchemaAcceptsUnassignedErrorData is the failure form New keeps
// when flag parsing returns before the command assigns data. A partial
// payload still fails.
func TestCommandSchemaAcceptsUnassignedErrorData(t *testing.T) {
	for _, strict := range []bool{false, true} {
		s, ok := schemaFor("version", strict)
		if !ok {
			t.Fatal("no version schema")
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := sjs.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		c := sjs.NewCompiler()
		if err := c.AddResource(string(s.ID), doc); err != nil {
			t.Fatal(err)
		}
		compiled, err := c.Compile(string(s.ID))
		if err != nil {
			t.Fatalf("strict %v: %v", strict, err)
		}
		empty, err := sjs.UnmarshalJSON(strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(empty); err != nil {
			t.Fatalf("strict %v: empty error data: %v", strict, err)
		}
		full, err := sjs.UnmarshalJSON(strings.NewReader(`{"version":"1.2.3","platform":"linux/amd64"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(full); err != nil {
			t.Fatalf("strict %v: version data: %v", strict, err)
		}
		partial, err := sjs.UnmarshalJSON(strings.NewReader(`{"version":"1.2.3"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(partial); err == nil {
			t.Fatalf("strict %v: partial version data was accepted", strict)
		}
	}
}

// TestPublishedSchemas keeps schemas/ (the files a consumer reads without
// running the CLI) byte-identical to what `haiven schema` prints.
func TestPublishedSchemas(t *testing.T) {
	dir := filepath.Join("..", "..", "schemas")
	want := map[string]bool{}
	for _, name := range schemaNames() {
		file := name + ".schema.json"
		want[file] = true
		doc, ok := schemaDocument(name)
		if !ok {
			t.Fatalf("schema %q does not render", name)
		}
		doc = append(doc, '\n')
		path := filepath.Join(dir, file)
		if *update {
			if err := os.WriteFile(path, doc, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, doc) {
			t.Errorf("%s is stale (run go test ./internal/cli -update)", path)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("%s is no longer published; remove it", e.Name())
		}
	}
}

// Every published schema compiles in strict and permissive form.
func TestSchemasCompile(t *testing.T) {
	for _, name := range schemaNames() {
		compile(t, name)
	}
}

func TestCapabilitiesListsEveryCommandAndSchema(t *testing.T) {
	got := invocation{args: []string{"capabilities", "--json"}}.run(t)
	var env struct {
		Data CapabilitiesData `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Commands) != len(commands) {
		t.Fatalf("capabilities lists %d commands, table has %d", len(env.Data.Commands), len(commands))
	}
	for _, name := range env.Data.Schemas {
		if _, ok := schemaDocument(name); !ok {
			t.Errorf("capabilities advertises schema %q that does not render", name)
		}
	}
}

// Every grouped command belongs to a declared group, and every group has
// commands, so capabilities and help never show an orphan.
func TestGroupsAreDeclared(t *testing.T) {
	declared := map[string]bool{}
	for _, g := range groups {
		declared[g.name] = true
		if len(groupCommands(g.name)) == 0 {
			t.Errorf("group %q has no commands", g.name)
		}
		if lookup(g.name) != nil {
			t.Errorf("group %q is also a command", g.name)
		}
	}
	for _, c := range commands {
		if g := groupOf(c.name); g != "" && !declared[g] {
			t.Errorf("command %q is in undeclared group %q", c.name, g)
		}
	}
}

func TestPinnedReleaseIsReexecutedWithOriginalArgs(t *testing.T) {
	var gotBinary, gotPinned string
	var gotArgs []string
	cache := ""
	in := invocation{
		args:  []string{"schema", "--json", "result"},
		files: fixture{"haiven.toml": "[haiven]\nversion = \"2.0.0\"\n"},
		pin: func(t *testing.T) *pin.Resolver {
			cache = t.TempDir()
			return &pin.Resolver{Cache: pin.Cache{Dir: cache}, Fetcher: writeFetcher{}, GOOS: "linux", GOARCH: "amd64", Running: testVersion}
		},
		reexec: func(binary string, args []string, pinned string) (int, error) {
			gotBinary, gotArgs, gotPinned = binary, args, pinned
			return 7, nil
		},
	}
	got := in.run(t)
	if got.exit != 7 {
		t.Fatalf("exit %d, want the child's 7; stdout %s", got.exit, got.stdout)
	}
	if got.stdout != "" || got.stderr != "" {
		t.Fatal("the parent must print nothing when it re-executes")
	}
	wantBinary := filepath.Join(cache, "versions", "2.0.0", "linux-amd64", "haiven")
	// The child gets the arguments exactly as given, --json included.
	if gotBinary != wantBinary || gotPinned != "2.0.0" || !reflect.DeepEqual(gotArgs, []string{"schema", "--json", "result"}) {
		t.Fatalf("re-exec %q %v pinned %q", gotBinary, gotArgs, gotPinned)
	}
}

type writeFetcher struct{}

func (writeFetcher) Fetch(_ context.Context, _, _, _, dest string) error {
	return os.WriteFile(dest, []byte("bin"), 0o755)
}

func TestHumanOutput(t *testing.T) {
	got := goldenCases["pin-unavailable"]
	got.args = []string{"version"}
	r := got.run(t)
	if r.exit != int(result.ExitEnvironment) {
		t.Fatalf("exit %d", r.exit)
	}
	if strings.HasPrefix(strings.TrimSpace(r.stdout), "{") {
		t.Fatal("human mode printed JSON")
	}
	for _, want := range []string{"error[pinned-version-unavailable]", "(haiven.toml)", "fix: "} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	r := invocation{}.run(t)
	if r.exit != 0 || !strings.Contains(r.stdout, "haiven capabilities") {
		t.Fatalf("exit %d stdout %q", r.exit, r.stdout)
	}
}

func TestJSONFlagAfterTerminatorIsAnArgument(t *testing.T) {
	jsonMode, rest := splitJSONFlag([]string{"explain", "--", "--json"}, nil)
	if jsonMode || !reflect.DeepEqual(rest, []string{"explain", "--", "--json"}) {
		t.Fatalf("got %v %v", jsonMode, rest)
	}
}

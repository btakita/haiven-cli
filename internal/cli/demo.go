package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haiven-dev/haiven-cli/internal/demo"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

// DemoOpenData identifies the immutable SDK source and exact generated
// project selected by `haiven sdk demo open`.
type DemoOpenData struct {
	Demo        string `json:"demo"`
	SDKRoot     string `json:"sdk_root"`
	SDKRevision string `json:"sdk_revision"`
	Destination string `json:"destination"`
	Project     string `json:"project"`
	Backend     string `json:"backend,omitempty" jsonschema_description:"The local backend; unity-bossroom only."`
}

// DemoBuildData identifies the immutable SDK source and standalone output
// produced by `haiven sdk demo build`.
type DemoBuildData struct {
	Demo        string `json:"demo"`
	SDKRoot     string `json:"sdk_root"`
	SDKRevision string `json:"sdk_revision"`
	Destination string `json:"destination"`
	Project     string `json:"project"`
	Target      string `json:"target"`
	Output      string `json:"output,omitempty" jsonschema_description:"The standalone output folder; absent when nothing standalone was produced (an unreal-lyra editor-only build, or Lyra without Fab content)."`
	Player      string `json:"player,omitempty" jsonschema_description:"The standalone player; unity-bossroom only."`
}

// DemoRegisterData identifies the exact generated Unity project registered
// with Hub by `haiven sdk demo register`.
type DemoRegisterData struct {
	Demo              string `json:"demo"`
	SDKRoot           string `json:"sdk_root"`
	SDKRevision       string `json:"sdk_revision"`
	Destination       string `json:"destination"`
	Project           string `json:"project"`
	AlreadyRegistered bool   `json:"already_registered"`
}

// demoFlags lists, per demo, the command flags beyond --sdk-root and
// --sdk-revision that apply to it. Any other flag is a usage error, so an
// engine option is never silently ignored.
var demoFlags = map[string]map[string][]string{
	"open": {
		demo.UnityBossRoom: {"dest", "api-root", "backend", "unity", "oauth", "no-editor"},
		demo.UnrealLyra:    {"dest", "engine", "content", "item", "no-editor"},
		demo.GodotDemo:     {"godot", "no-editor"},
	},
	"build": {
		demo.UnityBossRoom: {"dest", "target", "out", "unity", "timeout"},
		demo.UnrealLyra:    {"dest", "target", "out", "engine", "content", "item", "editor-only"},
	},
}

// selectDemo validates the positional demo and the flags given for it.
func selectDemo(c *call, fs *flag.FlagSet, operation, id string) bool {
	if !demo.Known(id) {
		c.out.Fail(result.DiagDemoUnknown, "unknown demo %q; supported demos: %s", id, strings.Join(demo.IDs, ", "))
		return false
	}
	allowed, ok := demoFlags[operation][id]
	if !ok {
		supported := make([]string, 0, len(demoFlags[operation]))
		for known := range demoFlags[operation] {
			supported = append(supported, known)
		}
		sort.Strings(supported)
		c.out.Fail(result.DiagDemoUnknown, "%s has no demo %s: %s; supported demos: %s", id, operation, unsupportedReason(operation, id), strings.Join(supported, ", "))
		return false
	}
	valid := map[string]bool{"sdk-root": true, "sdk-revision": true}
	for _, name := range allowed {
		valid[name] = true
	}
	var invalid []string
	fs.Visit(func(f *flag.Flag) {
		if !valid[f.Name] {
			invalid = append(invalid, "--"+f.Name)
		}
	})
	if len(invalid) > 0 {
		c.out.Fail(result.DiagInvalidFlag, "%s does not apply to %s; usage: %s", strings.Join(invalid, ", "), id, c.cmd.usage)
		return false
	}
	return true
}

func unsupportedReason(operation, id string) string {
	switch {
	case operation == "build" && id == demo.GodotDemo:
		return "export the standalone Godot project with its editor and the presets in the public demo repository"
	case operation == "register" && id == demo.UnrealLyra:
		return "Unreal Engine has no project registry with a public CLI; `haiven sdk demo open unreal-lyra` opens the exact project"
	case operation == "register" && id == demo.GodotDemo:
		return "the Godot project manager has no public CLI to add a project; `haiven sdk demo open godot-demo` opens the exact project"
	default:
		return "not supported by this CLI release"
	}
}

// demoChildStdout keeps stdout as exactly one result document in machine
// mode by sending child output to stderr.
func demoChildStdout(c *call) io.Writer {
	if c.json {
		return c.e.Stderr
	}
	return c.e.Stdout
}

func runDemoOpen(c *call) {
	fs := c.flags()
	sdkRoot := fs.String("sdk-root", "", "pinned demo or development SDK checkout")
	sdkRevision := fs.String("sdk-revision", "", "full expected commit ID of the checkout")
	destination := fs.String("dest", "", "development checkout only: generated Boss Room or Lyra folder")
	apiRoot := fs.String("api-root", "", "unity-bossroom development checkout: source checkout for the local backend")
	backend := fs.String("backend", "", "unity-bossroom development checkout: local-api (default), wsfixture, or auto")
	unity := fs.String("unity", "", "unity-bossroom: Unity Editor executable (standalone default: Unity on PATH)")
	oauth := fs.Bool("oauth", false, "unity-bossroom development checkout: use the local API's browser sign-in")
	engine := fs.String("engine", "", "unreal-lyra: Unreal Engine 5.8.2 root or its UnrealEditor executable (required unless --no-editor)")
	content := fs.String("content", "", "unreal-lyra: Lyra project created from Fab (apply the overlay first for a standalone checkout)")
	var items listFlag
	fs.Var(&items, "item", "unreal-lyra: item entitlement mapping code=item definition, repeatable")
	godot := fs.String("godot", "", "godot-demo: Godot 4.5.1 editor executable (default: godot on PATH)")
	noEditor := fs.Bool("no-editor", false, "verify the project or prepare a development checkout without launching an editor")
	rest, ok := c.parse(fs, 1, 1)
	if !ok || !selectDemo(c, fs, "open", rest[0]) {
		return
	}
	id := rest[0]
	if *sdkRoot == "" || *sdkRevision == "" {
		c.out.Fail(result.DiagMissingArgument, "--sdk-root and --sdk-revision are required; usage: %s", c.cmd.usage)
		return
	}
	switch id {
	case demo.UnityBossRoom:
		if *backend == "" && *apiRoot != "" {
			*backend = "local-api"
		}
		if *backend != "" && *backend != "local-api" && *backend != "wsfixture" && *backend != "auto" {
			c.out.Fail(result.DiagInvalidFlag, "invalid --backend %q; want local-api, wsfixture, or auto", *backend)
			return
		}
	case demo.UnrealLyra:
		if *engine == "" && !*noEditor {
			c.out.Fail(result.DiagMissingArgument, "--engine is required for %s unless --no-editor: the Unreal Engine 5.8.2 root or UnrealEditor executable; usage: %s", id, c.cmd.usage)
			return
		}
	}
	if c.e.DemoOpener == nil {
		c.out.Fail(result.DiagDemoOpenFailed, "demo automation is unavailable in this CLI environment")
		return
	}
	request := demo.Request{
		Demo: id, SDKRoot: *sdkRoot, ExpectedRevision: *sdkRevision, Destination: *destination, NoEditor: *noEditor,
		APIRoot: *apiRoot, Backend: *backend, Unity: *unity, OAuth: *oauth,
		Engine: *engine, Content: *content, Items: items, Godot: *godot,
	}
	if id != demo.UnityBossRoom {
		request.Backend = ""
	}
	opened, err := c.e.DemoOpener.Open(c.ctx, request, demoChildStdout(c), c.e.Stderr)
	if err != nil {
		code := result.DiagDemoOpenFailed
		if errors.Is(err, demo.ErrRevisionMismatch) {
			code = result.DiagDemoRevisionMismatch
		}
		c.out.Fail(code, "%v", err)
		return
	}
	c.out.Data = DemoOpenData{
		Demo: id, SDKRoot: opened.SDKRoot, SDKRevision: opened.Revision,
		Destination: opened.Destination, Project: opened.Project, Backend: request.Backend,
	}
	c.human = func(w io.Writer) {
		verb := "Opened"
		if *noEditor {
			verb = "Configured"
			if opened.Standalone {
				verb = "Verified"
			}
		}
		project := opened.Project
		if id == demo.UnrealLyra {
			project = filepath.Join(project, "Lyra.uproject")
		}
		source := "SDK"
		if opened.Standalone {
			source = "checkout"
		}
		fmt.Fprintf(w, "%s %s from %s %s at %s\n", verb, project, source, opened.Revision, opened.SDKRoot)
	}
}

func runDemoBuild(c *call) {
	fs := c.flags()
	sdkRoot := fs.String("sdk-root", "", "SDK checkout containing the demo")
	sdkRevision := fs.String("sdk-revision", "", "full expected commit ID of the SDK checkout")
	destination := fs.String("dest", "", "generated demo folder (default: <sdk-root>/build/bossroom-demo or lyra-demo)")
	target := fs.String("target", "", "standalone platform: linux64, win64, or osx")
	output := fs.String("out", "", "standalone output folder (default: <dest>/build/<target>; Unreal's Linux, Win64, or Mac for unreal-lyra)")
	unity := fs.String("unity", "", "unity-bossroom: Unity editor executable (otherwise SDK discovery is used)")
	timeout := fs.Float64("timeout", 0, "unity-bossroom: seconds to allow the Unity build")
	engine := fs.String("engine", "", "unreal-lyra: Unreal Engine 5.8.2 root (otherwise SDK discovery is used)")
	content := fs.String("content", "", "unreal-lyra: Lyra project created from Fab whose content is copied in")
	var items listFlag
	fs.Var(&items, "item", "unreal-lyra: item entitlement mapping code=item definition, repeatable")
	editorOnly := fs.Bool("editor-only", false, "unreal-lyra: compile LyraEditor without packaging a client")
	rest, ok := c.parse(fs, 1, 1)
	if !ok || !selectDemo(c, fs, "build", rest[0]) {
		return
	}
	id := rest[0]
	if *sdkRoot == "" || *sdkRevision == "" || *target == "" {
		c.out.Fail(result.DiagMissingArgument, "--sdk-root, --sdk-revision, and --target are required; usage: %s", c.cmd.usage)
		return
	}
	if *target != "linux64" && *target != "win64" && *target != "osx" {
		c.out.Fail(result.DiagInvalidFlag, "invalid --target %q; want linux64, win64, or osx", *target)
		return
	}
	if *timeout < 0 {
		c.out.Fail(result.DiagInvalidFlag, "invalid --timeout %v; want zero or a positive number of seconds", *timeout)
		return
	}
	if c.e.DemoBuilder == nil {
		c.out.Fail(result.DiagDemoBuildFailed, "demo build automation is unavailable in this CLI environment")
		return
	}
	built, err := c.e.DemoBuilder.Build(c.ctx, demo.BuildRequest{
		Demo: id, SDKRoot: *sdkRoot, ExpectedRevision: *sdkRevision, Destination: *destination,
		Target: *target, Output: *output, Unity: *unity, Timeout: *timeout,
		Engine: *engine, Content: *content, Items: items, EditorOnly: *editorOnly,
	}, demoChildStdout(c), c.e.Stderr)
	if err != nil {
		code := result.DiagDemoBuildFailed
		if errors.Is(err, demo.ErrRevisionMismatch) {
			code = result.DiagDemoRevisionMismatch
		}
		c.out.Fail(code, "%v", err)
		return
	}
	c.out.Data = DemoBuildData{
		Demo: id, SDKRoot: built.SDKRoot, SDKRevision: built.Revision,
		Destination: built.Destination, Project: built.Project,
		Target: built.Target, Output: built.Output, Player: built.Player,
	}
	c.human = func(w io.Writer) {
		switch {
		case built.Player != "":
			fmt.Fprintf(w, "Built %s from SDK %s at %s\n", built.Player, built.Revision, built.SDKRoot)
		case built.Output != "":
			fmt.Fprintf(w, "Packaged %s into %s from SDK %s at %s\n", built.Project, built.Output, built.Revision, built.SDKRoot)
		default:
			fmt.Fprintf(w, "Built the editor for %s from SDK %s at %s; no client was packaged\n", built.Project, built.Revision, built.SDKRoot)
		}
	}
}

func runDemoRegister(c *call) {
	fs := c.flags()
	sdkRoot := fs.String("sdk-root", "", "SDK checkout containing the assembled demo")
	sdkRevision := fs.String("sdk-revision", "", "full expected commit ID of the SDK checkout")
	destination := fs.String("dest", "", "assembled demo folder (default: <sdk-root>/build/bossroom-demo)")
	hubCLI := fs.String("hub-cli", "", "public Unity CLI command or path (default: unity on PATH)")
	rest, ok := c.parse(fs, 1, 1)
	if !ok {
		return
	}
	id := rest[0]
	if !demo.Known(id) {
		c.out.Fail(result.DiagDemoUnknown, "unknown demo %q; supported demos: %s", id, strings.Join(demo.IDs, ", "))
		return
	}
	if id != demo.UnityBossRoom {
		c.out.Fail(result.DiagDemoUnknown, "%s has no demo register: %s; supported demo: %s", id, unsupportedReason("register", id), demo.UnityBossRoom)
		return
	}
	if *sdkRoot == "" || *sdkRevision == "" {
		c.out.Fail(result.DiagMissingArgument, "--sdk-root and --sdk-revision are required; usage: %s", c.cmd.usage)
		return
	}
	if c.e.DemoRegistrar == nil {
		c.out.Fail(result.DiagDemoRegisterFailed, "demo registration automation is unavailable in this CLI environment")
		return
	}
	registered, err := c.e.DemoRegistrar.Register(c.ctx, demo.RegisterRequest{
		Demo: id, SDKRoot: *sdkRoot, ExpectedRevision: *sdkRevision, Destination: *destination, HubCLI: *hubCLI,
	}, demoChildStdout(c), c.e.Stderr)
	if err != nil {
		code := result.DiagDemoRegisterFailed
		if errors.Is(err, demo.ErrRevisionMismatch) {
			code = result.DiagDemoRevisionMismatch
		}
		c.out.Fail(code, "%v", err)
		return
	}
	c.out.Data = DemoRegisterData{
		Demo: id, SDKRoot: registered.SDKRoot, SDKRevision: registered.Revision,
		Destination: registered.Destination, Project: registered.Project,
		AlreadyRegistered: registered.AlreadyRegistered,
	}
	c.human = func(w io.Writer) {
		verb := "Registered"
		if registered.AlreadyRegistered {
			verb = "Already registered"
		}
		fmt.Fprintf(w, "%s %s in Unity Hub\n", verb, registered.Project)
		fmt.Fprintln(w, "If an already-open Unity Hub does not show the project, close and reopen the Hub; the registration is already saved.")
	}
}

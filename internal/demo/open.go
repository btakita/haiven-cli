// Package demo runs demos from explicitly pinned source checkouts.
// The public projects use native editor entry points. Development checkouts
// can still use SDK-owned assembly tools.
package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrRevisionMismatch means the checkout is not the revision the caller approved.
var ErrRevisionMismatch = errors.New("SDK checkout revision mismatch")

var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

// Request describes one interactive SDK demo workflow. Demo selects the
// engine (empty means unity-bossroom); the remaining fields apply to the
// engines named beside them.
type Request struct {
	Demo             string
	SDKRoot          string
	ExpectedRevision string
	Destination      string
	NoEditor         bool
	// unity-bossroom
	APIRoot string
	Backend string
	Unity   string
	OAuth   bool
	// unreal-lyra
	Engine  string
	Content string
	Items   []string
	// godot-demo
	Godot string
}

// Result identifies exactly which source and generated project were opened.
type Result struct {
	SDKRoot     string
	Revision    string
	Destination string
	Project     string
	Standalone  bool
}

// Opener is the injected boundary used by the CLI command.
type Opener interface {
	Open(context.Context, Request, io.Writer, io.Writer) (Result, error)
}

// Command is one argv-safe child process invocation.
type Command struct {
	Name string
	Args []string
	Dir  string
}

// Executor makes Runner deterministic in tests without a shell.
type Executor interface {
	Output(context.Context, Command) ([]byte, error)
	Run(context.Context, Command, io.Writer, io.Writer) error
}

// OSExecutor runs commands on the host without shell interpolation.
type OSExecutor struct{}

// Output runs a command and returns its standard output.
func (OSExecutor) Output(ctx context.Context, command Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	return cmd.Output()
}

// Run runs a command with streaming output.
func (OSExecutor) Run(ctx context.Context, command Command, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// Runner verifies the checkout and delegates each engine's workflow to the
// SDK-owned tool in that checkout. For Boss Room, haiven-bossroom registers
// the absolute project with Hub and launches the Editor with -projectPath;
// for Lyra, haiven-lyra assembles and compiles the project and the editor is
// opened on its absolute .uproject; for Godot, the SDK's install_addon.py
// prepares the in-tree project and the editor is opened with --path.
type Runner struct {
	Executor Executor
}

// Open verifies the exact source revision before allowing any demo mutation.
func (r Runner) Open(ctx context.Context, request Request, stdout, stderr io.Writer) (Result, error) {
	selected, err := r.selectPinned(ctx, request.Demo, request.SDKRoot, request.ExpectedRevision, request.Destination)
	if err != nil {
		return Result{}, err
	}
	if selected.Standalone {
		return r.openStandalone(ctx, selected, request, stdout, stderr)
	}
	switch demoID(request.Demo) {
	case UnrealLyra:
		return r.openLyra(ctx, selected, request, stdout, stderr)
	case GodotDemo:
		return r.openGodot(ctx, selected, request, stdout, stderr)
	}
	if request.APIRoot == "" {
		return Result{}, fmt.Errorf("the development Boss Room workflow requires --api-root; use the %s checkout for the standalone project", publicDemos[UnityBossRoom].url)
	}
	apiRoot, err := filepath.Abs(request.APIRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve API checkout: %w", err)
	}

	args := []string{
		"--from", filepath.Join(selected.SDKRoot, "demos", "bossroom"),
		"haiven-bossroom", "dev",
		"--dest", selected.Destination,
		"--sdk-root", selected.SDKRoot,
		"--api-root", apiRoot,
		"--backend", request.Backend,
	}
	if request.OAuth {
		args = append(args, "--oauth")
	}
	if request.NoEditor {
		args = append(args, "--no-editor")
	}
	if request.Unity != "" {
		unity, resolveErr := filepath.Abs(request.Unity)
		if resolveErr != nil {
			return Result{}, fmt.Errorf("resolve Unity executable: %w", resolveErr)
		}
		args = append(args, "--unity", unity)
	}
	if err := r.executor().Run(ctx, Command{Name: "uvx", Args: args, Dir: selected.SDKRoot}, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("run SDK-owned Boss Room development workflow: %w", err)
	}
	return selected, nil
}

// openLyra reassembles Lyra at the pinned revision, compiles LyraEditor with
// the SDK-owned tool, then opens the absolute project in that same engine.
func (r Runner) openLyra(ctx context.Context, selected Result, request Request, stdout, stderr io.Writer) (Result, error) {
	var editor, engine string
	if request.Engine != "" {
		var err error
		if engine, err = filepath.Abs(request.Engine); err != nil {
			return Result{}, fmt.Errorf("resolve Unreal Engine: %w", err)
		}
		if !request.NoEditor {
			if editor, err = unrealEditor(engine); err != nil {
				return Result{}, err
			}
		}
	} else if !request.NoEditor {
		return Result{}, fmt.Errorf("opening %s needs --engine: the Unreal Engine root that compiles and opens the project", UnrealLyra)
	}
	args := append(lyraTool(selected.SDKRoot, "all", selected.Destination), "--editor-only")
	if engine != "" {
		args = append(args, "--engine", engine)
	}
	args, err := lyraAssembly(args, request.Content, request.Items)
	if err != nil {
		return Result{}, err
	}
	if err := r.executor().Run(ctx, Command{Name: "uvx", Args: args, Dir: selected.SDKRoot}, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("run SDK-owned Lyra assembly and editor build: %w", err)
	}
	if request.NoEditor {
		return selected, nil
	}
	uproject := filepath.Join(selected.Project, "Lyra.uproject")
	if err := r.executor().Run(ctx, Command{Name: editor, Args: []string{uproject}, Dir: selected.Project}, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("run Unreal Editor on %s: %w", uproject, err)
	}
	return selected, nil
}

// openGodot installs the SDK's GDScript addon into the in-tree Godot demo with
// the SDK-owned script, then opens that exact project in the Godot editor.
func (r Runner) openGodot(ctx context.Context, selected Result, request Request, stdout, stderr io.Writer) (Result, error) {
	godot, err := executable(request.Godot, "godot", "Godot executable")
	if err != nil {
		return Result{}, err
	}
	install := Command{
		Name: "uv",
		Args: []string{"run", "--no-project", filepath.Join(selected.Project, "install_addon.py")},
		Dir:  selected.SDKRoot,
	}
	if err := r.executor().Run(ctx, install, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("run SDK-owned Godot addon install: %w", err)
	}
	if request.NoEditor {
		return selected, nil
	}
	editor := Command{Name: godot, Args: []string{"--editor", "--path", selected.Project}, Dir: selected.Project}
	if err := r.executor().Run(ctx, editor, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("run Godot editor on %s: %w", selected.Project, err)
	}
	return selected, nil
}

func (r Runner) executor() Executor {
	if r.Executor != nil {
		return r.Executor
	}
	return OSExecutor{}
}

func (r Runner) selectPinned(ctx context.Context, demo, sdkRoot, expectedRevision, destination string) (Result, error) {
	if !revisionPattern.MatchString(expectedRevision) {
		return Result{}, fmt.Errorf("expected SDK revision must be a full 40-64 character hexadecimal commit ID")
	}
	sdkRoot, err := filepath.Abs(sdkRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve SDK checkout: %w", err)
	}
	id := demoID(demo)
	published, ok := publicDemos[id]
	if !ok {
		return Result{}, fmt.Errorf("unknown demo %q", id)
	}
	if id == GodotDemo && destination != "" {
		return Result{}, fmt.Errorf("%s runs in place and takes no destination", id)
	}
	revisionBytes, err := r.executor().Output(ctx, Command{
		Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: sdkRoot,
	})
	if err != nil {
		return Result{}, fmt.Errorf("inspect SDK checkout %s: %w", sdkRoot, err)
	}
	revision := strings.ToLower(strings.TrimSpace(string(revisionBytes)))
	if !revisionPattern.MatchString(revision) {
		return Result{}, fmt.Errorf("inspect SDK checkout %s: git returned invalid revision %q", sdkRoot, revision)
	}
	if !strings.EqualFold(revision, expectedRevision) {
		return Result{}, fmt.Errorf("%w: expected %s, found %s at %s", ErrRevisionMismatch, strings.ToLower(expectedRevision), revision, sdkRoot)
	}
	if hasFiles(sdkRoot, published.markers...) {
		if destination != "" {
			return Result{}, fmt.Errorf("the standalone %s checkout runs in place and takes no --dest", id)
		}
		return Result{SDKRoot: sdkRoot, Revision: revision, Destination: sdkRoot, Project: sdkRoot, Standalone: true}, nil
	}
	if !isDevelopmentCheckout(sdkRoot, published.developmentDir) {
		return Result{}, fmt.Errorf("checkout %s does not contain the %s project; use the public source at %s", sdkRoot, id, published.url)
	}
	destination, project, err := layout(demo, sdkRoot, destination)
	if err != nil {
		return Result{}, err
	}
	return Result{
		SDKRoot: sdkRoot, Revision: revision, Destination: destination,
		Project: project,
	}, nil
}

package demo

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

var playerNames = map[string]string{
	"linux64": "BossRoom.x86_64",
	"win64":   "BossRoom.exe",
	"osx":     "BossRoom.app",
}

// BuildRequest describes a pinned demo assembly and standalone build. Demo
// selects the engine (empty means unity-bossroom); Target is linux64, win64,
// or osx for every engine.
type BuildRequest struct {
	Demo             string
	SDKRoot          string
	ExpectedRevision string
	Destination      string
	Target           string
	Output           string
	// unity-bossroom
	Unity   string
	Timeout float64
	// unreal-lyra
	Engine     string
	Content    string
	Items      []string
	EditorOnly bool
}

// BuildResult identifies the exact source, generated project, and build
// output. Output and Player are empty when nothing standalone was produced
// (a Lyra editor-only build, or Lyra without Fab content); Player is empty for
// Lyra, whose packaged layout belongs to Unreal.
type BuildResult struct {
	Result
	Target string
	Output string
	Player string
}

// Builder is the injected boundary used by the CLI build command.
type Builder interface {
	Build(context.Context, BuildRequest, io.Writer, io.Writer) (BuildResult, error)
}

// Build verifies the exact source revision, then delegates assembly and the
// standalone build to the demo's SDK-owned workflow.
func (r Runner) Build(ctx context.Context, request BuildRequest, stdout, stderr io.Writer) (BuildResult, error) {
	selected, err := r.selectPinned(ctx, request.Demo, request.SDKRoot, request.ExpectedRevision, request.Destination)
	if err != nil {
		return BuildResult{}, err
	}
	if selected.Standalone {
		return BuildResult{}, fmt.Errorf("%w: build the standalone %s project in its editor using the public README at %s", ErrUnsupported, demoID(request.Demo), publicDemos[demoID(request.Demo)].url)
	}
	switch demoID(request.Demo) {
	case UnrealLyra:
		return r.buildLyra(ctx, selected, request, stdout, stderr)
	case GodotDemo:
		return BuildResult{}, fmt.Errorf("%w: %s has no export preset, so there is no standalone player to build", ErrUnsupported, GodotDemo)
	}
	playerName, ok := playerNames[request.Target]
	if !ok {
		return BuildResult{}, fmt.Errorf("unsupported Boss Room build target %q", request.Target)
	}
	output := request.Output
	if output == "" {
		output = filepath.Join(selected.Destination, "build", request.Target)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return BuildResult{}, fmt.Errorf("resolve player output: %w", err)
	}

	args := []string{
		"--from", filepath.Join(selected.SDKRoot, "demos", "bossroom"),
		"haiven-bossroom", "all",
		"--dest", selected.Destination,
		"--sdk-root", selected.SDKRoot,
		"--target", request.Target,
		"--out", output,
	}
	if request.Unity != "" {
		unity, resolveErr := filepath.Abs(request.Unity)
		if resolveErr != nil {
			return BuildResult{}, fmt.Errorf("resolve Unity executable: %w", resolveErr)
		}
		args = append(args, "--unity", unity)
	}
	if request.Timeout > 0 {
		args = append(args, "--timeout", strconv.FormatFloat(request.Timeout, 'g', -1, 64))
	}
	if err := r.executor().Run(ctx, Command{Name: "uvx", Args: args, Dir: selected.SDKRoot}, stdout, stderr); err != nil {
		return BuildResult{}, fmt.Errorf("run SDK-owned Boss Room standalone build: %w", err)
	}
	return BuildResult{
		Result: selected,
		Target: request.Target,
		Output: output,
		Player: filepath.Join(output, playerName),
	}, nil
}

// buildLyra assembles Lyra, compiles LyraEditor and, unless editor-only,
// packages a client with the SDK-owned haiven-lyra tool.
func (r Runner) buildLyra(ctx context.Context, selected Result, request BuildRequest, stdout, stderr io.Writer) (BuildResult, error) {
	platform, ok := unrealPlatforms[request.Target]
	if !ok {
		return BuildResult{}, fmt.Errorf("unsupported Lyra build target %q", request.Target)
	}
	output := request.Output
	if output == "" {
		output = filepath.Join(selected.Destination, "build", platform)
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return BuildResult{}, fmt.Errorf("resolve package output: %w", err)
	}
	args := append(lyraTool(selected.SDKRoot, "all", selected.Destination), "--target", platform, "--out", output)
	if request.Engine != "" {
		engine, resolveErr := filepath.Abs(request.Engine)
		if resolveErr != nil {
			return BuildResult{}, fmt.Errorf("resolve Unreal Engine: %w", resolveErr)
		}
		args = append(args, "--engine", engine)
	}
	if request.EditorOnly {
		args = append(args, "--editor-only")
	}
	if args, err = lyraAssembly(args, request.Content, request.Items); err != nil {
		return BuildResult{}, err
	}
	if err := r.executor().Run(ctx, Command{Name: "uvx", Args: args, Dir: selected.SDKRoot}, stdout, stderr); err != nil {
		return BuildResult{}, fmt.Errorf("run SDK-owned Lyra build: %w", err)
	}
	built := BuildResult{Result: selected, Target: request.Target}
	if !request.EditorOnly && lyraHasContent(selected.Project) {
		built.Output = output
	}
	return built, nil
}

// lyraHasContent mirrors haiven-lyra's own packaging rule: the tool packages
// only when Fab content was copied in. Assembly always stages its test
// fixtures under Content, and those alone are not content.
func lyraHasContent(project string) bool {
	entries, err := os.ReadDir(filepath.Join(project, "Content"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != "HaivenTestFixtures" {
			return true
		}
	}
	return false
}

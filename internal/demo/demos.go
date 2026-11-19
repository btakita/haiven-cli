package demo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The development demos `haiven sdk demo` runs from a pinned SDK checkout.
const (
	UnityBossRoom = "unity-bossroom"
	UnrealLyra    = "unreal-lyra"
	GodotDemo     = "godot-demo"
)

// IDs lists every supported demo in a stable order.
var IDs = []string{UnityBossRoom, UnrealLyra, GodotDemo}

// ErrUnsupported means the demo exists but the requested operation has no
// meaning for its engine (for example, Unity Hub registration for Godot).
var ErrUnsupported = errors.New("operation not supported for this demo")

// Known reports whether id names a supported demo.
func Known(id string) bool {
	for _, known := range IDs {
		if id == known {
			return true
		}
	}
	return false
}

// hostOS is the platform whose Unreal editor binary layout is used; tests
// override it.
var hostOS = runtime.GOOS

// unrealPlatforms maps the CLI's standalone target names onto Unreal's.
var unrealPlatforms = map[string]string{
	"linux64": "Linux",
	"win64":   "Win64",
	"osx":     "Mac",
}

// demoID treats the zero value as the original Boss Room workflow.
func demoID(id string) string {
	if id == "" {
		return UnityBossRoom
	}
	return id
}

// layout returns the absolute generated destination and project folder of a
// demo inside an absolute SDK checkout.
func layout(id, sdkRoot, destination string) (string, string, error) {
	switch demoID(id) {
	case UnityBossRoom:
		dest, err := absDestination(sdkRoot, destination, "bossroom-demo")
		return dest, filepath.Join(dest, "BossRoom"), err
	case UnrealLyra:
		dest, err := absDestination(sdkRoot, destination, "lyra-demo")
		return dest, filepath.Join(dest, "Lyra"), err
	case GodotDemo:
		if destination != "" {
			return "", "", fmt.Errorf("%s runs in place at %s; it takes no destination",
				GodotDemo, filepath.Join(sdkRoot, "demos", "godot"))
		}
		project := filepath.Join(sdkRoot, "demos", "godot")
		return project, project, nil
	default:
		return "", "", fmt.Errorf("unknown demo %q", id)
	}
}

func absDestination(sdkRoot, destination, name string) (string, error) {
	if destination == "" {
		destination = filepath.Join(sdkRoot, "build", name)
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve demo destination: %w", err)
	}
	return destination, nil
}

// lyraTool is the uvx prefix that runs the SDK-owned haiven-lyra tool from the
// pinned checkout itself.
func lyraTool(sdkRoot, command, destination string) []string {
	return []string{
		"--from", filepath.Join(sdkRoot, "demos", "lyra"),
		"haiven-lyra", command,
		"--dest", destination,
		"--sdk-root", sdkRoot,
	}
}

// lyraAssembly appends the assembly options shared by open and build.
func lyraAssembly(args []string, content string, items []string) ([]string, error) {
	if content != "" {
		absolute, err := filepath.Abs(content)
		if err != nil {
			return nil, fmt.Errorf("resolve Fab Lyra project: %w", err)
		}
		args = append(args, "--content", absolute)
	}
	for _, item := range items {
		args = append(args, "--item", item)
	}
	return args, nil
}

// unrealEditor resolves the editor executable for an Unreal Engine root or an
// explicit UnrealEditor executable. The SDK tool compiles with the same
// engine, so the editor opened is the one the project was built for.
func unrealEditor(engine string) (string, error) {
	info, err := os.Stat(engine)
	if err != nil {
		return "", fmt.Errorf("inspect Unreal Engine %s: %w", engine, err)
	}
	if !info.IsDir() {
		if strings.TrimSuffix(filepath.Base(engine), ".exe") != "UnrealEditor" {
			return "", fmt.Errorf("--engine %s is neither an Unreal Engine root nor its UnrealEditor executable", engine)
		}
		return engine, nil
	}
	var editor string
	switch hostOS {
	case "windows":
		editor = filepath.Join(engine, "Engine", "Binaries", "Win64", "UnrealEditor.exe")
	case "darwin":
		editor = filepath.Join(engine, "Engine", "Binaries", "Mac", "UnrealEditor.app", "Contents", "MacOS", "UnrealEditor")
	default:
		editor = filepath.Join(engine, "Engine", "Binaries", "Linux", "UnrealEditor")
	}
	if _, err := os.Stat(editor); err != nil {
		return "", fmt.Errorf("--engine %s has no editor at %s: %w", engine, editor, err)
	}
	return editor, nil
}

// executable returns a bare command name unchanged, so the OS resolves it on
// PATH, and makes any other path absolute.
func executable(value, fallback, what string) (string, error) {
	if value == "" {
		return fallback, nil
	}
	if filepath.IsAbs(value) || filepath.Base(value) == value {
		return value, nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", what, err)
	}
	return absolute, nil
}

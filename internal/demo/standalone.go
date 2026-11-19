package demo

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type publicDemo struct {
	url            string
	markers        []string
	developmentDir string
}

var publicDemos = map[string]publicDemo{
	UnityBossRoom: {
		url:            "https://github.com/haiven-dev/haiven-sdk-unity-bossroom",
		markers:        []string{"ProjectSettings/ProjectVersion.txt", "Packages/com.haiven.demo.bossroom/package.json"},
		developmentDir: "bossroom",
	},
	UnrealLyra: {
		url:            "https://github.com/haiven-dev/haiven-sdk-unreal-lyra",
		markers:        []string{"Plugins/Haiven/Haiven.uplugin", "Plugins/HaivenLyraDemo/HaivenLyraDemo.uplugin"},
		developmentDir: "lyra",
	},
	GodotDemo: {
		url:            "https://github.com/haiven-dev/haiven-sdk-godot-demo",
		markers:        []string{"project.godot", "addons/haiven/plugin.cfg"},
		developmentDir: "godot",
	},
}

func hasFiles(root string, paths ...string) bool {
	for _, path := range paths {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func isDevelopmentCheckout(root, directory string) bool {
	info, err := os.Stat(filepath.Join(root, "demos", directory))
	return err == nil && info.IsDir()
}

func (r Runner) openStandalone(ctx context.Context, selected Result, request Request, stdout, stderr io.Writer) (Result, error) {
	var editor string
	var args []string
	var err error
	switch demoID(request.Demo) {
	case UnityBossRoom:
		if request.APIRoot != "" || request.Backend != "" || request.OAuth {
			return Result{}, fmt.Errorf("the standalone Boss Room project takes no --api-root, --backend, or --oauth; use its README to configure play")
		}
		editor, err = executable(request.Unity, "Unity", "Unity Editor")
		args = []string{"-projectPath", selected.Project}
	case UnrealLyra:
		if len(request.Items) > 0 {
			return Result{}, fmt.Errorf("configure item mappings in the assembled Lyra project before opening it")
		}
		project, pathErr := filepath.Abs(request.Content)
		if pathErr != nil {
			return Result{}, fmt.Errorf("resolve the assembled Fab Lyra project: %w", pathErr)
		}
		markers := append([]string{"Lyra.uproject", "Plugins/HaivenLyraInventory/HaivenLyraInventory.uplugin"}, publicDemos[UnrealLyra].markers...)
		if request.Content == "" || !hasFiles(project, markers...) {
			return Result{}, fmt.Errorf("apply the public Lyra overlay to your Fab project using its README, then pass the assembled project with --content: %s", publicDemos[UnrealLyra].url)
		}
		selected.Project, selected.Destination = project, project
		if !request.NoEditor {
			engine := request.Engine
			if engine != "" {
				engine, err = filepath.Abs(engine)
				if err != nil {
					return Result{}, fmt.Errorf("resolve Unreal Engine: %w", err)
				}
			}
			editor, err = unrealEditor(engine)
		}
		args = []string{filepath.Join(project, "Lyra.uproject")}
	case GodotDemo:
		editor, err = executable(request.Godot, "godot", "Godot executable")
		args = []string{"--editor", "--path", selected.Project}
	}
	if err != nil {
		return Result{}, err
	}
	if request.NoEditor {
		return selected, nil
	}
	if err := r.executor().Run(ctx, Command{Name: editor, Args: args, Dir: selected.Project}, stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("open the standalone %s project: %w", demoID(request.Demo), err)
	}
	return selected, nil
}

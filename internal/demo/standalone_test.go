package demo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func developmentFixture(t *testing.T, root, demo string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "demos", demo), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFixture(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenStandaloneDemosUseNativeEditorsWithoutDevelopmentTools(t *testing.T) {
	cases := []struct {
		id   string
		open func(*testing.T, Request) (Request, Command)
	}{
		{UnityBossRoom, func(_ *testing.T, request Request) (Request, Command) {
			request.Unity = "Unity"
			return request, Command{Name: "Unity", Args: []string{"-projectPath", request.SDKRoot}, Dir: request.SDKRoot}
		}},
		{GodotDemo, func(_ *testing.T, request Request) (Request, Command) {
			return request, Command{Name: "godot", Args: []string{"--editor", "--path", request.SDKRoot}, Dir: request.SDKRoot}
		}},
		{UnrealLyra, func(t *testing.T, request Request) (Request, Command) {
			project := t.TempDir()
			writeFixture(t, project, "Lyra.uproject", "Plugins/HaivenLyraInventory/HaivenLyraInventory.uplugin")
			writeFixture(t, project, publicDemos[UnrealLyra].markers...)
			engine, editor := fakeEngine(t)
			request.Content, request.Engine = project, engine
			return request, Command{Name: editor, Args: []string{filepath.Join(project, "Lyra.uproject")}, Dir: project}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, publicDemos[tc.id].markers...)
			request, command := tc.open(t, Request{Demo: tc.id, SDKRoot: root, ExpectedRevision: testRevision})
			executor := &fakeExecutor{output: []byte(testRevision + "\n")}
			got, err := (Runner{Executor: executor}).Open(context.Background(), request, io.Discard, io.Discard)
			if err != nil || got.Project != command.Dir || len(executor.commands) != 2 ||
				executor.commands[0].Name != "git" || !reflect.DeepEqual(executor.commands[1], command) {
				t.Fatalf("result=%+v err=%v commands=%+v, expected editor command %+v", got, err, executor.commands, command)
			}
		})
	}
}

func TestOpenStandaloneLyraResolvesRelativeEngineBeforeLaunching(t *testing.T) {
	overlay := t.TempDir()
	writeFixture(t, overlay, publicDemos[UnrealLyra].markers...)
	project := t.TempDir()
	writeFixture(t, project, "Lyra.uproject", "Plugins/HaivenLyraInventory/HaivenLyraInventory.uplugin")
	writeFixture(t, project, publicDemos[UnrealLyra].markers...)
	engine, editor := fakeEngine(t)
	caller := filepath.Dir(engine)
	t.Chdir(caller)

	for _, path := range []string{engine, editor} {
		relative, err := filepath.Rel(caller, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(relative, func(t *testing.T) {
			executor := &fakeExecutor{output: []byte(testRevision + "\n")}
			_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
				Demo: UnrealLyra, SDKRoot: overlay, ExpectedRevision: testRevision,
				Content: project, Engine: relative,
			}, io.Discard, io.Discard)
			want := Command{Name: editor, Args: []string{filepath.Join(project, "Lyra.uproject")}, Dir: project}
			if err != nil || len(executor.commands) != 2 || !reflect.DeepEqual(executor.commands[1], want) {
				t.Fatalf("err=%v commands=%+v, expected editor command %+v", err, executor.commands, want)
			}
		})
	}
}

func TestStandaloneBossRoomRegistersRepositoryRootAndGivesBuildGuidance(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, publicDemos[UnityBossRoom].markers...)
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	runner := Runner{Executor: executor}
	result, err := runner.Register(context.Background(), RegisterRequest{SDKRoot: root, ExpectedRevision: testRevision}, io.Discard, io.Discard)
	if err != nil || result.Project != root || len(executor.commands) != 2 ||
		!reflect.DeepEqual(executor.commands[1].Args, []string{"--no-banner", "--non-interactive", "projects", "add", root}) {
		t.Fatalf("result=%+v err=%v commands=%+v", result, err, executor.commands)
	}
	_, err = runner.Build(context.Background(), BuildRequest{SDKRoot: root, ExpectedRevision: testRevision, Target: "linux64"}, io.Discard, io.Discard)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), publicDemos[UnityBossRoom].url) || len(executor.commands) != 3 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

func TestUnrecognizedCheckoutDoesNotFallBackToDevelopmentTools(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: GodotDemo, SDKRoot: t.TempDir(), ExpectedRevision: testRevision,
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), publicDemos[GodotDemo].url) || len(executor.commands) != 1 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

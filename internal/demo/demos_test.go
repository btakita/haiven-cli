package demo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeEngine creates an Unreal Engine root with a Linux editor binary and
// pins the editor layout to Linux for the test.
func fakeEngine(t *testing.T) (string, string) {
	t.Helper()
	previous := hostOS
	hostOS = "linux"
	t.Cleanup(func() { hostOS = previous })
	root := filepath.Join(t.TempDir(), "UE_5.8")
	editor := filepath.Join(root, "Engine", "Binaries", "Linux", "UnrealEditor")
	if err := os.MkdirAll(filepath.Dir(editor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(editor, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, editor
}

func TestRunnerBuildsLyraWithSDKOwnedTool(t *testing.T) {
	root := t.TempDir()
	sdkRoot := developmentFixture(t, filepath.Join(root, "sdk"), "lyra")
	destination := filepath.Join(root, "lyra-demo")
	output := filepath.Join(root, "clients")
	engine := filepath.Join(root, "UE_5.8")
	fab := filepath.Join(root, "Fab", "Lyra")
	// What a Fab content copy leaves in the assembled project.
	if err := os.MkdirAll(filepath.Join(destination, "Lyra", "Content", "Characters"), 0o755); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Build(context.Background(), BuildRequest{
		Demo: UnrealLyra, SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination,
		Target: "linux64", Output: output, Engine: engine, Content: fab, Items: []string{"tag/hero=ID_Pistol"},
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != filepath.Join(destination, "Lyra") || got.Output != output || got.Player != "" || got.Target != "linux64" {
		t.Fatalf("result=%+v", got)
	}
	wantArgs := []string{
		"--from", filepath.Join(sdkRoot, "demos", "lyra"),
		"haiven-lyra", "all", "--dest", destination, "--sdk-root", sdkRoot,
		"--target", "Linux", "--out", output, "--engine", engine,
		"--content", fab, "--item", "tag/hero=ID_Pistol",
	}
	if len(executor.commands) != 2 || executor.commands[1].Name != "uvx" || executor.commands[1].Dir != sdkRoot || !reflect.DeepEqual(executor.commands[1].Args, wantArgs) {
		t.Fatalf("commands=%+v want args=%+v", executor.commands, wantArgs)
	}
}

func TestRunnerLyraBuildReportsNoOutputWhenNothingWasPackaged(t *testing.T) {
	sdkRoot := developmentFixture(t, t.TempDir(), "lyra")
	// Only the SDK's staged fixtures: the tool compiles but does not package,
	// even though an older package folder may already exist.
	for _, dir := range []string{
		filepath.Join(sdkRoot, "build", "lyra-demo", "Lyra", "Content", "HaivenTestFixtures"),
		filepath.Join(sdkRoot, "build", "lyra-demo", "build", "Win64"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Build(context.Background(), BuildRequest{
		Demo: UnrealLyra, SDKRoot: sdkRoot, ExpectedRevision: testRevision, Target: "win64",
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(sdkRoot, "build", "lyra-demo")
	if got.Destination != destination || got.Output != "" {
		t.Fatalf("result=%+v", got)
	}
	args := executor.commands[1].Args
	if !reflect.DeepEqual(args[len(args)-4:], []string{"--target", "Win64", "--out", filepath.Join(destination, "build", "Win64")}) {
		t.Fatalf("args=%+v", args)
	}
}

func TestRunnerOpensLyraInTheEngineThatCompiledIt(t *testing.T) {
	engine, editor := fakeEngine(t)
	sdkRoot := developmentFixture(t, t.TempDir(), "lyra")
	destination := filepath.Join(t.TempDir(), "lyra-demo")
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: UnrealLyra, SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination, Engine: engine,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(destination, "Lyra")
	if got.Project != project || len(executor.commands) != 3 {
		t.Fatalf("result=%+v commands=%+v", got, executor.commands)
	}
	wantArgs := []string{
		"--from", filepath.Join(sdkRoot, "demos", "lyra"),
		"haiven-lyra", "all", "--dest", destination, "--sdk-root", sdkRoot,
		"--editor-only", "--engine", engine,
	}
	if !reflect.DeepEqual(executor.commands[1].Args, wantArgs) {
		t.Fatalf("uvx args=%+v want=%+v", executor.commands[1].Args, wantArgs)
	}
	want := Command{Name: editor, Args: []string{filepath.Join(project, "Lyra.uproject")}, Dir: project}
	if !reflect.DeepEqual(executor.commands[2], want) {
		t.Fatalf("editor command=%+v want=%+v", executor.commands[2], want)
	}
}

func TestRunnerLyraOpenNeedsAnEngineBeforeAnyBuild(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: UnrealLyra, SDKRoot: developmentFixture(t, t.TempDir(), "lyra"), ExpectedRevision: testRevision,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || len(executor.commands) != 1 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
	_, err = (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: UnrealLyra, SDKRoot: developmentFixture(t, t.TempDir(), "lyra"), ExpectedRevision: testRevision, Engine: t.TempDir(),
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || len(executor.commands) != 2 {
		t.Fatalf("an engine root without an editor must fail before uvx: err=%v commands=%+v", err, executor.commands)
	}
}

func TestRunnerLyraOpenNoEditorOnlyAssemblesAndCompiles(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: UnrealLyra, SDKRoot: developmentFixture(t, t.TempDir(), "lyra"), ExpectedRevision: testRevision, NoEditor: true,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || len(executor.commands) != 2 || executor.commands[1].Name != "uvx" {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

func TestRunnerOpensTheInTreeGodotDemo(t *testing.T) {
	root := t.TempDir()
	sdkRoot := developmentFixture(t, filepath.Join(root, "sdk"), "godot")
	godot := filepath.Join(root, "bin", "godot")
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: GodotDemo, SDKRoot: sdkRoot, ExpectedRevision: testRevision, Godot: godot,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(sdkRoot, "demos", "godot")
	if got.Project != project || got.Destination != project || len(executor.commands) != 3 {
		t.Fatalf("result=%+v commands=%+v", got, executor.commands)
	}
	install := Command{Name: "uv", Args: []string{"run", "--no-project", filepath.Join(project, "install_addon.py")}, Dir: sdkRoot}
	if !reflect.DeepEqual(executor.commands[1], install) {
		t.Fatalf("install command=%+v want=%+v", executor.commands[1], install)
	}
	editor := Command{Name: godot, Args: []string{"--editor", "--path", project}, Dir: project}
	if !reflect.DeepEqual(executor.commands[2], editor) {
		t.Fatalf("editor command=%+v want=%+v", executor.commands[2], editor)
	}
}

func TestRunnerGodotDefaultsToGodotOnPathAndNoEditorOnlyInstalls(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	if _, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: GodotDemo, SDKRoot: developmentFixture(t, t.TempDir(), "godot"), ExpectedRevision: testRevision, NoEditor: true,
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil || len(executor.commands) != 2 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
	executor = &fakeExecutor{output: []byte(testRevision + "\n")}
	if _, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: GodotDemo, SDKRoot: developmentFixture(t, t.TempDir(), "godot"), ExpectedRevision: testRevision,
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil || executor.commands[2].Name != "godot" {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

func TestRunnerGodotRefusesADestinationBeforeInspectingTheCheckout(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		Demo: GodotDemo, SDKRoot: developmentFixture(t, t.TempDir(), "godot"), ExpectedRevision: testRevision, Destination: t.TempDir(),
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || len(executor.commands) != 0 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

func TestRunnerRefusesOperationsAnEngineDoesNotHave(t *testing.T) {
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	runner := Runner{Executor: executor}
	if _, err := runner.Build(context.Background(), BuildRequest{
		Demo: GodotDemo, SDKRoot: developmentFixture(t, t.TempDir(), "godot"), ExpectedRevision: testRevision, Target: "linux64",
	}, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("godot build err=%v", err)
	}
	for _, id := range []string{UnrealLyra, GodotDemo} {
		if _, err := runner.Register(context.Background(), RegisterRequest{
			Demo: id, SDKRoot: t.TempDir(), ExpectedRevision: testRevision,
		}, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s register err=%v", id, err)
		}
	}
	// Only the Godot build inspected the checkout; registration refuses first.
	if len(executor.commands) != 1 {
		t.Fatalf("commands=%+v", executor.commands)
	}
}

func TestUnrealEditorAcceptsTheExecutableAndRejectsOtherFiles(t *testing.T) {
	_, editor := fakeEngine(t)
	if got, err := unrealEditor(editor); err != nil || got != editor {
		t.Fatalf("got=%q err=%v", got, err)
	}
	other := filepath.Join(filepath.Dir(editor), "ShaderCompileWorker")
	if err := os.WriteFile(other, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := unrealEditor(other); err == nil {
		t.Fatal("a non-editor file inside the engine was accepted")
	}
}

package demo

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunnerBuildPinsRevisionAndRunsSDKOwnedTool(t *testing.T) {
	root := t.TempDir()
	sdkRoot := developmentFixture(t, filepath.Join(root, "sdk"), "bossroom")
	destination := filepath.Join(root, "demo")
	output := filepath.Join(root, "players", "linux")
	unity := filepath.Join(root, "Unity")
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Build(context.Background(), BuildRequest{
		SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination,
		Target: "linux64", Output: output, Unity: unity, Timeout: 45.5,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != testRevision || got.Project != filepath.Join(destination, "BossRoom") || got.Player != filepath.Join(output, "BossRoom.x86_64") {
		t.Fatalf("result=%+v", got)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("commands=%+v", executor.commands)
	}
	wantArgs := []string{
		"--from", filepath.Join(sdkRoot, "demos", "bossroom"),
		"haiven-bossroom", "all", "--dest", destination,
		"--sdk-root", sdkRoot, "--target", "linux64", "--out", output,
		"--unity", unity, "--timeout", "45.5",
	}
	if executor.commands[1].Name != "uvx" || executor.commands[1].Dir != sdkRoot || !reflect.DeepEqual(executor.commands[1].Args, wantArgs) {
		t.Fatalf("uvx command=%+v want args=%+v", executor.commands[1], wantArgs)
	}
}

func TestRunnerBuildUsesDeterministicDefaultOutput(t *testing.T) {
	sdkRoot := developmentFixture(t, t.TempDir(), "bossroom")
	destination := filepath.Join(t.TempDir(), "demo")
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Build(context.Background(), BuildRequest{
		SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination, Target: "win64",
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := filepath.Join(destination, "build", "win64")
	if got.Output != wantOutput || got.Player != filepath.Join(wantOutput, "BossRoom.exe") {
		t.Fatalf("result=%+v", got)
	}
}

package demo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

const testRevision = "9769859b1044d556da6994df999d17a2e83544bb"

type fakeExecutor struct {
	commands  []Command
	output    []byte
	err       error
	runStdout string
	runStderr string
	runErr    error
}

func (f *fakeExecutor) Output(_ context.Context, command Command) ([]byte, error) {
	f.commands = append(f.commands, command)
	return f.output, f.err
}

func (f *fakeExecutor) Run(_ context.Context, command Command, stdout, stderr io.Writer) error {
	f.commands = append(f.commands, command)
	_, _ = io.WriteString(stdout, f.runStdout)
	_, _ = io.WriteString(stderr, f.runStderr)
	if f.runErr != nil {
		return f.runErr
	}
	return f.err
}

func TestRunnerPinsRevisionAndRunsSDKOwnedToolWithAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	sdkRoot := developmentFixture(t, filepath.Join(root, "sdk"), "bossroom")
	apiRoot := filepath.Join(root, "api")
	destination := filepath.Join(root, "demo")
	unity := filepath.Join(root, "Unity")
	executor := &fakeExecutor{output: []byte(testRevision + "\n")}
	got, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination,
		APIRoot: apiRoot, Backend: "local-api", Unity: unity, OAuth: true, NoEditor: true,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != testRevision || got.Project != filepath.Join(destination, "BossRoom") {
		t.Fatalf("result=%+v", got)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("commands=%+v", executor.commands)
	}
	if want := (Command{Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: sdkRoot}); !reflect.DeepEqual(executor.commands[0], want) {
		t.Fatalf("git command=%+v want=%+v", executor.commands[0], want)
	}
	wantArgs := []string{
		"--from", filepath.Join(sdkRoot, "demos", "bossroom"),
		"haiven-bossroom", "dev", "--dest", destination,
		"--sdk-root", sdkRoot, "--api-root", apiRoot, "--backend", "local-api",
		"--oauth", "--no-editor",
		"--unity", unity,
	}
	if executor.commands[1].Name != "uvx" || executor.commands[1].Dir != sdkRoot || !reflect.DeepEqual(executor.commands[1].Args, wantArgs) {
		t.Fatalf("uvx command=%+v want args=%+v", executor.commands[1], wantArgs)
	}
}

func TestRunnerStopsBeforeUvxOnRevisionMismatch(t *testing.T) {
	executor := &fakeExecutor{output: []byte("0f30156c0f30156c0f30156c0f30156c0f30156c\n")}
	_, err := (Runner{Executor: executor}).Open(context.Background(), Request{
		SDKRoot: t.TempDir(), ExpectedRevision: testRevision, APIRoot: t.TempDir(), Backend: "local-api",
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, ErrRevisionMismatch) || len(executor.commands) != 1 {
		t.Fatalf("err=%v commands=%+v", err, executor.commands)
	}
}

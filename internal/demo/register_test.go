package demo

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunnerRegistersPinnedProjectWithPublicUnityCLI(t *testing.T) {
	root := t.TempDir()
	sdkRoot := developmentFixture(t, filepath.Join(root, "sdk"), "bossroom")
	destination := filepath.Join(root, "demo")
	hubCLI := filepath.Join(root, "bin", "unity")
	executor := &fakeExecutor{output: []byte(testRevision + "\n"), runStdout: "Project added\n"}
	var stdout bytes.Buffer
	got, err := (Runner{Executor: executor}).Register(context.Background(), RegisterRequest{
		SDKRoot: sdkRoot, ExpectedRevision: testRevision, Destination: destination, HubCLI: hubCLI,
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != testRevision || got.Project != filepath.Join(destination, "BossRoom") || got.AlreadyRegistered {
		t.Fatalf("result=%+v", got)
	}
	if stdout.String() != "Project added\n" || len(executor.commands) != 2 {
		t.Fatalf("stdout=%q commands=%+v", stdout.String(), executor.commands)
	}
	want := Command{
		Name: hubCLI,
		Args: []string{"--no-banner", "--non-interactive", "projects", "add", filepath.Join(destination, "BossRoom")},
		Dir:  sdkRoot,
	}
	if !reflect.DeepEqual(executor.commands[1], want) {
		t.Fatalf("hub command=%+v want=%+v", executor.commands[1], want)
	}
}

func TestRunnerTreatsExistingHubRegistrationAsSuccess(t *testing.T) {
	executor := &fakeExecutor{
		output:    []byte(testRevision + "\n"),
		runStderr: "Error: Project already added to Hub: /demo/BossRoom\n",
		runErr:    errors.New("exit status 6"),
	}
	got, err := (Runner{Executor: executor}).Register(context.Background(), RegisterRequest{
		SDKRoot: developmentFixture(t, t.TempDir(), "bossroom"), ExpectedRevision: testRevision,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || !got.AlreadyRegistered {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestRunnerReportsHubRegistrationFailure(t *testing.T) {
	executor := &fakeExecutor{
		output:    []byte(testRevision + "\n"),
		runStderr: "project folder is invalid\n",
		runErr:    errors.New("exit status 1"),
	}
	_, err := (Runner{Executor: executor}).Register(context.Background(), RegisterRequest{
		SDKRoot: developmentFixture(t, t.TempDir(), "bossroom"), ExpectedRevision: testRevision,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "project folder is invalid") {
		t.Fatalf("err=%v", err)
	}
}

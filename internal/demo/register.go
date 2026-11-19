package demo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

const alreadyRegisteredMessage = "Project already added to Hub:"

// RegisterRequest selects an assembled demo project and the public Unity CLI.
// Only unity-bossroom has a project registry (Unity Hub) to add it to.
type RegisterRequest struct {
	Demo             string
	SDKRoot          string
	ExpectedRevision string
	Destination      string
	HubCLI           string
}

// RegisterResult identifies the project made available in Unity Hub.
type RegisterResult struct {
	Result
	AlreadyRegistered bool
}

// Registrar is the injected boundary used by the CLI command.
type Registrar interface {
	Register(context.Context, RegisterRequest, io.Writer, io.Writer) (RegisterResult, error)
}

// Register verifies the exact SDK revision, then adds its assembled Boss Room
// project through Unity Hub's supported public CLI. Re-registering the same
// absolute path is idempotent even though the public CLI exits non-zero.
func (r Runner) Register(ctx context.Context, request RegisterRequest, stdout, stderr io.Writer) (RegisterResult, error) {
	if id := demoID(request.Demo); id != UnityBossRoom {
		return RegisterResult{}, fmt.Errorf("%w: %s has no Unity Hub project to register", ErrUnsupported, id)
	}
	selected, err := r.selectPinned(ctx, request.Demo, request.SDKRoot, request.ExpectedRevision, request.Destination)
	if err != nil {
		return RegisterResult{}, err
	}
	hubCLI, err := resolveHubCLI(request.HubCLI)
	if err != nil {
		return RegisterResult{}, err
	}
	command := Command{
		Name: hubCLI,
		Args: []string{"--no-banner", "--non-interactive", "projects", "add", selected.Project},
		Dir:  selected.SDKRoot,
	}
	var childStdout, childStderr bytes.Buffer
	err = r.executor().Run(ctx, command, &childStdout, &childStderr)
	combined := childStdout.String() + "\n" + childStderr.String()
	if err != nil && strings.Contains(combined, alreadyRegisteredMessage) {
		return RegisterResult{Result: selected, AlreadyRegistered: true}, nil
	}
	if err != nil {
		detail := strings.TrimSpace(childStderr.String())
		if detail == "" {
			detail = strings.TrimSpace(childStdout.String())
		}
		if detail != "" {
			return RegisterResult{}, fmt.Errorf("register Unity project with Hub: %w: %s", err, detail)
		}
		return RegisterResult{}, fmt.Errorf("register Unity project with Hub: %w", err)
	}
	_, _ = childStdout.WriteTo(stdout)
	_, _ = childStderr.WriteTo(stderr)
	return RegisterResult{Result: selected}, nil
}

func resolveHubCLI(value string) (string, error) {
	return executable(value, "unity", "Unity CLI")
}

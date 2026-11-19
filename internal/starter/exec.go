package starter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrRunnerIntegrity means a selected runner artifact failed digest or file
// safety validation and was not executed.
var ErrRunnerIntegrity = errors.New("starter runner integrity check failed")

type cancellationSignalError struct{ signal os.Signal }

func (e cancellationSignalError) Error() string {
	return "starter lifecycle cancelled by " + e.signal.String()
}

// CancellationSignal records the exact OS signal as a context cancellation
// cause so the lifecycle transport can forward SIGINT or SIGTERM unchanged.
func CancellationSignal(signal os.Signal) error { return cancellationSignalError{signal: signal} }

// ExecutableRunner resolves verified runner artifacts from one release-owned
// directory. Artifact names are <runner-id>, <runner-id>.exe, or
// <runner-id>.py. Python sources are optional SDK tooling and use a PATH-based
// interpreter; native executables remain runtime-independent.
type ExecutableRunner struct {
	Directory string
}

// Run verifies the selected artifact before exchanging one JSON request and
// result over stdin/stdout. Runner stderr is intentionally not copied into the
// public result because it may contain environment-specific details.
func (r ExecutableRunner) Run(ctx context.Context, spec RunnerSpec, request RunnerRequest) (RunnerResult, error) {
	_, command, err := r.verifiedCommand(spec)
	if err != nil {
		return RunnerResult{}, err
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return RunnerResult{}, err
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(append(requestBody, '\n'))
	stdout, err := cmd.Output()
	if err != nil {
		return RunnerResult{}, fmt.Errorf("runner %q failed: %w", spec.ID, err)
	}
	var result RunnerResult
	if err := decodeClosed(stdout, &result); err != nil {
		return RunnerResult{}, fmt.Errorf("runner %q returned invalid JSON: %w", spec.ID, err)
	}
	return result, nil
}

// RunLifecycle verifies and invokes one v2 runner. Context cancellation is
// forwarded to the runner process, which owns its engine process tree and must
// return cancelled exit evidence. The transport escalates only after the
// grace period the runner advertised during capability discovery.
func (r ExecutableRunner) RunLifecycle(ctx context.Context, spec RunnerSpec, request RunnerV2Request) (LifecycleResult, error) {
	_, command, err := r.verifiedCommand(spec)
	if err != nil {
		return LifecycleResult{}, err
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return LifecycleResult{}, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(append(requestBody, '\n'))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return LifecycleResult{}, fmt.Errorf("runner %q failed to start: %w", spec.ID, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err = <-wait:
	case <-ctx.Done():
		signal := os.Interrupt
		var signalCause cancellationSignalError
		if errors.As(context.Cause(ctx), &signalCause) {
			signal = signalCause.signal
		}
		_ = cmd.Process.Signal(signal)
		grace := time.Duration(request.CancellationGraceMS) * time.Millisecond
		if grace <= 0 {
			grace = 5 * time.Second
		}
		select {
		case err = <-wait:
		case <-time.After(grace):
			_ = cmd.Process.Kill()
			err = <-wait
		}
	}
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("runner %q failed without a valid result: %w", spec.ID, err)
	}
	var result LifecycleResult
	if err := decodeClosed(stdout.Bytes(), &result); err != nil {
		return LifecycleResult{}, fmt.Errorf("runner %q returned invalid JSON: %w", spec.ID, err)
	}
	return result, nil
}

func (r ExecutableRunner) verifiedCommand(spec RunnerSpec) (string, []string, error) {
	artifact, err := r.resolve(spec.ID)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(artifact)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrRunnerUnavailable, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%w: %s is not a regular file", ErrRunnerIntegrity, artifact)
	}
	body, err := os.ReadFile(artifact)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrRunnerUnavailable, err)
	}
	if got := digestBytes(body); got != spec.Digest {
		return "", nil, fmt.Errorf("%w: runner %q got %s, want %s", ErrRunnerIntegrity, spec.ID, got, spec.Digest)
	}
	command := []string{artifact}
	if filepath.Ext(artifact) == ".py" {
		python, lookErr := exec.LookPath("python3")
		if lookErr != nil {
			python, lookErr = exec.LookPath("python")
		}
		if lookErr != nil {
			return "", nil, fmt.Errorf("%w: Python is not on PATH", ErrRunnerUnavailable)
		}
		command = []string{python, artifact}
	}
	return artifact, command, nil
}

func (r ExecutableRunner) resolve(id string) (string, error) {
	if r.Directory == "" {
		return "", ErrRunnerUnavailable
	}
	root, err := filepath.Abs(r.Directory)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRunnerUnavailable, err)
	}
	for _, name := range []string{id, id + ".exe", id + ".py"} {
		candidate := filepath.Join(root, name)
		if _, statErr := os.Lstat(candidate); statErr == nil {
			return candidate, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %w", ErrRunnerUnavailable, statErr)
		}
	}
	return "", fmt.Errorf("%w: runner %q is not installed in %s", ErrRunnerUnavailable, id, root)
}

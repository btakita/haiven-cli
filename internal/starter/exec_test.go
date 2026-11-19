package starter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const testRunnerEnv = "HAIVEN_STARTER_TEST_RUNNER"

func TestMain(m *testing.M) {
	if os.Getenv(testRunnerEnv) == "1" {
		_, _ = os.Stdout.WriteString("{\"api_version\":\"starter-runner/v1\",\"request_id\":\"request-1\",\"operation\":\"plan\",\"status\":\"planned\",\"changes\":[],\"artifacts\":[],\"resolved_sources\":{},\"manual_steps\":{\"completed\":[\"step-1\"],\"pending\":[]},\"warnings\":[],\"errors\":[]}\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExecutableRunnerVerifiesArtifactBeforeProtocolExchange(t *testing.T) {
	directory := t.TempDir()
	name := "test-runner"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	artifact := filepath.Join(directory, name)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, body, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(testRunnerEnv, "1")
	runner := ExecutableRunner{Directory: directory}
	spec := RunnerSpec{ID: "test-runner", Digest: digestBytes(body), ProtocolVersion: 1}
	request := RunnerRequest{APIVersion: RunnerSchema, RequestID: "request-1", Operation: RunnerPlan, Recipe: RecipeIdentity{ID: "test/recipe", Version: "1.0.0", Digest: digestBytes([]byte("recipe"))}, Destination: t.TempDir(), Parameters: map[string]string{}, Preview: true}
	result, err := runner.Run(context.Background(), spec, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPlanned || result.RequestID != request.RequestID {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestExecutableRunnerRejectsMissingAndChangedArtifacts(t *testing.T) {
	directory := t.TempDir()
	runner := ExecutableRunner{Directory: directory}
	spec := RunnerSpec{ID: "test-runner", Digest: digestBytes([]byte("trusted")), ProtocolVersion: 1}
	if _, err := runner.Run(context.Background(), spec, RunnerRequest{}); !errors.Is(err, ErrRunnerUnavailable) {
		t.Fatalf("missing artifact error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, spec.ID), []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), spec, RunnerRequest{}); !errors.Is(err, ErrRunnerIntegrity) {
		t.Fatalf("changed artifact error = %v", err)
	}
}

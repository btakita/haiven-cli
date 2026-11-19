package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/haiven-dev/haiven-cli/internal/starter"
)

const lifecycleTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type lifecycleDemo struct {
	starterID     string
	recipeID      string
	engine        string
	engineVersion string
	guidePath     string
	guideDigest   string
	buildStep     string
	runStep       string
}

type lifecycleRecordingRunner struct {
	platform string
	demos    map[string]lifecycleDemo
	calls    []starter.RunnerV2Request
	specs    []starter.RunnerSpec
}

func (r *lifecycleRecordingRunner) RunLifecycle(_ context.Context, spec starter.RunnerSpec, request starter.RunnerV2Request) (starter.LifecycleResult, error) {
	r.calls = append(r.calls, request)
	r.specs = append(r.specs, spec)
	demo, ok := r.demos[request.Recipe.ID]
	if !ok {
		return starter.LifecycleResult{}, fmt.Errorf("unexpected recipe %q", request.Recipe.ID)
	}

	result := map[string]any{
		"api_version": starter.LifecycleSchema,
		"request_id":  request.RequestID,
		"operation":   request.Operation,
		"status":      "succeeded",
		"warnings":    []string{},
		"errors":      []string{},
	}
	if request.Operation == starter.RunnerCapabilities {
		result["capabilities"] = map[string]any{
			"operations": map[string]any{
				"build": map[string]any{"supported": true, "reason_code": nil, "modes": []string{"editor"}},
				"run":   map[string]any{"supported": true, "reason_code": nil, "modes": []string{"editor", "player"}},
			},
			"host": map[string]any{
				"platform":       r.platform,
				"engine":         demo.engine,
				"engine_version": demo.engineVersion,
			},
			"cancellation": map[string]any{
				"intents":      []string{"interrupt", "terminate"},
				"process_tree": true,
				"grace_ms":     10_000,
			},
			"logs": map[string]any{
				"max_bytes":         1_048_576,
				"max_line_bytes":    16_384,
				"redaction_profile": "starter-runner-redaction/v1",
			},
			"manual_fallback": map[string]any{
				"guide_path":   demo.guidePath,
				"guide_digest": demo.guideDigest,
				"operation_steps": map[string]any{
					"build": []string{demo.buildStep},
					"run":   []string{demo.runStep},
				},
			},
		}
	} else {
		step := demo.buildStep
		if request.Operation == starter.RunnerRun {
			step = demo.runStep
		}
		result["artifacts"] = []any{}
		result["exit_evidence"] = map[string]any{
			"started":                 true,
			"outcome":                 "exited",
			"exit_code":               0,
			"signal":                  nil,
			"started_at":              "2026-10-19T12:00:00Z",
			"finished_at":             "2026-10-19T12:00:00.001Z",
			"duration_ms":             1,
			"command_digest":          lifecycleTestDigest,
			"process_tree_terminated": true,
		}
		result["logs"] = []any{map[string]any{
			"stream":     "stdout",
			"digest":     lifecycleTestDigest,
			"bytes":      0,
			"truncated":  false,
			"redactions": 0,
		}}
		result["manual_steps"] = map[string]any{"completed": []string{step}, "pending": []string{}}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return starter.LifecycleResult{}, err
	}
	var decoded starter.LifecycleResult
	if err := json.Unmarshal(body, &decoded); err != nil {
		return starter.LifecycleResult{}, err
	}
	return decoded, nil
}

func TestStarterRunUsesOneCommandForEverySupportedDemoRecipe(t *testing.T) {
	platform := nativeStarterPlatform(t)
	source, demos := demoLifecycleCatalog(t)

	for _, demo := range demos {
		t.Run(demo.starterID, func(t *testing.T) {
			runner := &lifecycleRecordingRunner{
				platform: platform,
				demos:    map[string]lifecycleDemo{demo.recipeID: demo},
			}
			got := invocation{
				args:            []string{"sdk", "starter", "run", demo.starterID, "--project", "game", "--json"},
				files:           fixture{"game/project.txt": "native project\n"},
				starterCatalog:  source,
				lifecycleRunner: runner,
			}.run(t)
			if got.exit != 0 || got.stderr != "" {
				t.Fatalf("exit %d stderr %q stdout %s", got.exit, got.stderr, got.stdout)
			}
			if operations := lifecycleOperations(runner.calls); !equalOperations(operations, []starter.RunnerOperation{
				starter.RunnerCapabilities, starter.RunnerBuild, starter.RunnerRun,
			}) {
				t.Fatalf("operations = %v, want capabilities, build, run", operations)
			}
			for index, request := range runner.calls {
				if request.APIVersion != starter.LifecycleSchema || request.Recipe.ID != demo.recipeID || request.Recipe.Digest == "" {
					t.Fatalf("request %d is not pinned to the v2 demo recipe: %+v", index, request)
				}
				if request.Project == nil || !filepath.IsAbs(*request.Project) || request.Destination != nil || request.Preview {
					t.Fatalf("request %d has an unsafe lifecycle target: %+v", index, request)
				}
			}
			for index, spec := range runner.specs {
				if spec.ProtocolVersion != 2 || spec.ID == "" || spec.Digest == "" {
					t.Fatalf("runner spec %d is not a verified v2 runner: %+v", index, spec)
				}
			}
			if !strings.Contains(got.stdout, `"status": "succeeded"`) || !strings.Contains(got.stdout, `"operation": "run"`) {
				t.Fatalf("run evidence is incomplete: %s", got.stdout)
			}
		})
	}
}

func nativeStarterPlatform(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "windows":
		return runtime.GOOS
	case "darwin":
		return "macos"
	default:
		t.Skipf("starter demo lifecycle is qualified on Linux, macOS, and Windows, not %s", runtime.GOOS)
		return ""
	}
}

func lifecycleOperations(requests []starter.RunnerV2Request) []starter.RunnerOperation {
	operations := make([]starter.RunnerOperation, 0, len(requests))
	for _, request := range requests {
		operations = append(operations, request.Operation)
	}
	return operations
}

func equalOperations(got, want []starter.RunnerOperation) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func demoLifecycleCatalog(t *testing.T) (starter.CatalogSource, []lifecycleDemo) {
	t.Helper()
	artifact := starter.CatalogArtifact{Guides: map[string][]byte{}, Recipes: map[string][]byte{}}
	demos := []lifecycleDemo{
		{starterID: "unity-bossroom", recipeID: "unity-bossroom-assemble", engine: "unity", engineVersion: "2022.3.22f1"},
		{starterID: "unreal-lyra", recipeID: "unreal-lyra-assemble", engine: "unreal", engineVersion: "5.8.2"},
	}
	starters := make([]any, 0, len(demos))
	for index := range demos {
		demo := &demos[index]
		slug := strings.ReplaceAll(demo.recipeID, "/", "-")
		demo.guidePath = "MANUAL.md"
		demo.buildStep = slug + "-build"
		demo.runStep = slug + "-run"
		guide := demoLifecycleGuide(t, slug)
		demo.guideDigest = digestBytes(guide)
		recipe := map[string]any{
			"api_version":         starter.RecipeProtocol,
			"id":                  demo.recipeID,
			"version":             "1.0.0",
			"description":         "Cross-platform lifecycle fixture for " + demo.starterID,
			"runner":              map[string]any{"id": slug + "-runner", "digest": lifecycleTestDigest, "protocol_version": 2},
			"operations":          []string{"plan", "apply", "verify", "build", "run"},
			"parameters":          []any{},
			"requires":            []string{},
			"sources":             []any{},
			"prerequisites":       []string{},
			"network_hosts":       []string{},
			"outputs":             []any{map[string]any{"path": "generated", "scope": "tree", "action": "create"}},
			"licenses":            []string{},
			"access_requirements": []string{},
			"manual": map[string]any{
				"guide_path":   demo.guidePath,
				"guide_digest": demo.guideDigest,
				"operation_steps": map[string]any{
					"plan":   []string{slug + "-plan"},
					"apply":  []string{slug + "-apply"},
					"verify": []string{slug + "-verify"},
					"build":  []string{demo.buildStep},
					"run":    []string{demo.runStep},
				},
			},
		}
		recipeBody := mustLifecycleJSON(t, recipe)
		recipePath := "recipes/" + slug + "/recipe.json"
		artifact.Recipes[recipePath] = recipeBody
		artifact.Guides["recipes/"+slug+"/"+demo.guidePath] = guide
		starters = append(starters, map[string]any{
			"id": demo.starterID, "display_name": demo.starterID, "description": "Lifecycle demo fixture",
			"engine": demo.engine, "journey": "sample", "maturity": "preview",
			"supported_hosts": []string{"linux", "macos", "windows"}, "engine_versions": []string{demo.engineVersion},
			"features": []string{"build", "run"}, "language": "fixture", "topology": "fixture", "workflow": "fixture",
			"access": "test", "licenses": []string{},
			"recipes": []any{map[string]any{"id": demo.recipeID, "version": "1.0.0", "digest": digestBytes(recipeBody)}},
			"edges":   []any{}, "inputs": []any{},
		})
	}
	artifact.Bytes = mustLifecycleJSON(t, map[string]any{
		"api_version": starter.CatalogSchema,
		"id":          "lifecycle-e2e",
		"version":     "1.0.0",
		"protocol":    map[string]any{"minimum": 1, "maximum": 2},
		"starters":    starters,
	})
	artifact.ExpectedDigest = digestBytes(artifact.Bytes)
	return staticCatalogSource{artifact: artifact}, demos
}

func demoLifecycleGuide(t *testing.T, slug string) []byte {
	t.Helper()
	var guide strings.Builder
	guide.WriteString("# Lifecycle fixture\n\n")
	for _, operation := range []string{"plan", "apply", "verify", "build", "run"} {
		mutations := []string{}
		if operation == "apply" {
			mutations = []string{"generated"}
		}
		step := mustLifecycleJSON(t, map[string]any{
			"id": slug + "-" + operation, "operation": operation, "native_tool": "fixture runner",
			"action":              "Exercise the " + operation + " lifecycle operation.",
			"preconditions":       []string{"The fixture project exists."},
			"expected_outcome":    "The fixture operation succeeds.",
			"evidence":            []string{"The runner returns lifecycle evidence."},
			"requires_haiven_cli": false, "mutations": mutations,
		})
		guide.WriteString("```haiven-manual-step\n")
		guide.Write(step)
		guide.WriteString("\n```\n\n")
	}
	return []byte(guide.String())
}

func mustLifecycleJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

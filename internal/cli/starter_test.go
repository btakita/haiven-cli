package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/haiven-dev/haiven-cli/internal/result"
	"github.com/haiven-dev/haiven-cli/internal/starter"
)

type fakeStarterRunner struct {
	calls       int
	automated   bool
	drift       bool
	manualApply bool
}

func (f *fakeStarterRunner) Run(_ context.Context, _ starter.RunnerSpec, request starter.RunnerRequest) (starter.RunnerResult, error) {
	f.calls++
	if !f.automated {
		return starter.RunnerResult{}, starter.ErrRunnerUnavailable
	}
	steps := map[starter.RunnerOperation][]string{
		starter.RunnerPlan:   {"prerequisites"},
		starter.RunnerApply:  {"acquire-boss-room", "check-pinned-packages", "configure-demo", "open-in-unity"},
		starter.RunnerVerify: {"verify-checkout", "play-sample", "record-evidence"},
	}[request.Operation]
	status := map[starter.RunnerOperation]starter.RunnerStatus{
		starter.RunnerPlan: starter.StatusPlanned, starter.RunnerApply: starter.StatusApplied, starter.RunnerVerify: starter.StatusVerified,
	}[request.Operation]
	output := starter.RunnerResult{
		APIVersion: starter.RunnerSchema, RequestID: request.RequestID, Operation: request.Operation, Status: status,
		Changes: []starter.RunnerChange{}, Artifacts: []starter.Artifact{}, ResolvedSources: map[string]string{},
		ManualSteps: starter.ManualProgress{Completed: steps, Pending: []string{}}, Warnings: []string{}, Errors: []string{},
	}
	if request.Operation == starter.RunnerApply {
		if err := os.MkdirAll(filepath.Join(request.Destination, "BossRoom"), 0o755); err != nil {
			return starter.RunnerResult{}, err
		}
		if err := os.WriteFile(filepath.Join(request.Destination, "BossRoom", "game.txt"), []byte("created\n"), 0o644); err != nil {
			return starter.RunnerResult{}, err
		}
		output.Changes = []starter.RunnerChange{{Path: "BossRoom", Action: "create"}}
		output.Artifacts = []starter.Artifact{{Path: "BossRoom/game.txt", Digest: "sha256:169c67a03ed3ad9bf911acc7daadae2a4bf50ccd205b7b47e29d79e9d65ef0c4", Owner: request.Recipe.ID}}
		if f.manualApply {
			output.Status = starter.StatusManualRequired
			output.ManualSteps = starter.ManualProgress{Completed: steps[:2], Pending: steps[2:]}
		}
	}
	if request.Operation == starter.RunnerVerify && f.drift {
		output.Status = starter.StatusDrift
	}
	return output, nil
}

func decodeStarterEnvelope(t *testing.T, got ran) result.Envelope {
	t.Helper()
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatalf("decode result: %v\n%s", err, got.stdout)
	}
	return envelope
}

func TestStarterListFiltersEmbeddedCatalogAndExposesManualRefs(t *testing.T) {
	got := invocation{args: []string{"sdk", "starter", "list", "--engine", "unity", "--json"}}.run(t)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("exit %d stderr %q stdout %s", got.exit, got.stderr, got.stdout)
	}
	var envelope struct {
		Data StarterListData `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Starters) != 2 || envelope.Data.CatalogDigest != starter.EmbeddedDigest {
		t.Fatalf("unexpected list: %+v", envelope.Data)
	}
	starters := map[string]StarterListEntry{}
	for _, entry := range envelope.Data.Starters {
		starters[entry.Starter.ID] = entry
	}
	for _, id := range []string{"unity-bossroom", "unity-existing"} {
		entry, ok := starters[id]
		if !ok {
			t.Fatalf("starter %q absent: %+v", id, envelope.Data.Starters)
		}
		if len(entry.Manual) != 3 || entry.Manual[0].RequiresHaivenCLI || entry.Manual[0].GuideDigest == "" || len(entry.Manual[0].StepIDs) == 0 {
			t.Fatalf("manual contract absent for %q: %+v", id, entry.Manual)
		}
	}
}

func TestStarterDescribeUnityExistingPinsSDKRecipeAndRunner(t *testing.T) {
	got := invocation{args: []string{"sdk", "starter", "describe", "unity-existing", "--json"}}.run(t)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("exit %d stderr %q stdout %s", got.exit, got.stderr, got.stdout)
	}
	var envelope struct {
		Data StarterDescribeData `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope.Data
	if data.CatalogDigest != starter.EmbeddedDigest || data.Starter.ID != "unity-existing" || data.Starter.Journey != "integrate" {
		t.Fatalf("unexpected starter identity: %+v", data)
	}
	if len(data.Recipes) != 1 || data.Recipes[0].Digest != "sha256:c00e61831837abf4c6c39c3cddfc9888e968849fdc671acd8573e5be9780f585" {
		t.Fatalf("unexpected recipe: %+v", data.Recipes)
	}
	runner := data.Recipes[0].Runner
	if runner == nil || runner.ID != "haiven-unity-existing" || runner.Digest != "sha256:27a7710a693406866cea6efbdd5242884566aee91231715a974fe7d5d6b0b067" || runner.ProtocolVersion != 1 {
		t.Fatalf("unexpected runner: %+v", runner)
	}
	if len(data.Guides) != 1 || data.Guides[0].Digest != "sha256:94b747fb12e051e0ecfa081f9e25aaef1169bf1a8781426cb96b7069262014af" {
		t.Fatalf("unexpected guide: %+v", data.Guides)
	}
}

func TestStarterDescribeIncludesImmutableGuide(t *testing.T) {
	got := invocation{args: []string{"sdk", "starter", "describe", "unity-bossroom", "--json"}}.run(t)
	if got.exit != 0 {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	var envelope struct {
		Data StarterDescribeData `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Guides) != 1 || !strings.Contains(envelope.Data.Guides[0].Content, `"requires_haiven_cli": false`) {
		t.Fatalf("guide missing: %+v", envelope.Data.Guides)
	}
}

func TestStarterDescribeUnknown(t *testing.T) {
	got := invocation{args: []string{"sdk", "starter", "describe", "missing", "--json"}}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagStarterUnknown {
		t.Fatalf("exit %d envelope %+v", got.exit, envelope)
	}
}

func TestStarterCatalogDigestIsTrustRoot(t *testing.T) {
	source := staticCatalogSource{starter.CatalogArtifact{Bytes: []byte(`{"api_version":"starter-catalog/v1"}`), ExpectedDigest: starter.EmbeddedDigest}}
	got := invocation{args: []string{"sdk", "starter", "list", "--json"}, starterCatalog: source}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitIntegrity) || envelope.Diagnostics[0].Code != result.DiagStarterCatalogUntrusted {
		t.Fatalf("exit %d envelope %+v", got.exit, envelope)
	}
}

type staticCatalogSource struct{ artifact starter.CatalogArtifact }

func (s staticCatalogSource) Load(context.Context) (starter.CatalogArtifact, error) {
	return s.artifact, nil
}

func automatedCatalogSource(t *testing.T) starter.CatalogSource {
	t.Helper()
	artifact, err := (starter.EmbeddedSource{}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const recipeName = "recipes/unity-bossroom/recipe.json"
	var recipe starter.Recipe
	if err := json.Unmarshal(artifact.Recipes[recipeName], &recipe); err != nil {
		t.Fatal(err)
	}
	recipe.Runner = &starter.RunnerSpec{ID: "test-runner", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProtocolVersion: 1}
	recipeBytes, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	artifact.Recipes[recipeName] = recipeBytes
	var catalog starter.Catalog
	if err := json.Unmarshal(artifact.Bytes, &catalog); err != nil {
		t.Fatal(err)
	}
	for starterIndex := range catalog.Starters {
		if catalog.Starters[starterIndex].ID == "unity-bossroom" {
			catalog.Starters[starterIndex].Journey = "existing-project"
		}
		for recipeIndex := range catalog.Starters[starterIndex].Recipes {
			if catalog.Starters[starterIndex].Recipes[recipeIndex].ID == recipe.ID {
				catalog.Starters[starterIndex].Recipes[recipeIndex].Digest = digestBytes(recipeBytes)
			}
		}
	}
	artifact.Bytes, err = json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	artifact.ExpectedDigest = digestBytes(artifact.Bytes)
	return staticCatalogSource{artifact: artifact}
}

func TestStarterBossRoomManualHandoffHasZeroMutations(t *testing.T) {
	for _, mode := range []string{"--preview", "--yes"} {
		t.Run(mode, func(t *testing.T) {
			runner := &fakeStarterRunner{}
			dest := filepath.Join(t.TempDir(), "new-game")
			got := invocation{args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", dest, mode, "--json"}, starterRunner: runner}.run(t)
			envelope := decodeStarterEnvelope(t, got)
			if got.exit != int(result.ExitEnvironment) || envelope.Diagnostics[0].Code != result.DiagStarterManualRequired || runner.calls != 0 {
				t.Fatalf("exit=%d calls=%d envelope=%+v", got.exit, runner.calls, envelope)
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("manual handoff mutated destination: %v", err)
			}
			if !strings.Contains(got.stdout, `"status": "manual-required"`) || !strings.Contains(got.stdout, `"pending": [`) || !strings.Contains(got.stdout, `"requires_haiven_cli": false`) {
				t.Fatalf("manual evidence absent: %s", got.stdout)
			}
		})
	}
}

func TestStarterCreateRequiresConfirmation(t *testing.T) {
	runner := &fakeStarterRunner{}
	got := invocation{args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", "new-game", "--json"}, starterRunner: runner}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagStarterConfirmationRequired || runner.calls != 0 {
		t.Fatalf("exit %d calls %d envelope %+v", got.exit, runner.calls, envelope)
	}
}

func TestStarterOptionalAutomationWritesExactLockAndVerifies(t *testing.T) {
	source := automatedCatalogSource(t)
	runner := &fakeStarterRunner{automated: true}
	dest := filepath.Join(t.TempDir(), "new-game")
	created := invocation{
		args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", dest, "--yes", "--json"}, starterCatalog: source, starterRunner: runner,
	}.run(t)
	envelope := decodeStarterEnvelope(t, created)
	if created.exit != 0 || envelope.LockDigest == "" || runner.calls != 2 {
		t.Fatalf("create exit=%d calls=%d envelope=%+v", created.exit, runner.calls, envelope)
	}
	lock, _, err := readStarterLock(dest)
	if err != nil {
		t.Fatal(err)
	}
	if lock.APIVersion != starter.LockSchema || lock.Starter.ID != "unity-bossroom" || len(lock.Recipes) != 1 || lock.Recipes[0].RunnerDigest == "" {
		t.Fatalf("unexpected lock: %+v", lock)
	}
	runner.drift = true
	verified := invocation{args: []string{"sdk", "starter", "verify", "--project", dest, "--json"}, starterCatalog: source, starterRunner: runner}.run(t)
	verifyEnvelope := decodeStarterEnvelope(t, verified)
	if verified.exit != int(result.ExitCheckFailed) || verifyEnvelope.Diagnostics[0].Code != result.DiagStarterDrift || runner.calls != 3 {
		t.Fatalf("verify exit=%d calls=%d envelope=%+v", verified.exit, runner.calls, verifyEnvelope)
	}
}

func TestStarterManualRequiredApplyPublishesSafeWorkAndLock(t *testing.T) {
	source := automatedCatalogSource(t)
	runner := &fakeStarterRunner{automated: true, manualApply: true}
	dest := filepath.Join(t.TempDir(), "new-game")
	created := invocation{
		args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", dest, "--yes", "--json"}, starterCatalog: source, starterRunner: runner,
	}.run(t)
	envelope := decodeStarterEnvelope(t, created)
	if created.exit != int(result.ExitEnvironment) || envelope.Diagnostics[0].Code != result.DiagStarterManualRequired || envelope.LockDigest == "" {
		t.Fatalf("create exit=%d envelope=%+v", created.exit, envelope)
	}
	if _, err := os.Stat(filepath.Join(dest, "BossRoom", "game.txt")); err != nil {
		t.Fatalf("validated automated work was not published: %v", err)
	}
	if _, _, err := readStarterLock(dest); err != nil {
		t.Fatalf("manual-required apply did not write a rerun lock: %v", err)
	}
}

func TestStarterIntegrateDirtyTreePreviewStillManualRequired(t *testing.T) {
	runner := &fakeStarterRunner{}
	source := automatedCatalogSource(t)
	preview := invocation{
		args: []string{"sdk", "starter", "integrate", "unity-bossroom", "--project", "game", "--preview", "--json"}, files: fixture{"game/project.txt": "owned\n"},
		starterCatalog: source, starterRunner: runner, gitDirty: func(context.Context, string) (bool, error) { return true, nil },
	}.run(t)
	if preview.exit != int(result.ExitEnvironment) || runner.calls != 1 || !strings.Contains(preview.stdout, `"dirty_project": true`) {
		t.Fatalf("preview exit=%d calls=%d: %s", preview.exit, runner.calls, preview.stdout)
	}
	apply := invocation{
		args: []string{"sdk", "starter", "integrate", "unity-bossroom", "--project", "game", "--yes", "--json"}, files: fixture{"game/project.txt": "owned\n"},
		starterCatalog: source, starterRunner: runner, gitDirty: func(context.Context, string) (bool, error) { return true, nil },
	}.run(t)
	envelope := decodeStarterEnvelope(t, apply)
	if apply.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagStarterDirtyProject {
		t.Fatalf("apply exit=%d envelope=%+v", apply.exit, envelope)
	}
}

func TestStarterSampleCannotClaimExistingProjectAutomation(t *testing.T) {
	got := invocation{
		args:  []string{"sdk", "starter", "integrate", "unity-bossroom", "--project", "game", "--yes", "--json"},
		files: fixture{"game/project.txt": "owned\n"}, gitDirty: func(context.Context, string) (bool, error) { return false, nil },
	}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagStarterJourneyUnsupported {
		t.Fatalf("integrate exit=%d envelope=%+v", got.exit, envelope)
	}
}

func TestStarterVerifyWithoutLockUsesNormativeGuide(t *testing.T) {
	runner := &fakeStarterRunner{}
	verified := invocation{
		args:  []string{"sdk", "starter", "verify", "--project", "game", "--starter", "unity-bossroom", "--json"},
		files: fixture{"game/project.txt": "native project\n"}, starterRunner: runner,
	}.run(t)
	envelope := decodeStarterEnvelope(t, verified)
	if verified.exit != int(result.ExitEnvironment) || envelope.Diagnostics[0].Code != result.DiagStarterManualRequired || runner.calls != 0 {
		t.Fatalf("verify exit=%d calls=%d envelope=%+v", verified.exit, runner.calls, envelope)
	}
	if !strings.Contains(verified.stdout, `"verify-checkout"`) || strings.Contains(verified.stdout, `"lock":`) {
		t.Fatalf("native verification evidence is incomplete: %s", verified.stdout)
	}
}

func TestStarterIntegrateRejectsChangedLockWithoutWriting(t *testing.T) {
	source := automatedCatalogSource(t)
	runner := &fakeStarterRunner{automated: true}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "game"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "game", "keep.txt"), []byte("owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const revA = "28193c29b4115fdf37bd431e027400f56e340629"
	const revB = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	run := func(args ...string) ran {
		t.Helper()
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), Env{
			Stdout: &stdout, Stderr: &stderr,
			Getenv:         func(string) string { return "" },
			Cwd:            root,
			Version:        testVersion,
			GOOS:           "linux",
			GOARCH:         "amd64",
			StarterCatalog: source,
			StarterRunner:  runner,
			GitDirty:       func(context.Context, string) (bool, error) { return false, nil },
		}, args)
		return ran{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}
	base := []string{"sdk", "starter", "integrate", "unity-bossroom", "--project", "game", "--yes", "--json"}
	first := run(append(base, "--param", "sdk_revision="+revA)...)
	if first.exit != 0 || runner.calls != 2 {
		t.Fatalf("first exit=%d calls=%d stdout=%s", first.exit, runner.calls, first.stdout)
	}
	lockPath := filepath.Join(root, "game", ".haiven", "starter.lock.json")
	locked, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Contains(locked, []byte(revA)) {
		t.Fatalf("lock missing revision A: %v\n%s", err, locked)
	}
	rerun := run(append(base, "--param", "sdk_revision="+revA)...)
	if rerun.exit != 0 || runner.calls != 4 {
		t.Fatalf("rerun exit=%d calls=%d stdout=%s", rerun.exit, runner.calls, rerun.stdout)
	}
	afterRerun, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := run(append(base, "--param", "sdk_revision="+revB)...)
	envelope := decodeStarterEnvelope(t, changed)
	if changed.exit != int(result.ExitUsage) || len(envelope.Diagnostics) == 0 || envelope.Diagnostics[0].Code != result.DiagStarterLockChanged || runner.calls != 4 {
		t.Fatalf("changed exit=%d calls=%d envelope=%+v", changed.exit, runner.calls, envelope)
	}
	got, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(got, afterRerun) {
		t.Fatalf("changed parameters rewrote the lock: %v", err)
	}
	other := run("sdk", "starter", "integrate", "unity-existing", "--project", "game", "--param", "sdk_revision="+revA, "--yes", "--json")
	otherEnvelope := decodeStarterEnvelope(t, other)
	if other.exit != int(result.ExitUsage) || otherEnvelope.Diagnostics[0].Code != result.DiagStarterLockChanged || runner.calls != 4 {
		t.Fatalf("other starter exit=%d calls=%d envelope=%+v", other.exit, runner.calls, otherEnvelope)
	}
	if !bytes.Equal(mustRead(t, lockPath), afterRerun) {
		t.Fatal("other starter rewrote the lock")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestStarterRejectsSecretArg(t *testing.T) {
	got := invocation{args: []string{"sdk", "starter", "create", "unity-bossroom", "--dest", "game", "--preview", "--param", "api_token=secret", "--json"}}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagInvalidFlag {
		t.Fatalf("secret arg: exit=%d envelope=%+v", got.exit, envelope)
	}
}

type fakeLifecycleRunner struct {
	requests []starter.RunnerV2Request
}

func (f *fakeLifecycleRunner) RunLifecycle(_ context.Context, _ starter.RunnerSpec, request starter.RunnerV2Request) (starter.LifecycleResult, error) {
	f.requests = append(f.requests, request)
	base := starter.LifecycleResult{
		APIVersion: starter.LifecycleSchema, RequestID: request.RequestID, Operation: request.Operation,
		Status: starter.StatusSucceeded, Warnings: []string{}, Errors: []string{},
	}
	if request.Operation == starter.RunnerCapabilities {
		engineVersion := "6000.0"
		base.Capabilities = &starter.Capabilities{
			Operations: starter.LifecycleOperationCapabilities{
				Build: starter.OperationCapability{Supported: true, Modes: []string{"editor"}},
				Run:   starter.OperationCapability{Supported: true, Modes: []string{"editor"}},
			},
			Host: starter.RunnerHost{Platform: "linux", Engine: "unity", EngineVersion: &engineVersion},
			Cancellation: starter.CancellationCapability{
				Intents:     []starter.CancellationIntent{starter.CancellationInterrupt, starter.CancellationTerminate},
				ProcessTree: true, GraceMS: 250,
			},
			Logs: starter.LogCapability{MaxBytes: 4096, MaxLineBytes: 1024, RedactionProfile: "haiven-secrets/v1"},
			ManualFallback: starter.ManualFallback{
				GuidePath: "MANUAL.md", GuideDigest: lifecycleGuideDigest,
				OperationSteps: starter.LifecycleOperationSteps{Build: []string{"build-demo"}, Run: []string{"run-demo"}},
			},
		}
		return base, nil
	}
	artifacts, logs := []starter.Artifact{}, []starter.LogEvidence{}
	steps := []string{"build-demo"}
	if request.Operation == starter.RunnerRun {
		steps = []string{"run-demo"}
	}
	progress := starter.ManualProgress{Completed: steps, Pending: []string{}}
	zero, commandDigest := 0, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	started, finished := time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC()
	base.Artifacts, base.Logs, base.ManualSteps = &artifacts, &logs, &progress
	base.ExitEvidence = &starter.ExitEvidence{
		Started: true, Outcome: starter.ExitOutcomeExited, ExitCode: &zero,
		StartedAt: &started, FinishedAt: &finished, DurationMS: 1000,
		CommandDigest: &commandDigest, ProcessTreeTerminated: true,
	}
	return base, nil
}

var lifecycleGuideDigest string

func lifecycleCatalogSource(t *testing.T) starter.CatalogSource {
	t.Helper()
	artifact, err := (starter.EmbeddedSource{}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const recipeName = "recipes/unity-bossroom/recipe.json"
	var recipe starter.Recipe
	if err := json.Unmarshal(artifact.Recipes[recipeName], &recipe); err != nil {
		t.Fatal(err)
	}
	recipe.Runner = &starter.RunnerSpec{ID: "unity-runner", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProtocolVersion: 2}
	recipe.Operations = append(recipe.Operations, starter.RunnerBuild, starter.RunnerRun)
	recipe.Manual.OperationSteps[starter.RunnerBuild] = []string{"build-demo"}
	recipe.Manual.OperationSteps[starter.RunnerRun] = []string{"run-demo"}
	guideName := filepath.ToSlash(filepath.Join(filepath.Dir(recipeName), recipe.Manual.GuidePath))
	guide := append([]byte(nil), artifact.Guides[guideName]...)
	guide = append(guide, []byte("\n```haiven-manual-step\n{\"id\":\"build-demo\",\"operation\":\"build\",\"native_tool\":\"Unity Editor\",\"action\":\"Build the demo\",\"preconditions\":[\"project opens\"],\"expected_outcome\":\"build succeeds\",\"evidence\":[\"build log\"],\"requires_haiven_cli\":false,\"mutations\":[]}\n```\n\n```haiven-manual-step\n{\"id\":\"run-demo\",\"operation\":\"run\",\"native_tool\":\"Unity Editor\",\"action\":\"Run the demo\",\"preconditions\":[\"build succeeds\"],\"expected_outcome\":\"demo starts\",\"evidence\":[\"runtime log\"],\"requires_haiven_cli\":false,\"mutations\":[]}\n```\n")...)
	lifecycleGuideDigest = digestBytes(guide)
	recipe.Manual.GuideDigest = lifecycleGuideDigest
	artifact.Guides[guideName] = guide
	recipeBytes, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	artifact.Recipes[recipeName] = recipeBytes
	var catalog starter.Catalog
	if err := json.Unmarshal(artifact.Bytes, &catalog); err != nil {
		t.Fatal(err)
	}
	for starterIndex := range catalog.Starters {
		for recipeIndex := range catalog.Starters[starterIndex].Recipes {
			if catalog.Starters[starterIndex].Recipes[recipeIndex].ID == recipe.ID {
				catalog.Starters[starterIndex].Recipes[recipeIndex].Digest = digestBytes(recipeBytes)
			}
		}
	}
	artifact.Bytes, err = json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	artifact.ExpectedDigest = digestBytes(artifact.Bytes)
	return staticCatalogSource{artifact: artifact}
}

func TestStarterRunDiscoversThenBuildsThenRuns(t *testing.T) {
	runner := &fakeLifecycleRunner{}
	got := invocation{
		args:  []string{"sdk", "starter", "run", "unity-bossroom", "--project", "game", "--param", "configuration=development", "--json"},
		files: fixture{"game/project.txt": "demo\n"}, starterCatalog: lifecycleCatalogSource(t), lifecycleRunner: runner,
	}.run(t)
	if got.exit != 0 {
		t.Fatalf("run exit=%d stderr=%q stdout=%s", got.exit, got.stderr, got.stdout)
	}
	want := []starter.RunnerOperation{starter.RunnerCapabilities, starter.RunnerBuild, starter.RunnerRun}
	if len(runner.requests) != len(want) {
		t.Fatalf("requests=%+v", runner.requests)
	}
	for index, request := range runner.requests {
		if request.Operation != want[index] || request.APIVersion != starter.LifecycleSchema || request.Project == nil || !filepath.IsAbs(*request.Project) || request.Destination != nil || request.Preview {
			t.Fatalf("request %d = %+v", index, request)
		}
	}
	if got := runner.requests[1].Parameters["configuration"]; got != "development" {
		t.Fatalf("typed parameter = %#v", got)
	}
	if !strings.Contains(got.stdout, `"operation": "capabilities"`) || !strings.Contains(got.stdout, `"operation": "build"`) || !strings.Contains(got.stdout, `"operation": "run"`) {
		t.Fatalf("lifecycle evidence missing: %s", got.stdout)
	}
	if !strings.Contains(got.stdout, `"signal": null`) {
		t.Fatalf("normal exit did not serialize a null signal: %s", got.stdout)
	}
	inst, err := sjs.UnmarshalJSON(strings.NewReader(got.stdout))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := inst.(map[string]any)["data"]
	if err := compile(t, "sdk-starter-run").Validate(data); err != nil {
		t.Fatalf("normal-exit starter run does not match sdk-starter-run: %v", err)
	}
}

func TestStarterRunNeverDowngradesV1Recipe(t *testing.T) {
	runner := &fakeLifecycleRunner{}
	got := invocation{
		args:  []string{"sdk", "starter", "run", "unity-bossroom", "--project", "game", "--json"},
		files: fixture{"game/project.txt": "demo\n"}, lifecycleRunner: runner,
	}.run(t)
	envelope := decodeStarterEnvelope(t, got)
	if got.exit != int(result.ExitEnvironment) || envelope.Diagnostics[0].Code != result.DiagStarterRunnerUnavailable || len(runner.requests) != 0 {
		t.Fatalf("exit=%d requests=%d envelope=%+v", got.exit, len(runner.requests), envelope)
	}
}

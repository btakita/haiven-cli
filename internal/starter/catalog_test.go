package starter

import (
	"context"
	"errors"
	"testing"
)

func TestEmbeddedCatalogTrustFiltersAndManualGuide(t *testing.T) {
	catalog, err := Load(context.Background(), EmbeddedSource{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Digest != EmbeddedDigest {
		t.Fatalf("digest %s, want %s", catalog.Digest, EmbeddedDigest)
	}
	got := catalog.List(Filter{Engine: "unity", Platform: "linux", Feature: "chat"})
	if len(got) != 1 || got[0].ID != "unity-bossroom" {
		t.Fatalf("unexpected filtered starters: %+v", got)
	}
	_, recipes, err := catalog.Resolve("unity-bossroom")
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].EngineVersions) != 1 || got[0].EngineVersions[0] != "6000.0.52f1" {
		t.Fatalf("unexpected Boss Room editor pin: %v", got[0].EngineVersions)
	}
	if recipes[0].Runner != nil {
		t.Fatal("Boss Room advertises an automation runner for the retired assembler")
	}
	guide, ok := catalog.GuideFor(recipes[0].ID)
	if !ok || len(guide.Steps) != 8 {
		t.Fatalf("missing verified guide: %+v", guide)
	}
	for _, step := range guide.Steps {
		if step.RequiresHaivenCLI {
			t.Fatalf("step %q requires CLI", step.ID)
		}
	}
}

func TestCatalogRejectsDigestMismatch(t *testing.T) {
	_, err := Load(context.Background(), artifactSource{CatalogArtifact{Bytes: []byte("{}"), ExpectedDigest: EmbeddedDigest}})
	if !errors.Is(err, ErrCatalogIntegrity) {
		t.Fatalf("error %v, want catalog integrity", err)
	}
}

type artifactSource struct{ artifact CatalogArtifact }

func (s artifactSource) Load(context.Context) (CatalogArtifact, error) { return s.artifact, nil }

func TestProtocolRejectsMutationOutsideManualGuideAndPendingSuccess(t *testing.T) {
	catalog, err := Load(context.Background(), EmbeddedSource{})
	if err != nil {
		t.Fatal(err)
	}
	_, recipes, err := catalog.Resolve("unity-bossroom")
	if err != nil {
		t.Fatal(err)
	}
	recipe := recipes[0]
	req := RequestFor("unity-plan-0", RunnerPlan, recipe, "/game", map[string]string{}, true)
	manual := ManualRequiredResult(req, recipe)
	if err := ValidateRunnerResult(manual, req, recipe); err != nil {
		t.Fatalf("manual-required result rejected: %v", err)
	}
	bad := manual
	bad.Status = StatusPlanned
	if err := ValidateRunnerResult(bad, req, recipe); err == nil {
		t.Fatal("planned status with pending manual steps accepted")
	}
	apply := RequestFor("unity-apply-0", RunnerApply, recipe, "/game", map[string]string{}, false)
	unsafe := RunnerResult{
		APIVersion: RunnerSchema, RequestID: apply.RequestID, Operation: RunnerApply, Status: StatusApplied,
		Changes: []RunnerChange{{Path: "elsewhere", Action: "create"}}, Artifacts: []Artifact{}, ResolvedSources: map[string]string{},
		ManualSteps: ManualProgress{Completed: recipe.Manual.OperationSteps[RunnerApply], Pending: []string{}}, Warnings: []string{}, Errors: []string{},
	}
	if err := ValidateRunnerResult(unsafe, apply, recipe); err == nil {
		t.Fatal("mutation absent from manual guide accepted")
	}
}

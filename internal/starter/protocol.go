package starter

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes one exact starter-runner/v1 request. Implementations are
// supplied only after their optional recipe.runner artifact has been verified.
type Runner interface {
	Run(context.Context, RunnerSpec, RunnerRequest) (RunnerResult, error)
}

// LifecycleRunner executes starter-runner/v2 lifecycle requests. Keeping this
// separate from Runner prevents a build/run command from silently downgrading
// to the v1 onboarding protocol.
type LifecycleRunner interface {
	RunLifecycle(context.Context, RunnerSpec, RunnerV2Request) (LifecycleResult, error)
}

// RunnerOperation is one recipe lifecycle operation.
type RunnerOperation string

// Starter runner lifecycle operations.
const (
	RunnerPlan         RunnerOperation = "plan"
	RunnerApply        RunnerOperation = "apply"
	RunnerVerify       RunnerOperation = "verify"
	RunnerCapabilities RunnerOperation = "capabilities"
	RunnerBuild        RunnerOperation = "build"
	RunnerRun          RunnerOperation = "run"
)

func validRunnerOperation(operation RunnerOperation) bool {
	return operation == RunnerPlan || operation == RunnerApply || operation == RunnerVerify || operation == RunnerBuild || operation == RunnerRun
}

// RunnerStatus is the closed starter-runner/v1 result vocabulary.
type RunnerStatus string

// Starter runner result statuses.
const (
	StatusPlanned        RunnerStatus = "planned"
	StatusApplied        RunnerStatus = "applied"
	StatusVerified       RunnerStatus = "verified"
	StatusDrift          RunnerStatus = "drift"
	StatusManualRequired RunnerStatus = "manual-required"
	StatusFailed         RunnerStatus = "failed"
	StatusSucceeded      RunnerStatus = "succeeded"
	StatusCancelled      RunnerStatus = "cancelled"
	StatusUnsupported    RunnerStatus = "unsupported"
)

// RecipeIdentity is the immutable recipe identity sent to a runner.
type RecipeIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// RunnerRequest is the closed starter-runner/v1 request.
type RunnerRequest struct {
	APIVersion  string            `json:"api_version"`
	RequestID   string            `json:"request_id"`
	Operation   RunnerOperation   `json:"operation"`
	Recipe      RecipeIdentity    `json:"recipe"`
	Destination string            `json:"destination"`
	Parameters  map[string]string `json:"parameters"`
	Preview     bool              `json:"preview"`
}

// RunnerV2Request is the closed starter-runner/v2 request union. Parameters
// deliberately retain their JSON types rather than flattening them into
// command strings.
type RunnerV2Request struct {
	APIVersion  string          `json:"api_version"`
	RequestID   string          `json:"request_id"`
	Operation   RunnerOperation `json:"operation"`
	Recipe      RecipeRef       `json:"recipe"`
	Project     *string         `json:"project,omitempty"`
	Destination *string         `json:"destination,omitempty"`
	Parameters  map[string]any  `json:"parameters"`
	Preview     bool            `json:"preview"`
	// CancellationGraceMS is transport policy learned during capability
	// discovery. It is not part of the runner wire request.
	CancellationGraceMS int `json:"-"`
}

// OperationCapability reports whether and how a runner can perform one task.
type OperationCapability struct {
	Supported bool `json:"supported"`
	// A supported operation leaves this JSON null. The schema library does
	// not treat a pointer as nullable unless the tag says so.
	ReasonCode *CapabilityReasonCode `json:"reason_code" jsonschema:"nullable"`
	Modes      []string              `json:"modes"`
}

// CapabilityReasonCode is a stable runner-owned explanation for unsupported work.
type CapabilityReasonCode string

// CancellationIntent is a signal intent a runner accepts.
type CancellationIntent string

// ExitOutcome identifies how an engine task ended or failed to start.
type ExitOutcome string

// ExitSignal is the portable signal name recorded by a runner.
type ExitSignal string

// LogStream identifies the source represented by bounded log evidence.
type LogStream string

const (
	// CancellationInterrupt requests graceful interruption.
	CancellationInterrupt CancellationIntent = "interrupt"
	// CancellationTerminate requests graceful termination.
	CancellationTerminate CancellationIntent = "terminate"
	// ExitOutcomeExited means the process exited with an exit code.
	ExitOutcomeExited ExitOutcome = "exited"
	// ExitOutcomeSignaled means a signal ended the process.
	ExitOutcomeSignaled ExitOutcome = "signaled"
	// ExitOutcomeCancelled means requested cancellation ended the process tree.
	ExitOutcomeCancelled ExitOutcome = "cancelled"
	// ExitOutcomeLaunchFailed means process launch was attempted and failed.
	ExitOutcomeLaunchFailed ExitOutcome = "launch-failed"
	// ExitOutcomeNotStarted means capability or prerequisites prevented launch.
	ExitOutcomeNotStarted ExitOutcome = "not-started"
	// LogStdout identifies standard-output evidence.
	LogStdout LogStream = "stdout"
	// LogStderr identifies standard-error evidence.
	LogStderr LogStream = "stderr"
	// LogCombined identifies an intentionally merged output stream.
	LogCombined LogStream = "combined"
)

// RunnerHost describes the engine host discovered by the verified runner.
type RunnerHost struct {
	Platform      string  `json:"platform"`
	Engine        string  `json:"engine"`
	EngineVersion *string `json:"engine_version" jsonschema:"nullable"`
}

// CancellationCapability describes runner signal and process-tree behavior.
type CancellationCapability struct {
	Intents     []CancellationIntent `json:"intents"`
	ProcessTree bool                 `json:"process_tree"`
	GraceMS     int                  `json:"grace_ms"`
}

// LogCapability declares enforced output bounds and redaction policy.
type LogCapability struct {
	MaxBytes         int    `json:"max_bytes"`
	MaxLineBytes     int    `json:"max_line_bytes"`
	RedactionProfile string `json:"redaction_profile"`
}

// ManualFallback content-addresses native build and run steps.
type ManualFallback struct {
	GuidePath      string                  `json:"guide_path"`
	GuideDigest    string                  `json:"guide_digest"`
	OperationSteps LifecycleOperationSteps `json:"operation_steps"`
}

// LifecycleOperationSteps partitions fallback steps by lifecycle operation.
type LifecycleOperationSteps struct {
	Build []string `json:"build"`
	Run   []string `json:"run"`
}

// LifecycleOperationCapabilities contains build and run discovery results.
type LifecycleOperationCapabilities struct {
	Build OperationCapability `json:"build"`
	Run   OperationCapability `json:"run"`
}

// Capabilities is the starter-runner/v2 discovery payload.
type Capabilities struct {
	Operations     LifecycleOperationCapabilities `json:"operations"`
	Host           RunnerHost                     `json:"host"`
	Cancellation   CancellationCapability         `json:"cancellation"`
	Logs           LogCapability                  `json:"logs"`
	ManualFallback ManualFallback                 `json:"manual_fallback"`
}

// ExitEvidence records the complete disposition of an engine child process.
// Nil pointers marshal as JSON null and stay present: omitempty would drop
// a key the runner protocol always records. The schema tag accepts that null.
type ExitEvidence struct {
	Started               bool        `json:"started"`
	Outcome               ExitOutcome `json:"outcome"`
	ExitCode              *int        `json:"exit_code" jsonschema:"nullable"`
	Signal                *ExitSignal `json:"signal" jsonschema:"nullable"`
	StartedAt             *time.Time  `json:"started_at" jsonschema:"nullable"`
	FinishedAt            *time.Time  `json:"finished_at" jsonschema:"nullable"`
	DurationMS            int64       `json:"duration_ms"`
	CommandDigest         *string     `json:"command_digest" jsonschema:"nullable"`
	ProcessTreeTerminated bool        `json:"process_tree_terminated"`
}

// LogEvidence records one bounded, redacted runner-owned output stream.
type LogEvidence struct {
	Stream     LogStream `json:"stream"`
	Path       *string   `json:"path,omitempty"`
	Digest     string    `json:"digest"`
	Bytes      int64     `json:"bytes"`
	Truncated  bool      `json:"truncated"`
	Redactions int       `json:"redactions"`
}

// LifecycleResult is the closed starter-runner/v2 result union. Capability
// discovery populates Capabilities; build/run populate exit and log evidence.
type LifecycleResult struct {
	APIVersion   string          `json:"api_version"`
	RequestID    string          `json:"request_id"`
	Operation    RunnerOperation `json:"operation"`
	Status       RunnerStatus    `json:"status"`
	Capabilities *Capabilities   `json:"capabilities,omitempty"`
	Artifacts    *[]Artifact     `json:"artifacts,omitempty"`
	ExitEvidence *ExitEvidence   `json:"exit_evidence,omitempty"`
	Logs         *[]LogEvidence  `json:"logs,omitempty"`
	ManualSteps  *ManualProgress `json:"manual_steps,omitempty"`
	Warnings     []string        `json:"warnings"`
	Errors       []string        `json:"errors"`
}

// RunnerChange is one declared runner effect.
type RunnerChange struct {
	Path   string `json:"path"`
	Action string `json:"action" jsonschema:"enum=create,enum=replace,enum=none"`
}

// Artifact records one runner-owned output and its digest.
type Artifact struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Owner  string `json:"owner"`
}

// ManualProgress partitions the operation's stable manual step references.
type ManualProgress struct {
	Completed []string `json:"completed"`
	Pending   []string `json:"pending"`
}

// RunnerResult is the closed starter-runner/v1 result.
type RunnerResult struct {
	APIVersion      string            `json:"api_version"`
	RequestID       string            `json:"request_id"`
	Operation       RunnerOperation   `json:"operation"`
	Status          RunnerStatus      `json:"status"`
	Changes         []RunnerChange    `json:"changes"`
	Artifacts       []Artifact        `json:"artifacts"`
	ResolvedSources map[string]string `json:"resolved_sources"`
	ManualSteps     ManualProgress    `json:"manual_steps"`
	Warnings        []string          `json:"warnings"`
	Errors          []string          `json:"errors"`
}

// RunnerV2RequestFor constructs a capabilities, build, or run request for an
// absolute project path and one pinned recipe.
func RunnerV2RequestFor(requestID string, operation RunnerOperation, recipe Recipe, project string, parameters map[string]any) RunnerV2Request {
	return RunnerV2Request{
		APIVersion: LifecycleSchema, RequestID: requestID, Operation: operation,
		Recipe:  RecipeRef{ID: recipe.ID, Version: recipe.Version, Digest: recipe.Digest},
		Project: &project, Parameters: parameters, Preview: false,
	}
}

// ManualOperation exposes the normative guide and stable refs for one recipe
// operation. RequiresHaivenCLI is always false.
type ManualOperation struct {
	RecipeID          string          `json:"recipe_id"`
	Operation         RunnerOperation `json:"operation"`
	GuidePath         string          `json:"guide_path"`
	GuideDigest       string          `json:"guide_digest"`
	CanonicalURL      string          `json:"canonical_url,omitempty"`
	StepIDs           []string        `json:"step_ids"`
	RequiresHaivenCLI bool            `json:"requires_haiven_cli"`
}

// Plan is an aggregate, engine-neutral preview over a starter recipe graph.
type Plan struct {
	Schema        string            `json:"schema"`
	StarterID     string            `json:"starter_id"`
	Target        string            `json:"target"`
	CatalogDigest string            `json:"catalog_digest"`
	Manual        []ManualOperation `json:"manual"`
	Guides        []Guide           `json:"guides"`
	Results       []RunnerResult    `json:"results"`
}

// Evidence aggregates exact runner results for a starter operation.
type Evidence struct {
	Schema        string            `json:"schema"`
	StarterID     string            `json:"starter_id"`
	CatalogDigest string            `json:"catalog_digest"`
	Operation     RunnerOperation   `json:"operation"`
	Manual        []ManualOperation `json:"manual"`
	Guides        []Guide           `json:"guides"`
	Results       []RunnerResult    `json:"results"`
	Drift         bool              `json:"drift"`
}

// LockedRecipe is the recipe identity persisted in starter-lock/v1.
type LockedRecipe struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Digest       string `json:"digest"`
	RunnerDigest string `json:"runner_digest,omitempty"`
}

// LockedStarter binds a project to one immutable catalog entry.
type LockedStarter struct {
	ID             string `json:"id"`
	CatalogID      string `json:"catalog_id"`
	CatalogVersion string `json:"catalog_version"`
	CatalogDigest  string `json:"catalog_digest"`
}

// Lock records trusted, non-secret inputs and outputs. RunnerDigest is present
// exactly when the corresponding recipe declares optional automation.
type Lock struct {
	APIVersion string            `json:"api_version"`
	Starter    LockedStarter     `json:"starter"`
	Recipes    []LockedRecipe    `json:"recipes"`
	Resolved   map[string]string `json:"resolved"`
	Parameters map[string]string `json:"parameters"`
	Toolchains map[string]string `json:"toolchains"`
	Artifacts  []Artifact        `json:"artifacts"`
}

// RequestFor constructs a closed request for one trusted recipe.
func RequestFor(requestID string, operation RunnerOperation, recipe Recipe, destination string, parameters map[string]string, preview bool) RunnerRequest {
	return RunnerRequest{
		APIVersion: RunnerSchema, RequestID: requestID, Operation: operation,
		Recipe:      RecipeIdentity{ID: recipe.ID, Version: recipe.Version, Digest: recipe.Digest},
		Destination: destination, Parameters: parameters, Preview: preview,
	}
}

// ManualRequiredResult is the correct non-success result for a manual-only
// recipe or unavailable optional runner.
func ManualRequiredResult(req RunnerRequest, recipe Recipe) RunnerResult {
	pending := append([]string(nil), recipe.Manual.OperationSteps[req.Operation]...)
	changes := []RunnerChange{}
	if req.Operation == RunnerPlan {
		for _, output := range recipe.Outputs {
			changes = append(changes, RunnerChange{Path: output.Path, Action: output.Action})
		}
	}
	return RunnerResult{
		APIVersion: RunnerSchema, RequestID: req.RequestID, Operation: req.Operation,
		Status: StatusManualRequired, Changes: changes, Artifacts: []Artifact{},
		ResolvedSources: map[string]string{},
		ManualSteps:     ManualProgress{Completed: []string{}, Pending: pending},
		Warnings:        []string{}, Errors: []string{},
	}
}

// ManualFor returns the stable guide reference for one recipe operation.
func ManualFor(recipe Recipe, operation RunnerOperation) ManualOperation {
	return ManualOperation{
		RecipeID: recipe.ID, Operation: operation, GuidePath: recipe.Manual.GuidePath,
		GuideDigest: recipe.Manual.GuideDigest, CanonicalURL: recipe.Manual.CanonicalURL,
		StepIDs:           append([]string(nil), recipe.Manual.OperationSteps[operation]...),
		RequiresHaivenCLI: false,
	}
}

// ValidateRunnerResult checks correlation, status, manual-step partitioning,
// digests, and that every mutation is covered by the normative manual guide.
func ValidateRunnerResult(result RunnerResult, req RunnerRequest, recipe Recipe) error {
	if result.APIVersion != RunnerSchema || result.RequestID != req.RequestID || result.Operation != req.Operation {
		return fmt.Errorf("runner result does not correlate to its request")
	}
	if !statusAllowed(req.Operation, result.Status) {
		return fmt.Errorf("runner status %q is invalid for %s", result.Status, req.Operation)
	}
	if err := validateManualPartition(result.ManualSteps, recipe.Manual.OperationSteps[req.Operation], result.Status); err != nil {
		return err
	}
	allowed := recipeOutputPaths(recipe)
	for _, change := range result.Changes {
		if !safeRelativePath(change.Path) || !validChangeAction(change.Action) {
			return fmt.Errorf("runner result contains invalid change %q", change.Path)
		}
		if !pathCovered(change.Path, allowed) {
			return fmt.Errorf("runner mutation %q is absent from the manual guide", change.Path)
		}
		if req.Operation == RunnerVerify && change.Action != "none" {
			return fmt.Errorf("verify result declares mutation %q", change.Path)
		}
	}
	for _, artifact := range result.Artifacts {
		if !safeRelativePath(artifact.Path) || !validDigest(artifact.Digest) || artifact.Owner == "" || !pathCovered(artifact.Path, allowed) {
			return fmt.Errorf("runner result contains invalid artifact %q", artifact.Path)
		}
	}
	for id, revision := range result.ResolvedSources {
		if id == "" || revision == "" {
			return fmt.Errorf("runner result contains an invalid resolved source")
		}
	}
	return nil
}

// ValidateLifecycleResult validates v2 correlation and the security-critical
// discovery, fallback, exit-evidence, and bounded-log claims. Engine commands
// remain opaque to the CLI and are represented only by their digest.
func ValidateLifecycleResult(result LifecycleResult, req RunnerV2Request, recipe Recipe) error {
	if result.APIVersion != LifecycleSchema || result.RequestID != req.RequestID || result.Operation != req.Operation {
		return fmt.Errorf("runner result does not correlate to its lifecycle request")
	}
	if req.Project == nil || !filepath.IsAbs(*req.Project) || req.Destination != nil || req.Preview {
		return fmt.Errorf("lifecycle request project or preview is invalid")
	}
	if req.Operation == RunnerCapabilities {
		if result.Status != StatusSucceeded || result.Capabilities == nil || result.Artifacts != nil || result.ExitEvidence != nil || result.Logs != nil || result.ManualSteps != nil || result.Warnings == nil || result.Errors == nil {
			return fmt.Errorf("capability discovery result is incomplete")
		}
		caps := result.Capabilities
		if caps.Host.Platform == "" || caps.Host.Engine == "" || len(caps.Cancellation.Intents) == 0 || caps.Cancellation.GraceMS < 0 || caps.Logs.MaxBytes <= 0 || caps.Logs.MaxLineBytes <= 0 || caps.Logs.RedactionProfile == "" {
			return fmt.Errorf("capability discovery limits are invalid")
		}
		for _, capability := range []OperationCapability{caps.Operations.Build, caps.Operations.Run} {
			if (!capability.Supported && capability.ReasonCode == nil) || (capability.Supported && len(capability.Modes) == 0) {
				return fmt.Errorf("capability discovery has incomplete build/run support")
			}
		}
		if err := validateManualFallback(caps.ManualFallback, recipe, RunnerBuild, RunnerRun); err != nil {
			return err
		}
		return nil
	}
	if req.Operation != RunnerBuild && req.Operation != RunnerRun {
		return fmt.Errorf("unsupported lifecycle operation %q", req.Operation)
	}
	if result.Status != StatusSucceeded && result.Status != StatusFailed && result.Status != StatusCancelled && result.Status != StatusManualRequired && result.Status != StatusUnsupported {
		return fmt.Errorf("lifecycle status %q is invalid", result.Status)
	}
	if result.Capabilities != nil || result.Artifacts == nil || result.Logs == nil || result.ManualSteps == nil || result.Warnings == nil || result.Errors == nil || result.ExitEvidence == nil || !validExitEvidence(*result.ExitEvidence, result.Status) {
		return fmt.Errorf("lifecycle exit evidence is invalid")
	}
	if err := validateManualPartition(*result.ManualSteps, recipe.Manual.OperationSteps[req.Operation], result.Status); err != nil {
		return err
	}
	for _, log := range *result.Logs {
		if (log.Stream != LogStdout && log.Stream != LogStderr && log.Stream != LogCombined) || !validDigest(log.Digest) || log.Bytes < 0 || log.Redactions < 0 || (log.Path != nil && !safeRelativePath(*log.Path)) {
			return fmt.Errorf("lifecycle log evidence is invalid")
		}
	}
	for _, artifact := range *result.Artifacts {
		if !safeRelativePath(artifact.Path) || !validDigest(artifact.Digest) || artifact.Owner == "" {
			return fmt.Errorf("lifecycle artifact %q is invalid", artifact.Path)
		}
	}
	return nil
}

func validateManualFallback(fallback ManualFallback, recipe Recipe, operations ...RunnerOperation) error {
	if fallback.GuidePath != recipe.Manual.GuidePath || fallback.GuideDigest != recipe.Manual.GuideDigest {
		return fmt.Errorf("lifecycle manual fallback does not match the pinned guide")
	}
	for _, operation := range operations {
		want := recipe.Manual.OperationSteps[operation]
		got := fallback.OperationSteps.Build
		if operation == RunnerRun {
			got = fallback.OperationSteps.Run
		}
		if len(got) == 0 || len(got) != len(want) {
			return fmt.Errorf("lifecycle manual fallback omits %s steps", operation)
		}
		seen := map[string]bool{}
		for _, id := range got {
			if !containsString(want, id) || seen[id] {
				return fmt.Errorf("lifecycle manual fallback references an untrusted step")
			}
			seen[id] = true
		}
	}
	return nil
}

func validExitEvidence(evidence ExitEvidence, status RunnerStatus) bool {
	if evidence.DurationMS < 0 {
		return false
	}
	switch evidence.Outcome {
	case ExitOutcomeExited:
		return evidence.Started && evidence.CommandDigest != nil && validDigest(*evidence.CommandDigest) && evidence.StartedAt != nil && evidence.FinishedAt != nil && evidence.ExitCode != nil && ((status == StatusSucceeded && *evidence.ExitCode == 0) || (status == StatusFailed && *evidence.ExitCode != 0))
	case ExitOutcomeSignaled:
		return evidence.Started && evidence.CommandDigest != nil && validDigest(*evidence.CommandDigest) && evidence.Signal != nil && *evidence.Signal != "" && status == StatusFailed
	case ExitOutcomeCancelled:
		return evidence.Started && status == StatusCancelled && evidence.ProcessTreeTerminated
	case ExitOutcomeLaunchFailed:
		return !evidence.Started && (status == StatusFailed || status == StatusManualRequired || status == StatusUnsupported)
	case ExitOutcomeNotStarted:
		return !evidence.Started && (status == StatusManualRequired || status == StatusUnsupported)
	default:
		return false
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func statusAllowed(operation RunnerOperation, status RunnerStatus) bool {
	if status == StatusManualRequired || status == StatusFailed {
		return true
	}
	switch operation {
	case RunnerPlan:
		return status == StatusPlanned
	case RunnerApply:
		return status == StatusApplied
	case RunnerVerify:
		return status == StatusVerified || status == StatusDrift
	default:
		return false
	}
}

func validateManualPartition(progress ManualProgress, refs []string, status RunnerStatus) error {
	want := map[string]bool{}
	for _, id := range refs {
		want[id] = true
	}
	seen := map[string]bool{}
	for _, group := range [][]string{progress.Completed, progress.Pending} {
		for _, id := range group {
			if !want[id] || seen[id] {
				return fmt.Errorf("runner manual steps do not partition manifest references")
			}
			seen[id] = true
		}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("runner manual steps omit manifest references")
	}
	if (status == StatusPlanned || status == StatusApplied || status == StatusVerified || status == StatusDrift || status == StatusSucceeded) && len(progress.Pending) != 0 {
		return fmt.Errorf("successful runner status has pending manual steps")
	}
	if (status == StatusManualRequired || status == StatusUnsupported) && len(progress.Pending) == 0 {
		return fmt.Errorf("manual-required runner status has no pending steps")
	}
	return nil
}

func validChangeAction(action string) bool {
	return action == "create" || action == "replace" || action == "none"
}

func pathCovered(candidate string, roots []string) bool {
	for _, root := range roots {
		if candidate == root || strings.HasPrefix(candidate, root+"/") {
			return true
		}
	}
	return false
}

func safeRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// UnavailableRunner marks an absent optional runner.
type UnavailableRunner struct{}

// Run reports that no verified automation runner is installed.
func (UnavailableRunner) Run(context.Context, RunnerSpec, RunnerRequest) (RunnerResult, error) {
	return RunnerResult{}, ErrRunnerUnavailable
}

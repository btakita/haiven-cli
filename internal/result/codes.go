package result

import (
	"github.com/invopop/jsonschema"
)

// Every vocabulary in this file is part of the cli-result/v1 compatibility
// promise: codes, not messages. A released value is never renamed or removed;
// new values are additive. Prose (Summary, Fix, Message) stays free to change.

// Outcome is the run outcome of one command: the envelope's top-level "code".
type Outcome string

// The closed outcome vocabulary.
const (
	OutcomeOK                Outcome = "ok"
	OutcomeDrift             Outcome = "drift"
	OutcomeInvalidManifest   Outcome = "invalid-manifest"
	OutcomeLockMismatch      Outcome = "lock-mismatch"
	OutcomeConformanceFailed Outcome = "conformance-failed"
	OutcomeRunnerUnavailable Outcome = "runner-unavailable"
	OutcomeToolchainMissing  Outcome = "toolchain-missing"
	OutcomeUsage             Outcome = "usage"
	OutcomeInternal          Outcome = "internal"
	OutcomeAPIError          Outcome = "api-error"
)

// Outcomes lists every Outcome in publication order.
func Outcomes() []Outcome {
	return []Outcome{
		OutcomeOK,
		OutcomeDrift,
		OutcomeInvalidManifest,
		OutcomeLockMismatch,
		OutcomeConformanceFailed,
		OutcomeRunnerUnavailable,
		OutcomeToolchainMissing,
		OutcomeUsage,
		OutcomeInternal,
		OutcomeAPIError,
	}
}

// ExitCode is the process exit status. The table is finite and frozen.
type ExitCode int

// The exit-code table.
const (
	ExitOK           ExitCode = 0
	ExitCheckFailed  ExitCode = 1
	ExitUsage        ExitCode = 2
	ExitEnvironment  ExitCode = 3
	ExitIntegrity    ExitCode = 4
	ExitConformance  ExitCode = 5
	ExitInternal     ExitCode = 10
	exitCodeSentinel ExitCode = -1
)

// ExitCodes lists every exit code in ascending order.
func ExitCodes() []ExitCode {
	return []ExitCode{ExitOK, ExitCheckFailed, ExitUsage, ExitEnvironment, ExitIntegrity, ExitConformance, ExitInternal}
}

// Meaning is the one-line description published in `haiven schema exit-codes`.
func (e ExitCode) Meaning() string {
	switch e {
	case ExitOK:
		return "ok"
	case ExitCheckFailed:
		return "check failed (drift, invalid manifest, parity gap) or the API answered with an error"
	case ExitUsage:
		return "usage error"
	case ExitEnvironment:
		return "environment: toolchain, engine or runner missing (never a silent skip)"
	case ExitIntegrity:
		return "integrity: lock mismatch, tampered pack, digest mismatch"
	case ExitConformance:
		return "conformance scenario failed"
	case ExitInternal:
		return "internal error (a bug in the CLI)"
	}
	return ""
}

// Exit maps an outcome to its exit code. It is the single mapping table.
func (o Outcome) Exit() ExitCode {
	switch o {
	case OutcomeOK:
		return ExitOK
	case OutcomeDrift, OutcomeInvalidManifest, OutcomeAPIError:
		return ExitCheckFailed
	case OutcomeUsage:
		return ExitUsage
	case OutcomeRunnerUnavailable, OutcomeToolchainMissing:
		return ExitEnvironment
	case OutcomeLockMismatch:
		return ExitIntegrity
	case OutcomeConformanceFailed:
		return ExitConformance
	case OutcomeInternal:
		return ExitInternal
	}
	return exitCodeSentinel
}

// Valid reports whether o is in the closed vocabulary.
func (o Outcome) Valid() bool { return o.Exit() != exitCodeSentinel }

// JSONSchema publishes the closed vocabulary as a JSON-Schema enum.
func (Outcome) JSONSchema() *jsonschema.Schema {
	return enumSchema("Run outcome (closed vocabulary).", Outcomes())
}

// Severity is a diagnostic's severity.
type Severity string

// The closed severity vocabulary.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Severities lists every Severity.
func Severities() []Severity { return []Severity{SeverityError, SeverityWarning, SeverityInfo} }

// JSONSchema publishes the closed vocabulary as a JSON-Schema enum.
func (Severity) JSONSchema() *jsonschema.Schema {
	return enumSchema("Diagnostic severity (closed vocabulary).", Severities())
}

// DiagnosticCode identifies one diagnostic kind.
type DiagnosticCode string

// The closed diagnostic vocabulary.
const (
	DiagUnknownCommand              DiagnosticCode = "unknown-command"
	DiagInvalidFlag                 DiagnosticCode = "invalid-flag"
	DiagMissingArgument             DiagnosticCode = "missing-argument"
	DiagUnexpectedArgument          DiagnosticCode = "unexpected-argument"
	DiagUnknownSchema               DiagnosticCode = "unknown-schema"
	DiagUnknownCode                 DiagnosticCode = "unknown-code"
	DiagProjectNotFound             DiagnosticCode = "project-not-found"
	DiagInvalidProjectFile          DiagnosticCode = "invalid-project-file"
	DiagInvalidVersionPin           DiagnosticCode = "invalid-version-pin"
	DiagPinnedVersionUnavailable    DiagnosticCode = "pinned-version-unavailable"
	DiagPinnedChecksumMismatch      DiagnosticCode = "pinned-checksum-mismatch"
	DiagInternalError               DiagnosticCode = "internal-error"
	DiagOpenAPINotConfigured        DiagnosticCode = "openapi-not-configured"
	DiagOpenAPIUnavailable          DiagnosticCode = "openapi-unavailable"
	DiagInvalidOpenAPI              DiagnosticCode = "invalid-openapi"
	DiagUnknownOperation            DiagnosticCode = "unknown-operation"
	DiagAmbiguousOperation          DiagnosticCode = "ambiguous-operation"
	DiagMissingParameter            DiagnosticCode = "missing-parameter"
	DiagUnknownParameter            DiagnosticCode = "unknown-parameter"
	DiagInvalidRequestBody          DiagnosticCode = "invalid-request-body"
	DiagAPIUnreachable              DiagnosticCode = "api-unreachable"
	DiagAPIUnauthorized             DiagnosticCode = "api-unauthorized"
	DiagAPIHTTPError                DiagnosticCode = "api-http-error"
	DiagFakeServerUnavailable       DiagnosticCode = "fake-server-unavailable"
	DiagStarterCatalogInvalid       DiagnosticCode = "starter-catalog-invalid"
	DiagStarterCatalogUntrusted     DiagnosticCode = "starter-catalog-untrusted"
	DiagStarterUnknown              DiagnosticCode = "starter-unknown"
	DiagStarterRunnerUnavailable    DiagnosticCode = "starter-runner-unavailable"
	DiagStarterRunnerFailed         DiagnosticCode = "starter-runner-failed"
	DiagStarterManualRequired       DiagnosticCode = "starter-manual-required"
	DiagStarterJourneyUnsupported   DiagnosticCode = "starter-journey-unsupported"
	DiagStarterUnsafeDestination    DiagnosticCode = "starter-unsafe-destination"
	DiagStarterDirtyProject         DiagnosticCode = "starter-dirty-project"
	DiagStarterConfirmationRequired DiagnosticCode = "starter-confirmation-required"
	DiagStarterEvidenceUntrusted    DiagnosticCode = "starter-evidence-untrusted"
	DiagStarterLockNotFound         DiagnosticCode = "starter-lock-not-found"
	DiagStarterDrift                DiagnosticCode = "starter-drift"
	DiagDemoUnknown                 DiagnosticCode = "demo-unknown"
	DiagDemoRevisionMismatch        DiagnosticCode = "demo-revision-mismatch"
	DiagDemoOpenFailed              DiagnosticCode = "demo-open-failed"
	DiagDemoBuildFailed             DiagnosticCode = "demo-build-failed"
	DiagDemoRegisterFailed          DiagnosticCode = "demo-register-failed"
	DiagContractArtifactUnverified  DiagnosticCode = "contract-artifact-unverified"
	DiagStarterUnversionedProject   DiagnosticCode = "starter-unversioned-project"
	DiagStarterLockChanged          DiagnosticCode = "starter-lock-changed"
	DiagMalformedPathEscape         DiagnosticCode = "malformed-path-escape"
)

// Explanation is the deterministic text `haiven explain` prints for a code.
type Explanation struct {
	// Outcome is the run outcome this diagnostic produces when it is the
	// deciding error.
	Outcome Outcome
	Summary string
	Fix     string
}

// diagnostics is the single table of diagnostic codes, their outcome and
// their explanation. DiagnosticCodes and Explain both read it, so a code
// cannot exist without an explanation.
var diagnostics = []struct {
	Code DiagnosticCode
	Explanation
}{
	{DiagUnknownCommand, Explanation{OutcomeUsage, "The command is not one this haiven binary provides.", "Run `haiven capabilities` to list the commands."}},
	{DiagInvalidFlag, Explanation{OutcomeUsage, "A flag is unknown or its value is malformed.", "Run `haiven help <command>` for the accepted flags."}},
	{DiagMissingArgument, Explanation{OutcomeUsage, "A required positional argument is missing.", "Run `haiven help <command>` for the usage line."}},
	{DiagUnexpectedArgument, Explanation{OutcomeUsage, "The command received more positional arguments than it accepts.", "Run `haiven help <command>` for the usage line."}},
	{DiagUnknownSchema, Explanation{OutcomeUsage, "`haiven schema` was asked for a schema it does not publish.", "Run `haiven capabilities` and read `schemas`."}},
	{DiagUnknownCode, Explanation{OutcomeUsage, "`haiven explain` was asked about a code outside the published vocabularies.", "Run `haiven schema result` for the outcome and diagnostic enums."}},
	{DiagProjectNotFound, Explanation{OutcomeUsage, "No haiven.toml was found in this directory or any parent.", "Run the command inside a Haiven project, or create haiven.toml."}},
	{DiagInvalidProjectFile, Explanation{OutcomeInvalidManifest, "haiven.toml is not valid TOML or does not match the project schema.", "Fix haiven.toml; `haiven schema project` prints its schema."}},
	{DiagInvalidVersionPin, Explanation{OutcomeInvalidManifest, "[haiven].version in haiven.toml is not a semantic version.", "Set [haiven].version to MAJOR.MINOR.PATCH, without a leading v."}},
	{DiagPinnedVersionUnavailable, Explanation{OutcomeToolchainMissing, "The haiven release pinned by haiven.toml is not cached and could not be downloaded.", "Check network access, or install that release and retry."}},
	{DiagPinnedChecksumMismatch, Explanation{OutcomeLockMismatch, "A downloaded haiven release did not match its published sha256 checksum.", "Retry; if it persists, do not run the download and report it."}},
	{DiagInternalError, Explanation{OutcomeInternal, "The haiven CLI hit an unexpected error.", "Report it with `haiven version --json` output."}},
	{DiagOpenAPINotConfigured, Explanation{OutcomeUsage, "`haiven api` was not told which API description to read. The Haiven API does not serve its description, so there is no default.", "Pass --openapi <file|URL> or set HAIVEN_OPENAPI to the public API description."}},
	{DiagOpenAPIUnavailable, Explanation{OutcomeRunnerUnavailable, "The API description could not be read: the file is missing or the URL did not answer 200.", "Check the --openapi or HAIVEN_OPENAPI value and network access."}},
	{DiagInvalidOpenAPI, Explanation{OutcomeInvalidManifest, "An API description is not valid OpenAPI 3 JSON, or two descriptions name the same surface.", "Point --openapi at the public API description; give each description its own x-haiven-surface."}},
	{DiagUnknownOperation, Explanation{OutcomeUsage, "No published operation matches the method and path.", "Run `haiven api list` for the operations."}},
	{DiagAmbiguousOperation, Explanation{OutcomeUsage, "The method and path match operations in more than one API surface.", "Pass --surface with one of the surfaces named in the message."}},
	{DiagMissingParameter, Explanation{OutcomeUsage, "A required path, query or form parameter of the operation was not given.", "Pass it with -p name=value (path), -q name=value (query) or -f name=value (form)."}},
	{DiagUnknownParameter, Explanation{OutcomeUsage, "A parameter was given that the operation does not declare.", "Run `haiven api list --surface <surface>` and check the operation's parameters."}},
	{DiagInvalidRequestBody, Explanation{OutcomeUsage, "The request body is not valid JSON, or the operation takes no body of that kind.", "Pass --body with a JSON document, @file or @- for stdin."}},
	{DiagAPIUnreachable, Explanation{OutcomeRunnerUnavailable, "The API base URL could not be reached.", "Check the base URL (--base-url or HAIVEN_API_URL) and network access."}},
	{DiagAPIUnauthorized, Explanation{OutcomeAPIError, "The API refused the request as unauthenticated or forbidden (HTTP 401 or 403).", "Set HAIVEN_TOKEN to a valid access token for this environment."}},
	{DiagAPIHTTPError, Explanation{OutcomeAPIError, "The API answered with an HTTP error status.", "Read data.response for the API's own error code."}},
	{DiagFakeServerUnavailable, Explanation{OutcomeRunnerUnavailable, "The fake server could not bind its address or open its record file.", "Pick a free --addr, or a writable --record path."}},
	{DiagStarterCatalogInvalid, Explanation{OutcomeInvalidManifest, "The embedded starter catalog is malformed or uses an unsupported protocol.", "Install a CLI release with a valid official catalog."}},
	{DiagStarterCatalogUntrusted, Explanation{OutcomeLockMismatch, "The starter catalog bytes do not match the digest trusted by this CLI release.", "Do not execute recipes; reinstall the CLI and report the mismatch."}},
	{DiagStarterUnknown, Explanation{OutcomeUsage, "The requested starter is not in the trusted catalog.", "Run `haiven sdk starter list`."}},
	{DiagStarterRunnerUnavailable, Explanation{OutcomeRunnerUnavailable, "The verified SDK recipe runner is unavailable.", "Install or restore the SDK recipe artifacts selected by this CLI release."}},
	{DiagStarterRunnerFailed, Explanation{OutcomeRunnerUnavailable, "The verified SDK recipe runner could not complete its requested operation.", "Read the runner errors in data, correct the prerequisite or destination problem, and retry safely."}},
	{DiagStarterManualRequired, Explanation{OutcomeRunnerUnavailable, "Optional automation stopped because normative native guide steps remain pending.", "Follow the guide and stable step references in the result; the haiven CLI is not required."}},
	{DiagStarterJourneyUnsupported, Explanation{OutcomeUsage, "The selected starter journey does not support this mutating command.", "Use integrate for an existing-project starter, create for a greenfield/sample starter, or follow the normative guide manually."}},
	{DiagStarterUnsafeDestination, Explanation{OutcomeUsage, "The destination is not absent, empty, or owned by the same starter lock.", "Choose an absent or empty destination, or use the matching locked project."}},
	{DiagStarterDirtyProject, Explanation{OutcomeUsage, "An existing project has uncommitted changes, so integration will not apply.", "Commit or stash the changes, then retry; preview remains read-only."}},
	{DiagStarterConfirmationRequired, Explanation{OutcomeUsage, "A mutating starter command requires explicit non-interactive confirmation.", "Review `--preview`, then repeat with `--yes`."}},
	{DiagStarterEvidenceUntrusted, Explanation{OutcomeLockMismatch, "Recipe evidence does not match the catalog, starter, operation, or trusted recipe digests.", "Do not use the output; restore the trusted runner and retry."}},
	{DiagStarterLockNotFound, Explanation{OutcomeUsage, "The project has no readable .haiven/starter.lock.json.", "Run a starter create or integrate operation first."}},
	{DiagStarterDrift, Explanation{OutcomeDrift, "Starter-owned files or resolved inputs drift from starter.lock.json.", "Review the evidence and re-run the supported starter integration workflow."}},
	{DiagDemoUnknown, Explanation{OutcomeUsage, "The requested development demo, or this operation on it, is not supported by this CLI release.", "Run `haiven help sdk demo` for the supported demos and operations."}},
	{DiagDemoRevisionMismatch, Explanation{OutcomeLockMismatch, "The SDK checkout is not the exact revision approved for the demo run.", "Update the checkout or pass its intended full commit ID with --sdk-revision."}},
	{DiagDemoOpenFailed, Explanation{OutcomeRunnerUnavailable, "The SDK-owned development demo workflow could not start or complete.", "Check git, uv, the engine editor (Unity, Unreal, or Godot), Docker, the SDK/API checkout paths, and the child-process error, then retry."}},
	{DiagDemoBuildFailed, Explanation{OutcomeRunnerUnavailable, "The SDK-owned standalone demo build could not start or complete.", "Check git, uvx, the engine (Unity and its target build-support module, or Unreal Engine), and the selected SDK checkout, then retry."}},
	{DiagDemoRegisterFailed, Explanation{OutcomeRunnerUnavailable, "The assembled Unity demo could not be added to Unity Hub.", "Install the public `unity` CLI on PATH (or pass --hub-cli), verify the assembled project path, and retry."}},
	{DiagContractArtifactUnverified, Explanation{OutcomeRunnerUnavailable, "`haiven api call` does not send a request until this CLI can verify an immutable public contract artifact by version and digest.", "Pass --dry-run to inspect a description without sending, or use a CLI release that verifies the published contract artifact."}},
	{DiagStarterUnversionedProject, Explanation{OutcomeUsage, "integrate apply refuses a project that is not a Git worktree, because the original files would not be recoverable.", "Commit the project in Git and retry, or pass --preview. This release does not take a backup."}},
	{DiagStarterLockChanged, Explanation{OutcomeUsage, "The requested starter, recipe identities, or parameters do not match .haiven/starter.lock.json.", "Rerun with the locked starter and parameters. Upgrade and migration are outside this release."}},
	{DiagMalformedPathEscape, Explanation{OutcomeUsage, "A concrete path segment is not valid percent-encoding.", "Percent-encode the path segment, or pass the decoded value with -p name=value."}},
}

// outcomeExplanations explains each run outcome.
var outcomeExplanations = map[Outcome]Explanation{
	OutcomeOK:                {OutcomeOK, "The command succeeded.", ""},
	OutcomeDrift:             {OutcomeDrift, "Checked-in files differ from what their pinned inputs produce.", "Regenerate the files named in the diagnostics, then commit them."},
	OutcomeInvalidManifest:   {OutcomeInvalidManifest, "A project or component manifest is invalid.", "Fix the file named in the diagnostics."},
	OutcomeLockMismatch:      {OutcomeLockMismatch, "Pinned bytes do not match what is on disk or downloaded.", "Restore the pinned revision; never edit a lock by hand."},
	OutcomeConformanceFailed: {OutcomeConformanceFailed, "A conformance scenario failed.", "Read the failing scenario in the diagnostics."},
	OutcomeRunnerUnavailable: {OutcomeRunnerUnavailable, "A runner, checkout or fixture needed by the command is unavailable.", "Follow the fix in the diagnostics; this never skips green."},
	OutcomeToolchainMissing:  {OutcomeToolchainMissing, "A required toolchain is not installed.", "Install the toolchain named in the diagnostics."},
	OutcomeUsage:             {OutcomeUsage, "The command line was malformed.", "Run `haiven help <command>`."},
	OutcomeInternal:          {OutcomeInternal, "An internal error in the CLI.", "Report it with `haiven version --json` output."},
	OutcomeAPIError:          {OutcomeAPIError, "The Haiven API answered the request with an error status.", "Read the diagnostics and data.response."},
}

// DiagnosticCodes lists every DiagnosticCode in publication order.
func DiagnosticCodes() []DiagnosticCode {
	codes := make([]DiagnosticCode, len(diagnostics))
	for i, d := range diagnostics {
		codes[i] = d.Code
	}
	return codes
}

// Explain returns the explanation for a diagnostic code.
func (c DiagnosticCode) Explain() (Explanation, bool) {
	for _, d := range diagnostics {
		if d.Code == c {
			return d.Explanation, true
		}
	}
	return Explanation{}, false
}

// Valid reports whether c is in the closed vocabulary.
func (c DiagnosticCode) Valid() bool {
	_, ok := c.Explain()
	return ok
}

// Explain returns the explanation for an outcome.
func (o Outcome) Explain() (Explanation, bool) {
	e, ok := outcomeExplanations[o]
	return e, ok
}

// JSONSchema publishes the closed vocabulary as a JSON-Schema enum.
func (DiagnosticCode) JSONSchema() *jsonschema.Schema {
	return enumSchema("Diagnostic code (closed vocabulary).", DiagnosticCodes())
}

func enumSchema[T ~string](description string, values []T) *jsonschema.Schema {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = string(v)
	}
	return &jsonschema.Schema{Type: "string", Description: description, Enum: enum}
}

package result

import (
	"bytes"
	"testing"
)

// frozenOutcomes and frozenDiagnostics are the released vocabularies. The
// compatibility promise is additive-only: a value may be added to the
// vocabulary, never renamed or removed. Append here when adding a code.
var frozenOutcomes = []Outcome{
	"ok", "drift", "invalid-manifest", "lock-mismatch", "conformance-failed",
	"runner-unavailable", "toolchain-missing", "usage", "internal",
}

var frozenDiagnostics = []DiagnosticCode{
	"unknown-command", "invalid-flag", "missing-argument", "unexpected-argument",
	"unknown-schema", "unknown-code", "project-not-found", "invalid-project-file",
	"invalid-version-pin", "pinned-version-unavailable", "pinned-checksum-mismatch", "internal-error",
	"demo-unknown", "demo-revision-mismatch", "demo-open-failed", "demo-register-failed",
}

var frozenExits = map[Outcome]ExitCode{
	"ok": 0, "drift": 1, "invalid-manifest": 1, "usage": 2,
	"runner-unavailable": 3, "toolchain-missing": 3, "lock-mismatch": 4,
	"conformance-failed": 5, "internal": 10,
}

func TestVocabulariesAreAdditiveOnly(t *testing.T) {
	outcomes := map[Outcome]bool{}
	for _, o := range Outcomes() {
		outcomes[o] = true
	}
	for _, o := range frozenOutcomes {
		if !outcomes[o] {
			t.Errorf("released outcome %q was removed or renamed", o)
		}
	}
	diags := map[DiagnosticCode]bool{}
	for _, d := range DiagnosticCodes() {
		diags[d] = true
	}
	for _, d := range frozenDiagnostics {
		if !diags[d] {
			t.Errorf("released diagnostic code %q was removed or renamed", d)
		}
	}
	for o, want := range frozenExits {
		if got := o.Exit(); got != want {
			t.Errorf("outcome %q exits %d, released as %d", o, got, want)
		}
	}
}

func TestEveryOutcomeIsComplete(t *testing.T) {
	seen := map[Outcome]bool{}
	for _, o := range Outcomes() {
		if seen[o] {
			t.Errorf("duplicate outcome %q", o)
		}
		seen[o] = true
		if !o.Valid() {
			t.Errorf("outcome %q has no exit code", o)
		}
		if x, ok := o.Explain(); !ok || x.Summary == "" {
			t.Errorf("outcome %q has no explanation", o)
		}
	}
	if len(outcomeExplanations) != len(Outcomes()) {
		t.Errorf("explanations cover %d outcomes, vocabulary has %d", len(outcomeExplanations), len(Outcomes()))
	}
}

func TestEveryDiagnosticIsComplete(t *testing.T) {
	seen := map[DiagnosticCode]bool{}
	for _, d := range DiagnosticCodes() {
		if seen[d] {
			t.Errorf("duplicate diagnostic code %q", d)
		}
		seen[d] = true
		x, ok := d.Explain()
		if !ok || x.Summary == "" || x.Fix == "" {
			t.Errorf("diagnostic %q lacks a summary or fix", d)
		}
		if !x.Outcome.Valid() || x.Outcome == OutcomeOK {
			t.Errorf("diagnostic %q maps to outcome %q, want a failing outcome", d, x.Outcome)
		}
	}
}

func TestEveryExitCodeIsReachable(t *testing.T) {
	for _, x := range ExitCodes() {
		if x.Meaning() == "" {
			t.Errorf("exit %d has no meaning", x)
		}
		reached := false
		for _, o := range Outcomes() {
			reached = reached || o.Exit() == x
		}
		if !reached {
			t.Errorf("exit %d is not produced by any outcome", x)
		}
	}
}

func TestEnvelopeAddDecidesOutcome(t *testing.T) {
	e := New("check", "1.0.0", OutcomeOK)
	e.Add(Diagnostic{Code: DiagInvalidProjectFile, Message: "x", Severity: SeverityWarning})
	if !e.OK {
		t.Fatal("a warning must not fail the envelope")
	}
	e.Add(Diagnostic{Code: DiagInvalidProjectFile, Message: "x"})
	if e.OK || e.Code != OutcomeInvalidManifest || e.Exit() != ExitCheckFailed {
		t.Fatalf("got ok=%v code=%q exit=%d", e.OK, e.Code, e.Exit())
	}
	if e.Diagnostics[1].Severity != SeverityError || e.Diagnostics[1].Fix == "" {
		t.Fatalf("default severity/fix not applied: %+v", e.Diagnostics[1])
	}
	// The first error decides; later errors do not overwrite it.
	e.Add(Diagnostic{Code: DiagInternalError, Message: "y"})
	if e.Code != OutcomeInvalidManifest {
		t.Fatalf("later error overwrote the outcome: %q", e.Code)
	}
}

func TestEnvelopeJSONHasNoNullContainers(t *testing.T) {
	var buf bytes.Buffer
	if err := New("version", "1.0.0", OutcomeOK).WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("null")) {
		t.Fatalf("envelope JSON contains null:\n%s", buf.String())
	}
}

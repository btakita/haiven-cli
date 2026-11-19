// Package result defines haiven.dev/cli-result/v1: the one JSON envelope
// every haiven command prints with --json, its closed code vocabularies and
// the exit-code table.
package result

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SchemaID is the envelope's "schema" value.
const SchemaID = "haiven.dev/cli-result/v1"

// Envelope is one command's result. JSON and human output are both rendered
// from it; the JSON is never a scrape of the human text.
type Envelope struct {
	Schema      string       `json:"schema" jsonschema_description:"Always haiven.dev/cli-result/v1."`
	Command     string       `json:"command" jsonschema_description:"The command that produced this result, space-separated for subcommands."`
	OK          bool         `json:"ok" jsonschema_description:"True exactly when code is ok."`
	Code        Outcome      `json:"code"`
	KitVersion  string       `json:"kit_version" jsonschema_description:"The haiven CLI version that produced this result."`
	LockDigest  string       `json:"lock_digest,omitempty" jsonschema_description:"sha256 digest of the project's lock, when one was read."`
	Data        any          `json:"data" jsonschema_description:"Command-specific payload; its schema is published by haiven schema <command>."`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Next        []string     `json:"next" jsonschema_description:"Deterministic follow-up commands, in order."`
	Truncated   bool         `json:"truncated" jsonschema_description:"True when data or diagnostics were cut to a bound."`
}

// Diagnostic is one finding.
type Diagnostic struct {
	Code     DiagnosticCode `json:"code"`
	Severity Severity       `json:"severity"`
	Message  string         `json:"message" jsonschema_description:"Human prose; free to change between releases. Branch on code, not message."`
	Path     string         `json:"path,omitempty"`
	Line     int            `json:"line,omitempty" jsonschema:"minimum=1"`
	Node     string         `json:"node,omitempty" jsonschema_description:"Contract model node, when known."`
	Fix      string         `json:"fix,omitempty" jsonschema_description:"A command or action that resolves the diagnostic."`
}

// New returns an envelope with the given command and outcome. Data defaults
// to an empty object and the lists to empty arrays, so the JSON never carries
// null where a consumer expects a container.
func New(command, kitVersion string, outcome Outcome) *Envelope {
	return &Envelope{
		Schema:      SchemaID,
		Command:     command,
		OK:          outcome == OutcomeOK,
		Code:        outcome,
		KitVersion:  kitVersion,
		Data:        struct{}{},
		Diagnostics: []Diagnostic{},
		Next:        []string{},
	}
}

// SetOutcome sets the outcome and keeps OK consistent with it.
func (e *Envelope) SetOutcome(o Outcome) {
	e.Code = o
	e.OK = o == OutcomeOK
}

// Add appends a diagnostic. When it is an error, the envelope's outcome
// becomes the diagnostic's outcome unless an error already decided it.
func (e *Envelope) Add(d Diagnostic) {
	if d.Severity == "" {
		d.Severity = SeverityError
	}
	if d.Fix == "" {
		if x, ok := d.Code.Explain(); ok {
			d.Fix = x.Fix
		}
	}
	if d.Severity == SeverityError && e.OK {
		if x, ok := d.Code.Explain(); ok {
			e.SetOutcome(x.Outcome)
		} else {
			e.SetOutcome(OutcomeInternal)
		}
	}
	e.Diagnostics = append(e.Diagnostics, d)
}

// Fail appends an error diagnostic built from a code and a message.
func (e *Envelope) Fail(code DiagnosticCode, format string, args ...any) {
	e.Add(Diagnostic{Code: code, Severity: SeverityError, Message: fmt.Sprintf(format, args...)})
}

// Exit is the process exit code for this envelope.
func (e *Envelope) Exit() ExitCode { return e.Code.Exit() }

// WriteJSON prints exactly one JSON document followed by a newline.
func (e *Envelope) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// WriteHuman prints the diagnostics and next steps for a terminal. The
// command's own human body is printed by the caller before this.
func (e *Envelope) WriteHuman(w io.Writer) error {
	var b strings.Builder
	for _, d := range e.Diagnostics {
		fmt.Fprintf(&b, "%s[%s]: %s", d.Severity, d.Code, d.Message)
		if d.Path != "" {
			loc := d.Path
			if d.Line > 0 {
				loc = fmt.Sprintf("%s:%d", d.Path, d.Line)
			}
			fmt.Fprintf(&b, " (%s)", loc)
		}
		b.WriteString("\n")
		if d.Fix != "" {
			fmt.Fprintf(&b, "  fix: %s\n", d.Fix)
		}
	}
	for _, n := range e.Next {
		fmt.Fprintf(&b, "next: %s\n", n)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

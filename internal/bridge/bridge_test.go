package bridge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sjs "github.com/santhosh-tekuri/jsonschema/v6"
)

func compile(t *testing.T, strict bool) *sjs.Schema {
	t.Helper()
	const id = "urn:test:bridge-command"
	raw, err := json.Marshal(Schema(id, strict))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sjs.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := sjs.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func examples(t *testing.T, dir string) map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples in %s: %v", dir, err)
	}
	out := map[string]any{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := sjs.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[filepath.Base(f)] = inst
	}
	return out
}

func TestValidExamples(t *testing.T) {
	for _, strict := range []bool{true, false} {
		s := compile(t, strict)
		for name, inst := range examples(t, "valid") {
			if err := s.Validate(inst); err != nil {
				t.Errorf("strict=%v %s: %v", strict, name, err)
			}
		}
	}
}

func TestInvalidExamples(t *testing.T) {
	s := compile(t, false)
	for name, inst := range examples(t, "invalid") {
		if err := s.Validate(inst); err == nil {
			t.Errorf("%s validated, want a rejection", name)
		}
	}
}

// Every command has an arguments and a data definition, so generated
// handler dispatch has a stub to emit for each.
func TestEveryCommandIsSpecified(t *testing.T) {
	if len(specs) != len(Commands()) {
		t.Fatalf("%d specs for %d commands", len(specs), len(Commands()))
	}
	for i, c := range Commands() {
		if specs[i].command != c || specs[i].summary == "" {
			t.Errorf("spec %d is %q, want %q with a summary", i, specs[i].command, c)
		}
	}
	defs := Schema("urn:x", false).Definitions
	for _, c := range Commands() {
		for _, k := range []string{"args.", "data."} {
			if defs[k+string(c)] == nil {
				t.Errorf("no $defs/%s%s", k, c)
			}
		}
	}
}

// The released vocabularies are additive-only. Append here when adding.
var (
	frozenCommands = []Command{"health", "play", "stop", "run-scenario", "capture-log", "screenshot"}
	frozenCodes    = []Code{"ok", "unknown-command", "unimplemented", "invalid-args", "invalid-state", "unauthorized", "busy", "timeout", "scenario-failed", "engine-error"}
)

func TestVocabulariesAreAdditiveOnly(t *testing.T) {
	cmds := map[Command]bool{}
	for _, c := range Commands() {
		cmds[c] = true
	}
	for _, c := range frozenCommands {
		if !cmds[c] {
			t.Errorf("released command %q was removed or renamed", c)
		}
	}
	codes := map[Code]bool{}
	for _, c := range Codes() {
		codes[c] = true
		if o, ok := c.Outcome(); !ok || !o.Valid() {
			t.Errorf("code %q maps to no valid cli-result outcome", c)
		}
	}
	for _, c := range frozenCodes {
		if !codes[c] {
			t.Errorf("released code %q was removed or renamed", c)
		}
	}
	if o, _ := CodeOK.Outcome(); o != "ok" {
		t.Errorf("ok maps to %q", o)
	}
}

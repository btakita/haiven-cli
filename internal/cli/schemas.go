package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/haiven-dev/haiven-cli/internal/bridge"
	"github.com/haiven-dev/haiven-cli/internal/fakeserver"
	"github.com/haiven-dev/haiven-cli/internal/project"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

// Schema names that are not command names.
const (
	SchemaResult    = "result"
	SchemaExitCodes = "exit-codes"
	SchemaProject   = "project"
	// SchemaBridgeCommand is bridge-command/v1, the editor bridge contract
	// this repository owns alongside cli-result/v1.
	SchemaBridgeCommand = "bridge-command"
	// SchemaTranscript is one line of a `haiven sdk fake-server --record` file.
	SchemaTranscript = "fake-server-transcript"
)

// schemaIDPrefix namespaces published schema ids. URNs avoid implying a
// hosted URL that does not exist yet.
const schemaIDPrefix = "urn:haiven.dev:"

// reflectSchema reflects a Go type into a JSON Schema. strict forbids
// properties the type does not declare; the published schemas are not
// strict, because cli-result/v1 grows additively and an old consumer must
// accept a newer result.
func reflectSchema(v any, id string, strict bool) *jsonschema.Schema {
	r := jsonschema.Reflector{
		ExpandedStruct:            true,
		DoNotReference:            true,
		AllowAdditionalProperties: !strict,
	}
	s := r.Reflect(v)
	s.ID = jsonschema.ID(schemaIDPrefix + id)
	return s
}

// exitCodesSchema publishes the exit-code table as a JSON Schema over the
// integer exit status, one const branch per code.
func exitCodesSchema() *jsonschema.Schema {
	s := &jsonschema.Schema{
		Version:     jsonschema.Version,
		ID:          jsonschema.ID(schemaIDPrefix + "cli-exit-codes:v1"),
		Title:       "haiven exit codes",
		Description: "The finite, frozen process exit codes. x-haiven-outcomes lists the run outcomes that exit with each code.",
		Type:        "integer",
	}
	for _, row := range exitCodeTable() {
		outcomes := make([]string, len(row.Outcomes))
		for i, o := range row.Outcomes {
			outcomes[i] = string(o)
		}
		s.OneOf = append(s.OneOf, &jsonschema.Schema{
			Const:       row.Exit,
			Description: row.Meaning,
			Extras:      map[string]any{"x-haiven-outcomes": outcomes},
		})
	}
	return s
}

// schemaFor builds a named schema; ok is false for an unknown name.
func schemaFor(name string, strict bool) (*jsonschema.Schema, bool) {
	switch name {
	case SchemaResult:
		s := reflectSchema(&result.Envelope{}, "cli-result:v1", strict)
		s.Title = result.SchemaID
		if p, ok := s.Properties.Get("schema"); ok {
			p.Const = result.SchemaID
		}
		return s, true
	case SchemaExitCodes:
		return exitCodesSchema(), true
	case SchemaBridgeCommand:
		return bridge.Schema(schemaIDPrefix+"bridge-command:v1", strict), true
	case SchemaTranscript:
		s := reflectSchema(&fakeserver.Entry{}, "fake-server-transcript:v1", strict)
		s.Title = fakeserver.TranscriptSchemaID
		if p, ok := s.Properties.Get("schema"); ok {
			p.Const = fakeserver.TranscriptSchemaID
		}
		return s, true
	case SchemaProject:
		s := reflectSchema(&project.File{}, "project:v1", strict)
		s.Title = project.FileName
		s.Description = "The haiven project file, written as TOML; this schema describes its decoded shape."
		return s, true
	}
	cmd := commandForSchema(name)
	if cmd == nil || cmd.data == nil {
		return nil, false
	}
	s := reflectSchema(cmd.data, "cli-data:"+schemaName(cmd)+":v1", strict)
	s.Title = "haiven " + cmd.name + " data"
	return withErrorData(s), true
}

// withErrorData accepts the empty object New leaves when a command fails
// before it assigns data. A populated payload still has to match the
// reflected schema, including its required fields.
func withErrorData(payload *jsonschema.Schema) *jsonschema.Schema {
	zero := uint64(0)
	root := &jsonschema.Schema{
		Version: payload.Version,
		ID:      payload.ID,
		Title:   payload.Title,
		Description: "Command payload, or an empty object when the command fails before it assigns data. " +
			"An empty object is not a successful payload.",
		AnyOf: []*jsonschema.Schema{
			{
				Description:   "Empty object kept when the command fails before assigning data.",
				Type:          "object",
				MaxProperties: &zero,
			},
			payload,
		},
	}
	payload.Version = ""
	payload.ID = ""
	payload.Title = ""
	return root
}

// schemaName is a command's schema name: its words joined by "-" ("sdk
// generate" -> "sdk-generate"), so it is also a file name.
func schemaName(c *command) string { return strings.ReplaceAll(c.name, " ", "-") }

// commandForSchema finds a command by schema name or by command name.
func commandForSchema(name string) *command {
	if c := lookup(name); c != nil {
		return c
	}
	for _, c := range commands {
		if schemaName(c) == name {
			return c
		}
	}
	return nil
}

// schemaNames lists every name `haiven schema` accepts.
func schemaNames() []string {
	fixed := []string{SchemaResult, SchemaExitCodes, SchemaProject, SchemaBridgeCommand, SchemaTranscript}
	names := append([]string(nil), fixed...)
	for _, c := range commands {
		if c.data != nil {
			names = append(names, schemaName(c))
		}
	}
	sort.Strings(names[len(fixed):])
	return names
}

// schemaDocument renders a published schema as indented JSON.
func schemaDocument(name string) (json.RawMessage, bool) {
	s, ok := schemaFor(name, false)
	if !ok {
		return nil, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

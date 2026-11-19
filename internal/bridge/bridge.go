// Package bridge defines haiven.dev/bridge-command/v1: the request and
// response an engine editor bridge exchanges with `haiven editor`. The
// schema is transport-neutral (loopback HTTP, WebSocket or Roblox long-poll
// all carry the same documents) and is the single table each engine's
// handler dispatch is generated from, so a command added here appears as an
// unimplemented stub, and a red parity cell, in every engine until it is
// implemented.
//
// Like cli-result/v1 the promise covers codes, not messages: command names,
// argument and data field names, and the code vocabulary are additive-only.
package bridge

import (
	"encoding/json"

	"github.com/invopop/jsonschema"

	"github.com/haiven-dev/haiven-cli/internal/result"
)

// SchemaID is the "schema" value of every request and response.
const SchemaID = "haiven.dev/bridge-command/v1"

// Command names one bridge command.
type Command string

// The closed command vocabulary (sdk-editor-integration.md: play, stop,
// run-scenario, capture-log, screenshot, plus health).
const (
	CommandHealth      Command = "health"
	CommandPlay        Command = "play"
	CommandStop        Command = "stop"
	CommandRunScenario Command = "run-scenario"
	CommandCaptureLog  Command = "capture-log"
	CommandScreenshot  Command = "screenshot"
)

// Code is a response's outcome.
type Code string

// The closed code vocabulary. Each maps to the cli-result/v1 outcome the
// `haiven editor` wrapper reports, so its exit code follows the CLI table.
const (
	CodeOK             Code = "ok"
	CodeUnknownCommand Code = "unknown-command"
	CodeUnimplemented  Code = "unimplemented"
	CodeInvalidArgs    Code = "invalid-args"
	CodeInvalidState   Code = "invalid-state"
	CodeUnauthorized   Code = "unauthorized"
	CodeBusy           Code = "busy"
	CodeTimeout        Code = "timeout"
	CodeScenarioFailed Code = "scenario-failed"
	CodeEngineError    Code = "engine-error"
)

// codeTable is the single table of codes, their CLI outcome and meaning.
var codeTable = []struct {
	Code    Code
	Outcome result.Outcome
	Meaning string
}{
	{CodeOK, result.OutcomeOK, "The command ran; data holds its result."},
	{CodeUnknownCommand, result.OutcomeUsage, "The bridge does not know the command (an older plugin, or a typo)."},
	{CodeUnimplemented, result.OutcomeRunnerUnavailable, "The command is in the schema but this engine's bridge has not implemented it (a generated stub)."},
	{CodeInvalidArgs, result.OutcomeUsage, "args does not match the command's arguments."},
	{CodeInvalidState, result.OutcomeUsage, "The editor is not in a state the command can run in, e.g. stop while not playing."},
	{CodeUnauthorized, result.OutcomeRunnerUnavailable, "The request did not carry the bridge's per-session token."},
	{CodeBusy, result.OutcomeRunnerUnavailable, "The editor is compiling, importing or running another command; retry."},
	{CodeTimeout, result.OutcomeRunnerUnavailable, "The command did not finish within the bridge's bound."},
	{CodeScenarioFailed, result.OutcomeConformanceFailed, "run-scenario ran and the scenario failed; data holds the failures."},
	{CodeEngineError, result.OutcomeInternal, "The engine raised an error running the command."},
}

// Commands lists every command in publication order.
func Commands() []Command {
	return []Command{CommandHealth, CommandPlay, CommandStop, CommandRunScenario, CommandCaptureLog, CommandScreenshot}
}

// Codes lists every code in publication order.
func Codes() []Code {
	out := make([]Code, len(codeTable))
	for i, c := range codeTable {
		out[i] = c.Code
	}
	return out
}

// Outcome is the cli-result/v1 outcome a code maps to; ok is false for a
// code outside the vocabulary.
func (c Code) Outcome() (result.Outcome, bool) {
	for _, row := range codeTable {
		if row.Code == c {
			return row.Outcome, true
		}
	}
	return "", false
}

// Engines lists the engines a bridge reports in health.
func Engines() []string { return []string{"godot", "unity", "unreal", "roblox"} }

// LogLevels lists capture-log line levels.
func LogLevels() []string { return []string{"debug", "info", "warning", "error"} }

// commandSpec is one command's arguments and result data.
type commandSpec struct {
	command Command
	summary string
	args    func(strict bool) *jsonschema.Schema
	data    func(strict bool) *jsonschema.Schema
}

type prop struct {
	name string
	s    *jsonschema.Schema
}

func object(strict bool, desc string, required []string, props ...prop) *jsonschema.Schema {
	p := jsonschema.NewProperties()
	for _, x := range props {
		p.Set(x.name, x.s)
	}
	s := &jsonschema.Schema{Type: "object", Description: desc, Properties: p, Required: required}
	if strict {
		s.AdditionalProperties = jsonschema.FalseSchema
	}
	return s
}

func str(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc}
}

func boundedStr(desc string, minLen, maxLen uint64) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc, MinLength: &minLen, MaxLength: &maxLen}
}

func boolean(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "boolean", Description: desc}
}

func integer(desc, minimum, maximum string) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "integer", Description: desc}
	if minimum != "" {
		s.Minimum = json.Number(minimum)
	}
	if maximum != "" {
		s.Maximum = json.Number(maximum)
	}
	return s
}

func enumOf[T ~string](desc string, values []T) *jsonschema.Schema {
	e := make([]any, len(values))
	for i, v := range values {
		e[i] = string(v)
	}
	return &jsonschema.Schema{Type: "string", Description: desc, Enum: e}
}

func arrayOf(desc string, items *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "array", Description: desc, Items: items}
}

func ref(def string) *jsonschema.Schema { return &jsonschema.Schema{Ref: "#/$defs/" + def} }

var specs = []commandSpec{
	{
		command: CommandHealth,
		summary: "Report the engine, plugin and play state, and which commands this bridge implements.",
		args:    func(strict bool) *jsonschema.Schema { return object(strict, "No arguments.", nil) },
		data: func(strict bool) *jsonschema.Schema {
			return object(strict, "", []string{"engine", "engine_version", "plugin_version", "playing", "implemented"},
				prop{"engine", enumOf("The engine this bridge runs in.", Engines())},
				prop{"engine_version", str("The engine's own version string.")},
				prop{"plugin_version", str("The Haiven plugin or addon version.")},
				prop{"sdk_version", str("The Haiven SDK version the plugin bundles, when known.")},
				prop{"playing", boolean("True while the editor is in play mode.")},
				prop{"implemented", arrayOf("Commands this bridge implements; the rest answer unimplemented.", enumOf("", Commands()))},
			)
		},
	},
	{
		command: CommandPlay,
		summary: "Enter play mode, optionally in one scene.",
		args: func(strict bool) *jsonschema.Schema {
			return object(strict, "", nil, prop{"scene", str("Project-relative scene path; the main scene when absent.")})
		},
		data: func(strict bool) *jsonschema.Schema {
			return object(strict, "", []string{"playing"},
				prop{"playing", &jsonschema.Schema{Type: "boolean", Const: true}},
				prop{"scene", str("The scene that is playing, project-relative.")},
			)
		},
	},
	{
		command: CommandStop,
		summary: "Leave play mode.",
		args:    func(strict bool) *jsonschema.Schema { return object(strict, "No arguments.", nil) },
		data: func(strict bool) *jsonschema.Schema {
			return object(strict, "", []string{"playing"}, prop{"playing", &jsonschema.Schema{Type: "boolean", Const: false}})
		},
	},
	{
		command: CommandRunScenario,
		summary: "Run one conformance scenario in the engine (editor-bridge/v0 and the in-engine suites).",
		args: func(strict bool) *jsonschema.Schema {
			return object(strict, "", []string{"suite", "scenario"},
				prop{"suite", boundedStr("Suite id, e.g. gaming-client/v0.", 1, 128)},
				prop{"scenario", boundedStr("Scenario id within the suite.", 1, 256)},
			)
		},
		data: func(strict bool) *jsonschema.Schema {
			return object(strict, "Present for ok and for scenario-failed.", []string{"suite", "scenario", "passed", "failures"},
				prop{"suite", str("")},
				prop{"scenario", str("")},
				prop{"passed", boolean("")},
				prop{"failures", arrayOf("One entry per failed expectation; empty when passed.", str(""))},
			)
		},
	},
	{
		command: CommandCaptureLog,
		summary: "Return editor and game log lines after a cursor, bounded.",
		args: func(strict bool) *jsonschema.Schema {
			return object(strict, "", nil,
				prop{"since", integer("Return lines with seq greater than this; 0 or absent for the oldest retained.", "0", "")},
				prop{"max_lines", integer("Upper bound on lines returned (default 500).", "1", "10000")},
			)
		},
		data: func(strict bool) *jsonschema.Schema {
			line := object(strict, "", []string{"seq", "level", "message"},
				prop{"seq", integer("Monotonic line number.", "1", "")},
				prop{"level", enumOf("", LogLevels())},
				prop{"message", str("The line, after the bridge's secret redaction.")},
			)
			return object(strict, "", []string{"lines", "next", "truncated"},
				prop{"lines", arrayOf("", line)},
				prop{"next", integer("Pass as since to continue.", "0", "")},
				prop{"truncated", boolean("True when more lines were available than max_lines.")},
			)
		},
	},
	{
		command: CommandScreenshot,
		summary: "Capture the game or editor viewport to a PNG.",
		args: func(strict bool) *jsonschema.Schema {
			return object(strict, "", nil, prop{"path", str("Output path, project-relative; the bridge picks one under its scratch directory when absent.")})
		},
		data: func(strict bool) *jsonschema.Schema {
			return object(strict, "", []string{"path", "width", "height", "format"},
				prop{"path", str("Where the image was written, project-relative.")},
				prop{"width", integer("", "1", "")},
				prop{"height", integer("", "1", "")},
				prop{"format", enumOf("", []string{"png"})},
			)
		},
	},
}

// Schema builds the published bridge-command/v1 schema. A document is valid
// when it is a request or a response. strict forbids undeclared properties;
// the published form is permissive, because v1 grows additively.
func Schema(id string, strict bool) *jsonschema.Schema {
	defs := jsonschema.Definitions{}
	requests := make([]*jsonschema.Schema, 0, len(specs))
	responses := make([]*jsonschema.Schema, 0, len(specs))
	commandList := make([]any, 0, len(specs))
	for _, sp := range specs {
		name := string(sp.command)
		defs["args."+name] = sp.args(strict)
		defs["data."+name] = sp.data(strict)
		commandList = append(commandList, map[string]any{"command": name, "summary": sp.summary})

		reqProps := jsonschema.NewProperties()
		reqProps.Set("command", &jsonschema.Schema{Const: name})
		reqProps.Set("args", ref("args."+name))
		requests = append(requests, &jsonschema.Schema{Properties: reqProps})

		respProps := jsonschema.NewProperties()
		respProps.Set("command", &jsonschema.Schema{Const: name})
		respProps.Set("data", ref("data."+name))
		responses = append(responses, &jsonschema.Schema{Properties: respProps})
	}

	envelope := func(desc string, required []string, props ...prop) *jsonschema.Schema {
		base := []prop{
			{"schema", &jsonschema.Schema{Type: "string", Const: SchemaID}},
			{"id", boundedStr("Correlation id chosen by the caller; the response echoes it.", 1, 128)},
			{"command", enumOf("The command (closed vocabulary).", Commands())},
		}
		return object(strict, desc, append([]string{"schema", "id", "command"}, required...), append(base, props...)...)
	}

	request := envelope("A command sent to the bridge.", []string{"args"},
		prop{"args", &jsonschema.Schema{Type: "object", Description: "The command's arguments; {} when it takes none."}},
	)
	request.OneOf = requests

	codes := make([]any, 0, len(codeTable))
	outcomes := map[string]any{}
	for _, row := range codeTable {
		codes = append(codes, string(row.Code))
		outcomes[string(row.Code)] = string(row.Outcome)
	}
	codeSchema := &jsonschema.Schema{Type: "string", Enum: codes, Description: "Outcome (closed vocabulary). x-haiven-outcomes maps each to the cli-result/v1 outcome `haiven editor` reports.", Extras: map[string]any{"x-haiven-outcomes": outcomes}}
	response := envelope("The bridge's answer to one request.", []string{"ok", "code"},
		prop{"ok", boolean("True exactly when code is ok.")},
		prop{"code", codeSchema},
		prop{"message", str("Human prose; free to change. Branch on code.")},
		prop{"data", &jsonschema.Schema{Type: "object", Description: "The command's result: required for ok, and present for scenario-failed."}},
	)
	response.OneOf = responses
	okProps := jsonschema.NewProperties()
	okProps.Set("code", &jsonschema.Schema{Const: string(CodeOK)})
	thenProps := jsonschema.NewProperties()
	thenProps.Set("ok", &jsonschema.Schema{Const: true})
	elseProps := jsonschema.NewProperties()
	elseProps.Set("ok", &jsonschema.Schema{Const: false})
	response.If = &jsonschema.Schema{Properties: okProps, Required: []string{"code"}}
	response.Then = &jsonschema.Schema{Properties: thenProps, Required: []string{"data"}}
	response.Else = &jsonschema.Schema{Properties: elseProps}

	defs["request"] = request
	defs["response"] = response
	return &jsonschema.Schema{
		Version:     jsonschema.Version,
		ID:          jsonschema.ID(id),
		Title:       SchemaID,
		Description: "A request to, or a response from, an engine editor bridge. Transport-neutral; `haiven editor <command>` wraps it in cli-result/v1.",
		OneOf:       []*jsonschema.Schema{ref("request"), ref("response")},
		Definitions: defs,
		Extras:      map[string]any{"x-haiven-commands": commandList},
	}
}

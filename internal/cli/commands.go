package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haiven-dev/haiven-cli/internal/pin"
	"github.com/haiven-dev/haiven-cli/internal/project"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

func init() {
	commands = []*command{
		{name: "version", usage: "haiven version", summary: "Print the CLI version and platform.", data: VersionData{}, run: runVersion},
		{name: "help", usage: "haiven help [command...]", summary: "Show usage for every command, one command family, or one command.", data: HelpData{}, run: runHelp},
		{name: "capabilities", usage: "haiven capabilities", summary: "Describe this CLI: commands, schemas, codes, and the project pin. The first call an agent makes.", readsProject: true, data: CapabilitiesData{}, run: runCapabilities},
		{name: "schema", usage: "haiven schema <name>", summary: "Print a published JSON Schema: result, exit-codes, project, or a command's data.", data: SchemaData{}, run: runSchema},
		{name: "explain", usage: "haiven explain <code>", summary: "Explain an outcome or diagnostic code and its fix.", data: ExplainData{}, run: runExplain},
		{name: "sdk fake-server", usage: "haiven sdk fake-server [--addr host:port] [--token VALUE:USER[:expired|:invalid]]... [--route type]... [--rate-limited type]... [--deferred type]... [--record file.jsonl] [--ready-file file.json]", summary: "Serve a local model of the gaming WebSocket (/v0/ws) with control levers, until interrupted.", network: true, data: FakeServerData{}, run: runFakeServer},
		{name: "sdk demo open", usage: "haiven sdk demo open unity-bossroom|unreal-lyra|godot-demo --sdk-root path --sdk-revision commit [--no-editor] [unity-bossroom: [--unity path]] [unity-bossroom development checkout: --api-root path [--dest path] [--backend local-api|wsfixture|auto] [--oauth]] [unreal-lyra: [--engine path] [--dest path] [--content path] [--item code=item]...] [godot-demo: [--godot path]]", summary: "Open a pinned Boss Room, Lyra, or Godot demo in its editor.", network: true, data: DemoOpenData{}, run: runDemoOpen},
		{name: "sdk demo build", usage: "haiven sdk demo build unity-bossroom|unreal-lyra --sdk-root path --sdk-revision commit --target linux64|win64|osx [--dest path] [--out path] [unity-bossroom: [--unity path] [--timeout seconds]] [unreal-lyra: [--engine path] [--content path] [--item code=item]... [--editor-only]]", summary: "Build Boss Room or Lyra from a development SDK checkout. Use the native editor to build standalone demo checkouts.", network: true, data: DemoBuildData{}, run: runDemoBuild},
		{name: "sdk demo register", usage: "haiven sdk demo register unity-bossroom --sdk-root path --sdk-revision commit [--dest path] [--hub-cli command]", summary: "Add a pinned Boss Room project to Unity Hub.", data: DemoRegisterData{}, run: runDemoRegister},
		{name: "sdk starter list", usage: "haiven sdk starter list [--engine name] [--journey name] [--platform name] [--feature name]", summary: "List trusted SDK starters with immutable native guides and stable manual steps.", data: StarterListData{}, run: runStarterList},
		{name: "sdk starter describe", usage: "haiven sdk starter describe <starter>", summary: "Describe a trusted starter and its normative native onboarding guide.", data: StarterDescribeData{}, run: runStarterDescribe},
		{name: "sdk starter create", usage: "haiven sdk starter create <starter> --dest path [--preview] [--yes] [--param name=value]...", summary: "Optionally preview or automate sample creation without replacing the native guide.", network: true, data: StarterOperationData{}, run: runStarterCreate},
		{name: "sdk starter integrate", usage: "haiven sdk starter integrate <starter> --project path [--preview] [--yes] [--param name=value]...", summary: "Optionally preview or automate native onboarding in a clean Git worktree. A changed starter lock is refused.", network: true, data: StarterOperationData{}, run: runStarterIntegrate},
		{name: "sdk starter run", usage: "haiven sdk starter run <starter> --project path [--param name=value]...", summary: "Build and run a supported demo through its verified SDK-owned lifecycle runner.", network: true, data: StarterRunData{}, run: runStarterRun},
		{name: "sdk starter verify", usage: "haiven sdk starter verify --project path [--starter id]", summary: "Verify locked or native/manual onboarding; pending guide steps are never success.", data: StarterVerifyData{}, run: runStarterVerify},
		{name: "api list", usage: "haiven api list [--openapi file|URL]... [--surface name]... [--match text]", summary: "List the operations of a public HTTP API description.", network: true, data: APIListData{}, run: runAPIList},
		{name: "api call", usage: "haiven api call <METHOD> <path> [-p name=value]... [-q name=value]... [-f name=value]... [--body json|@file|@-] [--surface s] [--base-url url] [--dry-run] [--show-secrets] [--openapi file|URL]...", summary: "Build a public HTTP API request. Sending stays unavailable until an immutable contract artifact is verified by version and digest; --dry-run prints the request.", network: true, data: APICallData{}, run: runAPICall},
	}
	groups = []group{
		{name: "api", summary: "Inspect the Haiven HTTP API from a public description. Requests are not sent until a verified contract artifact exists."},
		{name: "sdk", summary: "Commands for SDK integrators: trusted starters and a local fake of the gaming WebSocket."},
	}
}

// VersionData is the data of `haiven version`.
type VersionData struct {
	Version  string `json:"version"`
	Platform string `json:"platform" jsonschema_description:"GOOS/GOARCH of this binary."`
}

func runVersion(c *call) {
	if _, ok := c.parse(c.flags(), 0, 0); !ok {
		return
	}
	d := VersionData{Version: c.e.Version, Platform: c.e.GOOS + "/" + c.e.GOARCH}
	c.out.Data = d
	c.human = func(w io.Writer) { fmt.Fprintf(w, "haiven %s (%s)\n", d.Version, d.Platform) }
}

// HelpData is the data of `haiven help`.
type HelpData struct {
	Commands []CommandInfo `json:"commands"`
}

func runHelp(c *call) {
	rest, ok := c.parse(c.flags(), 0, -1)
	if !ok {
		return
	}
	infos := commandInfos()
	if len(rest) > 0 {
		name := strings.Join(rest, " ")
		if cmd := lookup(name); cmd != nil {
			infos = []CommandInfo{infoFor(cmd)}
		} else if subs := groupCommands(name); len(subs) > 0 {
			infos = nil
			for _, sub := range subs {
				infos = append(infos, infoFor(lookup(name+" "+sub)))
			}
		} else {
			c.out.Fail(result.DiagUnknownCommand, "unknown command %q", name)
			c.out.Next = []string{"haiven help"}
			return
		}
	}
	c.out.Data = HelpData{Commands: infos}
	c.human = func(w io.Writer) {
		if len(rest) == 0 {
			fmt.Fprintln(w, "haiven: the Haiven SDK CLI for game developers")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Every command accepts --json (or HAIVEN_OUTPUT=json) and then prints one")
			fmt.Fprintln(w, result.SchemaID+" document.")
			fmt.Fprintln(w)
		}
		heading := ""
		for _, i := range infos {
			if i.Group != heading {
				heading = i.Group
				fmt.Fprintf(w, "\nhaiven %s: %s\n", heading, groupSummary(heading))
			}
			fmt.Fprintf(w, "  %-40s %s\n", i.Usage, i.Summary)
		}
	}
}

func groupSummary(name string) string {
	for _, g := range groups {
		if g.name == name {
			return g.summary
		}
	}
	return ""
}

// CommandInfo describes one command in help and capabilities.
type CommandInfo struct {
	Name         string `json:"name" jsonschema_description:"The full command name, space-separated for a command in a group."`
	Group        string `json:"group,omitempty" jsonschema_description:"The group the command belongs to; absent for a top-level command."`
	Usage        string `json:"usage"`
	Summary      string `json:"summary"`
	Network      bool   `json:"network" jsonschema_description:"True when the command may make network calls."`
	ReadsProject bool   `json:"reads_project" jsonschema_description:"True when the command reads haiven.toml."`
}

func infoFor(c *command) CommandInfo {
	return CommandInfo{Name: c.name, Group: groupOf(c.name), Usage: c.usage, Summary: c.summary, Network: c.network, ReadsProject: c.readsProject}
}

func commandInfos() []CommandInfo {
	var infos []CommandInfo
	for _, c := range sortedCommands() {
		infos = append(infos, infoFor(c))
	}
	return infos
}

// CapabilitiesData is the data of `haiven capabilities`. Fields are additive:
// targets, packs and harness installs join it as those commands land.
type CapabilitiesData struct {
	CLIVersion      string                  `json:"cli_version"`
	Platform        string                  `json:"platform"`
	ResultSchema    string                  `json:"result_schema"`
	Commands        []CommandInfo           `json:"commands" jsonschema_description:"Every command, top-level first, then each group's commands together."`
	Groups          []GroupInfo             `json:"groups" jsonschema_description:"Command groups: haiven <group> <command>."`
	Schemas         []string                `json:"schemas" jsonschema_description:"Names accepted by haiven schema."`
	Outcomes        []result.Outcome        `json:"outcomes"`
	DiagnosticCodes []result.DiagnosticCode `json:"diagnostic_codes"`
	ExitCodes       []ExitCodeInfo          `json:"exit_codes"`
	Project         *ProjectInfo            `json:"project,omitempty" jsonschema_description:"Present when a haiven.toml was found."`
}

// GroupInfo describes one command group in capabilities.
type GroupInfo struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Commands []string `json:"commands" jsonschema_description:"Full names of the group's commands."`
}

func groupInfos() []GroupInfo {
	infos := []GroupInfo{}
	for _, g := range groups {
		info := GroupInfo{Name: g.name, Summary: g.summary, Commands: []string{}}
		for _, sub := range groupCommands(g.name) {
			info.Commands = append(info.Commands, g.name+" "+sub)
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

// ExitCodeInfo is one row of the exit-code table.
type ExitCodeInfo struct {
	Exit     int              `json:"exit"`
	Meaning  string           `json:"meaning"`
	Outcomes []result.Outcome `json:"outcomes"`
}

// ProjectInfo summarizes the project the CLI found.
type ProjectInfo struct {
	File          string     `json:"file" jsonschema_description:"haiven.toml path relative to the working directory, slash-separated."`
	Valid         bool       `json:"valid"`
	PinnedVersion string     `json:"pinned_version,omitempty"`
	Pin           pin.Reason `json:"pin,omitempty" jsonschema:"enum=unpinned,enum=matches,enum=dev-build,enum=already-reexecuted,enum=pinned-release"`
}

func exitCodeTable() []ExitCodeInfo {
	var rows []ExitCodeInfo
	for _, x := range result.ExitCodes() {
		row := ExitCodeInfo{Exit: int(x), Meaning: x.Meaning(), Outcomes: []result.Outcome{}}
		for _, o := range result.Outcomes() {
			if o.Exit() == x {
				row.Outcomes = append(row.Outcomes, o)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func runCapabilities(c *call) {
	if _, ok := c.parse(c.flags(), 0, 0); !ok {
		return
	}
	d := CapabilitiesData{
		CLIVersion:      c.e.Version,
		Platform:        c.e.GOOS + "/" + c.e.GOARCH,
		ResultSchema:    result.SchemaID,
		Commands:        commandInfos(),
		Groups:          groupInfos(),
		Schemas:         schemaNames(),
		Outcomes:        result.Outcomes(),
		DiagnosticCodes: result.DiagnosticCodes(),
		ExitCodes:       exitCodeTable(),
	}
	if c.project != nil {
		f := c.project.File
		d.Project = &ProjectInfo{File: relToCwd(c.e.Cwd, c.project.Path), Valid: true, PinnedVersion: f.Haiven.Version, Pin: pinReason(c.e, f.Haiven.Version)}
	} else if path, err := project.Find(c.e.Cwd); err == nil {
		// Found but invalid: say why as warnings without failing
		// capabilities, which must stay usable to diagnose a broken project.
		d.Project = &ProjectInfo{File: relToCwd(c.e.Cwd, path), Valid: false}
		recordProjectError(c.out, c.projectErr, result.SeverityWarning)
		c.out.Next = []string{"haiven schema project"}
	}
	c.out.Data = d
	c.human = func(w io.Writer) {
		fmt.Fprintf(w, "haiven %s (%s), results: %s\n\ncommands:\n", d.CLIVersion, d.Platform, d.ResultSchema)
		heading := ""
		for _, i := range d.Commands {
			if i.Group != heading {
				heading = i.Group
				fmt.Fprintf(w, "\nhaiven %s commands:\n", heading)
			}
			fmt.Fprintf(w, "  %-16s %s\n", i.Name, i.Summary)
		}
		fmt.Fprintf(w, "\nschemas: %s\n", strings.Join(d.Schemas, ", "))
		if p := d.Project; p != nil {
			fmt.Fprintf(w, "project: %s", p.File)
			if !p.Valid {
				fmt.Fprint(w, " (invalid)")
			} else if p.PinnedVersion != "" {
				fmt.Fprintf(w, " pins haiven %s (%s)", p.PinnedVersion, p.Pin)
			}
			fmt.Fprintln(w)
		}
	}
}

// pinReason reports why this process serves the command itself; when it is
// running, a pin that required re-exec has already happened.
func pinReason(e Env, pinned string) pin.Reason {
	switch {
	case pinned == "":
		return pin.ReasonUnpinned
	case pinned == e.Version:
		return pin.ReasonMatches
	case e.Getenv(pin.ActiveEnv) != "":
		return pin.ReasonActive
	default:
		return pin.ReasonDevBuild
	}
}

func relToCwd(cwd, path string) string {
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// SchemaData is the data of `haiven schema`.
type SchemaData struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema" jsonschema:"type=object"`
}

func runSchema(c *call) {
	rest, ok := c.parse(c.flags(), 1, 1)
	if !ok {
		return
	}
	doc, ok := schemaDocument(rest[0])
	if !ok {
		c.out.Fail(result.DiagUnknownSchema, "no schema named %q; known: %s", rest[0], strings.Join(schemaNames(), ", "))
		return
	}
	c.out.Data = SchemaData{Name: rest[0], Schema: doc}
	c.human = func(w io.Writer) { _, _ = w.Write(append(doc, '\n')) }
}

// ExplainKind says which vocabulary a code belongs to.
type ExplainKind string

// The closed explain-kind vocabulary.
const (
	ExplainOutcome    ExplainKind = "outcome"
	ExplainDiagnostic ExplainKind = "diagnostic"
)

// ExplainData is the data of `haiven explain`.
type ExplainData struct {
	Code    string         `json:"code"`
	Kind    ExplainKind    `json:"kind" jsonschema:"enum=outcome,enum=diagnostic"`
	Outcome result.Outcome `json:"outcome"`
	Exit    int            `json:"exit"`
	Summary string         `json:"summary"`
	Fix     string         `json:"fix,omitempty"`
}

func runExplain(c *call) {
	rest, ok := c.parse(c.flags(), 1, 1)
	if !ok {
		return
	}
	code := rest[0]
	var d ExplainData
	if x, ok := result.Outcome(code).Explain(); ok {
		d = ExplainData{Code: code, Kind: ExplainOutcome, Outcome: x.Outcome, Exit: int(x.Outcome.Exit()), Summary: x.Summary, Fix: x.Fix}
	} else if x, ok := result.DiagnosticCode(code).Explain(); ok {
		d = ExplainData{Code: code, Kind: ExplainDiagnostic, Outcome: x.Outcome, Exit: int(x.Outcome.Exit()), Summary: x.Summary, Fix: x.Fix}
	} else {
		c.out.Fail(result.DiagUnknownCode, "%q is not an outcome or diagnostic code", code)
		c.out.Next = []string{"haiven capabilities"}
		return
	}
	c.out.Data = d
	c.human = func(w io.Writer) {
		fmt.Fprintf(w, "%s (%s): %s\n", d.Code, d.Kind, d.Summary)
		fmt.Fprintf(w, "outcome: %s, exit %d\n", d.Outcome, d.Exit)
		if d.Fix != "" {
			fmt.Fprintf(w, "fix: %s\n", d.Fix)
		}
	}
}

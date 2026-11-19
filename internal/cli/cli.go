// Package cli is the haiven command line: argument dispatch, output mode,
// the project pin re-exec, and the commands themselves. Every command
// produces one result.Envelope; --json prints it, a terminal gets text
// rendered from the same envelope.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/haiven-dev/haiven-cli/internal/demo"
	"github.com/haiven-dev/haiven-cli/internal/pin"
	"github.com/haiven-dev/haiven-cli/internal/project"
	"github.com/haiven-dev/haiven-cli/internal/result"
	"github.com/haiven-dev/haiven-cli/internal/starter"
	"github.com/haiven-dev/haiven-cli/internal/version"
)

// RootCommand is the envelope "command" for failures before a command was
// selected (an unknown command, for example).
const RootCommand = "haiven"

// OutputEnv selects JSON output for every command when set to "json". Skills
// and editor integrations set it; --json does the same per call.
const OutputEnv = "HAIVEN_OUTPUT"

// Env is everything a run touches outside its arguments, injected so tests
// run in-process and deterministically.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Cwd            string
	Version        string
	GOOS, GOARCH   string
	// HTTP carries `haiven api call` requests. Nil uses
	// http.DefaultTransport.
	HTTP http.RoundTripper
	// StarterCatalog and StarterRunner are the engine-neutral boundary to
	// SDK-owned starter artifacts. Nil selects the embedded catalog and an
	// unavailable runner respectively.
	StarterCatalog         starter.CatalogSource
	StarterRunner          starter.Runner
	StarterLifecycleRunner starter.LifecycleRunner
	// DemoOpener and DemoBuilder run SDK-owned tooling from an exact checkout;
	// DemoRegistrar adds an already assembled project to Unity Hub. Nil reports
	// that the corresponding demo automation is unavailable.
	DemoOpener    demo.Opener
	DemoBuilder   demo.Builder
	DemoRegistrar demo.Registrar
	// GitDirty is injected because integrate's clean-tree guard must work in
	// tests without invoking a host-specific shell.
	GitDirty func(context.Context, string) (bool, error)
	// Pin decides whether a project-pinned release runs instead. Nil
	// disables pinning (tests that do not exercise it).
	Pin *pin.Resolver
	// pinCacheErr is set when the user cache directory cannot be resolved,
	// so Pin stays nil. A release build whose project pin needs that cache
	// fails instead of running as if it were a dev build.
	pinCacheErr error
	// Reexec runs the pinned binary with the original arguments and returns
	// its exit status. pin.Exec in production.
	Reexec func(binary string, args []string, pinned string) (int, error)
}

// command is one entry in the command table. The table is the single source
// for dispatch, help, capabilities and per-command schemas.
type command struct {
	// name is the full command name, space-separated for a command in a
	// group ("group command").
	name    string
	usage   string
	summary string
	// network marks commands that may make network calls.
	network bool
	// readsProject marks commands that read haiven.toml.
	readsProject bool
	// data is a zero value of the command's data type, reflected into its
	// published schema.
	data any
	run  func(c *call)
}

// call is one running command.
type call struct {
	ctx  context.Context
	e    Env
	cmd  *command
	args []string
	out  *result.Envelope
	// human renders the command's body for a terminal. Optional.
	human func(w io.Writer)
	// json is true when the envelope is printed as JSON, so a command must
	// not write anything else to stdout.
	json bool
	// project is the loaded project, or nil; projectErr is why not.
	project    *project.Project
	projectErr error
}

func (c *call) flags() *flag.FlagSet {
	fs := flag.NewFlagSet(c.cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse parses flags and returns positionals, or records a usage diagnostic.
// Flags and positionals may be interleaved; everything after "--" is
// positional.
func (c *call) parse(fs *flag.FlagSet, minArgs, maxArgs int) ([]string, bool) {
	rest := []string{}
	args := c.args
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.out.Fail(result.DiagInvalidFlag, "help requested; usage: %s", c.cmd.usage)
			} else {
				c.out.Fail(result.DiagInvalidFlag, "%v; usage: %s", err, c.cmd.usage)
			}
			return nil, false
		}
		left := fs.Args()
		if len(left) == 0 {
			break
		}
		if consumed := len(args) - len(left); consumed > 0 && args[consumed-1] == "--" {
			rest = append(rest, left...)
			break
		}
		rest = append(rest, left[0])
		args = left[1:]
	}
	if len(rest) < minArgs {
		c.out.Fail(result.DiagMissingArgument, "usage: %s", c.cmd.usage)
		return nil, false
	}
	if maxArgs >= 0 && len(rest) > maxArgs {
		c.out.Fail(result.DiagUnexpectedArgument, "unexpected argument %q; usage: %s", rest[maxArgs], c.cmd.usage)
		return nil, false
	}
	return rest, true
}

// recordProjectError explains why haiven.toml could not be loaded, at the
// given severity: an error for a command that needs the project, a warning
// for one that must keep working without it.
func recordProjectError(out *result.Envelope, err error, severity result.Severity) {
	add := func(code result.DiagnosticCode, message string) {
		out.Add(result.Diagnostic{Code: code, Severity: severity, Message: message, Path: project.FileName})
	}
	var invalid *project.InvalidError
	switch {
	case err == nil:
		add(result.DiagInternalError, "project was not loaded")
	case errors.Is(err, project.ErrNotFound):
		add(result.DiagProjectNotFound, fmt.Sprintf("no %s in this directory or any parent", project.FileName))
	case errors.As(err, &invalid):
		for _, p := range invalid.Problems {
			code := result.DiagInvalidProjectFile
			if p.Pin {
				code = result.DiagInvalidVersionPin
			}
			add(code, p.Field+": "+p.Message)
		}
	default:
		add(result.DiagInternalError, fmt.Sprintf("reading %s: %v", project.FileName, err))
	}
}

// Run executes one haiven invocation and returns the process exit code.
func Run(ctx context.Context, e Env, args []string) int {
	original := args
	jsonMode, args := splitJSONFlag(args, e.Getenv)
	if e.Getenv == nil {
		e.Getenv = func(string) string { return "" }
	}

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	switch name {
	case "", "-h", "--help":
		name = "help"
	case "--version", "-V":
		name = "version"
	}
	if len(args) == 0 {
		args = []string{name}
	} else {
		args[0] = name
	}
	cmd, rest := lookupArgs(args)
	if cmd == nil {
		out := result.New(RootCommand, e.Version, result.OutcomeOK)
		prefix, unknown := longestGroupPrefix(args)
		if subs := groupCommands(prefix); len(subs) > 0 {
			out.Command = prefix
			if unknown != "" {
				out.Fail(result.DiagUnknownCommand, "unknown command %q; haiven %s has: %s", strings.Join(args, " "), prefix, strings.Join(subs, ", "))
			} else {
				out.Fail(result.DiagUnknownCommand, "haiven %s needs a subcommand: %s", prefix, strings.Join(subs, ", "))
			}
			out.Next = []string{"haiven help " + prefix}
			return emit(e, jsonMode, out, nil)
		}
		out.Fail(result.DiagUnknownCommand, "unknown command %q", name)
		out.Next = []string{"haiven capabilities"}
		return emit(e, jsonMode, out, nil)
	}

	c := &call{ctx: ctx, e: e, cmd: cmd, args: rest, json: jsonMode, out: result.New(cmd.name, e.Version, result.OutcomeOK)}

	// Load the project once. The pin applies to every command, so the whole
	// project uses one CLI release; a broken project file is reported by the
	// commands that read it, not here.
	c.project, c.projectErr = project.Load(e.Cwd)
	if c.project != nil && e.Reexec != nil {
		pinned := c.project.File.Haiven.Version
		if e.Pin == nil {
			if e.pinCacheErr != nil && releasePinNeedsCache(e, pinned) {
				c.out.Fail(result.DiagPinnedVersionUnavailable, "haiven %s pinned by %s: user cache directory: %v", pinned, project.FileName, e.pinCacheErr)
				return emit(e, jsonMode, c.out, nil)
			}
		} else {
			active := ""
			if e.Getenv != nil {
				active = e.Getenv(pin.ActiveEnv)
			}
			decision, err := e.Pin.Resolve(ctx, pinned, active)
			if err != nil {
				code := result.DiagPinnedVersionUnavailable
				if errors.Is(err, pin.ErrChecksumMismatch) {
					code = result.DiagPinnedChecksumMismatch
				}
				c.out.Add(result.Diagnostic{Code: code, Message: fmt.Sprintf("haiven %s pinned by %s: %v", pinned, project.FileName, err), Path: project.FileName})
				return emit(e, jsonMode, c.out, nil)
			}
			if decision.Reexec {
				status, err := e.Reexec(decision.Binary, original, pinned)
				if err != nil {
					c.out.Fail(result.DiagPinnedVersionUnavailable, "running haiven %s from %s: %v", pinned, decision.Binary, err)
					return emit(e, jsonMode, c.out, nil)
				}
				return status
			}
		}
	}

	cmd.run(c)
	return emit(e, jsonMode, c.out, c.human)
}

// releasePinNeedsCache reports whether a non-dev binary must materialize a
// different project pin from the user cache before it runs a command.
func releasePinNeedsCache(e Env, pinned string) bool {
	if pinned == "" || pinned == e.Version || version.IsDev(e.Version) {
		return false
	}
	if e.Getenv != nil && e.Getenv(pin.ActiveEnv) != "" {
		return false
	}
	return true
}

// lookupArgs resolves the longest leading command name. This permits nested
// command families such as `sdk starter list` without teaching dispatch about
// starter or engine names.
func lookupArgs(args []string) (*command, []string) {
	for n := len(args); n > 0; n-- {
		if c := lookup(strings.Join(args[:n], " ")); c != nil {
			return c, args[n:]
		}
	}
	if len(args) > 1 {
		return nil, args[1:]
	}
	return nil, nil
}

func longestGroupPrefix(args []string) (string, string) {
	for n := len(args); n > 0; n-- {
		prefix := strings.Join(args[:n], " ")
		if len(groupCommands(prefix)) > 0 {
			if n < len(args) {
				return prefix, args[n]
			}
			return prefix, ""
		}
	}
	if len(args) > 0 {
		return args[0], ""
	}
	return "", ""
}

// emit prints the envelope and returns its exit code.
func emit(e Env, jsonMode bool, out *result.Envelope, human func(io.Writer)) int {
	if jsonMode {
		if err := out.WriteJSON(e.Stdout); err != nil {
			return int(result.ExitInternal)
		}
		return int(out.Exit())
	}
	if human != nil {
		human(e.Stdout)
	}
	_ = out.WriteHuman(e.Stderr)
	return int(out.Exit())
}

// splitJSONFlag removes --json from anywhere before a "--" terminator and
// reports whether JSON output was requested by flag or by HAIVEN_OUTPUT.
func splitJSONFlag(args []string, getenv func(string) string) (bool, []string) {
	jsonMode := getenv != nil && strings.EqualFold(getenv(OutputEnv), "json")
	kept := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			kept = append(kept, args[i:]...)
			break
		}
		if a == "--json" || a == "-json" {
			jsonMode = true
			continue
		}
		kept = append(kept, a)
	}
	return jsonMode, kept
}

// commands is the command table, sorted top-level first, then by group, in
// capabilities and help.
var commands []*command

// group is a named set of subcommands, `haiven <group> <command>`. The top
// level is for every Haiven user; a group gathers the commands of one
// audience.
type group struct {
	name    string
	summary string
}

// groups is the group table. Every command whose name has a space belongs
// to one of these.
var groups []group

// groupOf returns the group part of a command name, or "" at the top level.
func groupOf(name string) string {
	g, _, ok := strings.Cut(name, " ")
	if !ok {
		return ""
	}
	return g
}

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// groupCommands lists the subcommand names of a command group, or nil when
// name is not a group.
func groupCommands(name string) []string {
	if name == "" {
		return nil
	}
	var subs []string
	for _, c := range sortedCommands() {
		if rest, ok := strings.CutPrefix(c.name, name+" "); ok {
			subs = append(subs, rest)
		}
	}
	return subs
}

func sortedCommands() []*command {
	out := append([]*command(nil), commands...)
	sort.Slice(out, func(i, j int) bool {
		gi, gj := groupOf(out[i].name), groupOf(out[j].name)
		if gi != gj {
			return gi < gj
		}
		return out[i].name < out[j].name
	})
	return out
}

// Main is the production entry point.
func Main() int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "haiven: cannot read the working directory:", err)
		return int(result.ExitInternal)
	}
	// Long-running commands stop on Ctrl-C or SIGTERM and still print their
	// envelope. Preserve the exact signal as the cancellation cause so a v2
	// lifecycle runner receives the same cancellation intent.
	ctx, cancel := context.WithCancelCause(context.Background())
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case received := <-signals:
			cancel(starter.CancellationSignal(received))
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(signals)
		close(done)
		cancel(nil)
	}()
	return Run(ctx, Production(cwd), os.Args[1:])
}

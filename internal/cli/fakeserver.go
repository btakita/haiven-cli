package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haiven-dev/haiven-cli/internal/fakeserver"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

// DefaultFakeToken is the token `haiven sdk fake-server` accepts when no --token
// is given. It is a fixture value, not a credential.
const (
	DefaultFakeToken  = "dev-token"
	DefaultFakeUserID = "dev-user"
)

// FakeServerData is the data of `haiven sdk fake-server`, printed when it stops.
// The same document, with zero stats, is written to --ready-file once the
// socket is listening.
type FakeServerData struct {
	URL             string             `json:"url" jsonschema_description:"Base http:// URL (the bound address)."`
	WSURL           string             `json:"ws_url" jsonschema_description:"The gaming socket: ws://<addr>/v0/ws."`
	ControlURL      string             `json:"control_url" jsonschema_description:"Base of the unauthenticated control levers: push, expire, answer-deferred, report-dropped (POST, ?user= selects one user) and stats (GET)."`
	Subprotocol     string             `json:"subprotocol" jsonschema_description:"Accepted when a client offers it; never required."`
	Tokens          []fakeserver.Token `json:"tokens" jsonschema_description:"The fake verifier's tokens. Fixture values, not credentials."`
	Routes          []string           `json:"routes" jsonschema_description:"Message types the router answers; ping is always routed."`
	RateLimited     []string           `json:"rate_limited"`
	Deferred        []string           `json:"deferred"`
	Record          string             `json:"record,omitempty" jsonschema_description:"The --record transcript path, relative to the working directory, slash-separated."`
	RecordedEntries int                `json:"recorded_entries"`
	Stats           fakeserver.Stats   `json:"stats"`
}

// listFlag collects a repeatable, comma-separable string flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("empty value in %q", v)
		}
		*l = append(*l, part)
	}
	return nil
}

// tokenFlag parses --token VALUE:USER[:expired|:invalid].
type tokenFlag []fakeserver.Token

func (t *tokenFlag) String() string { return fmt.Sprint(len(*t)) }

func (t *tokenFlag) Set(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("--token %q: want VALUE:USER or VALUE:USER:expired or VALUE:USER:invalid", v)
	}
	tok := fakeserver.Token{Value: parts[0], UserID: parts[1], Valid: true}
	if len(parts) == 3 {
		switch parts[2] {
		case "expired":
			tok.Expired = true
		case "invalid":
			tok.Valid = false
		default:
			return fmt.Errorf("--token %q: the third field is expired or invalid", v)
		}
	}
	*t = append(*t, tok)
	return nil
}

func sortedUnique(xs []string, always ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range append(append([]string{}, always...), xs...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func runFakeServer(c *call) {
	fs := c.flags()
	addr := fs.String("addr", "127.0.0.1:0", "address to bind; keep it loopback, the /control/ levers are unauthenticated")
	var tokens tokenFlag
	var routes, rateLimited, deferred listFlag
	fs.Var(&tokens, "token", "VALUE:USER[:expired|:invalid], repeatable")
	fs.Var(&routes, "route", "message type the router answers, repeatable")
	fs.Var(&rateLimited, "rate-limited", "message type the rate limiter rejects, repeatable")
	fs.Var(&deferred, "deferred", "routed type answered only on /control/answer-deferred, repeatable")
	record := fs.String("record", "", "write a JSON Lines transcript of every socket to this file")
	readyFile := fs.String("ready-file", "", "write the listening addresses as JSON to this file once ready")
	if _, ok := c.parse(fs, 0, 0); !ok {
		return
	}
	if len(tokens) == 0 {
		tokens = tokenFlag{{Value: DefaultFakeToken, UserID: DefaultFakeUserID, Valid: true}}
	}
	opts := fakeserver.Options{
		Tokens:      tokens,
		Routes:      sortedUnique(routes, fakeserver.TypePing),
		RateLimited: sortedUnique(rateLimited),
		Deferred:    sortedUnique(deferred),
	}
	d := FakeServerData{
		Subprotocol: fakeserver.Subprotocol,
		Tokens:      opts.Tokens,
		Routes:      opts.Routes,
		RateLimited: opts.RateLimited,
		Deferred:    opts.Deferred,
	}

	var rec *fakeserver.Recorder
	if *record != "" {
		path := c.abs(*record)
		f, err := os.Create(path)
		if err != nil {
			c.out.Fail(result.DiagFakeServerUnavailable, "opening the record file: %v", err)
			return
		}
		rec = fakeserver.NewRecorder(f)
		d.Record = relToCwd(c.e.Cwd, path)
	}

	srv := fakeserver.New(opts, rec)
	base, err := srv.Listen(*addr)
	if err != nil {
		_ = rec.Close()
		c.out.Fail(result.DiagFakeServerUnavailable, "binding %s: %v", *addr, err)
		return
	}
	d.URL = base
	d.WSURL = "ws" + strings.TrimPrefix(base, "http") + fakeserver.WSPath
	d.ControlURL = base + fakeserver.ControlPrefix

	if *readyFile != "" {
		if err := writeReadyFile(c.abs(*readyFile), d); err != nil {
			_ = rec.Close()
			c.out.Fail(result.DiagFakeServerUnavailable, "writing the ready file: %v", err)
			return
		}
	}
	if !c.json {
		fmt.Fprintf(c.e.Stdout, "haiven sdk fake-server listening on %s\ncontrol: %s\ntokens: %s\nCtrl-C stops it.\n", d.WSURL, d.ControlURL, tokenSummary(d.Tokens))
	}
	serveErr := srv.Serve(c.ctx)
	if err := rec.Close(); err != nil {
		c.out.Fail(result.DiagFakeServerUnavailable, "writing the record file: %v", err)
	}
	if serveErr != nil {
		c.out.Fail(result.DiagFakeServerUnavailable, "serving: %v", serveErr)
	}
	d.RecordedEntries = rec.Entries()
	d.Stats = srv.Stats()
	c.out.Data = d
	c.human = func(w io.Writer) {
		st := d.Stats
		fmt.Fprintf(w, "stopped: %d connection(s), %d client frame(s), %d server frame(s), %d dropped in, %d dropped out\n",
			st.Connections, st.ClientFrames, st.ServerFrames, st.DroppedInbound, st.DroppedOutbound)
		if d.Record != "" {
			fmt.Fprintf(w, "transcript: %s (%d entries)\n", d.Record, d.RecordedEntries)
		}
	}
}

func tokenSummary(ts []fakeserver.Token) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.Value + " -> " + t.UserID
		if !t.Valid {
			parts[i] += " (invalid)"
		} else if t.Expired {
			parts[i] += " (expired)"
		}
	}
	return strings.Join(parts, ", ")
}

// writeReadyFile writes atomically, so a watcher never reads half a file.
func writeReadyFile(path string, d FakeServerData) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".haiven-ready-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// abs resolves a user path against the working directory.
func (c *call) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.e.Cwd, p)
}

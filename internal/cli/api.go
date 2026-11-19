package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/haiven-dev/haiven-cli/internal/api"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

// Environment variables `haiven api` reads.
const (
	// OpenAPIEnv names the public API description to read when --openapi
	// is not given: a file path or an http(s) URL.
	OpenAPIEnv = "HAIVEN_OPENAPI"
	// APIURLEnv overrides the description's server URL.
	APIURLEnv = "HAIVEN_API_URL"
	// TokenEnv is the bearer token `haiven api call` sends to operations
	// that declare security. It is never printed.
	TokenEnv = "HAIVEN_TOKEN"
)

// MaxResponseBytes bounds the response body `haiven api call` reads.
const MaxResponseBytes = 1 << 20

// MaxDescriptionBytes bounds one API description `haiven api` reads.
const MaxDescriptionBytes = 32 << 20

// MaxListedOperations bounds `haiven api list`.
const MaxListedOperations = 1000

// APISurfaceInfo is one HTTP description.
type APISurfaceInfo struct {
	Surface    string `json:"surface" jsonschema_description:"The description's x-haiven-surface (e.g. gaming/v0), or else its info.title in lower kebab case."`
	Title      string `json:"title"`
	Version    string `json:"version" jsonschema_description:"The description's info.version."`
	Server     string `json:"server" jsonschema_description:"The description's first server URL; the default base URL."`
	Source     string `json:"source" jsonschema_description:"Where the description was read from: a URL, or a file path relative to the working directory, slash-separated."`
	Operations int    `json:"operations"`
}

// APIOperationInfo is one operation in `haiven api list`.
type APIOperationInfo struct {
	Surface      string          `json:"surface"`
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	Summary      string          `json:"summary"`
	RequiresAuth bool            `json:"requires_auth" jsonschema_description:"True when the operation declares a security requirement with no anonymous alternative; set HAIVEN_TOKEN."`
	Parameters   []api.Parameter `json:"parameters"`
	Body         []string        `json:"body" jsonschema_description:"Request body media types the operation accepts."`
}

// APIListData is the data of `haiven api list`.
type APIListData struct {
	Surfaces   []APISurfaceInfo   `json:"surfaces"`
	Operations []APIOperationInfo `json:"operations"`
}

// APIResponse is the API's answer.
type APIResponse struct {
	Status      int             `json:"status"`
	ContentType string          `json:"content_type"`
	Body        json.RawMessage `json:"body,omitempty" jsonschema_description:"The response body when it is JSON."`
	BodyText    string          `json:"body_text,omitempty" jsonschema_description:"The response body when it is not JSON, or was cut at the bound."`
	ErrorCode   string          `json:"error_code,omitempty" jsonschema_description:"The API's own error code (the body's code or error field) on an error status."`
	Redacted    int             `json:"redacted" jsonschema_description:"Token, secret and password fields replaced with [redacted]; --show-secrets keeps them."`
}

// APICallData is the data of `haiven api call`.
type APICallData struct {
	Surface     string       `json:"surface"`
	Method      string       `json:"method"`
	Path        string       `json:"path" jsonschema_description:"The operation's path template."`
	URL         string       `json:"url" jsonschema_description:"The request URL, query included."`
	Auth        api.AuthMode `json:"auth" jsonschema:"enum=none,enum=bearer,enum=missing" jsonschema_description:"none: no security declared; bearer: HAIVEN_TOKEN attached; missing: required but HAIVEN_TOKEN is unset."`
	ContentType string       `json:"content_type,omitempty" jsonschema_description:"The request body's media type, when there is one."`
	DryRun      bool         `json:"dry_run"`
	Response    *APIResponse `json:"response,omitempty" jsonschema_description:"Absent on --dry-run and when the API could not be reached."`
}

// descriptions reads the API descriptions named by --openapi, or else by
// HAIVEN_OPENAPI. Each is a file or an http(s) URL. There is no default: the
// Haiven API does not serve its description, and the CLI never reads one from
// anywhere but the caller's choice.
func (c *call) descriptions(sources []string) ([]*api.Surface, bool) {
	if len(sources) == 0 {
		if env := strings.TrimSpace(c.e.Getenv(OpenAPIEnv)); env != "" {
			sources = []string{env}
		}
	}
	if len(sources) == 0 {
		c.out.Fail(result.DiagOpenAPINotConfigured, "no API description: pass --openapi <file|URL> or set %s (the Haiven API does not serve its description)", OpenAPIEnv)
		return nil, false
	}
	var surfaces []*api.Surface
	seen := map[string]string{}
	for _, src := range sources {
		raw, label, err := c.readDescription(src)
		if err != nil {
			c.out.Add(result.Diagnostic{Code: result.DiagOpenAPIUnavailable, Message: err.Error(), Path: label})
			return nil, false
		}
		s, err := api.Parse(label, raw)
		var invalid *api.InvalidError
		if errors.As(err, &invalid) {
			c.out.Add(result.Diagnostic{Code: result.DiagInvalidOpenAPI, Message: invalid.Err.Error(), Path: label})
			return nil, false
		} else if err != nil {
			c.out.Fail(result.DiagInternalError, "reading %s: %v", label, err)
			return nil, false
		}
		if first, dup := seen[s.ID]; dup {
			c.out.Add(result.Diagnostic{Code: result.DiagInvalidOpenAPI, Message: fmt.Sprintf("surface %q is described by both %s and %s; set %s in one of them", s.ID, first, label, api.SurfaceExtension), Path: label})
			return nil, false
		}
		seen[s.ID] = label
		surfaces = append(surfaces, s)
	}
	sort.Slice(surfaces, func(i, j int) bool { return surfaces[i].ID < surfaces[j].ID })
	return surfaces, true
}

func isURL(src string) bool {
	return strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://")
}

// readDescription reads one description, bounded, and returns its bytes
// and the label diagnostics use: the URL, or the path relative to the
// working directory.
func (c *call) readDescription(src string) ([]byte, string, error) {
	if !isURL(src) {
		path := c.abs(src)
		label := relToCwd(c.e.Cwd, path)
		f, err := os.Open(path)
		if err != nil {
			// Without the absolute path or OS wording, so the message is the
			// same on every machine.
			var pe *fs.PathError
			switch {
			case errors.Is(err, fs.ErrNotExist):
				err = fmt.Errorf("%s: no such file", label)
			case errors.As(err, &pe):
				err = fmt.Errorf("%s: %w", label, pe.Err)
			}
			return nil, label, err
		}
		defer f.Close()
		raw, err := readBounded(f, label)
		return raw, label, err
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, src, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "haiven-cli/"+c.e.Version)
	resp, err := (&http.Client{Transport: c.e.HTTP}).Do(req)
	if err != nil {
		return nil, src, fmt.Errorf("GET %s: %w", src, errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, src, fmt.Errorf("GET %s: HTTP %d", src, resp.StatusCode)
	}
	raw, err := readBounded(resp.Body, src)
	return raw, src, err
}

func readBounded(r io.Reader, label string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxDescriptionBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxDescriptionBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", label, MaxDescriptionBytes)
	}
	return raw, nil
}

// selectSurfaces narrows surfaces to the --surface values.
func (c *call) selectSurfaces(all []*api.Surface, want []string) ([]*api.Surface, bool) {
	if len(want) == 0 {
		return all, true
	}
	byID := map[string]*api.Surface{}
	ids := make([]string, len(all))
	for i, s := range all {
		byID[s.ID] = s
		ids[i] = s.ID
	}
	var out []*api.Surface
	for _, w := range want {
		s, ok := byID[w]
		if !ok {
			c.out.Fail(result.DiagInvalidFlag, "--surface %q is not published; known: %s", w, strings.Join(ids, ", "))
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func summaryOf(o *api.Op) string {
	s := strings.TrimSpace(o.Summary)
	if s == "" {
		s = strings.TrimSpace(o.Description)
		if i := strings.Index(s, ". "); i >= 0 {
			s = s[:i+1]
		}
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		}
	}
	const limit = 160
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit-1]) + "…"
	}
	return s
}

func bodyTypes(o *api.Op) []string {
	out := []string{}
	if o.RequestBody != nil {
		for t := range o.RequestBody.Content {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func runAPIList(c *call) {
	fs := c.flags()
	var sources, surfaces listFlag
	fs.Var(&sources, "openapi", "API description file or http(s) URL, repeatable (default: $"+OpenAPIEnv+")")
	fs.Var(&surfaces, "surface", "only this surface, repeatable")
	match := fs.String("match", "", "only paths containing this text (case-insensitive)")
	if _, ok := c.parse(fs, 0, 0); !ok {
		return
	}
	all, ok := c.descriptions(sources)
	if !ok {
		return
	}
	selected, ok := c.selectSurfaces(all, surfaces)
	if !ok {
		return
	}
	d := APIListData{Surfaces: []APISurfaceInfo{}, Operations: []APIOperationInfo{}}
	needle := strings.ToLower(*match)
	for _, s := range selected {
		d.Surfaces = append(d.Surfaces, APISurfaceInfo{
			Surface: s.ID, Title: s.Spec.Info.Title, Version: s.Spec.Info.Version,
			Server: s.Server(), Source: s.Source, Operations: len(s.Ops),
		})
		for _, o := range s.Ops {
			if needle != "" && !strings.Contains(strings.ToLower(o.Path), needle) {
				continue
			}
			if len(d.Operations) == MaxListedOperations {
				c.out.Truncated = true
				break
			}
			d.Operations = append(d.Operations, APIOperationInfo{
				Surface: s.ID, Method: o.Method, Path: o.Path, Summary: summaryOf(o),
				RequiresAuth: o.RequiresAuth(), Parameters: o.Parameters, Body: bodyTypes(o),
			})
		}
	}
	c.out.Data = d
	c.human = func(w io.Writer) {
		for _, s := range d.Surfaces {
			fmt.Fprintf(w, "%s  %s %s  %s  (%d operations)\n", s.Surface, s.Title, s.Version, s.Server, s.Operations)
		}
		fmt.Fprintln(w)
		for _, o := range d.Operations {
			lock := " "
			if o.RequiresAuth {
				lock = "*"
			}
			fmt.Fprintf(w, "%s %-6s %-48s %s  [%s]\n", lock, o.Method, o.Path, o.Summary, o.Surface)
		}
		fmt.Fprintln(w, "\n* requires HAIVEN_TOKEN")
	}
}

// pairFlag collects repeatable name=value flags.
type pairFlag []api.Pair

func (p *pairFlag) String() string { return fmt.Sprint(len(*p)) }

func (p *pairFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("%q is not name=value", v)
	}
	*p = append(*p, api.Pair{Name: name, Value: value})
	return nil
}

func runAPICall(c *call) {
	fs := c.flags()
	var sources, surfaces listFlag
	fs.Var(&sources, "openapi", "API description file or http(s) URL, repeatable (default: $"+OpenAPIEnv+")")
	fs.Var(&surfaces, "surface", "the surface, when the path is in several")
	var pathParams, queryParams, formFields pairFlag
	fs.Var(&pathParams, "p", "path parameter name=value, repeatable")
	fs.Var(&queryParams, "q", "query parameter name=value, repeatable")
	fs.Var(&formFields, "f", "form field name=value, repeatable")
	bodyArg := fs.String("body", "", "JSON request body, @file, or @- for stdin")
	baseURL := fs.String("base-url", "", "API base URL (default: $"+APIURLEnv+", then the description's server)")
	dryRun := fs.Bool("dry-run", false, "build and print the request without sending it")
	showSecrets := fs.Bool("show-secrets", false, "do not redact token, secret and password fields in the response")
	pos, ok := c.parse(fs, 2, 2)
	if !ok {
		return
	}
	method, path := strings.ToUpper(pos[0]), pos[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	all, ok := c.descriptions(sources)
	if !ok {
		return
	}
	selected, ok := c.selectSurfaces(all, surfaces)
	if !ok {
		return
	}
	matches := api.Find(selected, method, path)
	switch len(matches) {
	case 0:
		c.out.Fail(result.DiagUnknownOperation, "no published operation %s %s", method, path)
		c.out.Next = []string{"haiven api list --match " + firstSegment(path)}
		return
	case 1:
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.Surface.ID
		}
		c.out.Fail(result.DiagAmbiguousOperation, "%s %s is published by %s", method, path, strings.Join(ids, ", "))
		return
	}
	op := matches[0]

	var body []byte
	if *bodyArg != "" {
		var err error
		if body, err = c.readBody(*bodyArg); err != nil {
			c.out.Fail(result.DiagInvalidRequestBody, "%v", err)
			return
		}
	}
	base := *baseURL
	if base == "" {
		base = c.e.Getenv(APIURLEnv)
	}
	if base == "" {
		base = op.Surface.Server()
	}
	if base == "" {
		c.out.Fail(result.DiagMissingArgument, "%s declares no server; pass --base-url", op.Surface.ID)
		return
	}
	built, problems := api.Call{
		Op: op, RequestPath: path, BaseURL: base,
		Path: pathParams, Query: queryParams, Form: formFields, Body: body,
		Token: c.e.Getenv(TokenEnv), UserAgent: "haiven-cli/" + c.e.Version,
	}.Build()
	for _, p := range problems {
		code := result.DiagUnknownParameter
		switch p.Kind {
		case api.ProblemMissing:
			code = result.DiagMissingParameter
		case api.ProblemBody:
			code = result.DiagInvalidRequestBody
		case api.ProblemEscape:
			code = result.DiagMalformedPathEscape
		}
		c.out.Fail(code, "%s", p.Message)
	}
	if built == nil {
		return
	}
	d := APICallData{Surface: op.Surface.ID, Method: op.Method, Path: op.Path, URL: built.URL, Auth: built.Auth, ContentType: built.ContentType, DryRun: *dryRun}
	c.out.Data = d
	c.human = func(w io.Writer) { fmt.Fprintf(w, "%s %s (auth: %s)\n", d.Method, d.URL, d.Auth) }
	if *dryRun {
		return
	}
	// The description the caller named is not an approved immutable contract
	// artifact. Do not send, and do not attach HAIVEN_TOKEN to its server.
	if reason := unverifiedContractReason(); reason != "" {
		c.out.Fail(result.DiagContractArtifactUnverified, "%s", reason)
		c.out.Next = []string{"Repeat with --dry-run to inspect the request without sending it."}
		return
	}

	client := &http.Client{
		Transport: c.e.HTTP,
		// Report a redirect as the answer rather than following it with
		// the token attached.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(built.Request.WithContext(c.ctx))
	if err != nil {
		c.out.Fail(result.DiagAPIUnreachable, "%s %s: %v", op.Method, built.URL, errors.Unwrap(err))
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		c.out.Fail(result.DiagAPIUnreachable, "reading the response: %v", err)
		return
	}
	r := &APIResponse{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type")}
	if len(raw) > MaxResponseBytes {
		raw = raw[:MaxResponseBytes]
		c.out.Truncated = true
		if !*showSecrets {
			var n int
			raw, n = redactPrefix(raw)
			r.Redacted = n
		}
		r.BodyText = string(raw)
	} else if len(raw) > 0 {
		if json.Valid(raw) {
			var n int
			if !*showSecrets {
				raw, n = redact(raw)
			}
			r.Body, r.Redacted = compactJSON(raw), n
		} else {
			r.BodyText = string(raw)
		}
	}
	if resp.StatusCode >= 400 {
		r.ErrorCode = apiErrorCode(r.Body)
		msg := fmt.Sprintf("HTTP %d from %s %s", resp.StatusCode, op.Method, op.Path)
		if r.ErrorCode != "" {
			msg += ": " + r.ErrorCode
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			if built.Auth == api.AuthMissing {
				msg += " (" + TokenEnv + " is not set)"
			}
			c.out.Fail(result.DiagAPIUnauthorized, "%s", msg)
		} else {
			c.out.Fail(result.DiagAPIHTTPError, "%s", msg)
		}
	}
	d.Response = r
	c.out.Data = d
	c.human = func(w io.Writer) {
		fmt.Fprintf(w, "%s %s -> %d %s\n", d.Method, d.URL, r.Status, r.ContentType)
		if len(r.Body) > 0 {
			var buf bytes.Buffer
			if json.Indent(&buf, r.Body, "", "  ") == nil {
				fmt.Fprintln(w, buf.String())
			}
		} else if r.BodyText != "" {
			fmt.Fprintln(w, r.BodyText)
		}
	}
}

// unverifiedContractReason reports why api call must not send. An empty
// string is the only permission to execute, and only a resolver that has
// checked an immutable public contract artifact's version and digest may
// return one. No such resolver exists yet.
func unverifiedContractReason() string {
	return "API execution is unavailable until an immutable public contract artifact is verified by version and digest"
}

func firstSegment(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	return "/" + segs[0]
}

func (c *call) readBody(arg string) ([]byte, error) {
	switch {
	case arg == "@-":
		if c.e.Stdin == nil {
			return nil, errors.New("--body @-: no stdin")
		}
		b, err := io.ReadAll(io.LimitReader(c.e.Stdin, MaxResponseBytes+1))
		if err != nil {
			return nil, fmt.Errorf("--body @-: %w", err)
		}
		return b, nil
	case strings.HasPrefix(arg, "@"):
		b, err := os.ReadFile(c.abs(arg[1:]))
		if err != nil {
			return nil, fmt.Errorf("--body %s: %w", arg, err)
		}
		return b, nil
	}
	return []byte(arg), nil
}

// secretKeys are response fields redacted unless --show-secrets: the
// workspace is livestreamed and agent transcripts are long-lived.
var secretKeys = map[string]bool{
	"access_token": true, "accesstoken": true, "refresh_token": true, "refreshtoken": true,
	"id_token": true, "idtoken": true, "token": true, "secret": true, "client_secret": true,
	"clientsecret": true, "password": true, "api_key": true, "apikey": true,
}

// redact replaces secret-named values in a JSON document. Key order is not
// preserved (objects are re-encoded sorted), which is why it only runs on a
// response, never on anything compared byte for byte.
func redact(raw []byte) ([]byte, int) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw, 0
	}
	n := 0
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if secretKeys[strings.ToLower(k)] {
					if s, ok := val.(string); ok && s != "" {
						t[k] = "[redacted]"
						n++
						continue
					}
				}
				t[k] = walk(val)
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		}
		return x
	}
	v = walk(v)
	if n == 0 {
		return raw, 0
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw, 0
	}
	return out, n
}

// redactPrefix redacts a JSON body cut at the bound, which redact cannot
// parse. It scans tokens as far as they decode, replaces each non-empty string
// under a secret key in place, and drops the tail from the first token that
// does not decode (or after the last one that did), so a secret cut mid-value
// is never printed. A prefix that
// does not open a JSON object or array is returned as it is, like an
// unbounded non-JSON body.
func redactPrefix(raw []byte) ([]byte, int) {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return raw, 0
	}
	type frame struct{ object, wantKey bool }
	var (
		stack   []frame
		secret  bool
		spans   [][2]int64
		scanned int64
	)
	valueDone := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].wantKey = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			break
		}
		scanned = dec.InputOffset()
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, frame{object: true, wantKey: true})
			case '[':
				stack = append(stack, frame{})
			default:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				valueDone()
			}
			secret = false
		case string:
			if top := len(stack) - 1; top >= 0 && stack[top].object && stack[top].wantKey {
				stack[top].wantKey = false
				secret = secretKeys[strings.ToLower(t)]
				continue
			}
			if secret && t != "" {
				start := before + int64(bytes.IndexByte(raw[before:scanned], '"'))
				spans = append(spans, [2]int64{start, scanned})
			}
			secret = false
			valueDone()
		default:
			secret = false
			valueDone()
		}
	}
	var out bytes.Buffer
	at := int64(0)
	for _, sp := range spans {
		out.Write(raw[at:sp[0]])
		out.WriteString(`"[redacted]"`)
		at = sp[1]
	}
	out.Write(raw[at:scanned])
	return out.Bytes(), len(spans)
}

func compactJSON(raw []byte) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

// apiErrorCode reads the API's own error code from an error body.
func apiErrorCode(body json.RawMessage) string {
	var e struct {
		Code  any `json:"code"`
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	for _, v := range []any{e.Code, e.Error} {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

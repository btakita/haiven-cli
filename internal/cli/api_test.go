package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haiven-dev/haiven-cli/internal/result"
)

// The descriptions under testdata/openapi are synthetic: written by hand in
// the shape of a public API description (OpenAPI 3.0.3, no operationIds, an
// apiKey Authorization header carrying a bearer token), copying no real API.
// gaming.json names its surface with x-haiven-surface; studio.json does not,
// so its surface is its title in kebab case.
const (
	gamingSurface = "gaming/v0"
	studioSurface = "example-studio-http-api"
)

func readTestdata(name string) string {
	raw, err := os.ReadFile(filepath.Join("testdata", "openapi", name))
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// descriptionsFixture writes both descriptions under openapi/.
func descriptionsFixture() fixture {
	return fixture{
		"openapi/gaming.json": readTestdata("gaming.json"),
		"openapi/studio.json": readTestdata("studio.json"),
	}
}

// both is the --openapi flags for both descriptions.
var both = []string{"--openapi", "openapi/gaming.json", "--openapi", "openapi/studio.json"}

func merge(fs ...fixture) fixture {
	out := fixture{}
	for _, f := range fs {
		for k, v := range f {
			out[k] = v
		}
	}
	return out
}

// fakeAPI answers by method and path, and records each request.
type fakeAPI struct {
	routes map[string]fakeResponse
	err    error
	seen   []*http.Request
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	f.seen = append(f.seen, r)
	if f.err != nil {
		return nil, f.err
	}
	resp, ok := f.routes[r.Method+" "+r.URL.Path]
	if !ok {
		resp = fakeResponse{404, `{"code":"not_found","message":"no route"}`}
	}
	return &http.Response{
		StatusCode: resp.status,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(resp.body)),
		Request:    r,
	}, nil
}

const testBase = "https://api.example.test/v0"

func apiGolden() map[string]invocation {
	files := descriptionsFixture()
	call := func(args ...string) []string {
		out := append([]string{"api", "call"}, args...)
		out = append(out, both...)
		return append(out, "--base-url", testBase, "--json")
	}
	list := func(args ...string) []string {
		return append(append([]string{"api", "list"}, args...), "--json")
	}
	ok := &fakeAPI{routes: map[string]fakeResponse{
		"GET /v0/apps/a-1":               {200, `{"id": "a-1", "name": "Boss Room"}`},
		"POST /v0/auth/handoff/exchange": {200, fmt.Sprintf(`{"access_token": %q, "token_type": "Bearer", "expires_in": 300}`, "live-looking-value")},
		"GET /v0/me":                     {401, `{"code": "unauthorized", "message": "missing bearer"}`},
		"GET /v0/apps/tags":              {500, `{"error": "internal_error"}`},
	}}
	return map[string]invocation{
		"api-list":                   {args: list(both...), files: files},
		"api-list-filtered":          {args: list("--surface", gamingSurface, "--match", "APPS/"), files: files, env: map[string]string{OpenAPIEnv: "openapi/gaming.json"}},
		"api-list-no-source":         {args: list()},
		"api-list-url":               {args: list("--openapi", testBase+"/openapi.json"), http: &fakeAPI{routes: map[string]fakeResponse{"GET /v0/openapi.json": {200, readTestdata("studio.json")}}}},
		"api-list-url-unavailable":   {args: list("--openapi", testBase+"/openapi.json"), http: &fakeAPI{routes: map[string]fakeResponse{}}},
		"api-list-missing-file":      {args: list("--openapi", "openapi/nope.json")},
		"api-list-unknown-surface":   {args: list(append(both, "--surface", "nope/v9")...), files: files},
		"api-list-invalid-openapi":   {args: list("--openapi", "openapi/swagger2.json"), files: fixture{"openapi/swagger2.json": readTestdata("swagger2.json")}},
		"api-list-duplicate-surface": {args: list("--openapi", "openapi/gaming.json", "--openapi", "copy/gaming.json"), files: merge(files, fixture{"copy/gaming.json": readTestdata("gaming.json")})},
		"api-group":                  {args: []string{"api", "--json"}},
		"api-call-dry-run":           {args: call("GET", "/apps", "-q", "q=boss room", "-q", "tag=coop", "-q", "tag=pvp", "--dry-run"), files: files},
		"api-call-ok":                {args: call("get", "apps/a-1"), files: files, http: ok},
		"api-call-template":          {args: call("GET", "/apps/{app_id}", "-p", "app_id=a-1"), files: files, http: ok},
		"api-call-form-redacted":     {args: call("POST", "/auth/handoff/exchange", "-f", "code=c", "-f", "code_verifier=v"), files: files, http: ok},
		"api-call-unauthorized":      {args: call("GET", "/me"), files: files, http: ok},
		"api-call-http-error":        {args: call("GET", "/apps/tags", "--surface", gamingSurface), files: files, http: ok},
		"api-call-ambiguous":         {args: call("GET", "/apps/tags"), files: files, http: ok},
		"api-call-unknown-operation": {args: call("DELETE", "/apps"), files: files},
		"api-call-missing-param":     {args: call("POST", "/auth/handoff/exchange", "-f", "code=c"), files: files},
		"api-call-unknown-param":     {args: call("GET", "/apps", "-q", "color=red"), files: files},
		"api-call-bad-body":          {args: call("POST", "/apps/a-1/report", "--body", "{not json"), files: files},
		"api-call-unreachable":       {args: call("GET", "/apps"), files: files, http: &fakeAPI{err: errors.New("dial tcp: connection refused")}},
	}
}

func init() {
	for name, in := range apiGolden() {
		goldenCases[name] = in
	}
}

func TestAPICallDryRunUnescapesConcretePathOnce(t *testing.T) {
	encoded := invocation{
		args:  []string{"api", "call", "GET", "/apps/a%2Fb", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(),
	}.run(t)
	if encoded.exit != 0 || !strings.Contains(encoded.stdout, `"url": "https://api.example.test/v0/apps/a%2Fb"`) || strings.Contains(encoded.stdout, "%252F") {
		t.Fatalf("encoded path exit %d stdout %s", encoded.exit, encoded.stdout)
	}
	rawParam := invocation{
		args:  []string{"api", "call", "GET", "/apps/{app_id}", "-p", "app_id=a/b", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(),
	}.run(t)
	if rawParam.exit != 0 || !strings.Contains(rawParam.stdout, `"url": "https://api.example.test/v0/apps/a%2Fb"`) {
		t.Fatalf("-p value exit %d stdout %s", rawParam.exit, rawParam.stdout)
	}
	literal := invocation{
		args:  []string{"api", "call", "GET", "/apps/{app_id}", "-p", "app_id=a%2Fb", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(),
	}.run(t)
	if literal.exit != 0 || !strings.Contains(literal.stdout, `"url": "https://api.example.test/v0/apps/a%252Fb"`) {
		t.Fatalf("-p keeps a literal escape exit %d stdout %s", literal.exit, literal.stdout)
	}
	bad := invocation{
		args:  []string{"api", "call", "GET", "/apps/credential%zz", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(),
	}.run(t)
	if bad.exit != int(result.ExitUsage) || !strings.Contains(bad.stdout, string(result.DiagMalformedPathEscape)) || strings.Contains(bad.stdout, `"url"`) || strings.Contains(bad.stdout, "credential") {
		t.Fatalf("malformed escape exit %d stdout %s", bad.exit, bad.stdout)
	}
}

func TestAPICallDoesNotSendUntilContractIsVerified(t *testing.T) {
	const token = "test-token-value"
	f := &fakeAPI{routes: map[string]fakeResponse{"GET /v0/me": {200, `{"id":"u"}`}, "GET /v0/apps": {200, `[]`}}}
	live := invocation{
		args:  []string{"api", "call", "GET", "/me", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--json"},
		files: descriptionsFixture(), http: f, env: map[string]string{TokenEnv: token},
	}.run(t)
	if len(f.seen) != 0 {
		t.Fatalf("a live call sent %d requests before contract verification", len(f.seen))
	}
	if live.exit != int(result.ExitEnvironment) || !strings.Contains(live.stdout, string(result.DiagContractArtifactUnverified)) || strings.Contains(live.stdout, token) {
		t.Fatalf("exit %d stdout %s", live.exit, live.stdout)
	}
	dry := invocation{
		args:  []string{"api", "call", "GET", "/me", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(), http: f, env: map[string]string{TokenEnv: token},
	}.run(t)
	if dry.exit != 0 || len(f.seen) != 0 || !strings.Contains(dry.stdout, `"auth": "bearer"`) || strings.Contains(dry.stdout, token) {
		t.Fatalf("dry-run exit %d seen %d stdout %s", dry.exit, len(f.seen), dry.stdout)
	}
	public := invocation{
		args:  []string{"api", "call", "GET", "/apps", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--dry-run", "--json"},
		files: descriptionsFixture(), http: f, env: map[string]string{TokenEnv: token},
	}.run(t)
	if public.exit != 0 || len(f.seen) != 0 || !strings.Contains(public.stdout, `"auth": "none"`) {
		t.Fatalf("public dry-run exit %d seen %d stdout %s", public.exit, len(f.seen), public.stdout)
	}
}

func TestAPICallReadsBodyFromStdinAndFile(t *testing.T) {
	for _, arg := range []string{"@-", "@body.json"} {
		f := &fakeAPI{routes: map[string]fakeResponse{"POST /v0/apps/a-1/report": {200, `{}`}}}
		in := invocation{
			args:  []string{"api", "call", "POST", "/apps/a-1/report", "--body", arg, "--openapi", "openapi/gaming.json", "--base-url", testBase, "--json"},
			files: merge(descriptionsFixture(), fixture{"body.json": `{"reason":"spam"}`}),
			http:  f, stdin: `{"reason":"spam"}`,
		}
		r := in.run(t)
		if r.exit != int(result.ExitEnvironment) || len(f.seen) != 0 || !strings.Contains(r.stdout, string(result.DiagContractArtifactUnverified)) || strings.Contains(r.stdout, "spam") {
			t.Fatalf("%s: exit %d seen %d\n%s", arg, r.exit, len(f.seen), r.stdout)
		}
	}
}

func TestAPICallShowSecrets(t *testing.T) {
	const mockToken = "x-1"
	f := &fakeAPI{routes: map[string]fakeResponse{"POST /v0/auth/handoff/exchange": {200, fmt.Sprintf(`{"access_token":%q}`, mockToken)}}}
	r := invocation{
		args:  []string{"api", "call", "POST", "/auth/handoff/exchange", "-f", "code=c", "-f", "code_verifier=v", "--show-secrets", "--openapi", "openapi/gaming.json", "--base-url", testBase, "--json"},
		files: descriptionsFixture(), http: f,
	}.run(t)
	if r.exit != int(result.ExitEnvironment) || len(f.seen) != 0 || strings.Contains(r.stdout, mockToken) || !strings.Contains(r.stdout, string(result.DiagContractArtifactUnverified)) {
		t.Fatalf("--show-secrets sent or printed a token: exit %d seen %d\n%s", r.exit, len(f.seen), r.stdout)
	}
}

func TestAPICallRefusesUnverifiedExecutionInTextAndJSON(t *testing.T) {
	const live = "live-oversized-value"
	for _, extra := range [][]string{{"--json"}, {}} {
		f := &fakeAPI{routes: map[string]fakeResponse{"POST /v0/auth/handoff/exchange": {200, `{"access_token":"` + live + `"}`}}}
		args := append([]string{"api", "call", "POST", "/auth/handoff/exchange", "-f", "code=c", "-f", "code_verifier=v", "--openapi", "openapi/gaming.json", "--base-url", testBase}, extra...)
		r := invocation{args: args, files: descriptionsFixture(), http: f}.run(t)
		text := r.stdout + r.stderr
		if r.exit != int(result.ExitEnvironment) || len(f.seen) != 0 || strings.Contains(text, live) || !strings.Contains(text, string(result.DiagContractArtifactUnverified)) {
			t.Fatalf("%v: exit %d seen %d\nstdout %s\nstderr %s", extra, r.exit, len(f.seen), r.stdout, r.stderr)
		}
	}
}

func TestRedactPrefix(t *testing.T) {
	pad := strings.Repeat("x", 64)
	cases := map[string]struct {
		in, want string
		n        int
	}{
		"secret before the cut": {fmt.Sprintf(`{"token":"s1","a":[{"Password":%q,"keep":"k"}],"pad":"`, "s2") + pad, fmt.Sprintf(`{"token":%q,"a":[{"Password":%q,"keep":"k"}],"pad"`, "[redacted]", "[redacted]"), 2},
		"secret cut mid-value":  {`{"keep":"k","client_secret":"` + "s3-partial", `{"keep":"k","client_secret"`, 0},
		"nested under a secret": {`{"token":{"nested":"keep"},"secret":"`, `{"token":{"nested":"keep"},"secret"`, 0},
		"empty secret kept":     {`{"token":"","id":"i",`, `{"token":"","id":"i"`, 0},
		"not JSON":              {`plain text token=abc`, `plain text token=abc`, 0},
	}
	for name, c := range cases {
		out, n := redactPrefix([]byte(c.in))
		if string(out) != c.want || n != c.n {
			t.Errorf("%s: got %q (n=%d), want %q (n=%d)", name, out, n, c.want, c.n)
		}
		for _, s := range []string{"s1", "s2", "s3"} {
			if strings.Contains(string(out), `"`+s) {
				t.Errorf("%s: secret %s printed in %q", name, s, out)
			}
		}
	}
}

func TestAPIHumanOutput(t *testing.T) {
	r := invocation{args: []string{"api", "list", "--openapi", "openapi/gaming.json"}, files: descriptionsFixture()}.run(t)
	if r.exit != 0 || !strings.Contains(r.stdout, "* GET    /me") || !strings.Contains(r.stdout, gamingSurface) {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
}

func TestRedact(t *testing.T) {
	out, n := redact([]byte(fmt.Sprintf(`{"a":{"refreshToken":%q,"token":""},"list":[{"password":%q}],"token":{"nested":"keep"}}`, "r", "p")))
	if n != 2 || bytes.Contains(out, []byte(`"r"`)) || bytes.Contains(out, []byte(`"p"`)) || !bytes.Contains(out, []byte(`"keep"`)) {
		t.Fatalf("n=%d %s", n, out)
	}
	var v any
	if json.Unmarshal(out, &v) != nil {
		t.Fatal("redacted output is not JSON")
	}
}

func TestAPIExitCodes(t *testing.T) {
	cases := map[string]result.ExitCode{
		"api-call-unauthorized":      result.ExitEnvironment,
		"api-call-unreachable":       result.ExitEnvironment,
		"api-call-unknown-operation": result.ExitUsage,
		"api-list-no-source":         result.ExitUsage,
		"api-list-url-unavailable":   result.ExitEnvironment,
		"api-list-invalid-openapi":   result.ExitCheckFailed,
	}
	all := apiGolden()
	for name, want := range cases {
		if got := all[name].run(t).exit; got != int(want) {
			t.Errorf("%s: exit %d, want %d", name, got, want)
		}
	}
}

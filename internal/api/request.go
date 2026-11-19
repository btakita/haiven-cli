package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Pair is one ordered name=value argument.
type Pair struct {
	Name, Value string
}

// Call is one request to build from an operation.
type Call struct {
	Op *Op
	// RequestPath is what the user typed: the template or a concrete path.
	RequestPath string
	BaseURL     string
	Path        []Pair // -p
	Query       []Pair // -q, repeatable names allowed
	Form        []Pair // -f
	// Body is a JSON document, or nil.
	Body []byte
	// Token is a bearer token, or "". It is sent only to operations that
	// declare a security requirement and is never echoed.
	Token     string
	UserAgent string
}

// ProblemKind classifies why a request could not be built.
type ProblemKind string

// The closed problem vocabulary.
const (
	ProblemMissing ProblemKind = "missing"
	ProblemUnknown ProblemKind = "unknown"
	ProblemBody    ProblemKind = "body"
	// ProblemEscape is a concrete path segment that is not valid percent-encoding.
	ProblemEscape ProblemKind = "escape"
)

// Problem is one reason a request could not be built.
type Problem struct {
	Kind    ProblemKind
	Message string
}

// AuthMode says how a request was authenticated.
type AuthMode string

// The closed auth-mode vocabulary.
const (
	// AuthNone: the operation declares no security, or allows anonymous.
	AuthNone AuthMode = "none"
	// AuthBearer: a token was attached.
	AuthBearer AuthMode = "bearer"
	// AuthMissing: the operation requires a token and none was given; the
	// request is still sent so the API's own answer is reported.
	AuthMissing AuthMode = "missing"
)

// Built is a request ready to send.
type Built struct {
	Request     *http.Request
	URL         string
	Auth        AuthMode
	ContentType string
}

// Build validates arguments against the operation and builds the request.
func (c Call) Build() (*Built, []Problem) {
	op := c.Op
	var problems []Problem
	declared := map[string]map[string]Parameter{}
	for _, p := range op.Parameters {
		if declared[p.In] == nil {
			declared[p.In] = map[string]Parameter{}
		}
		declared[p.In][p.Name] = p
	}

	pathValues, err := op.PathValues(c.RequestPath)
	if err != nil {
		return nil, []Problem{{ProblemEscape, err.Error()}}
	}
	for _, p := range c.Path {
		if _, ok := declared["path"][p.Name]; !ok {
			problems = append(problems, Problem{ProblemUnknown, fmt.Sprintf("%s %s has no path parameter %q", op.Method, op.Path, p.Name)})
			continue
		}
		pathValues[p.Name] = p.Value
	}
	for _, p := range c.Query {
		if _, ok := declared["query"][p.Name]; !ok {
			problems = append(problems, Problem{ProblemUnknown, fmt.Sprintf("%s %s has no query parameter %q", op.Method, op.Path, p.Name)})
		}
	}
	given := map[string]bool{}
	for _, p := range c.Query {
		given[p.Name] = true
	}
	for _, p := range op.Parameters {
		switch {
		case p.In == "path" && pathValues[p.Name] == "":
			problems = append(problems, Problem{ProblemMissing, fmt.Sprintf("path parameter %q is required (-p %s=...)", p.Name, p.Name)})
		case p.In == "query" && p.Required && !given[p.Name]:
			problems = append(problems, Problem{ProblemMissing, fmt.Sprintf("query parameter %q is required (-q %s=...)", p.Name, p.Name)})
		}
	}

	var body []byte
	contentType := ""
	switch {
	case len(c.Form) > 0 && c.Body != nil:
		problems = append(problems, Problem{ProblemBody, "pass --body or -f fields, not both"})
	case len(c.Form) > 0:
		mt, ok := op.mediaType("application/x-www-form-urlencoded")
		if !ok {
			problems = append(problems, Problem{ProblemBody, fmt.Sprintf("%s %s takes no form body", op.Method, op.Path)})
			break
		}
		form := url.Values{}
		for _, f := range c.Form {
			if _, known := mt.Schema.Properties[f.Name]; !known && len(mt.Schema.Properties) > 0 {
				problems = append(problems, Problem{ProblemUnknown, fmt.Sprintf("%s %s has no form field %q", op.Method, op.Path, f.Name)})
			}
			form.Add(f.Name, f.Value)
		}
		for _, name := range mt.Schema.Required {
			if _, ok := form[name]; !ok {
				problems = append(problems, Problem{ProblemMissing, fmt.Sprintf("form field %q is required (-f %s=...)", name, name)})
			}
		}
		body = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case c.Body != nil:
		if _, ok := op.mediaType("application/json"); !ok {
			problems = append(problems, Problem{ProblemBody, fmt.Sprintf("%s %s takes no JSON body", op.Method, op.Path)})
			break
		}
		if !json.Valid(c.Body) {
			problems = append(problems, Problem{ProblemBody, "--body is not valid JSON"})
			break
		}
		body = c.Body
		contentType = "application/json"
	case op.RequestBody != nil && op.RequestBody.Required:
		problems = append(problems, Problem{ProblemMissing, fmt.Sprintf("%s %s requires a request body (--body or -f)", op.Method, op.Path)})
	}
	if len(problems) > 0 {
		return nil, problems
	}

	// Expand the template in declaration order of its segments.
	segs := splitPath(op.Path)
	for i, s := range segs {
		if isParam(s) {
			segs[i] = url.PathEscape(pathValues[strings.Trim(s, "{}")])
		}
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + strings.Join(segs, "/")
	if len(c.Query) > 0 {
		parts := make([]string, len(c.Query))
		for i, q := range c.Query {
			parts[i] = url.QueryEscape(q.Name) + "=" + url.QueryEscape(q.Value)
		}
		u += "?" + strings.Join(parts, "&")
	}

	req, err := http.NewRequest(op.Method, u, bytes.NewReader(body))
	if err != nil {
		return nil, []Problem{{ProblemUnknown, fmt.Sprintf("base URL %q does not form a valid request URL: %v", c.BaseURL, err)}}
	}
	if body == nil {
		req.Body = http.NoBody
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	b := &Built{Request: req, URL: u, Auth: AuthNone, ContentType: contentType}
	if names := op.SecurityNames(); len(names) > 0 {
		switch {
		case c.Token != "":
			op.applyAuth(req, names, c.Token)
			b.Auth = AuthBearer
		case op.RequiresAuth():
			b.Auth = AuthMissing
		}
	}
	return b, nil
}

func (o *Op) mediaType(want string) (MediaType, bool) {
	if o.RequestBody == nil {
		return MediaType{}, false
	}
	if mt, ok := o.RequestBody.Content[want]; ok {
		return mt, true
	}
	mt, ok := o.RequestBody.Content["*/*"]
	return mt, ok
}

// applyAuth attaches the token per the first named scheme the document
// defines. The Haiven descriptions declare an apiKey "Authorization" header
// that carries a bearer token.
func (o *Op) applyAuth(req *http.Request, names []string, token string) {
	schemes := o.Surface.Spec.Components.SecuritySchemes
	keys := append([]string(nil), names...)
	sort.Strings(keys)
	for _, n := range keys {
		s, ok := schemes[n]
		if !ok {
			continue
		}
		switch {
		case s.Type == "http" && strings.EqualFold(s.Scheme, "bearer"):
			req.Header.Set("Authorization", "Bearer "+token)
			return
		case s.Type == "apiKey" && s.In == "header" && s.Name != "":
			v := token
			if strings.EqualFold(s.Name, "Authorization") && !strings.HasPrefix(strings.ToLower(token), "bearer ") {
				v = "Bearer " + token
			}
			req.Header.Set(s.Name, v)
			return
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

// Package api reads public OpenAPI 3 descriptions of the Haiven HTTP API and
// builds requests from them. It owns no domain logic: every operation,
// parameter and security requirement comes from the description's bytes.
package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Methods lists the HTTP methods an operation may use, in listing order.
var Methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Parameter is one OpenAPI parameter (path, query, header or cookie).
type Parameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}

// parameterEntry is either an inline parameter or an OpenAPI Reference Object.
type parameterEntry struct {
	Parameter
	Ref json.RawMessage `json:"$ref"`
}

// Operation is one method on one path.
type Operation struct {
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Parameters  []Parameter            `json:"parameters"`
	RequestBody *RequestBody           `json:"requestBody"`
	Security    *[]map[string][]string `json:"security"`
}

// RequestBody lists the media types an operation accepts and, for form
// bodies, the field names.
type RequestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]MediaType `json:"content"`
}

// MediaType is one requestBody content entry. Only the top-level properties
// and required list are read, to validate form fields.
type MediaType struct {
	Schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"schema"`
}

// SecurityScheme is one components.securitySchemes entry.
type SecurityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme"`
	In     string `json:"in"`
	Name   string `json:"name"`
}

// SurfaceExtension is the top-level OpenAPI extension that names a
// description's surface (for example "gaming/v0").
const SurfaceExtension = "x-haiven-surface"

// Spec is the part of an OpenAPI 3 document the CLI reads.
type Spec struct {
	OpenAPI string `json:"openapi"`
	// Surface is the x-haiven-surface extension, when the description sets it.
	Surface string `json:"x-haiven-surface"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Servers []struct {
		URL string `json:"url"`
	} `json:"servers"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Security   []map[string][]string                 `json:"security"`
	Components struct {
		SecuritySchemes map[string]SecurityScheme  `json:"securitySchemes"`
		Parameters      map[string]json.RawMessage `json:"parameters"`
	} `json:"components"`
}

// Surface is one HTTP description.
type Surface struct {
	// ID names the surface: the description's x-haiven-surface, or else
	// its info.title in lower kebab case.
	ID string
	// Source is where the description was read from: a file path or a URL.
	Source string
	Spec   *Spec
	// Ops is every operation, sorted by path then method.
	Ops []*Op
}

// Op is one operation of one surface.
type Op struct {
	Surface *Surface
	Method  string // upper case
	Path    string // the template, e.g. /apps/{app_id}
	Operation
	segments []string
}

// Server is the surface's first server URL, or "".
func (s *Surface) Server() string {
	if len(s.Spec.Servers) == 0 {
		return ""
	}
	return s.Spec.Servers[0].URL
}

// InvalidError is a description that could not be read as OpenAPI.
type InvalidError struct {
	Source string
	Err    error
}

func (e *InvalidError) Error() string { return e.Source + ": " + e.Err.Error() }

func (e *InvalidError) Unwrap() error { return e.Err }

var nonSlug = regexp.MustCompile(`[^a-z0-9/._-]+`)

// SurfaceID is the description's surface name: x-haiven-surface when set,
// otherwise info.title in lower kebab case.
func SurfaceID(spec *Spec) string {
	if id := strings.TrimSpace(spec.Surface); id != "" {
		return id
	}
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(spec.Info.Title), "-"), "-")
}

// Parse decodes one description read from source.
func Parse(source string, raw []byte) (*Surface, error) {
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, &InvalidError{Source: source, Err: err}
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		return nil, &InvalidError{Source: source, Err: fmt.Errorf("openapi version %q is not 3.x", spec.OpenAPI)}
	}
	id := SurfaceID(&spec)
	if id == "" {
		return nil, &InvalidError{Source: source, Err: fmt.Errorf("no %s and no info.title to name the surface", SurfaceExtension)}
	}
	s := &Surface{ID: id, Source: source, Spec: &spec}
	for path, item := range spec.Paths {
		var entries []parameterEntry
		if rawParams, ok := item["parameters"]; ok {
			if err := json.Unmarshal(rawParams, &entries); err != nil {
				return nil, &InvalidError{Source: source, Err: fmt.Errorf("%s parameters: %w", path, err)}
			}
		}
		shared, err := resolveParameters(entries, spec.Components.Parameters)
		if err != nil {
			return nil, &InvalidError{Source: source, Err: fmt.Errorf("%s parameters: %w", path, err)}
		}
		for _, m := range Methods {
			rawOp, ok := item[m]
			if !ok {
				continue
			}
			op := &Op{Surface: s, Method: strings.ToUpper(m), Path: path, segments: splitPath(path)}
			var operation struct {
				Operation
				Parameters []parameterEntry `json:"parameters"`
			}
			if err := json.Unmarshal(rawOp, &operation); err != nil {
				return nil, &InvalidError{Source: source, Err: fmt.Errorf("%s %s: %w", op.Method, path, err)}
			}
			own, err := resolveParameters(operation.Parameters, spec.Components.Parameters)
			if err != nil {
				return nil, &InvalidError{Source: source, Err: fmt.Errorf("%s %s parameters: %w", op.Method, path, err)}
			}
			op.Operation = operation.Operation
			op.Parameters = mergeParams(shared, own)
			s.Ops = append(s.Ops, op)
		}
	}
	sort.Slice(s.Ops, func(i, j int) bool {
		if s.Ops[i].Path != s.Ops[j].Path {
			return s.Ops[i].Path < s.Ops[j].Path
		}
		return methodRank(s.Ops[i].Method) < methodRank(s.Ops[j].Method)
	})
	return s, nil
}

// resolveParameters resolves references inside the already loaded description.
func resolveParameters(entries []parameterEntry, components map[string]json.RawMessage) ([]Parameter, error) {
	parameters := make([]Parameter, 0, len(entries))
	for _, entry := range entries {
		seen := map[string]bool{}
		for entry.Ref != nil {
			name, err := parameterReferenceName(entry.Ref)
			if err != nil {
				return nil, err
			}
			if seen[name] {
				return nil, fmt.Errorf("cyclic parameter reference to %q", name)
			}
			seen[name] = true
			raw, ok := components[name]
			if !ok {
				return nil, fmt.Errorf("parameter reference %q was not found", name)
			}
			entry = parameterEntry{}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return nil, fmt.Errorf("parameter component %q: %w", name, err)
			}
		}
		if len(seen) > 0 && (entry.Name == "" || entry.In == "") {
			return nil, fmt.Errorf("referenced parameter must have a name and location")
		}
		parameters = append(parameters, entry.Parameter)
	}
	return parameters, nil
}

var componentKey = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func parameterReferenceName(raw json.RawMessage) (string, error) {
	var ref string
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", fmt.Errorf("parameter reference must be a string")
	}
	fragment, local := strings.CutPrefix(ref, "#")
	pointer, err := url.PathUnescape(fragment)
	name, component := strings.CutPrefix(pointer, "/components/parameters/")
	if !local || err != nil || !component || !componentKey.MatchString(name) {
		return "", fmt.Errorf("parameter reference must point to a local #/components/parameters/<name>")
	}
	return name, nil
}

func methodRank(m string) int {
	for i, x := range Methods {
		if strings.EqualFold(x, m) {
			return i
		}
	}
	return len(Methods)
}

// mergeParams applies operation parameters over path-level ones (same name
// and location).
func mergeParams(shared, own []Parameter) []Parameter {
	out := []Parameter{}
	seen := map[string]bool{}
	for _, p := range own {
		seen[p.In+"\x00"+p.Name] = true
		out = append(out, p)
	}
	for _, p := range shared {
		if !seen[p.In+"\x00"+p.Name] {
			out = append(out, p)
		}
	}
	for i := range out {
		// Path parameters are always required (OpenAPI 3).
		if out[i].In == "path" {
			out[i].Required = true
		}
	}
	return out
}

func splitPath(p string) []string {
	return strings.Split(strings.Trim(p, "/"), "/")
}

// SecurityNames lists the security schemes the operation accepts, from its
// own security or the document's. An empty requirement object means the
// operation may also be called anonymously; that shows as "" in the list.
func (o *Op) SecurityNames() []string {
	reqs := o.Surface.Spec.Security
	if o.Security != nil {
		reqs = *o.Security
	}
	var names []string
	for _, req := range reqs {
		if len(req) == 0 {
			names = append(names, "")
		}
		for name := range req {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// RequiresAuth reports whether the operation declares a security
// requirement without an anonymous alternative.
func (o *Op) RequiresAuth() bool {
	names := o.SecurityNames()
	if len(names) == 0 {
		return false
	}
	for _, n := range names {
		if n == "" {
			return false
		}
	}
	return true
}

// match reports whether a concrete or template path matches the operation,
// and how specific the match is (literal segments matched).
func (o *Op) match(path string) (bool, int) {
	if path == o.Path {
		return true, len(o.segments) + 1
	}
	segs := splitPath(path)
	if len(segs) != len(o.segments) {
		return false, 0
	}
	literal := 0
	for i, want := range o.segments {
		if isParam(want) {
			if segs[i] == "" {
				return false, 0
			}
			continue
		}
		if segs[i] != want {
			return false, 0
		}
		literal++
	}
	return true, literal
}

// PathValues extracts path parameter values from a concrete path that
// matched the template. Concrete segments are percent-encoded; the returned
// values are decoded once so template expansion can escape them once.
// A path equal to the template yields no values. A malformed escape is an
// error that names the parameter but not the segment, because a mistyped
// segment can be a pasted credential and the error reaches CLI output.
func (o *Op) PathValues(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == o.Path {
		return out, nil
	}
	segs := splitPath(path)
	for i, want := range o.segments {
		if isParam(want) && i < len(segs) && !isParam(segs[i]) {
			name := strings.Trim(want, "{}")
			value, err := url.PathUnescape(segs[i])
			if err != nil {
				return nil, fmt.Errorf("path parameter %q value is not valid percent-encoding", name)
			}
			out[name] = value
		}
	}
	return out, nil
}

func isParam(seg string) bool { return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") }

// Find returns the best-matching operations for a method and path across
// surfaces: for each surface the most specific match, and only the surfaces
// tied for the best match overall.
func Find(surfaces []*Surface, method, path string) []*Op {
	method = strings.ToUpper(method)
	var best []*Op
	bestScore := -1
	for _, s := range surfaces {
		var top *Op
		topScore := -1
		for _, o := range s.Ops {
			if o.Method != method {
				continue
			}
			if ok, score := o.match(path); ok && score > topScore {
				top, topScore = o, score
			}
		}
		switch {
		case top == nil:
		case topScore > bestScore:
			best, bestScore = []*Op{top}, topScore
		case topScore == bestScore:
			best = append(best, top)
		}
	}
	return best
}

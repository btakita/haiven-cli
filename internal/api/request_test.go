package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReferencedParametersBuildRequest(t *testing.T) {
	surface, err := Parse("test", []byte(`{
		"openapi": "3.0.3",
		"info": {"title": "Example"},
		"components": {"parameters": {
			"App.Id": {"name": "app_id", "in": "path", "required": true, "schema": {"type": "string"}},
			"AppLookup": {"$ref": "#/components/parameters/App%2EId"},
			"SharedFilter": {"name": "filter", "in": "query", "required": true, "schema": {"type": "string"}},
			"OptionalFilter": {"name": "filter", "in": "query", "schema": {"type": "string"}}
		}},
		"paths": {"/apps/{app_id}": {
			"parameters": [
				{"$ref": "#/components/parameters/AppLookup"},
				{"$ref": "#/components/parameters/SharedFilter"}
			],
			"get": {"parameters": [{"$ref": "#/components/parameters/OptionalFilter"}]}
		}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	call := Call{
		Op:          surface.Ops[0],
		RequestPath: "/apps/{app_id}",
		BaseURL:     "https://api.example.test/v0",
		Path:        []Pair{{Name: "app_id", Value: "a-1"}},
	}
	built, problems := call.Build()
	if len(problems) != 0 || builtURL(built) != "https://api.example.test/v0/apps/a-1" {
		t.Fatalf("url %q problems %v", builtURL(built), problems)
	}
	call.Path = nil
	built, problems = call.Build()
	if built != nil || len(problems) != 1 || problems[0].Kind != ProblemMissing {
		t.Fatalf("missing referenced path parameter: url %q problems %v", builtURL(built), problems)
	}
}

func TestInvalidParameterReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		want string
	}{
		{"missing", `"#/components/parameters/Missing"`, `parameter reference "Missing" was not found`},
		{"cyclic", `"#/components/parameters/Cyclic"`, `cyclic parameter reference to "Cyclic"`},
		{"malformed escape", `"#/components/parameters/Bad%zz"`, "must point to a local"},
		{"wrong component", `"#/components/schemas/App"`, "must point to a local"},
		{"external", `"https://example.test/secret#/components/parameters/App"`, "must point to a local"},
		{"null", `null`, "must point to a local"},
		{"non-string", `42`, "must be a string"},
		{"invalid target", `"#/components/parameters/Empty"`, "must have a name and location"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{
				"openapi": "3.1.0",
				"info": {"title": "Example"},
				"paths": {"/apps": {"get": {"parameters": [{"$ref": %s}]}}},
				"components": {"parameters": {
					"Cyclic": {"$ref": "#/components/parameters/Alias"},
					"Alias": {"$ref": "#/components/parameters/Cyclic"},
					"Empty": {}
				}}
			}`, tc.ref)
			_, err := Parse("test", []byte(raw))
			var invalid *InvalidError
			if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "GET /apps parameters: ") ||
				!strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestConcretePathIsUnescapedOnce(t *testing.T) {
	op := appOp(t)
	cases := []struct {
		path string
		pair *Pair
		url  string
	}{
		{path: "/apps/a%2Fb", url: "https://api.example.test/v0/apps/a%2Fb"},
		{path: "/apps/a-1", url: "https://api.example.test/v0/apps/a-1"},
		{path: "/apps/a%252Fb", url: "https://api.example.test/v0/apps/a%252Fb"},
		{path: "/apps/{app_id}", pair: &Pair{Name: "app_id", Value: "a/b"}, url: "https://api.example.test/v0/apps/a%2Fb"},
		{path: "/apps/{app_id}", pair: &Pair{Name: "app_id", Value: "a%2Fb"}, url: "https://api.example.test/v0/apps/a%252Fb"},
	}
	for _, tc := range cases {
		call := Call{Op: op, RequestPath: tc.path, BaseURL: "https://api.example.test/v0"}
		if tc.pair != nil {
			call.Path = []Pair{*tc.pair}
		}
		built, problems := call.Build()
		if len(problems) != 0 || built == nil || built.URL != tc.url {
			t.Fatalf("%s %+v: url %v problems %v", tc.path, tc.pair, builtURL(built), problems)
		}
	}
}

func TestMalformedPathEscapeIsRejected(t *testing.T) {
	op := appOp(t)
	built, problems := Call{Op: op, RequestPath: "/apps/secret%zz", BaseURL: "https://api.example.test/v0"}.Build()
	if built != nil || len(problems) != 1 || problems[0].Kind != ProblemEscape {
		t.Fatalf("built %v problems %v", builtURL(built), problems)
	}
	if msg := problems[0].Message; !strings.Contains(msg, `"app_id"`) || strings.Contains(msg, "secret") || strings.Contains(msg, "%zz") {
		t.Fatalf("message names the parameter but must not echo the segment: %s", msg)
	}
}

func appOp(t *testing.T) *Op {
	t.Helper()
	surface, err := Parse("test", []byte(`{
		"openapi": "3.0.3",
		"info": {"title": "Example", "version": "0"},
		"paths": {"/apps/{app_id}": {"get": {"parameters": [{"name": "app_id", "in": "path", "required": true, "schema": {"type": "string"}}]}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Ops) != 1 {
		t.Fatalf("ops = %d", len(surface.Ops))
	}
	return surface.Ops[0]
}

func builtURL(built *Built) string {
	if built == nil {
		return ""
	}
	return built.URL
}

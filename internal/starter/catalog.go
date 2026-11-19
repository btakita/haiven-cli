// Package starter implements the engine-neutral SDK starter catalog and the
// narrow protocol boundary used by optional SDK-owned recipe runners.
package starter

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Published schema and protocol identifiers.
const (
	CatalogSchema   = "starter-catalog/v1"
	RecipeProtocol  = "starter-recipe/v1"
	RunnerSchema    = "starter-runner/v1"
	LifecycleSchema = "starter-runner/v2"
	LockSchema      = "starter-lock/v1"
	EvidenceSchema  = "starter-evidence/v1"
)

// Stable catalog error categories.
var (
	ErrCatalogIntegrity  = errors.New("starter catalog integrity check failed")
	ErrInvalidCatalog    = errors.New("invalid starter catalog")
	ErrUnknownStarter    = errors.New("unknown starter")
	ErrRunnerUnavailable = errors.New("starter recipe runner unavailable")
	stepIDPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

//go:embed catalog.json guides recipes
var bundled embed.FS

// EmbeddedDigest is the catalog digest trusted by this CLI build.
const EmbeddedDigest = "sha256:284cf80add8c5e3254f32ee36e7af0634bd510f77a47d396ec3dc69d22e276fb"

// CatalogArtifact contains catalog bytes and their bundled dependencies.
type CatalogArtifact struct {
	Bytes          []byte
	ExpectedDigest string
	Guides         map[string][]byte
	Recipes        map[string][]byte
}

// CatalogSource loads a catalog trust root and its bundled artifacts.
type CatalogSource interface {
	Load(context.Context) (CatalogArtifact, error)
}

// EmbeddedSource loads the catalog compiled into the CLI.
type EmbeddedSource struct{}

// Load returns the embedded catalog and private copies of its artifacts.
func (EmbeddedSource) Load(context.Context) (CatalogArtifact, error) {
	artifact := CatalogArtifact{ExpectedDigest: EmbeddedDigest, Guides: map[string][]byte{}, Recipes: map[string][]byte{}}
	var err error
	artifact.Bytes, err = bundled.ReadFile("catalog.json")
	if err != nil {
		return CatalogArtifact{}, err
	}
	if err := fs.WalkDir(bundled, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || name == "catalog.json" {
			return walkErr
		}
		body, readErr := bundled.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		switch path.Ext(name) {
		case ".json":
			if path.Base(name) == "recipe.json" || path.Dir(name) == "recipes" {
				artifact.Recipes[name] = body
			}
		case ".md":
			artifact.Guides[name] = body
			if strings.HasPrefix(name, "guides/") {
				artifact.Guides[name] = body
			}
		}
		return nil
	}); err != nil {
		return CatalogArtifact{}, err
	}
	return artifact, nil
}

// ProtocolRange declares compatible starter runner protocol versions.
type ProtocolRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

// Catalog is one verified starter-catalog/v1 document.
type Catalog struct {
	APIVersion string        `json:"api_version"`
	ID         string        `json:"id"`
	Version    string        `json:"version"`
	Protocol   ProtocolRange `json:"protocol"`
	Starters   []Starter     `json:"starters"`
	Digest     string        `json:"-"`
	recipes    map[string]Recipe
	guides     map[string]Guide
}

// RecipeRef content-addresses one recipe manifest.
type RecipeRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Edge orders two recipes in a starter graph.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Input connects one recipe output to another recipe parameter.
type Input struct {
	FromRecipe string `json:"from_recipe"`
	OutputPath string `json:"output_path"`
	ToRecipe   string `json:"to_recipe"`
	Parameter  string `json:"parameter"`
}

// Starter describes one engine-neutral onboarding journey.
type Starter struct {
	ID             string      `json:"id"`
	DisplayName    string      `json:"display_name"`
	Description    string      `json:"description"`
	Engine         string      `json:"engine"`
	Journey        string      `json:"journey"`
	Maturity       string      `json:"maturity"`
	SupportedHosts []string    `json:"supported_hosts"`
	EngineVersions []string    `json:"engine_versions"`
	Features       []string    `json:"features"`
	Language       string      `json:"language"`
	Topology       string      `json:"topology"`
	Workflow       string      `json:"workflow"`
	Access         string      `json:"access"`
	Licenses       []string    `json:"licenses"`
	Recipes        []RecipeRef `json:"recipes"`
	Edges          []Edge      `json:"edges"`
	Inputs         []Input     `json:"inputs"`
}

// Parameter declares one recipe input.
type Parameter struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
	Default  any    `json:"default,omitempty"`
}

// Source declares one immutable recipe source.
type Source struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	URI      string `json:"uri"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

// Output declares one mutation covered by the normative guide.
type Output struct {
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Action      string `json:"action"`
	HandoffFrom string `json:"handoff_from,omitempty"`
}

// Recipe is one verified starter-recipe/v1 manifest.
type Recipe struct {
	APIVersion         string            `json:"api_version"`
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	Description        string            `json:"description"`
	Runner             *RunnerSpec       `json:"runner,omitempty"`
	Operations         []RunnerOperation `json:"operations"`
	Parameters         []Parameter       `json:"parameters"`
	Requires           []string          `json:"requires"`
	Sources            []Source          `json:"sources"`
	Prerequisites      []string          `json:"prerequisites"`
	NetworkHosts       []string          `json:"network_hosts"`
	Outputs            []Output          `json:"outputs"`
	Licenses           []string          `json:"licenses"`
	AccessRequirements []string          `json:"access_requirements"`
	Manual             Manual            `json:"manual"`
	Digest             string            `json:"digest"`
}

// RunnerSpec identifies an optional content-addressed automation runner.
type RunnerSpec struct {
	ID              string `json:"id"`
	Digest          string `json:"digest"`
	ProtocolVersion int    `json:"protocol_version"`
}

// Manual binds a recipe to an immutable normative guide.
type Manual struct {
	GuidePath      string                       `json:"guide_path"`
	GuideDigest    string                       `json:"guide_digest"`
	CanonicalURL   string                       `json:"canonical_url,omitempty"`
	OperationSteps map[RunnerOperation][]string `json:"operation_steps"`
}

// ManualStep is one stable native operation embedded in a guide.
type ManualStep struct {
	ID                string          `json:"id"`
	Operation         RunnerOperation `json:"operation"`
	NativeTool        string          `json:"native_tool"`
	Action            string          `json:"action"`
	Preconditions     []string        `json:"preconditions"`
	ExpectedOutcome   string          `json:"expected_outcome"`
	Evidence          []string        `json:"evidence"`
	RequiresHaivenCLI bool            `json:"requires_haiven_cli"`
	Mutations         []string        `json:"mutations"`
}

// Guide contains verified Markdown and its parsed manual steps.
type Guide struct {
	Path    string       `json:"path"`
	Digest  string       `json:"digest"`
	Content string       `json:"content"`
	Steps   []ManualStep `json:"steps"`
}

// Filter selects starters by portable catalog metadata.
type Filter struct {
	Engine   string `json:"engine,omitempty"`
	Journey  string `json:"journey,omitempty"`
	Platform string `json:"platform,omitempty"`
	Feature  string `json:"feature,omitempty"`
}

// Load verifies and decodes a catalog and its referenced artifacts.
func Load(ctx context.Context, source CatalogSource) (*Catalog, error) {
	if source == nil {
		source = EmbeddedSource{}
	}
	artifact, err := source.Load(ctx)
	if err != nil {
		return nil, err
	}
	digest := digestBytes(artifact.Bytes)
	if artifact.ExpectedDigest == "" || digest != strings.ToLower(artifact.ExpectedDigest) {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrCatalogIntegrity, digest, artifact.ExpectedDigest)
	}
	var catalog Catalog
	if err := decodeClosed(artifact.Bytes, &catalog); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	catalog.Digest = digest
	if err := validateCatalog(&catalog, artifact); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func validateCatalog(c *Catalog, artifact CatalogArtifact) error {
	if c.APIVersion != CatalogSchema || c.ID == "" || c.Version == "" || c.Protocol.Minimum < 1 || c.Protocol.Maximum < c.Protocol.Minimum {
		return fmt.Errorf("%w: catalog identity or protocol range is invalid", ErrInvalidCatalog)
	}
	c.recipes, c.guides = map[string]Recipe{}, map[string]Guide{}
	for name, body := range artifact.Recipes {
		var recipe Recipe
		if err := decodeClosed(body, &recipe); err != nil {
			return fmt.Errorf("%w: recipe %s: %w", ErrInvalidCatalog, name, err)
		}
		recipe.Digest = digestBytes(body)
		if err := validateRecipe(recipe); err != nil {
			return fmt.Errorf("%w: recipe %s: %w", ErrInvalidCatalog, name, err)
		}
		if _, duplicate := c.recipes[recipe.ID]; duplicate {
			return fmt.Errorf("%w: duplicate recipe %q", ErrInvalidCatalog, recipe.ID)
		}
		guideBody := artifact.Guides[path.Join(path.Dir(name), recipe.Manual.GuidePath)]
		if len(guideBody) == 0 {
			guideBody = artifact.Guides[recipe.ID+"/"+recipe.Manual.GuidePath]
		}
		if len(guideBody) == 0 {
			// A flat key is convenient for embedded catalogs whose guide paths
			// are already globally unique. SDK bundle adapters use the
			// recipe-qualified key so several recipes may each own manual.md.
			guideBody = artifact.Guides[recipe.Manual.GuidePath]
		}
		guide, err := validateGuide(recipe, guideBody)
		if err != nil {
			return fmt.Errorf("%w: recipe %q: %w", ErrInvalidCatalog, recipe.ID, err)
		}
		c.recipes[recipe.ID], c.guides[recipe.ID] = recipe, guide
	}
	seen := map[string]bool{}
	for _, s := range c.Starters {
		if s.ID == "" || s.DisplayName == "" || s.Description == "" || s.Engine == "" || len(s.SupportedHosts) == 0 || len(s.EngineVersions) == 0 || len(s.Recipes) == 0 || seen[s.ID] {
			return fmt.Errorf("%w: invalid or duplicate starter %q", ErrInvalidCatalog, s.ID)
		}
		seen[s.ID] = true
		refs := map[string]bool{}
		for _, ref := range s.Recipes {
			recipe, ok := c.recipes[ref.ID]
			if !ok || refs[ref.ID] || ref.Version != recipe.Version || ref.Digest != recipe.Digest {
				return fmt.Errorf("%w: starter %q has missing, duplicate, or untrusted recipe %q", ErrInvalidCatalog, s.ID, ref.ID)
			}
			refs[ref.ID] = true
		}
	}
	return nil
}

func validateRecipe(recipe Recipe) error {
	if recipe.APIVersion != RecipeProtocol || recipe.ID == "" || recipe.Version == "" || recipe.Description == "" || !exactOperations(recipe.Operations) {
		return fmt.Errorf("identity or operations are invalid")
	}
	if recipe.Runner != nil && (recipe.Runner.ID == "" || !validDigest(recipe.Runner.Digest) || (recipe.Runner.ProtocolVersion != 1 && recipe.Runner.ProtocolVersion != 2)) {
		return fmt.Errorf("runner is invalid")
	}
	if len(recipe.Operations) == 5 && (recipe.Runner == nil || recipe.Runner.ProtocolVersion != 2) {
		return fmt.Errorf("lifecycle operations require a v2 runner")
	}
	for _, output := range recipe.Outputs {
		if !safeRelativePath(output.Path) || (output.Scope != "file" && output.Scope != "tree") || (output.Action != "create" && output.Action != "replace") {
			return fmt.Errorf("output %q is invalid", output.Path)
		}
	}
	return nil
}

func exactOperations(got []RunnerOperation) bool {
	if len(got) != 3 && len(got) != 5 {
		return false
	}
	want := map[RunnerOperation]bool{RunnerPlan: true, RunnerApply: true, RunnerVerify: true}
	if len(got) == 5 {
		want[RunnerBuild], want[RunnerRun] = true, true
	}
	for _, operation := range got {
		if !want[operation] {
			return false
		}
		delete(want, operation)
	}
	return len(want) == 0
}

func validateGuide(recipe Recipe, body []byte) (Guide, error) {
	manual := recipe.Manual
	if !safeRelativePath(manual.GuidePath) || path.Ext(manual.GuidePath) != ".md" || !validDigest(manual.GuideDigest) || len(body) == 0 {
		return Guide{}, fmt.Errorf("manual guide metadata is invalid")
	}
	if manual.CanonicalURL != "" && !strings.HasPrefix(manual.CanonicalURL, "https://") {
		return Guide{}, fmt.Errorf("manual canonical_url must be HTTPS")
	}
	if got := digestBytes(body); got != manual.GuideDigest {
		return Guide{}, fmt.Errorf("manual guide digest got %s, want %s", got, manual.GuideDigest)
	}
	steps, err := parseManualSteps(body)
	if err != nil {
		return Guide{}, err
	}
	byID := map[string]ManualStep{}
	for _, step := range steps {
		if _, exists := byID[step.ID]; exists {
			return Guide{}, fmt.Errorf("duplicate manual step %q", step.ID)
		}
		byID[step.ID] = step
	}
	seen, mutations := map[string]bool{}, map[string]bool{}
	for _, operation := range recipe.Operations {
		refs := manual.OperationSteps[operation]
		if len(refs) == 0 {
			return Guide{}, fmt.Errorf("manual operation %q has no steps", operation)
		}
		for _, id := range refs {
			step, ok := byID[id]
			if !ok || step.Operation != operation || seen[id] {
				return Guide{}, fmt.Errorf("manual step reference %q is missing, mismatched, or duplicate", id)
			}
			seen[id] = true
			if operation != RunnerApply && len(step.Mutations) != 0 {
				return Guide{}, fmt.Errorf("manual %s step %q declares mutations", operation, id)
			}
			for _, mutation := range step.Mutations {
				if !safeRelativePath(mutation) || mutations[mutation] {
					return Guide{}, fmt.Errorf("manual step %q has invalid or duplicate mutation %q", id, mutation)
				}
				mutations[mutation] = true
			}
		}
	}
	outputs := recipeOutputPaths(recipe)
	if len(seen) != len(byID) || !sameStrings(mutations, outputs) {
		return Guide{}, fmt.Errorf("manual steps are dangling or apply mutations differ from recipe outputs")
	}
	return Guide{Path: manual.GuidePath, Digest: manual.GuideDigest, Content: string(body), Steps: steps}, nil
}

func parseManualSteps(body []byte) ([]ManualStep, error) {
	lines, steps := strings.Split(string(body), "\n"), []ManualStep{}
	for i := 0; i < len(lines); i++ {
		if lines[i] != "```haiven-manual-step" {
			continue
		}
		start := i + 1
		for i = start; i < len(lines) && lines[i] != "```"; i++ {
		}
		if i == len(lines) {
			return nil, fmt.Errorf("unterminated haiven-manual-step block")
		}
		raw := strings.Join(lines[start:i], "\n")
		var step ManualStep
		if err := decodeClosed([]byte(raw), &step); err != nil {
			return nil, fmt.Errorf("invalid manual step: %w", err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &fields)
		if _, present := fields["requires_haiven_cli"]; !present || step.RequiresHaivenCLI {
			return nil, fmt.Errorf("manual step %q must declare requires_haiven_cli:false", step.ID)
		}
		if !stepIDPattern.MatchString(step.ID) || step.NativeTool == "" || step.Action == "" || len(step.Preconditions) == 0 || step.ExpectedOutcome == "" || len(step.Evidence) == 0 || !validRunnerOperation(step.Operation) {
			return nil, fmt.Errorf("manual step %q is incomplete", step.ID)
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("manual guide contains no steps")
	}
	return steps, nil
}

func decodeClosed(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func sameStrings(got map[string]bool, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, value := range want {
		if !got[value] || !safeRelativePath(value) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func recipeOutputPaths(recipe Recipe) []string {
	paths := make([]string, 0, len(recipe.Outputs))
	for _, output := range recipe.Outputs {
		paths = append(paths, output.Path)
	}
	return paths
}

// List returns starters matching every non-empty filter.
func (c *Catalog) List(filter Filter) []Starter {
	out := []Starter{}
	for _, s := range c.Starters {
		if filter.Engine != "" && s.Engine != filter.Engine || filter.Journey != "" && s.Journey != filter.Journey || filter.Platform != "" && !contains(s.SupportedHosts, filter.Platform) || filter.Feature != "" && !contains(s.Features, filter.Feature) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Resolve returns a starter and its verified recipe graph.
func (c *Catalog) Resolve(id string) (Starter, []Recipe, error) {
	for _, s := range c.Starters {
		if s.ID != id {
			continue
		}
		resolved := make([]Recipe, 0, len(s.Recipes))
		for _, ref := range s.Recipes {
			resolved = append(resolved, c.recipes[ref.ID])
		}
		return s, resolved, nil
	}
	return Starter{}, nil, fmt.Errorf("%w: %s", ErrUnknownStarter, id)
}

// GuideFor returns the verified guide for a recipe.
func (c *Catalog) GuideFor(recipeID string) (Guide, bool) {
	guide, ok := c.guides[recipeID]
	return guide, ok
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

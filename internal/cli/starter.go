package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haiven-dev/haiven-cli/internal/result"
	"github.com/haiven-dev/haiven-cli/internal/starter"
)

const starterLockPath = ".haiven/starter.lock.json"

// StarterListEntry couples discovery metadata to normative manual operations.
type StarterListEntry struct {
	Starter starter.Starter           `json:"starter"`
	Manual  []starter.ManualOperation `json:"manual"`
}

// StarterListData is the machine-readable starter discovery result.
type StarterListData struct {
	CatalogDigest  string             `json:"catalog_digest"`
	CatalogVersion string             `json:"catalog_version"`
	Filters        starter.Filter     `json:"filters"`
	Starters       []StarterListEntry `json:"starters"`
}

// StarterDescribeData includes verified recipe manifests and guide content.
type StarterDescribeData struct {
	CatalogDigest string                    `json:"catalog_digest"`
	Starter       starter.Starter           `json:"starter"`
	Recipes       []starter.Recipe          `json:"recipes"`
	Guides        []starter.Guide           `json:"guides"`
	Manual        []starter.ManualOperation `json:"manual"`
}

// StarterOperationData is a preview or completed optional automation result.
type StarterOperationData struct {
	Preview  bool              `json:"preview"`
	Dirty    bool              `json:"dirty_project"`
	Plan     *starter.Plan     `json:"plan,omitempty"`
	Evidence *starter.Evidence `json:"evidence,omitempty"`
	Lock     string            `json:"lock,omitempty"`
}

// StarterVerifyData is verification evidence tied to a project lock.
type StarterVerifyData struct {
	Lock     string           `json:"lock,omitempty"`
	Evidence starter.Evidence `json:"evidence"`
}

// StarterRunData records the generic v2 lifecycle sequence. Engine commands,
// logs, cancellation, and exit evidence are entirely runner-owned.
type StarterRunData struct {
	Schema        string                    `json:"schema"`
	StarterID     string                    `json:"starter_id"`
	Project       string                    `json:"project"`
	CatalogDigest string                    `json:"catalog_digest"`
	Manual        []starter.ManualOperation `json:"manual"`
	Guides        []starter.Guide           `json:"guides"`
	Results       []starter.LifecycleResult `json:"results"`
}

func runStarterList(c *call) {
	fs := c.flags()
	filter := starter.Filter{}
	fs.StringVar(&filter.Engine, "engine", "", "engine catalog tag")
	fs.StringVar(&filter.Journey, "journey", "", "onboarding journey")
	fs.StringVar(&filter.Platform, "platform", "", "supported host platform")
	fs.StringVar(&filter.Feature, "feature", "", "required feature")
	if _, ok := c.parse(fs, 0, 0); !ok {
		return
	}
	catalog := loadStarterCatalog(c)
	if catalog == nil {
		return
	}
	data := StarterListData{CatalogDigest: catalog.Digest, CatalogVersion: catalog.Version, Filters: filter, Starters: []StarterListEntry{}}
	for _, item := range catalog.List(filter) {
		_, recipes, _ := catalog.Resolve(item.ID)
		data.Starters = append(data.Starters, StarterListEntry{Starter: item, Manual: manualOperations(recipes)})
	}
	c.out.Data = data
	c.human = func(w io.Writer) {
		for _, entry := range data.Starters {
			fmt.Fprintf(w, "%-20s %-8s %-12s %s\n", entry.Starter.ID, entry.Starter.Engine, entry.Starter.Journey, entry.Starter.Description)
			for _, manual := range entry.Manual {
				fmt.Fprintf(w, "  %s: %s#%s (haiven CLI required: false)\n", manual.Operation, manual.GuidePath, strings.Join(manual.StepIDs, ","))
			}
		}
	}
}

func runStarterDescribe(c *call) {
	rest, ok := c.parse(c.flags(), 1, 1)
	if !ok {
		return
	}
	catalog := loadStarterCatalog(c)
	if catalog == nil {
		return
	}
	item, recipes, err := catalog.Resolve(rest[0])
	if err != nil {
		c.out.Fail(result.DiagStarterUnknown, "%v", err)
		return
	}
	guides := make([]starter.Guide, 0, len(recipes))
	for _, recipe := range recipes {
		guide, found := catalog.GuideFor(recipe.ID)
		if !found {
			c.out.Fail(result.DiagStarterCatalogInvalid, "recipe %q has no verified manual guide", recipe.ID)
			return
		}
		guides = append(guides, guide)
	}
	data := StarterDescribeData{CatalogDigest: catalog.Digest, Starter: item, Recipes: recipes, Guides: guides, Manual: manualOperations(recipes)}
	c.out.Data = data
	c.human = func(w io.Writer) {
		fmt.Fprintf(w, "%s (%s, %s)\n%s\n", item.DisplayName, item.Engine, item.Journey, item.Description)
		for _, manual := range data.Manual {
			fmt.Fprintf(w, "%s %s %s steps=%s requires_haiven_cli=false\n", manual.RecipeID, manual.Operation, manual.GuidePath, strings.Join(manual.StepIDs, ","))
		}
	}
}

type stringMapFlag map[string]string

func (m *stringMapFlag) String() string { return "" }
func (m *stringMapFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return fmt.Errorf("want name=value")
	}
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") {
		return fmt.Errorf("secret parameters must use a recipe-declared secret channel, not argv")
	}
	if *m == nil {
		*m = stringMapFlag{}
	}
	(*m)[key] = val
	return nil
}

type starterMutation string

const (
	starterCreate    starterMutation = "create"
	starterIntegrate starterMutation = "integrate"
)

func runStarterCreate(c *call)    { runStarterMutation(c, starterCreate) }
func runStarterIntegrate(c *call) { runStarterMutation(c, starterIntegrate) }

func runStarterMutation(c *call, command starterMutation) {
	fs := c.flags()
	var dest, project string
	var preview, yes bool
	params := stringMapFlag{}
	if command == starterCreate {
		fs.StringVar(&dest, "dest", "", "new project destination")
	} else {
		fs.StringVar(&project, "project", "", "existing project root")
	}
	fs.BoolVar(&preview, "preview", false, "resolve and plan without mutation")
	fs.BoolVar(&yes, "yes", false, "confirm the reviewed plan non-interactively")
	fs.Var(&params, "param", "recipe parameter name=value (repeatable; never for secrets)")
	rest, ok := c.parse(fs, 1, 1)
	if !ok {
		return
	}
	target := project
	if command == starterCreate {
		target = dest
	}
	if target == "" {
		flag := "project"
		if command == starterCreate {
			flag = "dest"
		}
		c.out.Fail(result.DiagMissingArgument, "--%s is required; usage: %s", flag, c.cmd.usage)
		return
	}
	if !preview && !yes {
		c.out.Fail(result.DiagStarterConfirmationRequired, "review --preview before applying starter %q, then pass --yes", rest[0])
		return
	}
	abs, err := resolveTarget(c.e.Cwd, target)
	if err != nil {
		c.out.Fail(result.DiagStarterUnsafeDestination, "resolve target %q: %v", target, err)
		return
	}
	catalog := loadStarterCatalog(c)
	if catalog == nil {
		return
	}
	item, recipes, err := catalog.Resolve(rest[0])
	if err != nil {
		c.out.Fail(result.DiagStarterUnknown, "%v", err)
		return
	}
	if command == starterIntegrate && !preview && item.Journey != "integrate" && item.Journey != "existing-project" {
		c.out.Fail(result.DiagStarterJourneyUnsupported, "starter %q is a %s journey; integrate --yes requires an existing-project starter", item.ID, item.Journey)
		c.out.Next = []string{"Use `haiven sdk starter create " + item.ID + " --dest <path> --preview` or follow the bundled native guide."}
		return
	}
	dirty, createMode := false, createDestinationAbsent
	if command == starterCreate {
		createMode, err = inspectCreateDestination(abs, item.ID, catalog.Digest)
		if err != nil {
			c.out.Fail(result.DiagStarterUnsafeDestination, "%v", err)
			return
		}
	} else {
		info, statErr := os.Stat(abs)
		if statErr != nil || !info.IsDir() {
			c.out.Fail(result.DiagStarterUnsafeDestination, "existing project %q is not a directory", abs)
			return
		}
		if c.e.GitDirty == nil {
			c.out.Fail(result.DiagStarterRunnerUnavailable, "the version-control cleanliness probe is unavailable")
			return
		}
		dirty, err = c.e.GitDirty(c.ctx, abs)
		if errors.Is(err, errProjectNotVersionControlled) {
			if !preview {
				c.out.Fail(result.DiagStarterUnversionedProject, "project %q is not a Git worktree; integrate apply needs a clean version-controlled project", abs)
				c.out.Next = []string{"haiven sdk starter integrate " + item.ID + " --project " + target + " --preview"}
				return
			}
			dirty, err = false, nil
		}
		if err != nil {
			c.out.Fail(result.DiagStarterRunnerUnavailable, "check project cleanliness: %v", err)
			return
		}
		if dirty && !preview {
			c.out.Fail(result.DiagStarterDirtyProject, "project %q has uncommitted changes", abs)
			return
		}
		if msg, ok := existingIntegrateLock(abs, item, recipes, map[string]string(params)); !ok {
			c.out.Fail(result.DiagStarterLockChanged, "%s", msg)
			return
		}
	}
	runner := c.e.StarterRunner
	if runner == nil {
		runner = starter.UnavailableRunner{}
	}
	planResults, err := executeRecipes(c, runner, item.ID, recipes, starter.RunnerPlan, abs, map[string]string(params), true)
	if err != nil {
		return
	}
	plan := starter.Plan{Schema: starter.RunnerSchema, StarterID: item.ID, Target: filepath.ToSlash(filepath.Clean(target)), CatalogDigest: catalog.Digest, Manual: manualOperations(recipes), Guides: guidesFor(catalog, recipes), Results: planResults}
	data := StarterOperationData{Preview: preview, Dirty: dirty, Plan: &plan}
	c.out.Data = data
	c.human = renderStarterOperation(data)
	if failRunnerResults(c, planResults, plan.Manual) {
		return
	}
	if pendingResults(planResults) {
		setManualRequired(c, plan.Manual)
		return
	}
	if preview {
		return
	}
	applyRoot, stage := abs, ""
	if command == starterCreate && createMode != createDestinationOwned {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			c.out.Fail(result.DiagStarterUnsafeDestination, "prepare destination parent: %v", err)
			return
		}
		stage, err = os.MkdirTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".haiven-*")
		if err != nil {
			c.out.Fail(result.DiagStarterUnsafeDestination, "stage destination: %v", err)
			return
		}
		defer func() {
			if stage != "" {
				_ = os.RemoveAll(stage)
			}
		}()
		applyRoot = stage
	}
	applyResults, err := executeRecipes(c, runner, item.ID, recipes, starter.RunnerApply, applyRoot, map[string]string(params), false)
	if err != nil {
		return
	}
	evidence := starter.Evidence{Schema: starter.RunnerSchema, StarterID: item.ID, CatalogDigest: catalog.Digest, Operation: starter.RunnerApply, Manual: manualFor(recipes, starter.RunnerApply), Guides: guidesFor(catalog, recipes), Results: applyResults}
	data.Evidence = &evidence
	c.out.Data = data
	if failRunnerResults(c, applyResults, evidence.Manual) {
		return
	}
	manualRequired := pendingResults(applyResults)
	lock := makeStarterLock(catalog, item, recipes, map[string]string(params), applyResults)
	lockPath, digest, err := writeStarterLock(applyRoot, lock)
	if err != nil {
		c.out.Fail(result.DiagInternalError, "write starter lock: %v", err)
		return
	}
	if stage != "" {
		if createMode == createDestinationEmpty {
			if err := os.Remove(abs); err != nil {
				c.out.Fail(result.DiagStarterUnsafeDestination, "replace empty destination: %v", err)
				return
			}
		}
		if err := os.Rename(stage, abs); err != nil {
			c.out.Fail(result.DiagInternalError, "publish staged starter project: %v", err)
			return
		}
		stage, lockPath = "", filepath.Join(abs, filepath.FromSlash(starterLockPath))
	}
	c.out.LockDigest = digest
	data.Lock = filepath.ToSlash(lockPath)
	c.out.Data = data
	c.human = renderStarterOperation(data)
	if manualRequired {
		setManualRequired(c, evidence.Manual)
	}
}

func runStarterVerify(c *call) {
	fs := c.flags()
	var project, starterID string
	fs.StringVar(&project, "project", "", "project root")
	fs.StringVar(&starterID, "starter", "", "starter ID for a native/manual project without a CLI lock")
	if _, ok := c.parse(fs, 0, 0); !ok {
		return
	}
	if project == "" {
		c.out.Fail(result.DiagMissingArgument, "--project is required; usage: %s", c.cmd.usage)
		return
	}
	abs, err := resolveTarget(c.e.Cwd, project)
	if err != nil {
		c.out.Fail(result.DiagStarterUnsafeDestination, "resolve project: %v", err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		c.out.Fail(result.DiagStarterUnsafeDestination, "project %q is not a directory", abs)
		return
	}
	catalog := loadStarterCatalog(c)
	if catalog == nil {
		return
	}
	lock, lockBytes, err := readStarterLock(abs)
	var item starter.Starter
	var recipes []starter.Recipe
	lockPath := ""
	parameters := map[string]string{}
	if err == nil {
		c.out.LockDigest = digestBytes(lockBytes)
		lockPath = filepath.ToSlash(filepath.Join(abs, starterLockPath))
		if lock.Starter.CatalogDigest != catalog.Digest || lock.Starter.CatalogID != catalog.ID || lock.Starter.CatalogVersion != catalog.Version {
			c.out.Fail(result.DiagStarterEvidenceUntrusted, "lock catalog does not match the trusted catalog")
			return
		}
		if starterID != "" && starterID != lock.Starter.ID {
			c.out.Fail(result.DiagStarterEvidenceUntrusted, "--starter %q does not match locked starter %q", starterID, lock.Starter.ID)
			return
		}
		item, recipes, err = catalog.Resolve(lock.Starter.ID)
		if err != nil || !sameRecipes(lock.Recipes, recipes) {
			c.out.Fail(result.DiagStarterEvidenceUntrusted, "lock recipe graph does not match the trusted catalog")
			return
		}
		parameters = lock.Parameters
	} else {
		if !errors.Is(err, os.ErrNotExist) || starterID == "" {
			c.out.Fail(result.DiagStarterLockNotFound, "project %q has no readable %s; pass --starter <id> to verify through the normative guide without a CLI lock", filepath.ToSlash(filepath.Clean(project)), starterLockPath)
			return
		}
		item, recipes, err = catalog.Resolve(starterID)
		if err != nil {
			c.out.Fail(result.DiagStarterUnknown, "%v", err)
			return
		}
	}
	runner := c.e.StarterRunner
	if runner == nil {
		runner = starter.UnavailableRunner{}
	}
	results, err := executeRecipes(c, runner, item.ID, recipes, starter.RunnerVerify, abs, parameters, false)
	if err != nil {
		return
	}
	evidence := starter.Evidence{Schema: starter.RunnerSchema, StarterID: item.ID, CatalogDigest: catalog.Digest, Operation: starter.RunnerVerify, Manual: manualFor(recipes, starter.RunnerVerify), Guides: guidesFor(catalog, recipes), Results: results}
	for _, result := range results {
		if result.Status == starter.StatusDrift {
			evidence.Drift = true
		}
	}
	c.out.Data = StarterVerifyData{Lock: lockPath, Evidence: evidence}
	if failRunnerResults(c, results, evidence.Manual) {
		return
	}
	if pendingResults(results) {
		setManualRequired(c, evidence.Manual)
		return
	}
	if evidence.Drift {
		c.out.Fail(result.DiagStarterDrift, "starter-owned project state differs from %s", starterLockPath)
	}
}

func runStarterRun(c *call) {
	fs := c.flags()
	var project string
	params := stringMapFlag{}
	fs.StringVar(&project, "project", "", "project root")
	fs.Var(&params, "param", "recipe parameter name=value (repeatable; never for secrets)")
	rest, ok := c.parse(fs, 1, 1)
	if !ok {
		return
	}
	if project == "" {
		c.out.Fail(result.DiagMissingArgument, "--project is required; usage: %s", c.cmd.usage)
		return
	}
	abs, err := resolveTarget(c.e.Cwd, project)
	if err != nil {
		c.out.Fail(result.DiagStarterUnsafeDestination, "resolve project: %v", err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		c.out.Fail(result.DiagStarterUnsafeDestination, "project %q is not a directory", abs)
		return
	}
	catalog := loadStarterCatalog(c)
	if catalog == nil {
		return
	}
	item, recipes, err := catalog.Resolve(rest[0])
	if err != nil {
		c.out.Fail(result.DiagStarterUnknown, "%v", err)
		return
	}
	manual := append(manualFor(recipes, starter.RunnerBuild), manualFor(recipes, starter.RunnerRun)...)
	data := StarterRunData{
		Schema: starter.LifecycleSchema, StarterID: item.ID,
		Project: filepath.ToSlash(filepath.Clean(project)), CatalogDigest: catalog.Digest,
		Manual: manual, Guides: guidesFor(catalog, recipes), Results: []starter.LifecycleResult{},
	}
	c.out.Data = data
	c.human = renderStarterRun(&data)
	for _, recipe := range recipes {
		if recipe.Runner == nil || recipe.Runner.ProtocolVersion != 2 || !recipeSupportsLifecycle(recipe) {
			c.out.Next = []string{"Install a Haiven SDK/CLI release whose pinned recipe declares starter-runner/v2 build and run operations."}
			c.out.Fail(result.DiagStarterRunnerUnavailable, "starter %q does not publish a verified v2 build/run lifecycle", item.ID)
			return
		}
	}
	runner := c.e.StarterLifecycleRunner
	if runner == nil {
		setManualRequired(c, manual)
		return
	}
	typedParams := make(map[string]any, len(params))
	for key, value := range params {
		typedParams[key] = value
	}
	capabilities := make([]starter.Capabilities, len(recipes))
	for index, recipe := range recipes {
		req := starter.RunnerV2RequestFor(fmt.Sprintf("%s-capabilities-%d", item.ID, index), starter.RunnerCapabilities, recipe, abs, typedParams)
		output, runErr := runner.RunLifecycle(c.ctx, *recipe.Runner, req)
		if !acceptLifecycleResult(c, recipe, req, output, runErr) {
			return
		}
		normalizeLifecycleResult(&output)
		data.Results = append(data.Results, output)
		c.out.Data = data
		if output.Capabilities == nil || !output.Capabilities.Operations.Build.Supported || !output.Capabilities.Operations.Run.Supported {
			setManualRequired(c, manual)
			return
		}
		capabilities[index] = *output.Capabilities
	}
	for _, operation := range []starter.RunnerOperation{starter.RunnerBuild, starter.RunnerRun} {
		for index, recipe := range recipes {
			req := starter.RunnerV2RequestFor(fmt.Sprintf("%s-%s-%d", item.ID, operation, index), operation, recipe, abs, typedParams)
			req.CancellationGraceMS = capabilities[index].Cancellation.GraceMS
			output, runErr := runner.RunLifecycle(c.ctx, *recipe.Runner, req)
			if !acceptLifecycleResult(c, recipe, req, output, runErr) {
				return
			}
			normalizeLifecycleResult(&output)
			data.Results = append(data.Results, output)
			c.out.Data = data
			switch output.Status {
			case starter.StatusManualRequired, starter.StatusUnsupported:
				setManualRequired(c, manualFor(recipes, operation))
				return
			case starter.StatusFailed, starter.StatusCancelled:
				failLifecycleResult(c, output)
				return
			}
		}
	}
}

func recipeSupportsLifecycle(recipe starter.Recipe) bool {
	seen := map[starter.RunnerOperation]bool{}
	for _, operation := range recipe.Operations {
		seen[operation] = true
	}
	return seen[starter.RunnerBuild] && seen[starter.RunnerRun]
}

func acceptLifecycleResult(c *call, recipe starter.Recipe, request starter.RunnerV2Request, output starter.LifecycleResult, err error) bool {
	if errors.Is(err, starter.ErrRunnerIntegrity) {
		c.out.Fail(result.DiagStarterEvidenceUntrusted, "%v", err)
		return false
	}
	if err != nil {
		starterRunnerError(c, err)
		return false
	}
	if err := starter.ValidateLifecycleResult(output, request, recipe); err != nil {
		c.out.Fail(result.DiagStarterEvidenceUntrusted, "%v", err)
		return false
	}
	return true
}

func normalizeLifecycleResult(output *starter.LifecycleResult) {
	if output.ManualSteps != nil {
		if output.ManualSteps.Completed == nil {
			output.ManualSteps.Completed = []string{}
		}
		if output.ManualSteps.Pending == nil {
			output.ManualSteps.Pending = []string{}
		}
	}
	if output.Warnings == nil {
		output.Warnings = []string{}
	}
	if output.Errors == nil {
		output.Errors = []string{}
	}
}

func failLifecycleResult(c *call, output starter.LifecycleResult) {
	detail := strings.Join(output.Errors, "; ")
	if detail == "" {
		detail = fmt.Sprintf("%s returned %s", output.RequestID, output.Status)
	}
	c.out.Fail(result.DiagStarterRunnerFailed, "%s", detail)
}

func executeRecipes(c *call, runner starter.Runner, starterID string, recipes []starter.Recipe, operation starter.RunnerOperation, destination string, parameters map[string]string, preview bool) ([]starter.RunnerResult, error) {
	results := make([]starter.RunnerResult, 0, len(recipes))
	for index, recipe := range recipes {
		req := starter.RequestFor(fmt.Sprintf("%s-%s-%d", starterID, operation, index), operation, recipe, destination, parameters, preview)
		var output starter.RunnerResult
		var err error
		if recipe.Runner == nil {
			output = starter.ManualRequiredResult(req, recipe)
		} else {
			output, err = runner.Run(c.ctx, *recipe.Runner, req)
			if errors.Is(err, starter.ErrRunnerUnavailable) {
				output, err = starter.ManualRequiredResult(req, recipe), nil
			}
		}
		if errors.Is(err, starter.ErrRunnerIntegrity) {
			c.out.Fail(result.DiagStarterEvidenceUntrusted, "%v", err)
			return nil, err
		}
		if err != nil {
			starterRunnerError(c, err)
			return nil, err
		}
		if err := starter.ValidateRunnerResult(output, req, recipe); err != nil {
			c.out.Fail(result.DiagStarterEvidenceUntrusted, "%v", err)
			return nil, err
		}
		normalizeRunnerResult(&output)
		results = append(results, output)
	}
	return results, nil
}

func manualOperations(recipes []starter.Recipe) []starter.ManualOperation {
	manual := []starter.ManualOperation{}
	for _, recipe := range recipes {
		for _, operation := range recipe.Operations {
			manual = append(manual, starter.ManualFor(recipe, operation))
		}
	}
	return manual
}

func manualFor(recipes []starter.Recipe, operation starter.RunnerOperation) []starter.ManualOperation {
	manual := make([]starter.ManualOperation, 0, len(recipes))
	for _, recipe := range recipes {
		manual = append(manual, starter.ManualFor(recipe, operation))
	}
	return manual
}

func guidesFor(catalog *starter.Catalog, recipes []starter.Recipe) []starter.Guide {
	guides := make([]starter.Guide, 0, len(recipes))
	for _, recipe := range recipes {
		if guide, ok := catalog.GuideFor(recipe.ID); ok {
			guides = append(guides, guide)
		}
	}
	return guides
}

func pendingResults(results []starter.RunnerResult) bool {
	for _, output := range results {
		if output.Status == starter.StatusManualRequired || len(output.ManualSteps.Pending) != 0 {
			return true
		}
	}
	return false
}

func failRunnerResults(c *call, results []starter.RunnerResult, manual []starter.ManualOperation) bool {
	errorsFound := []string{}
	for _, output := range results {
		if output.Status != starter.StatusFailed {
			continue
		}
		if len(output.Errors) == 0 {
			errorsFound = append(errorsFound, fmt.Sprintf("%s failed without an error detail", output.RequestID))
		} else {
			for _, detail := range output.Errors {
				errorsFound = append(errorsFound, output.RequestID+": "+detail)
			}
		}
	}
	if len(errorsFound) == 0 {
		return false
	}
	c.out.Next = c.out.Next[:0]
	for _, item := range manual {
		c.out.Next = append(c.out.Next, fmt.Sprintf("Review %s steps %s, correct the reported problem, and retry.", item.GuidePath, strings.Join(item.StepIDs, ", ")))
	}
	c.out.Fail(result.DiagStarterRunnerFailed, "%s", strings.Join(errorsFound, "; "))
	return true
}

func setManualRequired(c *call, manual []starter.ManualOperation) {
	c.out.Next = c.out.Next[:0]
	for _, item := range manual {
		c.out.Next = append(c.out.Next, fmt.Sprintf("Follow %s steps %s with native tools; haiven CLI is optional.", item.GuidePath, strings.Join(item.StepIDs, ", ")))
	}
	c.out.Fail(result.DiagStarterManualRequired, "normative native guide steps remain pending")
}

func normalizeRunnerResult(output *starter.RunnerResult) {
	if output.Changes == nil {
		output.Changes = []starter.RunnerChange{}
	}
	if output.Artifacts == nil {
		output.Artifacts = []starter.Artifact{}
	}
	if output.ResolvedSources == nil {
		output.ResolvedSources = map[string]string{}
	}
	if output.ManualSteps.Completed == nil {
		output.ManualSteps.Completed = []string{}
	}
	if output.ManualSteps.Pending == nil {
		output.ManualSteps.Pending = []string{}
	}
	if output.Warnings == nil {
		output.Warnings = []string{}
	}
	if output.Errors == nil {
		output.Errors = []string{}
	}
}

func makeStarterLock(catalog *starter.Catalog, item starter.Starter, recipes []starter.Recipe, parameters map[string]string, results []starter.RunnerResult) starter.Lock {
	lock := starter.Lock{APIVersion: starter.LockSchema, Starter: starter.LockedStarter{ID: item.ID, CatalogID: catalog.ID, CatalogVersion: catalog.Version, CatalogDigest: catalog.Digest}, Recipes: []starter.LockedRecipe{}, Resolved: map[string]string{}, Parameters: parameters, Toolchains: map[string]string{}, Artifacts: []starter.Artifact{}}
	for _, recipe := range recipes {
		entry := starter.LockedRecipe{ID: recipe.ID, Version: recipe.Version, Digest: recipe.Digest}
		if recipe.Runner != nil {
			entry.RunnerDigest = recipe.Runner.Digest
		}
		lock.Recipes = append(lock.Recipes, entry)
	}
	for _, output := range results {
		for key, value := range output.ResolvedSources {
			lock.Resolved[key] = value
		}
		lock.Artifacts = append(lock.Artifacts, output.Artifacts...)
	}
	return lock
}

func loadStarterCatalog(c *call) *starter.Catalog {
	catalog, err := starter.Load(c.ctx, c.e.StarterCatalog)
	if err == nil {
		return catalog
	}
	if errors.Is(err, starter.ErrCatalogIntegrity) {
		c.out.Fail(result.DiagStarterCatalogUntrusted, "%v", err)
	} else {
		c.out.Fail(result.DiagStarterCatalogInvalid, "%v", err)
	}
	return nil
}

func starterRunnerError(c *call, err error) {
	c.out.Fail(result.DiagStarterRunnerUnavailable, "%v", err)
}

type createDestinationMode int

const (
	createDestinationAbsent createDestinationMode = iota
	createDestinationEmpty
	createDestinationOwned
)

func inspectCreateDestination(path, starterID, catalogDigest string) (createDestinationMode, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return createDestinationAbsent, nil
	}
	if err != nil {
		return createDestinationAbsent, err
	}
	if len(entries) == 0 {
		return createDestinationEmpty, nil
	}
	lock, _, err := readStarterLock(path)
	if err == nil && lock.Starter.ID == starterID && lock.Starter.CatalogDigest == catalogDigest {
		return createDestinationOwned, nil
	}
	return createDestinationAbsent, fmt.Errorf("destination %q is not empty or owned by starter %q", path, starterID)
}

func writeStarterLock(root string, lock starter.Lock) (string, string, error) {
	dir := filepath.Join(root, ".haiven")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	body, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return "", "", err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, "starter.lock-*.json")
	if err != nil {
		return "", "", err
	}
	name, ok := tmp.Name(), false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return "", "", err
	}
	if _, err := tmp.Write(body); err != nil {
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	final := filepath.Join(root, filepath.FromSlash(starterLockPath))
	if err := os.Rename(name, final); err != nil {
		return "", "", err
	}
	ok = true
	return final, digestBytes(body), nil
}

func readStarterLock(root string) (starter.Lock, []byte, error) {
	name := filepath.Join(root, filepath.FromSlash(starterLockPath))
	body, err := os.ReadFile(name)
	if err != nil {
		return starter.Lock{}, nil, fmt.Errorf("read %s: %w", name, err)
	}
	var lock starter.Lock
	if err := json.Unmarshal(body, &lock); err != nil || lock.APIVersion != starter.LockSchema {
		return starter.Lock{}, nil, fmt.Errorf("invalid %s", name)
	}
	return lock, body, nil
}

// existingIntegrateLock allows a first integration or a rerun of the same
// starter, recipe identities, and parameters. A different input fails
// before any runner work. Upgrade is outside this release.
func existingIntegrateLock(root string, item starter.Starter, recipes []starter.Recipe, parameters map[string]string) (string, bool) {
	lock, _, err := readStarterLock(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", true
		}
		return fmt.Sprintf("%s cannot be read; integrate will not replace it", starterLockPath), false
	}
	if lock.Starter.ID != item.ID {
		return fmt.Sprintf("locked starter %q does not match requested %q; upgrade is outside this release", lock.Starter.ID, item.ID), false
	}
	if !sameRecipes(lock.Recipes, recipes) {
		return "locked recipe identities do not match the requested starter; upgrade is outside this release", false
	}
	if !sameStringMap(lock.Parameters, parameters) {
		return "locked parameters do not match the request; upgrade is outside this release", false
	}
	return "", true
}

func sameStringMap(locked, requested map[string]string) bool {
	if len(locked) == 0 && len(requested) == 0 {
		return true
	}
	if len(locked) != len(requested) {
		return false
	}
	for key, value := range requested {
		if locked[key] != value {
			return false
		}
	}
	return true
}

func sameRecipes(locked []starter.LockedRecipe, recipes []starter.Recipe) bool {
	if len(locked) != len(recipes) {
		return false
	}
	for index, recipe := range recipes {
		wantRunner := ""
		if recipe.Runner != nil {
			wantRunner = recipe.Runner.Digest
		}
		if locked[index].ID != recipe.ID || locked[index].Version != recipe.Version || locked[index].Digest != recipe.Digest || locked[index].RunnerDigest != wantRunner {
			return false
		}
	}
	return true
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func resolveTarget(cwd, target string) (string, error) {
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	return filepath.Abs(filepath.Join(cwd, target))
}

func renderStarterOperation(data StarterOperationData) func(io.Writer) {
	return func(w io.Writer) {
		if data.Plan != nil {
			fmt.Fprintf(w, "preview %s at %s\n", data.Plan.StarterID, data.Plan.Target)
			completed, pending := map[string]bool{}, map[string]bool{}
			changes := []starter.RunnerChange{}
			for _, output := range data.Plan.Results {
				for _, id := range output.ManualSteps.Completed {
					completed[id] = true
				}
				for _, id := range output.ManualSteps.Pending {
					pending[id] = true
				}
				changes = append(changes, output.Changes...)
			}
			for _, guide := range data.Plan.Guides {
				fmt.Fprintf(w, "guide: %s (%s)\n", guide.Path, guide.Digest)
				for _, step := range guide.Steps {
					state := "later"
					if completed[step.ID] {
						state = "complete"
					} else if pending[step.ID] {
						state = "pending"
					}
					fmt.Fprintf(w, "  [%s] %s (%s) via %s\n", state, step.ID, step.Operation, step.NativeTool)
					fmt.Fprintf(w, "    %s\n", step.Action)
					fmt.Fprintf(w, "    prerequisites: %s\n", strings.Join(step.Preconditions, "; "))
					fmt.Fprintf(w, "    expected: %s\n", step.ExpectedOutcome)
					fmt.Fprintf(w, "    evidence: %s\n", strings.Join(step.Evidence, "; "))
				}
			}
			sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
			for _, change := range changes {
				fmt.Fprintf(w, "planned change: %s %s\n", change.Action, change.Path)
			}
		}
		if data.Lock != "" {
			fmt.Fprintf(w, "lock: %s\n", data.Lock)
		}
	}
}

func renderStarterRun(data *StarterRunData) func(io.Writer) {
	return func(w io.Writer) {
		fmt.Fprintf(w, "starter %s project=%s protocol=%s\n", data.StarterID, data.Project, data.Schema)
		for _, output := range data.Results {
			fmt.Fprintf(w, "%s: %s\n", output.Operation, output.Status)
			if output.ExitEvidence != nil {
				fmt.Fprintf(w, "  exit=%s duration_ms=%d\n", output.ExitEvidence.Outcome, output.ExitEvidence.DurationMS)
			}
		}
	}
}

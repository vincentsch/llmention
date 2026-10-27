package run

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/benchmark"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/dimension"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/recognition"
	"github.com/vincentsch/llmention/internal/suite"
)

// maxWorkUnits stops a typo such as repetitions: 10000 from building a plan
// that could never fit any budget.
const maxWorkUnits = 100_000

// PlanOptions narrows what a run sends. Empty fields mean "use the project".
type PlanOptions struct {
	SuiteIDs    []string
	EngineIDs   []string
	Modes       []engine.ToolPolicy
	Repetitions int
	MaxRequests int
}

// BudgetError says that a plan needs more requests than the budget allows.
type BudgetError struct {
	Requests    int
	MaxRequests int
}

func (e BudgetError) Error() string {
	return fmt.Sprintf("this run needs %d requests but the budget is %d (execution.max_requests); ask fewer prompts or engines, or raise the budget with --max-requests %d", e.Requests, e.MaxRequests, e.Requests)
}

// BuildPlan turns the selected suites into work units, one for each request.
// It reads no keys and sends nothing.
func BuildPlan(project *config.Project, catalog suite.Catalog, engines []engine.Engine, opts PlanOptions) (*Plan, error) {
	if project == nil {
		return nil, fmt.Errorf("project config is nil")
	}
	engines, err := selectEngines(engines, opts.EngineIDs)
	if err != nil {
		return nil, err
	}
	selections, err := suiteSelections(project, catalog, opts)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		Retries:          project.Execution.Retries,
		MaxRequests:      project.Execution.MaxRequests,
		Concurrency:      project.Execution.Concurrency,
		engineDefaults:   map[string]defaults{},
		engineIdentities: map[string]identity{},
	}
	if opts.MaxRequests > 0 {
		plan.MaxRequests = opts.MaxRequests
	}
	for _, cfg := range project.Engines {
		plan.engineDefaults[cfg.ID] = defaults{maxOutputTokens: cfg.MaxOutputTokens}
	}
	for _, eng := range engines {
		plan.engineIdentities[eng.ID()] = identity{provider: eng.Provider(), model: eng.Model()}
	}

	seenMatrix := map[string]bool{}
	for _, sel := range selections {
		s := sel.loaded.Suite
		rendered, variantCount, err := renderedVariants(s)
		if err != nil {
			return nil, err
		}
		suitePlan := SuitePlan{
			ID:           s.ID,
			Version:      s.Version,
			Type:         s.Type,
			Source:       string(sel.loaded.Source),
			Path:         sel.loaded.Path,
			ItemCount:    len(s.Items),
			VariantCount: variantCount,
			Modes:        policyStrings(sel.modes),
			Repetitions:  sel.repetitions,
		}
		pairUnits := variantCount * sel.repetitions
		if pairUnits*len(sel.modes)*len(engines)+len(plan.WorkUnits) > maxWorkUnits {
			return nil, fmt.Errorf("suite %q would send more than %d requests; lower repetitions or ask fewer engines", s.ID, maxWorkUnits)
		}
		for _, mode := range sel.modes {
			for _, eng := range engines {
				support := supportFor(eng, mode)
				if key := eng.ID() + "\x00" + mode.String(); !seenMatrix[key] {
					seenMatrix[key] = true
					plan.EngineModeMatrix = append(plan.EngineModeMatrix, support)
				}
				if !support.Supported {
					plan.UnsupportedPairs = append(plan.UnsupportedPairs, unsupported(s.ID, eng, mode, support.Reason, pairUnits))
					suitePlan.SkippedWorkUnits += pairUnits
					continue
				}
				for _, item := range s.Items {
					for _, variant := range rendered[item.ID] {
						for rep := 1; rep <= sel.repetitions; rep++ {
							unit := workUnit(s, item, variant, mode, eng, rep)
							if err := eng.ValidateRequest(RequestForWorkUnit(unit, plan)); err != nil {
								plan.UnsupportedPairs = append(plan.UnsupportedPairs, unsupported(s.ID, eng, mode, err.Error(), 1))
								suitePlan.SkippedWorkUnits++
								continue
							}
							plan.WorkUnits = append(plan.WorkUnits, unit)
							suitePlan.ValidWorkUnits++
						}
					}
				}
			}
		}
		plan.Suites = append(plan.Suites, suitePlan)
	}
	plan.MaxRequestsWithRetries = len(plan.WorkUnits) * (1 + plan.Retries)
	engineIDs := uniqueEngineIDs(plan.WorkUnits)
	if len(engineIDs) > 0 {
		envs, err := project.RequiredKeyEnvVars(config.KeySelection{EngineIDs: engineIDs})
		if err != nil {
			return nil, err
		}
		plan.RequiredKeyEnv = envs
	}
	slices.SortStableFunc(plan.EngineModeMatrix, func(a, b EngineModeSupport) int {
		return strings.Compare(a.EngineID+"\x00"+a.Mode, b.EngineID+"\x00"+b.Mode)
	})
	return plan, nil
}

// CheckBudget reports whether the first attempt of every work unit fits the
// request budget. Retries only use what is left over.
func CheckBudget(plan *Plan) error {
	if plan.MaxRequests <= 0 {
		return fmt.Errorf("the request budget must be 1 or more")
	}
	if len(plan.WorkUnits) > plan.MaxRequests {
		return BudgetError{Requests: len(plan.WorkUnits), MaxRequests: plan.MaxRequests}
	}
	return nil
}

// RequestForWorkUnit builds the engine request for a work unit.
func RequestForWorkUnit(unit WorkUnit, plan *Plan) engine.Request {
	req := engine.Request{
		SuiteID:       unit.SuiteID,
		SuiteVersion:  unit.SuiteVersion,
		ItemID:        unit.ItemID,
		VariantID:     unit.VariantID,
		Mode:          unit.Mode,
		Prompt:        unit.Prompt,
		OutputSchema:  unit.OutputSchema,
		SearchOptions: cloneSearchOptions(unit.SearchOptions),
		Metadata: map[string]string{
			"engine_id":  unit.EngineID,
			"repetition": fmt.Sprint(unit.Repetition),
		},
	}
	if plan != nil {
		req.MaxOutputTokens = plan.engineDefaults[unit.EngineID].maxOutputTokens
	}
	return req
}

func workUnit(s *suite.Suite, item suite.Item, variant suite.RenderedVariant, mode engine.ToolPolicy, eng engine.Engine, rep int) WorkUnit {
	prompt := variant.Prompt
	var schema *engine.OutputSchema
	switch {
	case s.Type == "recognition":
		prompt = recognition.BuildPrompt(item.Query, variant.Prompt, mode.String())
		name, format, strict, schemaJSON := recognition.OutputSchemaFields()
		schema = &engine.OutputSchema{Name: name, Format: format, Strict: strict, SchemaJSON: schemaJSON}
	case benchmarkSuiteType(s.Type):
		prompt = benchmark.BuildPrompt(item.Question, variant.Prompt, mode.String())
		name, format, strict, schemaJSON := benchmark.OutputSchemaFields()
		schema = &engine.OutputSchema{Name: name, Format: format, Strict: strict, SchemaJSON: schemaJSON}
	}
	return WorkUnit{
		SuiteID:           s.ID,
		SuiteVersion:      s.Version,
		ItemID:            item.ID,
		VariantID:         variant.VariantID,
		Dimensions:        dimension.Map(variant.Dimensions),
		ComparisonGroupID: variant.ComparisonGroupID,
		Mode:              mode,
		EngineID:          eng.ID(),
		Repetition:        rep,
		Prompt:            prompt,
		OutputSchema:      schema,
		SearchOptions:     searchOptionsForItem(s, item, mode, eng.Capabilities()),
	}
}

func unsupported(suiteID string, eng engine.Engine, mode engine.ToolPolicy, reason string, skipped int) UnsupportedPair {
	return UnsupportedPair{
		SuiteID:          suiteID,
		EngineID:         eng.ID(),
		Provider:         eng.Provider(),
		Model:            eng.Model(),
		Mode:             mode.String(),
		Reason:           reason,
		WorkUnitsSkipped: skipped,
	}
}

func selectEngines(engines []engine.Engine, ids []string) ([]engine.Engine, error) {
	if len(engines) == 0 {
		return nil, fmt.Errorf("add at least one engine under engines: in llmention.yaml")
	}
	if len(ids) == 0 {
		return engines, nil
	}
	var out []engine.Engine
	for _, id := range ids {
		i := slices.IndexFunc(engines, func(e engine.Engine) bool { return e.ID() == id })
		if i < 0 {
			var known []string
			for _, e := range engines {
				known = append(known, e.ID())
			}
			return nil, fmt.Errorf("engine %q is not in llmention.yaml; configured engines: %s", id, strings.Join(known, ", "))
		}
		out = append(out, engines[i])
	}
	return out, nil
}

type suiteSelection struct {
	loaded      suite.LoadedSuite
	modes       []engine.ToolPolicy
	repetitions int
}

// suiteSelections picks the suites to run: the ones named with --suite, or
// the project's prompts when none are named.
func suiteSelections(project *config.Project, catalog suite.Catalog, opts PlanOptions) ([]suiteSelection, error) {
	ids := opts.SuiteIDs
	if len(ids) == 0 {
		if _, ok := catalog.Find(config.PromptsSuiteID); !ok {
			return nil, fmt.Errorf("llmention.yaml has no prompts; add some under prompts:, or pick a suite with --suite (see `llmention suites list`)")
		}
		ids = []string{config.PromptsSuiteID}
	}
	var out []suiteSelection
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		loaded, ok := catalog.Find(id)
		if !ok {
			return nil, fmt.Errorf("there is no suite %q; `llmention suites list` shows the ones you can run", id)
		}
		modes := opts.Modes
		if len(modes) == 0 {
			parsed, err := parsePolicies(loaded.Suite.DefaultModes)
			if err != nil {
				return nil, err
			}
			modes = parsed
		}
		repetitions := opts.Repetitions
		if repetitions == 0 {
			repetitions = loaded.Suite.Repetitions
		}
		if repetitions <= 0 {
			return nil, fmt.Errorf("repetitions must be 1 or more")
		}
		out = append(out, suiteSelection{loaded: loaded, modes: modes, repetitions: repetitions})
	}
	return out, nil
}

func renderedVariants(s *suite.Suite) (map[string][]suite.RenderedVariant, int, error) {
	out := make(map[string][]suite.RenderedVariant, len(s.Items))
	total := 0
	for _, item := range s.Items {
		variants, err := suite.RenderVariants(s, item)
		if err != nil {
			return nil, 0, err
		}
		out[item.ID] = variants
		total += len(variants)
	}
	return out, total, nil
}

func searchOptionsForItem(s *suite.Suite, item suite.Item, mode engine.ToolPolicy, caps engine.CapabilityProfile) *engine.SearchOptions {
	if !mode.IsSearchMode() {
		return nil
	}
	opts := &engine.SearchOptions{}
	switch mode {
	case engine.ToolPolicySearchRequired:
		opts.RequireSearch = true
	case engine.ToolPolicySearchCacheOnly:
		opts.CacheOnly = true
	}
	if caps.SupportsSourceMetadataRequest && s != nil && (s.Type == "sources" || len(item.PreferredDomains) > 0 || len(item.BlockedDomains) > 0 || temporalSourceRequired(item, mode) || (s.Type == "propaganda" && item.SourcePolicy != nil)) {
		opts.IncludeSourceMetadata = true
	}
	if opts.IsZero() {
		return nil
	}
	return opts
}

func benchmarkSuiteType(value string) bool {
	return value == "factoid" || value == "programming" || value == "temporal"
}

func temporalSourceRequired(item suite.Item, mode engine.ToolPolicy) bool {
	return item.SourceExpectation != nil && slices.Contains(item.SourceExpectation.RequiredForModes, mode.String())
}

func cloneSearchOptions(in *engine.SearchOptions) *engine.SearchOptions {
	if in == nil {
		return nil
	}
	out := *in
	out.AllowedDomains = slices.Clone(in.AllowedDomains)
	out.BlockedDomains = slices.Clone(in.BlockedDomains)
	if in.UserLocation != nil {
		location := *in.UserLocation
		out.UserLocation = &location
	}
	return &out
}

func supportFor(eng engine.Engine, mode engine.ToolPolicy) EngineModeSupport {
	support := EngineModeSupport{
		EngineID:  eng.ID(),
		Provider:  eng.Provider(),
		Model:     eng.Model(),
		Mode:      mode.String(),
		Supported: eng.Capabilities().SupportsMode(mode),
	}
	if !support.Supported {
		support.Reason = fmt.Sprintf("%s does not support %s", eng.Provider(), config.ModeLabel(mode.String()))
		if base, err := engine.CapabilityProfileForConfig(eng.Provider(), eng.Model(), nil); err == nil && base.SupportsMode(mode) {
			support.Reason = fmt.Sprintf("%s is turned off under capabilities in llmention.yaml", config.ModeLabel(mode.String()))
		}
	}
	return support
}

func parsePolicies(values []string) ([]engine.ToolPolicy, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one mode is required")
	}
	var out []engine.ToolPolicy
	for _, value := range values {
		policy, err := engine.ParseToolPolicy(value)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, policy) {
			return nil, fmt.Errorf("mode %q is listed twice", policy)
		}
		out = append(out, policy)
	}
	return out, nil
}

func policyStrings(modes []engine.ToolPolicy) []string {
	out := make([]string, len(modes))
	for i, mode := range modes {
		out[i] = mode.String()
	}
	return out
}

func uniqueEngineIDs(units []WorkUnit) []string {
	var ids []string
	for _, unit := range units {
		if !slices.Contains(ids, unit.EngineID) {
			ids = append(ids, unit.EngineID)
		}
	}
	return ids
}

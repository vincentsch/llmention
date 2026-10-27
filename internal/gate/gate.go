// Package gate runs the --fail-under checks on a scored run, so a scheduled
// job fails when your numbers drop. The docs and messages call a gate a check.
//
// The short form covers the usual cases:
//
//	mention-rate=40%                    every engine and mode
//	share-of-voice[engine=chatgpt]=25%  one engine
//	citation-rate[mode=search]=10%      one mode
//
// The long form reaches any metric in the full table, for example
// metric:sources.owned_domain_citation_rate[level=suite_mode_engine,engine=chatgpt,mode=search_auto]=10%.
//
// A check passes only when every engine and mode it covers has a number at or
// above the threshold. If one of them has no number, for example because
// every request failed, the check is inconclusive. That fails the run like a
// miss does, so a broken engine never looks like a pass.
package gate

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/vincentsch/llmention/internal/analysis"
	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/evaluate"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/suite"
	"github.com/vincentsch/llmention/internal/visibility"
)

// The short check names and the words the messages use for them.
var shortNames = map[string]string{
	"mention-rate":   "mention rate",
	"share-of-voice": "share of voice",
	"citation-rate":  "citation rate",
}

var shortFilters = []string{"engine", "mode"}

var metricFilters = []string{"level", "suite", "mode", "mode_from", "mode_to", "engine", "item", "variant", "tag", "entity_type", "entity", "domain", "source_channel"}

// Check results, saved with the run.
const (
	StatusPass         = "pass"
	StatusFail         = "fail"
	StatusInconclusive = "inconclusive"
	StatusError        = "error"
)

// Gate is one parsed --fail-under value.
type Gate struct {
	Raw       string
	Name      string // short name, or empty for a metric: check
	MetricID  string
	Filters   map[string]string
	Threshold float64
}

// Parse reads one --fail-under value.
func Parse(raw string) (Gate, error) {
	g := Gate{Raw: strings.TrimSpace(raw), Filters: map[string]string{}}
	text := g.Raw
	eq := strings.LastIndex(text, "=")
	if eq <= 0 || (strings.Contains(text, "[") && eq < strings.LastIndex(text, "]")) {
		return Gate{}, fmt.Errorf("%q needs a threshold, for example mention-rate=40%%", raw)
	}
	head, value := strings.TrimSpace(text[:eq]), strings.TrimSpace(text[eq+1:])
	name := head
	if open := strings.Index(head, "["); open >= 0 {
		if !strings.HasSuffix(head, "]") {
			return Gate{}, fmt.Errorf("%q has a [ but no closing ]", raw)
		}
		name = strings.TrimSpace(head[:open])
		if err := parseFilters(head[open+1:len(head)-1], g.Filters); err != nil {
			return Gate{}, fmt.Errorf("%q: %w", raw, err)
		}
	}
	allowed := shortFilters
	if id, ok := strings.CutPrefix(name, "metric:"); ok {
		g.MetricID = strings.TrimSpace(id)
		allowed = metricFilters
		if g.MetricID == "" {
			return Gate{}, fmt.Errorf("%q names no metric; write it as metric:<id>", raw)
		}
		if strings.Contains(g.MetricID, "rank") {
			return Gate{}, fmt.Errorf("%q: a lower rank is better, so --fail-under cannot check %s; check mention-rate instead", raw, g.MetricID)
		}
	} else if _, ok := shortNames[name]; ok {
		g.Name = name
	} else {
		return Gate{}, fmt.Errorf("unknown check %q; use mention-rate, share-of-voice, citation-rate or metric:<id>[filters]", name)
	}
	for key := range g.Filters {
		if !slices.Contains(allowed, key) {
			return Gate{}, fmt.Errorf("%q: unknown filter %q; this check takes %s", raw, key, strings.Join(allowed, ", "))
		}
	}
	if mode, ok := g.Filters["mode"]; ok {
		canonical, err := config.CanonicalMode(mode)
		if err != nil {
			return Gate{}, fmt.Errorf("%q: %w", raw, err)
		}
		g.Filters["mode"] = canonical
	}
	threshold, percent, err := parseThreshold(value)
	if err != nil {
		return Gate{}, fmt.Errorf("%q: %w", raw, err)
	}
	if g.Name != "" || percent {
		if threshold < 0 || threshold > 1 {
			return Gate{}, fmt.Errorf("%q: a rate threshold must be between 0%% and 100%%, written as 40%% or 0.4", raw)
		}
	}
	g.Threshold = threshold
	return g, nil
}

// ParseAll reads every --fail-under value.
func ParseAll(raw []string) ([]Gate, error) {
	var out []Gate
	for _, r := range raw {
		g, err := Parse(r)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func parseThreshold(value string) (float64, bool, error) {
	percent := strings.HasSuffix(value, "%")
	number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false, fmt.Errorf("the threshold %q is not a number", value)
	}
	if percent {
		number /= 100
	}
	return number, percent, nil
}

func parseFilters(text string, into map[string]string) error {
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"`)
		if !ok || key == "" || value == "" {
			return fmt.Errorf("filter %q must look like key=value", part)
		}
		if _, dup := into[key]; dup {
			return fmt.Errorf("filter %q is given twice", key)
		}
		into[key] = value
	}
	return nil
}

// Planned describes a run before it starts, so the checks can be tested
// before anything is sent.
type Planned struct {
	Suites  []*suite.Suite
	Engines []string
	Modes   []string
}

// Check finds checks that could never match before any request is paid for.
// That happens with an engine or mode the run does not ask, or a metric that
// no suite in the run reports.
func Check(gates []Gate, p Planned) error {
	for _, g := range gates {
		if e, ok := g.Filters["engine"]; ok && !slices.Contains(p.Engines, e) {
			return fmt.Errorf("check %q: engine %q is not part of this run (engines: %s)", g.Raw, e, strings.Join(p.Engines, ", "))
		}
		if m, ok := g.Filters["mode"]; ok && !slices.Contains(p.Modes, m) {
			var labels []string
			for _, mode := range p.Modes {
				labels = append(labels, config.ModeLabel(mode))
			}
			return fmt.Errorf("check %q: this run has no %s mode; its modes are %s", g.Raw, config.ModeLabel(m), config.JoinAnd(labels))
		}
		// Only answers with web search cite pages, so a citation-rate check
		// without a search mode could never pass. Say so before the run.
		if g.Name == "citation-rate" {
			if m, ok := g.Filters["mode"]; ok && !config.IsSearchMode(m) {
				return fmt.Errorf("check %q: only answers with web search cite sites, so use a web search mode such as mode=search", g.Raw)
			}
			if !slices.ContainsFunc(p.Modes, config.IsSearchMode) {
				return fmt.Errorf("check %q: only answers with web search cite sites, and this run asks without web search", g.Raw)
			}
		}
		if g.Name != "" {
			if !slices.ContainsFunc(p.Suites, func(s *suite.Suite) bool { return s.Type == "visibility" }) {
				return fmt.Errorf("check %q counts mentions, so the run needs your prompts; add prompts to llmention.yaml, or leave out --suite", g.Raw)
			}
			continue
		}
		declared := false
		for _, s := range p.Suites {
			if s.ID != g.Filters["suite"] && g.Filters["suite"] != "" {
				continue
			}
			declared = declared || slices.ContainsFunc(s.Metrics, func(m suite.MetricDeclaration) bool { return m.ID == g.MetricID })
		}
		if !declared {
			return fmt.Errorf("check %q: no suite in this run reports %s; see the metric ids in `llmention report --details`", g.Raw, g.MetricID)
		}
		if err := checkOneNumber(g, p); err != nil {
			return err
		}
	}
	return nil
}

// checkOneNumber refuses a metric: check that could match several numbers.
// Otherwise the run would be paid for and the check could still not decide.
func checkOneNumber(g Gate, p Planned) error {
	level := g.Filters["level"]
	if level == "" {
		return fmt.Errorf("check %q: add level=, for example level=suite_mode_engine, so it matches one number", g.Raw)
	}
	need := func(part, filter string, choices int) error {
		if strings.Contains(level, part) && g.Filters[filter] == "" && choices != 1 {
			return fmt.Errorf("check %q: level %s has one number per %s; add %s= to pick one", g.Raw, level, part, filter)
		}
		return nil
	}
	return cmp.Or(
		need("mode", "mode", len(p.Modes)),
		need("engine", "engine", len(p.Engines)),
		need("item", "item", 0),
		need("tag", "tag", 0),
		need("variant", "variant", 0),
	)
}

// Evaluate runs every check on a scored run.
func Evaluate(gates []Gate, a *analysis.Analysis) []scorecard.Gate {
	var out []scorecard.Gate
	for _, g := range gates {
		if g.Name != "" {
			out = append(out, evaluateShort(g, a.Visibility))
		} else {
			out = append(out, evaluateMetric(g, a.Metrics))
		}
	}
	return out
}

// Failed reports whether any check failed or could not decide.
func Failed(results []scorecard.Gate) bool {
	return slices.ContainsFunc(results, func(r scorecard.Gate) bool { return r.Status != StatusPass })
}

func evaluateShort(g Gate, s *visibility.Summary) scorecard.Gate {
	out := scorecard.Gate{Selector: g.Raw, Threshold: g.Threshold}
	if s == nil {
		out.Status, out.Message = StatusError, "this run has no answers to your prompts to check"
		return out
	}
	label := shortNames[g.Name]
	var worst *float64
	worstScope := ""
	var unknown []string
	covered := 0
	for _, cell := range s.Cells {
		if e, ok := g.Filters["engine"]; ok && cell.Engine != e {
			continue
		}
		if m, ok := g.Filters["mode"]; ok && cell.Mode != m {
			continue
		}
		if g.Name == "citation-rate" && !config.IsSearchMode(cell.Mode) {
			continue // answers without web search cite nothing
		}
		covered++
		scope := cell.Engine + " (" + config.ModeLabel(cell.Mode) + ")"
		value, ok := shortValue(g.Name, cell)
		if !ok {
			unknown = append(unknown, scope)
			continue
		}
		if worst == nil || value < *worst {
			v := value
			worst = &v
			worstScope = scope
		}
	}
	switch {
	case covered == 0:
		out.Status, out.Message = StatusError, "no engine and mode in this run matches this check"
		return out
	case worst != nil && *worst < g.Threshold:
		out.Value, out.Scope = worst, worstScope
		out.Status = StatusFail
		out.Message = fmt.Sprintf("%s %s for %s is below %s", label, percent(*worst), worstScope, percent(g.Threshold))
	case len(unknown) > 0:
		out.Value, out.Scope = worst, strings.Join(unknown, ", ")
		out.Status = StatusInconclusive
		out.Message = fmt.Sprintf("no %s for %s, so the check cannot pass (%s)", label, config.JoinAnd(unknown), whyUnknown(g.Name))
	default:
		out.Value, out.Scope = worst, worstScope
		out.Status = StatusPass
		out.Message = fmt.Sprintf("lowest %s is %s for %s, at least %s", label, percent(*worst), worstScope, percent(g.Threshold))
	}
	return out
}

// whyUnknown gives the usual reasons a check has no number.
func whyUnknown(name string) string {
	switch name {
	case "share-of-voice":
		return "there were no answers, or none named a brand you track"
	case "citation-rate":
		return "there were no answers, or the engine does not report the pages it cites"
	}
	return "there were no answers that count"
}

func shortValue(name string, cell visibility.Cell) (float64, bool) {
	you := cell.You()
	switch name {
	case "mention-rate":
		return you.MentionRate()
	case "share-of-voice":
		return you.SOV()
	case "citation-rate":
		return cell.CitationRate()
	}
	return 0, false
}

func evaluateMetric(g Gate, metrics []evaluate.MetricValue) scorecard.Gate {
	out := scorecard.Gate{Selector: g.Raw, Threshold: g.Threshold}
	var matches []evaluate.MetricValue
	for _, m := range metrics {
		if m.MetricID == g.MetricID && scopeMatches(m.Scope, g.Filters) {
			matches = append(matches, m)
		}
	}
	switch {
	case len(matches) == 0:
		out.Status, out.Message = StatusError, "no metric matches; check the id and filters with `llmention report --details`"
		return out
	case len(matches) > 1:
		levels := map[string]bool{}
		for _, m := range matches {
			levels[m.Scope.Level] = true
		}
		hint := "add more filters"
		if len(levels) > 1 {
			hint = "add level=suite_mode_engine or another level"
		}
		out.Status, out.Message = StatusError, fmt.Sprintf("%d metrics match; %s", len(matches), hint)
		return out
	}
	m := matches[0]
	out.Scope = m.Scope.Key
	value, ok := scalar(m.Value)
	if m.Status != evaluate.MetricStatusAvailable || !ok {
		out.Status, out.Message = StatusInconclusive, "the metric has no value in this run ("+strings.ReplaceAll(m.UnavailableReason, "_", " ")+")"
		return out
	}
	out.Value = &value
	if value < g.Threshold {
		out.Status, out.Message = StatusFail, fmt.Sprintf("%s is %g, below %g", g.MetricID, value, g.Threshold)
	} else {
		out.Status, out.Message = StatusPass, fmt.Sprintf("%s is %g, at least %g", g.MetricID, value, g.Threshold)
	}
	return out
}

func scopeMatches(s evaluate.MetricScope, filters map[string]string) bool {
	values := map[string]string{
		"level": s.Level, "suite": s.SuiteID, "mode": s.Mode, "mode_from": s.ModeFrom, "mode_to": s.ModeTo,
		"engine": s.EngineID, "item": s.ItemID, "variant": s.VariantID, "tag": s.Tag,
		"entity_type": s.EntityType, "entity": s.EntityID, "domain": s.Domain, "source_channel": s.SourceChannel,
	}
	for key, want := range filters {
		if values[key] != want {
			return false
		}
	}
	return true
}

func scalar(v evaluate.MetricScalar) (float64, bool) {
	switch {
	case v.Number != nil:
		return *v.Number, true
	case v.Integer != nil:
		return float64(*v.Integer), true
	}
	return 0, false
}

func percent(v float64) string { return strconv.FormatFloat(math.Round(v*1000)/10, 'f', -1, 64) + "%" }

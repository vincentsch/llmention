package evaluate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/run"
	"github.com/vincentsch/llmention/internal/suite"
)

func EngineCapabilitySnapshots(engines []engine.Engine) []EngineCapabilitySnapshot {
	out := make([]EngineCapabilitySnapshot, 0, len(engines))
	for _, eng := range engines {
		if eng == nil {
			continue
		}
		out = append(out, EngineCapabilitySnapshot{
			EngineID:     eng.ID(),
			Provider:     eng.Provider(),
			Model:        eng.Model(),
			Capabilities: eng.Capabilities(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EngineID < out[j].EngineID })
	return out
}

func SuiteSnapshots(entries []suite.LoadedSuite) []SuiteSnapshot {
	out := make([]SuiteSnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.Suite == nil {
			continue
		}
		mode := entry.Suite.ContextMode
		if mode == "" {
			mode = suite.ContextModeItemLocal
		}
		out = append(out, SuiteSnapshot{
			ID:          entry.Suite.ID,
			Version:     entry.Suite.Version,
			Type:        entry.Suite.Type,
			Source:      string(entry.Source),
			Path:        entry.Path,
			ContextMode: mode,
			Hash:        stableHash(entry.Suite),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func WorkKey(unit run.WorkUnit) string {
	return strings.Join([]string{
		unit.SuiteID,
		fmt.Sprintf("%d", unit.SuiteVersion),
		unit.ItemID,
		unit.VariantID,
		unit.Mode.String(),
		unit.EngineID,
		fmt.Sprintf("%d", unit.Repetition),
	}, "/")
}

func pairedWorkKey(unit run.WorkUnit) string {
	return strings.Join([]string{
		unit.SuiteID,
		fmt.Sprintf("%d", unit.SuiteVersion),
		unit.ItemID,
		unit.VariantID,
		unit.EngineID,
		fmt.Sprintf("%d", unit.Repetition),
	}, "/")
}

func suiteKey(id string, version int) string {
	return fmt.Sprintf("%s/%d", id, version)
}

func evaluatorKey(suiteID string, suiteVersion int, declarationID string) string {
	return suiteKey(suiteID, suiteVersion) + "/" + declarationID
}

func metricKey(suiteID string, suiteVersion int, metricID string) string {
	return suiteKey(suiteID, suiteVersion) + "/" + metricID
}

func declarationIDFromKey(key string) string {
	index := strings.LastIndex(key, "/")
	if index < 0 {
		return key
	}
	return key[index+1:]
}

func stableHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", value))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

func issueID(code string, parts ...string) string {
	all := append([]string{code}, parts...)
	return "issue_" + stableID(all...)
}

func resultID(runID, workKey string) string {
	return "result_" + stableID(runID, workKey)
}

func evaluationID(resultID, declarationID, evaluatorType, version string) string {
	return "eval_" + stableID(resultID, declarationID, evaluatorType, version)
}

func evidenceID(evalID, source string, start, end int, quote string) string {
	return "evidence_" + stableID(evalID, source, fmt.Sprintf("%d", start), fmt.Sprintf("%d", end), quote)
}

func metricValueID(metricID string, scope MetricScope) string {
	return metricID + "@" + scope.Key
}

func durationMillis(duration time.Duration) int64 {
	return int64(duration / time.Millisecond)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueInts(values []int) []int {
	seen := map[int]struct{}{}
	var out []int
	sort.Ints(values)
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cloneLoadedSuites(entries []suite.LoadedSuite) ([]suite.LoadedSuite, error) {
	out := make([]suite.LoadedSuite, 0, len(entries))
	for _, entry := range entries {
		if entry.Suite == nil {
			out = append(out, entry)
			continue
		}
		var copied suite.Suite
		data, err := json.Marshal(entry.Suite)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &copied); err != nil {
			return nil, err
		}
		out = append(out, suite.LoadedSuite{
			Suite:  &copied,
			Source: entry.Source,
			Path:   entry.Path,
		})
	}
	return out, nil
}

func cloneRawResult(result run.Result) run.Result {
	out := result
	if result.Answer != nil {
		answer := *result.Answer
		answer.Parsed = append([]byte(nil), result.Answer.Parsed...)
		answer.Raw = append([]byte(nil), result.Answer.Raw...)
		answer.Citations = append([]engine.Citation(nil), result.Answer.Citations...)
		answer.SourcesConsulted = append([]engine.Source(nil), result.Answer.SourcesConsulted...)
		answer.ToolCalls = append([]engine.ToolCall(nil), result.Answer.ToolCalls...)
		answer.ToolsAvailable = append([]string(nil), result.Answer.ToolsAvailable...)
		answer.ToolsUsed = append([]string(nil), result.Answer.ToolsUsed...)
		if len(result.Answer.ProviderMetadata) > 0 {
			answer.ProviderMetadata = map[string]string{}
			for key, value := range result.Answer.ProviderMetadata {
				answer.ProviderMetadata[key] = value
			}
		}
		out.Answer = &answer
	}
	return out
}

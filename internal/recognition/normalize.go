package recognition

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxAnswerBytes   = 64 * 1024
	MaxResults       = 10
	MaxNameRunes     = 160
	MaxSnippetRunes  = 320
	MaxReportSnippet = 240
)

type NormalizedText struct {
	Text                     string
	ControlCharactersChanged bool
	Truncated                bool
}

func SanitizeText(value string, maxRunes int) NormalizedText {
	changed := false
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToValidUTF8(value, " ") {
		if unicode.Is(unicode.C, r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			changed = true
			continue
		}
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	text := strings.TrimSpace(b.String())
	runes := []rune(text)
	truncated := false
	if maxRunes > 0 && len(runes) > maxRunes {
		text = string(runes[:maxRunes])
		text = strings.TrimSpace(text)
		truncated = true
	}
	return NormalizedText{Text: text, ControlCharactersChanged: changed, Truncated: truncated}
}

func NormalizeForMatch(value string) string {
	value = norm.NFKC.String(value)
	value = cases.Fold().String(value)
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimSpace(value)
}

func NormalizeConfiguredIdentity(name, typ string) (string, string) {
	return NormalizeForMatch(name), NormalizeForMatch(typ)
}

func StableResultID(workKey string, position int) string {
	return "recognition_result_" + first16hex(workKey+"\x00"+fmt.Sprintf("%d", position))
}

func StableClusterID(name, typ string) string {
	normalizedName, normalizedType := NormalizeConfiguredIdentity(name, typ)
	return "recognition_cluster_" + first16hex(normalizedName+"\x00"+normalizedType)
}

func SummaryScopeKey(suiteID string, suiteVersion int, itemID, variantID, mode string, dimensions map[string]string, comparisonGroupID string) string {
	parts := []string{
		"suite_id=" + suiteID,
		"suite_version=" + fmt.Sprintf("%d", suiteVersion),
		"item_id=" + itemID,
		"variant_id=" + variantID,
		"mode=" + mode,
		"comparison_group_id=" + comparisonGroupID,
	}
	for _, key := range sortedMapKeys(dimensions) {
		parts = append(parts, "dimension."+key+"="+dimensions[key])
	}
	return "recognition_summary_" + first16hex(strings.Join(parts, "\x00"))
}

func InputSetHash(workKeys []string) string {
	workKeys = uniqueSorted(workKeys)
	if len(workKeys) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(workKeys, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func CappedSnippet(value string, maxRunes int) (string, bool) {
	sanitized := SanitizeText(value, maxRunes)
	return sanitized.Text, sanitized.Truncated
}

func first16hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueSorted(values []string) []string {
	values = append([]string(nil), values...)
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == value {
			continue
		}
		out = append(out, value)
	}
	return out
}

func SortWarnings(values []Warning) []Warning {
	out := append([]Warning(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].FieldPath != out[j].FieldPath {
			return out[i].FieldPath < out[j].FieldPath
		}
		if out[i].ParsedResultID != out[j].ParsedResultID {
			return out[i].ParsedResultID < out[j].ParsedResultID
		}
		return out[i].ClusterID < out[j].ClusterID
	})
	return out
}

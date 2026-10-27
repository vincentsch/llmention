package benchmark

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var abstentionPhrases = []ConfiguredAnswer{
	{ID: "abstention:i-dont-know", Aliases: []string{"i don't know"}},
	{ID: "abstention:i-do-not-know", Aliases: []string{"i do not know"}},
	{ID: "abstention:unknown", Aliases: []string{"unknown"}},
	{ID: "abstention:not-sure", Aliases: []string{"not sure"}},
	{ID: "abstention:cannot-determine", Aliases: []string{"cannot determine"}},
	{ID: "abstention:cant-determine", Aliases: []string{"can't determine"}},
	{ID: "abstention:insufficient-information", Aliases: []string{"insufficient information"}},
	{ID: "abstention:i-cannot-answer", Aliases: []string{"i cannot answer"}},
	{ID: "abstention:i-cant-answer", Aliases: []string{"i can't answer"}},
}

type matchCandidate struct {
	ID    string
	Value string
	Class string
	Kind  string
}

func MatchAnswer(parsed ParsedAnswer, config MatchConfig, family string) MatchResult {
	var result MatchResult
	if parsed.Answer == nil {
		result.AnswerStatus = AnswerStatusNotAttempted
		result.MatchKind = MatchKindAbstention
		return result
	}
	candidate := NormalizeForMatch(*parsed.Answer)
	if abstention, ok := firstExactMatch(candidate, abstentionPhrases, MatchKindAbstention); ok {
		result.AnswerStatus = AnswerStatusNotAttempted
		result.MatchKind = MatchKindAbstention
		result.MatchedConfiguredID = abstention.ID
		result.MatchedConfiguredValue = abstention.Value
		return result
	}
	if canonical := NormalizeForMatch(config.Canonical); canonical != "" && normalizedEqual(candidate, canonical) {
		result.AnswerStatus = AnswerStatusCorrect
		result.MatchKind = MatchKindCanonical
		result.MatchedConfiguredID = "answer:canonical"
		result.MatchedConfiguredValue = config.Canonical
		return result
	}
	for _, alias := range config.Aliases {
		normalized := NormalizeForMatch(alias)
		if normalized != "" && normalizedEqual(candidate, normalized) {
			result.AnswerStatus = AnswerStatusCorrect
			result.MatchKind = MatchKindAlias
			result.MatchedConfiguredID = "answer:alias:" + first16hex(normalized)
			result.MatchedConfiguredValue = alias
			return result
		}
	}
	correctPhrase := configuredCorrectPhrase(config, candidate)
	failurePhrases := findConfiguredPhrases(candidate, failureCandidates(config, family))
	if correctPhrase != nil && len(failurePhrases) == 0 {
		result.AnswerStatus = AnswerStatusCorrect
		result.MatchKind = correctPhrase.Kind
		result.MatchedConfiguredID = correctPhrase.ID
		result.MatchedConfiguredValue = correctPhrase.Value
		return result
	}
	result.AnswerStatus = AnswerStatusIncorrect
	result.MatchKind = MatchKindNone
	result.FailureClass = defaultFailureClass(family)
	if parsed.Confidence != nil && *parsed.Confidence >= 80 {
		result.OverconfidentWrong = true
	}
	if len(failurePhrases) > 0 {
		sort.SliceStable(failurePhrases, func(i, j int) bool {
			if failurePhrases[i].ID != failurePhrases[j].ID {
				return failurePhrases[i].ID < failurePhrases[j].ID
			}
			return len(failurePhrases[i].Value) > len(failurePhrases[j].Value)
		})
		first := failurePhrases[0]
		ambiguous := false
		for _, match := range failurePhrases[1:] {
			if match.ID != first.ID {
				ambiguous = true
				break
			}
		}
		result.MatchKind = MatchKindConfiguredIncorrect
		result.MatchedConfiguredID = first.ID
		result.MatchedConfiguredValue = first.Value
		result.FailureClass = first.Class
		if ambiguous {
			result.FailureClass = defaultFailureClass(family)
			result.Warnings = append(result.Warnings, WarningAmbiguousFailureMatch)
		}
	}
	result.Warnings = uniqueSorted(result.Warnings)
	return result
}

func NormalizeForMatch(value string) string {
	sanitized, _ := SanitizeText(value)
	folded := cases.Fold().String(norm.NFKC.String(sanitized))
	folded, _ = SanitizeText(folded)
	return folded
}

func AbstentionCatalogNormalized() map[string]string {
	out := map[string]string{}
	for _, phrase := range abstentionPhrases {
		id := strings.TrimPrefix(phrase.ID, "abstention:")
		out[id] = NormalizeForMatch(phrase.Aliases[0])
	}
	return out
}

func configuredCorrectPhrase(config MatchConfig, candidate string) *matchCandidate {
	values := []matchCandidate{}
	if normalized := NormalizeForMatch(config.Canonical); normalized != "" {
		values = append(values, matchCandidate{ID: "answer:canonical", Value: config.Canonical, Kind: MatchKindCanonical})
	}
	for _, alias := range config.Aliases {
		normalized := NormalizeForMatch(alias)
		if normalized == "" {
			continue
		}
		values = append(values, matchCandidate{ID: "answer:alias:" + first16hex(normalized), Value: alias, Kind: MatchKindAlias})
	}
	var matches []matchCandidate
	for _, value := range values {
		if containsBoundedPhrase(candidate, NormalizeForMatch(value.Value)) {
			matches = append(matches, value)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if len(matches[i].Value) != len(matches[j].Value) {
			return len(matches[i].Value) > len(matches[j].Value)
		}
		return matches[i].ID < matches[j].ID
	})
	return &matches[0]
}

func failureCandidates(config MatchConfig, family string) []ConfiguredAnswer {
	var out []ConfiguredAnswer
	switch family {
	case SuiteFamilyProgramming:
		out = append(out, config.FailureCases...)
	case SuiteFamilyTemporal:
		for _, stale := range config.StaleAnswers {
			if stale.Class == "" {
				stale.Class = FailureClassStaleAnswer
			}
			out = append(out, stale)
		}
	default:
		for _, known := range config.KnownIncorrect {
			if known.Class == "" {
				known.Class = FailureClassIncorrect
			}
			out = append(out, known)
		}
	}
	return out
}

func defaultFailureClass(family string) string {
	switch family {
	case SuiteFamilyProgramming, SuiteFamilyTemporal:
		return FailureClassOtherIncorrect
	default:
		return ""
	}
}

func firstExactMatch(candidate string, phrases []ConfiguredAnswer, kind string) (matchCandidate, bool) {
	for _, phrase := range phrases {
		for _, alias := range phrase.Aliases {
			normalized := NormalizeForMatch(alias)
			if normalized == "" {
				continue
			}
			if normalizedEqual(candidate, normalized) {
				return matchCandidate{ID: phrase.ID, Value: alias, Class: phrase.Class, Kind: kind}, true
			}
		}
	}
	return matchCandidate{}, false
}

func findConfiguredPhrases(candidate string, phrases []ConfiguredAnswer) []matchCandidate {
	var out []matchCandidate
	for _, phrase := range phrases {
		class := phrase.Class
		if class == "" {
			class = FailureClassOtherIncorrect
		}
		for _, alias := range phrase.Aliases {
			normalized := NormalizeForMatch(alias)
			if normalized == "" {
				continue
			}
			if normalizedEqual(candidate, normalized) || containsBoundedPhrase(candidate, normalized) {
				out = append(out, matchCandidate{ID: phrase.ID, Value: alias, Class: class, Kind: MatchKindConfiguredIncorrect})
			}
		}
	}
	return out
}

func normalizedEqual(candidate, configured string) bool {
	if candidate == configured {
		return true
	}
	return strings.HasSuffix(candidate, ".") && strings.TrimSuffix(candidate, ".") == configured && !strings.HasSuffix(configured, ".")
}

func containsBoundedPhrase(candidate, phrase string) bool {
	if candidate == "" || phrase == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(candidate[start:], phrase)
		if idx < 0 {
			return false
		}
		byteStart := start + idx
		byteEnd := byteStart + len(phrase)
		start = byteStart + 1
		if hasTokenBoundary(candidate, byteStart, byteEnd) {
			return true
		}
	}
}

func hasTokenBoundary(value string, start, end int) bool {
	beforeOK := start == 0
	if !beforeOK {
		r, _ := utf8LastRune(value[:start])
		beforeOK = !isWordRune(r)
	}
	afterOK := end >= len(value)
	if !afterOK {
		r, _ := utf8FirstRune(value[end:])
		afterOK = !isWordRune(r)
	}
	return beforeOK && afterOK
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func utf8LastRune(value string) (rune, int) {
	return utf8.DecodeLastRuneInString(value)
}

func utf8FirstRune(value string) (rune, int) {
	return utf8.DecodeRuneInString(value)
}

func first16hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

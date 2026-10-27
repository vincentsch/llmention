package benchmark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxAnswerBytes        = 64 * 1024
	maxPlainFallbackBytes = 2 * 1024
	maxPreviewRunes       = 160
)

type parseError struct {
	reason string
}

func (e parseError) Error() string {
	return e.reason
}

func ParseAnswer(raw, confidencePolicy string) (ParsedAnswer, error) {
	if confidencePolicy == "" {
		confidencePolicy = ConfidencePolicyOptional
	}
	if len(raw) > maxAnswerBytes || !utf8.ValidString(raw) {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	if jsonIntended(trimmed) {
		return parseJSONObject(trimmed, confidencePolicy)
	}
	if len([]byte(raw)) >= maxPlainFallbackBytes {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	if confidencePolicy == ConfidencePolicyRequired {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	sanitized, changed := SanitizeText(trimmed)
	if sanitized == "" {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	warnings := []string{WarningPlainTextFallback}
	if changed {
		warnings = append(warnings, WarningControlCharactersSanitized)
	}
	preview, truncated := previewText(sanitized)
	if truncated {
		warnings = append(warnings, WarningAnswerTruncated)
	}
	warnings = uniqueSorted(warnings)
	return ParsedAnswer{
		Answer:                 stringPtr(sanitized),
		ParseStatus:            ParseStatusPlainTextFallback,
		SanitizedAnswer:        sanitized,
		SanitizedAnswerPreview: preview,
		AnswerPreviewTruncated: truncated,
		Warnings:               warnings,
	}, nil
}

func parseJSONObject(raw, confidencePolicy string) (ParsedAnswer, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		key, ok := tok.(string)
		if !ok {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		if key != "answer" && key != "confidence" {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		if _, dup := fields[key]; dup {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		fields[key] = value
	}
	tok, err = dec.Token()
	if err != nil {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	delim, ok = tok.(json.Delim)
	if !ok || delim != '}' {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	answerRaw, ok := fields["answer"]
	if !ok {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	out := ParsedAnswer{ParseStatus: ParseStatusParsed}
	var warnings []string
	if bytes.Equal(bytes.TrimSpace(answerRaw), []byte("null")) {
		out.Answer = nil
	} else {
		var answer string
		if err := json.Unmarshal(answerRaw, &answer); err != nil {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		sanitized, changed := SanitizeText(answer)
		if sanitized == "" {
			return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
		}
		if changed {
			warnings = append(warnings, WarningControlCharactersSanitized)
		}
		out.Answer = stringPtr(sanitized)
		out.SanitizedAnswer = sanitized
		out.SanitizedAnswerPreview, out.AnswerPreviewTruncated = previewText(sanitized)
		if out.AnswerPreviewTruncated {
			warnings = append(warnings, WarningAnswerTruncated)
		}
	}
	confidence, confidenceWarnings, err := parseConfidence(fields["confidence"], confidencePolicy)
	if err != nil {
		return ParsedAnswer{ParseStatus: ParseStatusError}, parseError{reason: ReasonExactAnswerParseError}
	}
	out.Confidence = confidence
	warnings = append(warnings, confidenceWarnings...)
	out.Warnings = uniqueSorted(warnings)
	return out, nil
}

func parseConfidence(raw json.RawMessage, policy string) (*int, []string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if policy == ConfidencePolicyRequired {
			return nil, nil, fmt.Errorf("confidence required")
		}
		return nil, nil, nil
	}
	if policy == ConfidencePolicyNone {
		return nil, nil, fmt.Errorf("confidence not allowed")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, nil, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, nil, fmt.Errorf("confidence must be an integer")
	}
	integer, err := number.Int64()
	if err != nil {
		return nil, nil, err
	}
	warnings := []string{}
	if integer < 0 {
		integer = 0
		warnings = append(warnings, WarningConfidenceClamped)
	}
	if integer > 100 {
		integer = 100
		warnings = append(warnings, WarningConfidenceClamped)
	}
	out := int(integer)
	return &out, warnings, nil
}

func jsonIntended(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	switch trimmed[0] {
	case '{', '[', '"':
		return true
	}
	switch trimmed {
	case "null", "true", "false":
		return true
	default:
		return false
	}
}

func SanitizeText(value string) (string, bool) {
	var b strings.Builder
	changed := false
	lastSpace := true
	for _, r := range value {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			r = ' '
			changed = true
		}
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
			}
			lastSpace = true
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	out := strings.TrimSpace(b.String())
	return out, changed || out != strings.TrimSpace(value)
}

func previewText(value string) (string, bool) {
	runes := []rune(value)
	if len(runes) <= maxPreviewRunes {
		return value, false
	}
	return string(runes[:maxPreviewRunes]), true
}

func uniqueSorted(values []string) []string {
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
	sort.Strings(out)
	return out
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

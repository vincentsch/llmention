package recognition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

type parseFailure struct {
	stage       string
	fieldPath   string
	messageCode string
	message     string
}

func (e parseFailure) Error() string {
	if e.message != "" {
		return e.message
	}
	return e.messageCode
}

func (e parseFailure) Safe() ParseError {
	return ParseError{
		ReasonCode:  ReasonRecognitionParseError,
		ParserStage: e.stage,
		FieldPath:   e.fieldPath,
		MessageCode: e.messageCode,
	}
}

func SafeParseError(err error) ParseError {
	var failure parseFailure
	if asParseFailure(err, &failure) {
		return failure.Safe()
	}
	return ParseError{
		ReasonCode:  ReasonRecognitionParseError,
		ParserStage: "json_decode",
		FieldPath:   "$",
		MessageCode: "invalid_json",
	}
}

func ParseAnswer(answerText, workKey string) (ParseOutput, error) {
	if len(answerText) > MaxAnswerBytes {
		return ParseOutput{}, parseFailure{stage: "pre_decode", fieldPath: "$", messageCode: "input_too_large", message: "recognition answer exceeds 64 KiB"}
	}
	root, err := decodeStrictJSON([]byte(answerText))
	if err != nil {
		return ParseOutput{}, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return ParseOutput{}, parseFailure{stage: "schema", fieldPath: "$", messageCode: "root_not_object"}
	}
	if len(obj) != 1 {
		for key := range obj {
			if key != "results" {
				return ParseOutput{}, parseFailure{stage: "schema", fieldPath: "$." + key, messageCode: "unknown_field"}
			}
		}
	}
	rawResults, ok := obj["results"]
	if !ok {
		return ParseOutput{}, parseFailure{stage: "schema", fieldPath: "$.results", messageCode: "missing_field"}
	}
	resultsArray, ok := rawResults.([]any)
	if !ok {
		return ParseOutput{}, parseFailure{stage: "schema", fieldPath: "$.results", messageCode: "results_not_array"}
	}
	if len(resultsArray) > MaxResults {
		return ParseOutput{}, parseFailure{stage: "schema", fieldPath: "$.results", messageCode: "too_many_results"}
	}

	out := ParseOutput{Results: make([]ParsedResult, 0, len(resultsArray))}
	seenRanks := map[int]struct{}{}
	for i, raw := range resultsArray {
		path := fmt.Sprintf("$.results[%d]", i)
		row, ok := raw.(map[string]any)
		if !ok {
			return ParseOutput{}, parseFailure{stage: "schema", fieldPath: path, messageCode: "result_not_object"}
		}
		parsed, warnings, err := parseResult(row, workKey, i, path)
		if err != nil {
			return ParseOutput{}, err
		}
		if _, dup := seenRanks[parsed.Rank]; dup {
			warning := Warning{Code: WarningDuplicateRank, FieldPath: path + ".rank", ParsedResultID: parsed.ParsedResultID}
			warnings = append(warnings, warning)
			parsed.Warnings = append(parsed.Warnings, WarningDuplicateRank)
		}
		seenRanks[parsed.Rank] = struct{}{}
		parsed.Warnings = uniqueSorted(parsed.Warnings)
		out.Results = append(out.Results, parsed)
		out.Warnings = append(out.Warnings, warnings...)
	}
	out.Warnings = SortWarnings(out.Warnings)
	return out, nil
}

func parseResult(row map[string]any, workKey string, position int, path string) (ParsedResult, []Warning, error) {
	required := []string{"rank", "name", "type", "confidence", "snippet"}
	for _, key := range required {
		if _, ok := row[key]; !ok {
			return ParsedResult{}, nil, parseFailure{stage: "schema", fieldPath: path + "." + key, messageCode: "missing_field"}
		}
	}
	for key := range row {
		switch key {
		case "rank", "name", "type", "confidence", "snippet":
		default:
			return ParsedResult{}, nil, parseFailure{stage: "schema", fieldPath: path + "." + key, messageCode: "unknown_field"}
		}
	}
	id := StableResultID(workKey, position)
	var warnings []Warning
	var rowWarnings []string

	rank, err := integerNumber(row["rank"], 1, maxNativeInt64())
	if err != nil {
		return ParsedResult{}, nil, parseFailure{stage: "schema", fieldPath: path + ".rank", messageCode: err.Error()}
	}
	name, fieldWarnings, err := stringField(row["name"], MaxNameRunes, path+".name", WarningNameTruncated, id)
	if err != nil {
		return ParsedResult{}, nil, err
	}
	if len(fieldWarnings) > 0 {
		warnings = append(warnings, fieldWarnings...)
		for _, warning := range fieldWarnings {
			rowWarnings = append(rowWarnings, warning.Code)
		}
	}
	typ, ok := row["type"].(string)
	if !ok {
		return ParsedResult{}, nil, parseFailure{stage: "schema", fieldPath: path + ".type", messageCode: "type_not_string"}
	}
	typ = strings.TrimSpace(typ)
	if !KnownEntityType(typ) {
		typ = EntityTypeUnknown
		warnings = append(warnings, Warning{Code: WarningUnknownEntityType, FieldPath: path + ".type", ParsedResultID: id})
		rowWarnings = append(rowWarnings, WarningUnknownEntityType)
	}
	confidence, err := integerNumber(row["confidence"], minNativeInt64(), maxNativeInt64())
	if err != nil {
		return ParsedResult{}, nil, parseFailure{stage: "schema", fieldPath: path + ".confidence", messageCode: err.Error()}
	}
	if confidence < 0 {
		confidence = 0
		warnings = append(warnings, Warning{Code: WarningConfidenceClamped, FieldPath: path + ".confidence", ParsedResultID: id})
		rowWarnings = append(rowWarnings, WarningConfidenceClamped)
	}
	if confidence > 100 {
		confidence = 100
		warnings = append(warnings, Warning{Code: WarningConfidenceClamped, FieldPath: path + ".confidence", ParsedResultID: id})
		rowWarnings = append(rowWarnings, WarningConfidenceClamped)
	}
	snippet, fieldWarnings, err := stringField(row["snippet"], MaxSnippetRunes, path+".snippet", WarningSnippetTruncated, id)
	if err != nil {
		return ParsedResult{}, nil, err
	}
	if len(fieldWarnings) > 0 {
		warnings = append(warnings, fieldWarnings...)
		for _, warning := range fieldWarnings {
			rowWarnings = append(rowWarnings, warning.Code)
		}
	}
	for _, field := range []struct {
		value string
		path  string
	}{
		{name, path + ".name"},
		{snippet, path + ".snippet"},
	} {
		if field.value == "" {
			return ParsedResult{}, nil, parseFailure{stage: "sanitize", fieldPath: field.path, messageCode: "empty_string"}
		}
	}
	return ParsedResult{
		ParsedResultID: id,
		Position:       position,
		Rank:           rank,
		Name:           name,
		Type:           typ,
		Confidence:     confidence,
		Snippet:        snippet,
		Warnings:       rowWarnings,
	}, warnings, nil
}

func stringField(raw any, maxRunes int, path, truncateWarning, parsedID string) (string, []Warning, error) {
	value, ok := raw.(string)
	if !ok {
		return "", nil, parseFailure{stage: "schema", fieldPath: path, messageCode: "string_required"}
	}
	sanitized := SanitizeText(value, maxRunes)
	var warnings []Warning
	if sanitized.ControlCharactersChanged {
		warnings = append(warnings, Warning{Code: WarningControlCharactersSanitized, FieldPath: path, ParsedResultID: parsedID})
	}
	if sanitized.Truncated {
		warnings = append(warnings, Warning{Code: truncateWarning, FieldPath: path, ParsedResultID: parsedID})
	}
	return sanitized.Text, warnings, nil
}

func integerNumber(raw any, min, max int64) (int, error) {
	number, ok := raw.(json.Number)
	if !ok {
		return 0, fmt.Errorf("number_required")
	}
	value, err := number.Int64()
	if err != nil {
		if f, ferr := number.Float64(); ferr == nil && math.Trunc(f) != f {
			return 0, fmt.Errorf("integer_required")
		}
		return 0, fmt.Errorf("integer_required")
	}
	if value < min {
		return 0, fmt.Errorf("number_too_small")
	}
	if value > max {
		return 0, fmt.Errorf("number_too_large")
	}
	return int(value), nil
}

func maxNativeInt64() int64 {
	return int64(^uint(0) >> 1)
}

func minNativeInt64() int64 {
	return -maxNativeInt64() - 1
}

func decodeStrictJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := parseJSONValue(dec, "$")
	if err != nil {
		return nil, err
	}
	if token, err := dec.Token(); err != io.EOF {
		if err != nil {
			return nil, parseFailure{stage: "json_decode", fieldPath: "$", messageCode: "invalid_json", message: err.Error()}
		}
		return nil, parseFailure{stage: "json_decode", fieldPath: "$", messageCode: "trailing_tokens", message: fmt.Sprintf("trailing token %v", token)}
	}
	return value, nil
}

func parseJSONValue(dec *json.Decoder, path string) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "invalid_json", message: err.Error()}
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]any{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "invalid_json", message: err.Error()}
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "object_key_not_string"}
				}
				if _, dup := obj[key]; dup {
					return nil, parseFailure{stage: "json_decode", fieldPath: path + "." + key, messageCode: "duplicate_key"}
				}
				child, err := parseJSONValue(dec, path+"."+key)
				if err != nil {
					return nil, err
				}
				obj[key] = child
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "invalid_json"}
			}
			return obj, nil
		case '[':
			var arr []any
			index := 0
			for dec.More() {
				child, err := parseJSONValue(dec, fmt.Sprintf("%s[%d]", path, index))
				if err != nil {
					return nil, err
				}
				arr = append(arr, child)
				index++
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "invalid_json"}
			}
			return arr, nil
		default:
			return nil, parseFailure{stage: "json_decode", fieldPath: path, messageCode: "invalid_json"}
		}
	default:
		return t, nil
	}
}

func asParseFailure(err error, out *parseFailure) bool {
	if err == nil {
		return false
	}
	if failure, ok := err.(parseFailure); ok {
		*out = failure
		return true
	}
	return false
}

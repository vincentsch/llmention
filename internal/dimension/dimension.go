// Package dimension cleans up the optional labels a suite item can carry,
// such as its market or persona, so items can be grouped and compared.
package dimension

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	Market     = "market"
	Region     = "region"
	Locale     = "locale"
	Language   = "language"
	Persona    = "persona"
	BuyerStage = "buyer_stage"
)

var (
	keys = []string{Market, Region, Locale, Language, Persona, BuyerStage}

	languagePattern          = regexp.MustCompile(`^[a-z]{2,3}$`)
	localePattern            = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	slugPattern              = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	comparisonGroupIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
)

type Filters map[string][]string

type Scope struct {
	Kind             string              `json:"kind"`
	Dimensions       map[string]string   `json:"dimensions"`
	CombinedValues   map[string][]string `json:"combined_values"`
	IncludesMissing  map[string]bool     `json:"includes_missing"`
	IncludesUnscoped bool                `json:"includes_unscoped"`
}

func Keys() []string {
	return append([]string(nil), keys...)
}

func IsKey(key string) bool {
	switch key {
	case Market, Region, Locale, Language, Persona, BuyerStage:
		return true
	default:
		return false
	}
}

func NormalizeMap(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for rawKey, rawValue := range in {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			return nil, fmt.Errorf("dimension key must not be empty")
		}
		if !IsKey(key) {
			return nil, fmt.Errorf("unknown dimension %q; use one of %s", rawKey, strings.Join(keys, ", "))
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("dimension %q is given twice", key)
		}
		value, err := NormalizeValue(key, rawValue)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	if language := out[Language]; language != "" {
		if locale := out[Locale]; locale != "" && !strings.HasPrefix(locale, language+"-") && locale != language {
			return nil, fmt.Errorf("dimension language %q does not match locale %q", language, locale)
		}
	}
	return out, nil
}

func NormalizeValue(key, value string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("dimension %q must not be empty", key)
	}
	switch key {
	case Market, Region, Persona, BuyerStage:
		normalized := strings.ToLower(value)
		if !slugPattern.MatchString(normalized) {
			return "", fmt.Errorf("dimension %s %q must start with a letter or digit and may only use letters, digits, dashes and underscores (63 characters at most)", key, value)
		}
		return normalized, nil
	case Language:
		normalized := strings.ToLower(value)
		if !languagePattern.MatchString(normalized) {
			return "", fmt.Errorf("dimension language %q is not a two- or three-letter language code such as en", value)
		}
		return normalized, nil
	case Locale:
		normalized, err := CanonicalLocale(value)
		if err != nil {
			return "", fmt.Errorf("dimension locale %q is not a locale such as en-US", value)
		}
		return normalized, nil
	default:
		return "", fmt.Errorf("unknown dimension %q; use one of %s", key, strings.Join(keys, ", "))
	}
}

func CanonicalLocale(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
	if value == "" {
		return "", fmt.Errorf("locale must not be empty")
	}
	parts := strings.Split(value, "-")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("a locale part is empty")
		}
		switch {
		case i == 0:
			parts[i] = strings.ToLower(part)
		case len(part) == 2:
			parts[i] = strings.ToUpper(part)
		case len(part) == 4:
			parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		default:
			parts[i] = strings.ToLower(part)
		}
	}
	normalized := strings.Join(parts, "-")
	if !localePattern.MatchString(normalized) {
		return "", fmt.Errorf("the locale is not valid")
	}
	return normalized, nil
}

func NormalizeComparisonGroupID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if !comparisonGroupIDPattern.MatchString(value) {
		return "", fmt.Errorf("comparison_group_id %q must start with a letter or digit and may only use letters, digits, dots, colons, dashes and underscores (128 characters at most)", value)
	}
	return value, nil
}

func Map(in map[string]string) map[string]string {
	normalized, err := NormalizeMap(in)
	if err != nil {
		out := make(map[string]string, len(in))
		for key, value := range in {
			out[key] = value
		}
		return out
	}
	return normalized
}

func Equal(a, b map[string]string) bool {
	am := Map(a)
	bm := Map(b)
	if len(am) != len(bm) {
		return false
	}
	for key, value := range am {
		if bm[key] != value {
			return false
		}
	}
	return true
}

func TupleKey(in map[string]string) string {
	dims := Map(in)
	if len(dims) == 0 {
		return ""
	}
	parts := make([]string, 0, len(dims))
	for _, key := range keys {
		if value := dims[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, "|")
}

func Label(in map[string]string) string {
	dims := Map(in)
	if len(dims) == 0 {
		return "unscoped"
	}
	return strings.Join(Pairs(dims), ", ")
}

func Pairs(in map[string]string) []string {
	dims := Map(in)
	out := make([]string, 0, len(dims))
	for _, key := range keys {
		if value := dims[key]; value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func NormalizeFilters(raw map[string][]string) (Filters, error) {
	out := Filters{}
	for rawKey, values := range raw {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			return nil, fmt.Errorf("dimension filter key must not be empty")
		}
		if !IsKey(key) {
			return nil, fmt.Errorf("unknown dimension filter %q; use one of %s", rawKey, strings.Join(keys, ", "))
		}
		seen := map[string]struct{}{}
		for _, rawValue := range values {
			value, err := NormalizeValue(key, rawValue)
			if err != nil {
				return nil, fmt.Errorf("dimension filter %s: %w", key, err)
			}
			if _, dup := seen[value]; dup {
				continue
			}
			seen[value] = struct{}{}
			out[key] = append(out[key], value)
		}
		sort.Strings(out[key])
	}
	if len(out) == 0 {
		return Filters{}, nil
	}
	return out, nil
}

func CloneFilters(filters Filters) Filters {
	out := Filters{}
	for key, values := range filters {
		if len(values) == 0 {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func Match(filters Filters, dims map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	normalized := Map(dims)
	for key, allowed := range filters {
		value := normalized[key]
		if value == "" {
			return false
		}
		if !contains(allowed, value) {
			return false
		}
	}
	return true
}

func ExactScope(dims map[string]string) Scope {
	normalized := Map(dims)
	kind := "exact"
	includesUnscoped := false
	if len(normalized) == 0 {
		kind = "unscoped"
		includesUnscoped = true
	}
	return Scope{
		Kind:             kind,
		Dimensions:       normalized,
		CombinedValues:   map[string][]string{},
		IncludesMissing:  map[string]bool{},
		IncludesUnscoped: includesUnscoped,
	}
}

func AggregateScope(maps []map[string]string) Scope {
	combined := map[string]map[string]struct{}{}
	missing := map[string]bool{}
	includesUnscoped := false
	for _, dims := range maps {
		normalized := Map(dims)
		if len(normalized) == 0 {
			includesUnscoped = true
		}
		for _, key := range keys {
			if normalized[key] == "" {
				missing[key] = true
				continue
			}
			if combined[key] == nil {
				combined[key] = map[string]struct{}{}
			}
			combined[key][normalized[key]] = struct{}{}
		}
	}
	scopeDims := map[string]string{}
	combinedValues := map[string][]string{}
	for _, key := range keys {
		values := setToSortedSlice(combined[key])
		if len(values) == 0 {
			continue
		}
		combinedValues[key] = values
		if len(values) == 1 && !missing[key] {
			scopeDims[key] = values[0]
		}
	}
	for _, key := range keys {
		if !missing[key] {
			delete(missing, key)
		}
	}
	return Scope{
		Kind:             "aggregate",
		Dimensions:       scopeDims,
		CombinedValues:   combinedValues,
		IncludesMissing:  missing,
		IncludesUnscoped: includesUnscoped,
	}
}

func ScopeLabel(scope Scope) string {
	switch scope.Kind {
	case "aggregate":
		var parts []string
		for _, key := range keys {
			values := append([]string(nil), scope.CombinedValues[key]...)
			if len(values) == 0 {
				if scope.IncludesMissing[key] {
					parts = append(parts, key+"=<missing>")
				}
				continue
			}
			if len(values) == 1 && !scope.IncludesMissing[key] {
				parts = append(parts, key+"="+values[0])
				continue
			}
			if scope.IncludesMissing[key] {
				values = append(values, "<missing>")
			}
			parts = append(parts, key+"=["+strings.Join(values, ",")+"]")
		}
		if len(parts) == 0 {
			return "all dimensions"
		}
		return strings.Join(parts, ", ")
	default:
		return Label(scope.Dimensions)
	}
}

func FamilyTuple(dims map[string]string, family []string) map[string]string {
	normalized := Map(dims)
	out := map[string]string{}
	for _, key := range family {
		if value := normalized[key]; value != "" {
			out[key] = value
		}
	}
	return out
}

func OutsideFamily(dims map[string]string, family []string) map[string]string {
	normalized := Map(dims)
	familySet := map[string]struct{}{}
	for _, key := range family {
		familySet[key] = struct{}{}
	}
	out := map[string]string{}
	for _, key := range keys {
		if _, skip := familySet[key]; skip {
			continue
		}
		if value := normalized[key]; value != "" {
			out[key] = value
		}
	}
	return out
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func setToSortedSlice(in map[string]struct{}) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for value := range in {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

package config

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var unknownField = regexp.MustCompile(`^(line \d+): field (\S+) not found in type config\.(\w+)$`)

// unclosedList is the error for a prompt list in square brackets that holds
// an unquoted question mark. The YAML library counts this line from zero and
// leaves it out for the first line.
var unclosedList = regexp.MustCompile(`^yaml: (?:line (\d+): )?did not find expected ',' or '\]'$`)

// sections names the part of llmention.yaml each Go type stands for, so an
// error can say "brand has no field domain" instead of naming a Go type.
var sections = map[string]any{
	"projectFile":      projectFile{},
	"BrandConfig":      BrandConfig{},
	"CompetitorConfig": CompetitorConfig{},
	"EngineConfig":     EngineConfig{},
	"plain":            Prompt{},
	"executionFile":    executionFile{},
	"AnalysisConfig":   AnalysisConfig{},
	"JudgeConfig":      JudgeConfig{},
	"SuiteConfig":      SuiteConfig{},
	"NotifyConfig":     NotifyConfig{},
}

var sectionNames = map[string]string{
	"projectFile": "the top level", "BrandConfig": "brand", "CompetitorConfig": "a competitor",
	"EngineConfig": "an engine", "plain": "a prompt", "executionFile": "execution",
	"AnalysisConfig": "analysis", "JudgeConfig": "analysis.judge", "SuiteConfig": "suites", "NotifyConfig": "notify",
}

// friendlyYAMLError rewrites "field domain not found in type
// config.BrandConfig" as "line 4: brand has no field "domain"; did you mean
// domains?".
func friendlyYAMLError(err error) error {
	if m := unclosedList.FindStringSubmatch(err.Error()); m != nil {
		line, _ := strconv.Atoi(m[1])
		return fmt.Errorf("line %d: did not find expected ',' or ']'. In a list in square brackets, put quotes around each prompt with a question mark, or write one prompt per \"- \" line", line+1)
	}
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err
	}
	var lines []string
	for _, msg := range typeErr.Errors {
		m := unknownField.FindStringSubmatch(msg)
		if m == nil {
			lines = append(lines, msg)
			continue
		}
		where, field, typ := m[1], m[2], m[3]
		section := sectionNames[typ]
		if section == "" {
			section = "this part"
		}
		line := where + ": " + section + " has no field \"" + field + "\""
		if guess := closestField(sections[typ], field); guess != "" {
			line += "; did you mean " + guess + "?"
		}
		lines = append(lines, line)
	}
	return errors.New(strings.Join(lines, "\n"))
}

// closestField suggests the known field nearest to a typo, if one is close.
func closestField(v any, field string) string {
	if v == nil {
		return ""
	}
	best, bestDist := "", 3
	t := reflect.TypeOf(v)
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		if d := editDistance(field, name); d < bestDist || strings.HasPrefix(name, field) && bestDist > 1 {
			best, bestDist = name, min(d, bestDist)
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

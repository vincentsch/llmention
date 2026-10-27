// Package discover suggests buyer questions to use as prompts. Without an
// account it fills question templates with your topics. With a Chab API key
// it asks Chab for the questions people search for and how often they search
// them.
package discover

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/mention"
)

// Candidate is one suggested prompt.
type Candidate struct {
	Prompt string `json:"prompt"`
	// Source says where it came from: template, people_also_ask,
	// search_result or keyword_idea.
	Source string `json:"source"`
	// Seed is the topic or search query that produced it.
	Seed string `json:"seed"`
	// MonthlySearches is the search volume of the phrase behind the prompt,
	// when Chab reported one.
	MonthlySearches *int `json:"monthly_searches,omitempty"`
}

// FromTemplates writes starter questions for each topic.
func FromTemplates(topics []string, competitors []string) []Candidate {
	var out []Candidate
	for i, topic := range topics {
		var names []string
		if i == 0 {
			names = competitors
		}
		for _, q := range config.StarterPrompts(topic, names) {
			out = append(out, Candidate{Prompt: q, Source: "template", Seed: topic})
		}
	}
	return out
}

// Clean drops prompts that name your brand and prompts you already have,
// including repeats within the list. It sorts the rest by search volume and
// returns how many it dropped for naming the brand.
func Clean(candidates []Candidate, brand mention.Brand, existing []string) ([]Candidate, int) {
	seen := map[string]bool{}
	for _, e := range existing {
		seen[key(e)] = true
	}
	namesBrand := 0
	var out []Candidate
	for _, c := range candidates {
		c.Prompt = tidy(c.Prompt)
		if c.Prompt == "" || seen[key(c.Prompt)] {
			continue
		}
		if brand.Name != "" && mention.Contains(c.Prompt, brand) {
			namesBrand++
			continue
		}
		seen[key(c.Prompt)] = true
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b Candidate) int {
		return cmp.Compare(volume(b), volume(a))
	})
	return out, namesBrand
}

func volume(c Candidate) int {
	if c.MonthlySearches == nil {
		return -1
	}
	return *c.MonthlySearches
}

func key(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

// tidy capitalizes a question and gives it a question mark.
func tidy(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	s = string(r)
	if !strings.HasSuffix(s, "?") && looksLikeQuestion(s) {
		s = strings.TrimRight(s, ".!") + "?"
	}
	return s
}

var questionStart = regexp.MustCompile(`(?i)^(what|which|who|how|why|when|where|is|are|can|should|do|does|best|top)\b`)

func looksLikeQuestion(s string) bool {
	return strings.HasSuffix(s, "?") || questionStart.MatchString(s)
}

// titleSuffix matches " - Site Name" or " | Site Name" at the end of a page title.
var titleSuffix = regexp.MustCompile(`\s+[-|–—:]\s+[^-|–—:]{1,40}$`)

// QuestionFromTitle turns a search result title such as "What is the best
// LLM cost tracker? - Example Blog" into a question, or returns "" when the
// title is not one.
func QuestionFromTitle(title string) string {
	title = strings.TrimSpace(titleSuffix.ReplaceAllString(strings.TrimSpace(title), ""))
	if !looksLikeQuestion(title) || len(strings.Fields(title)) < 4 {
		return ""
	}
	return tidy(title)
}

// QuestionFromKeyword turns a search phrase into a question a buyer could ask
// an assistant.
func QuestionFromKeyword(keyword string) string {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return ""
	}
	if looksLikeQuestion(keyword) {
		return tidy(keyword)
	}
	return "What would you recommend for " + keyword + "?"
}

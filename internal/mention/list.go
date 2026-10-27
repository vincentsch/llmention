package mention

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ListItem is one top-level entry of a Markdown list in an answer, such as
// "1. **Helicone**: hosted logging" or "- Langfuse - open source tracing".
type ListItem struct {
	Position int    // 1 for the first item
	Text     string // the whole line without the list marker
	Name     string // the product name the line starts with, or ""
}

var (
	numbered = regexp.MustCompile(`^(\s*)(\d{1,2})[.)]\s+(.+)$`)
	bulleted = regexp.MustCompile(`^(\s*)[-*•+]\s+(.+)$`)
	heading  = regexp.MustCompile(`^#{2,4}\s+(\d{1,2}[.)]\s*)?(.+)$`)
	bold     = regexp.MustCompile(`\*\*([^*]{1,80})\*\*|__([^_]{1,80})__`)
	link     = regexp.MustCompile(`^\[([^\]]{1,80})\]\([^)]*\)`)
	tableSep = regexp.MustCompile(`^\|?\s*:?-{2,}`)
)

// Kinds of list lines, strongest first.
const (
	numberedHeading = iota // ### 1. Helicone
	numberedItem           // 1. Helicone
	bulletItem             // - Helicone
	nameHeading            // ## Helicone
)

// ParseList returns the top-level list items of an answer in order. Nested
// bullets, which usually hold details such as "Pricing: ...", are left out.
//
// Answers often mix section headings with a list, as in "## Best tools"
// followed by "1. Helicone". The strongest kind of line with at least two
// entries wins: numbered headings, then numbered items, then bullets, then
// headings that look like a product name. Other headings are section labels
// and never take a place.
func ParseList(text string) []ListItem {
	type line struct {
		indent, kind int
		body         string
	}
	var lines []line
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, " \t\r")
		if m := heading.FindStringSubmatch(raw); m != nil {
			switch {
			case m[1] != "":
				lines = append(lines, line{0, numberedHeading, m[2]})
			case leadingName(m[2]) != "":
				lines = append(lines, line{0, nameHeading, m[2]})
			}
		} else if m := numbered.FindStringSubmatch(raw); m != nil {
			lines = append(lines, line{len(m[1]), numberedItem, m[3]})
		} else if m := bulleted.FindStringSubmatch(raw); m != nil {
			lines = append(lines, line{len(m[1]), bulletItem, m[2]})
		}
	}
	var fallback []ListItem
	for kind := numberedHeading; kind <= nameHeading; kind++ {
		indent := -1
		for _, l := range lines {
			if l.kind == kind && (indent < 0 || l.indent < indent) {
				indent = l.indent
			}
		}
		var items []ListItem
		for _, l := range lines {
			if l.kind == kind && l.indent == indent {
				items = append(items, ListItem{Position: len(items) + 1, Text: l.body, Name: leadingName(l.body)})
			}
		}
		if len(items) >= 2 {
			return items
		}
		if fallback == nil {
			fallback = items
		}
	}
	return fallback
}

// TableNames returns the first cell of each body row of the Markdown tables
// in text, when it looks like a name. Assistants often compare products in a
// table with one product per row.
func TableNames(text string) []string {
	var out []string
	inTable := false
	for _, raw := range strings.Split(text, "\n") {
		row := strings.TrimSpace(raw)
		if !strings.HasPrefix(row, "|") {
			inTable = false
			continue
		}
		if tableSep.MatchString(row) {
			inTable = true // the header row came before the separator
			continue
		}
		if !inTable {
			continue
		}
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if name := leadingName(cells[0]); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// leadingName pulls the product name out of a list line. It prefers bold text
// or a link label at the start, then the text before the first separator.
func leadingName(body string) string {
	body = strings.TrimSpace(body)
	var name string
	if m := bold.FindStringSubmatchIndex(body); m != nil && m[0] <= 2 {
		if m[2] >= 0 {
			name = body[m[2]:m[3]]
		} else {
			name = body[m[4]:m[5]]
		}
	} else if m := link.FindStringSubmatch(body); m != nil {
		name = m[1]
	} else {
		name = body
		for _, sep := range []string{": ", " - ", " – ", " — ", " (", ", ", ". ", " is ", " offers ", " provides "} {
			if i := strings.Index(name, sep); i > 0 {
				name = name[:i]
			}
		}
	}
	name = strings.Trim(strings.TrimSpace(name), ":.-–—*_`\"'")
	if !looksLikeName(name) {
		return ""
	}
	return name
}

// looksLikeName guesses whether a phrase is a name or a section label. A name
// has at most four words and starts with a capital letter or a digit. Each
// further word also starts with a capital, as in "Google Cloud Billing",
// while "Cost attribution" reads as a label. Labels that assistants often
// use, such as "Pro tip" or "German VAT", never count as names.
func looksLikeName(name string) bool {
	words := strings.Fields(name)
	if len(words) == 0 || len(words) > 4 || len(name) > 40 {
		return false
	}
	if !startsUpper(words[0]) || strings.HasSuffix(name, "?") {
		return false
	}
	for _, w := range words[1:] {
		if !startsUpper(w) && !connectors[w] {
			return false
		}
	}
	lower := strings.ToLower(name)
	if genericLabels[lower] || labelTails[strings.ToLower(words[len(words)-1])] && len(words) > 1 {
		return false
	}
	if len(words) > 1 && descriptors[strings.ToLower(words[0])] {
		return false
	}
	for _, prefix := range []string{"step ", "option ", "tier ", "phase ", "level ", "if ", "for ", "use ", "best ", "why ", "how ", "what ", "when ", "the best", "other ", "top "} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

func startsUpper(word string) bool {
	r, _ := utf8.DecodeRuneInString(word)
	return unicode.IsUpper(r) || unicode.IsDigit(r)
}

// connectors may stay lowercase inside a name, as in "Bank of America".
var connectors = map[string]bool{"of": true, "for": true, "and": true, "&": true, "by": true, "the": true, "on": true, "in": true, "to": true, "de": true, "-": true}

// descriptors start labels rather than names: "German VAT", "Free Plan".
var descriptors = map[string]bool{
	"german": true, "eu": true, "european": true, "us": true, "uk": true, "american": true, "british": true,
	"french": true, "canadian": true, "australian": true, "global": true, "international": true, "local": true,
	"free": true, "paid": true, "freemium": true, "premium": true, "pro": true, "bonus": true, "quick": true,
	"key": true, "main": true, "simple": true, "basic": true, "advanced": true, "automatic": true,
	"automated": true, "manual": true, "recurring": true, "monthly": true, "annual": true, "yearly": true,
	"more": true, "most": true, "my": true, "your": true, "our": true, "final": true, "overall": true,
}

// labelTails end labels: "Pro Tip", "Key Features", "Quick Answer".
var labelTails = map[string]bool{
	"tip": true, "tips": true, "note": true, "notes": true, "option": true, "options": true, "answer": true,
	"take": true, "summary": true, "verdict": true, "pick": true, "picks": true, "features": true,
	"considerations": true, "recommendation": true, "recommendations": true, "alternatives": true,
	"overview": true, "takeaway": true, "takeaways": true, "steps": true, "questions": true,
}

var genericLabels = map[string]bool{
	"pricing": true, "price": true, "cost": true, "costs": true, "pros": true, "cons": true,
	"features": true, "key features": true, "summary": true, "conclusion": true, "overview": true,
	"recommendation": true, "recommendations": true, "note": true, "notes": true, "tip": true,
	"tips": true, "example": true, "examples": true, "bottom line": true, "verdict": true,
	"use case": true, "use cases": true, "considerations": true, "alternatives": true,
	"comparison": true, "setup": true, "integrations": true, "integration": true, "free": true,
	"open source": true, "open-source": true, "self-hosted": true, "hosted": true, "cloud": true,
	"enterprise": true, "budget": true, "tl;dr": true, "tldr": true, "final thoughts": true, "my recommendation": true,
	"top picks": true, "honorable mentions": true, "ease of use": true, "support": true,
	"security": true, "performance": true, "scalability": true, "documentation": true,
	"community": true, "limitations": true, "drawbacks": true, "strengths": true,
	"weaknesses": true, "quick take": true, "next steps": true, "caveats": true, "caveat": true,
	"yes": true, "no": true, "it depends": true, "short answer": true, "long answer": true,
	"good for": true, "watch out for": true, "downsides": true, "upsides": true, "ideal for": true,
	"privacy": true, "speed": true, "accuracy": true, "reliability": true, "flexibility": true,
	"customization": true, "api": true, "apis": true, "mobile": true, "desktop": true, "web": true,
	"paid": true, "freemium": true, "pro": true, "premium": true, "bonus": true, "warning": true,
	"important": true, "update": true, "answer": true, "option": true, "options": true, "why": true,
	"how": true, "faq": true, "steps": true, "step": true, "winner": true, "overall": true, "cheapest": true,
	"fastest": true, "simplest": true, "easiest": true, "best": true, "other": true, "others": true,
	"also": true, "bottom": true, "context": true, "background": true, "requirements": true, "tools": true,
}

// BoldNames returns bold phrases in prose that look like product names.
// Assistants often bold the products they recommend outside of lists.
func BoldNames(text string) []string {
	var out []string
	for _, m := range bold.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		name = strings.Trim(strings.TrimSpace(name), ":.-–—")
		if looksLikeName(name) {
			out = append(out, name)
		}
	}
	return out
}

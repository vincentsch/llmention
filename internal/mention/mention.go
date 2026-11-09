// Package mention finds brand names in assistant answers.
//
// Matching is case-insensitive and Unicode-normalized, and it respects word
// boundaries: "Tabslip" matches "tabslip's" but not "Tabslipper". A brand
// whose name is also an everyday word can ask for the same capitals, so
// "Bonsai" matches "Bonsai" but not "bonsai". The package
// also reads simple Markdown lists so callers can tell where a brand was
// ranked and which other products an answer named.
package mention

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Brand is one name to look for. Aliases are the other spellings that count
// as a mention, such as old names or product names. Domains count too.
type Brand struct {
	ID      string
	Name    string
	Aliases []string
	Domains []string
	// MatchCase counts the name and the aliases only when an answer writes
	// them with the same capital letters. Domains match in any case.
	MatchCase bool
}

// Terms returns the name, aliases and domains without blanks or duplicates.
func (b Brand) Terms() []string {
	var out []string
	for _, t := range b.terms() {
		out = append(out, t.text)
	}
	return out
}

type term struct {
	text      string
	exactCase bool
}

func (b Brand) terms() []term {
	var out []term
	seen := map[string]bool{}
	add := func(values []string, exactCase bool) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			key := Normalize(value)
			if key == "" {
				continue
			}
			if exactCase {
				// "Bonsai" and "BONSAI" are two spellings here.
				key = "case:" + norm.NFKC.String(value)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, term{value, exactCase})
		}
	}
	add(append([]string{b.Name}, b.Aliases...), b.MatchCase)
	add(b.Domains, false)
	return out
}

// Match is the first place a brand appears in a text.
type Match struct {
	BrandID string
	Start   int // byte offset in the original text
	End     int
	Quote   string // the text as written, for example "tabslip's" -> "tabslip"
}

// Find returns the first match of each brand in text, ordered by position.
// A match inside a longer match of another brand does not count, so
// "Acme Pro" is not also counted as "Acme".
func Find(text string, brands []Brand) []Match {
	normalized := normalize(text)
	var all []Match
	for _, brand := range brands {
		all = append(all, occurrences(text, normalized, brand)...)
	}
	slices.SortStableFunc(all, func(a, b Match) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return b.End - a.End
	})
	var out []Match
	taken := map[string]bool{}
	coveredUntil := 0
	for _, m := range all {
		if m.Start < coveredUntil {
			continue
		}
		coveredUntil = m.End
		if !taken[m.BrandID] {
			taken[m.BrandID] = true
			out = append(out, m)
		}
	}
	return out
}

// Contains reports whether text mentions the brand.
func Contains(text string, brand Brand) bool {
	return len(occurrences(text, normalize(text), brand)) > 0
}

// occurrences lists every place a brand term appears on word boundaries.
func occurrences(text string, normalized normalizedText, brand Brand) []Match {
	var out []Match
	for _, term := range brand.terms() {
		needle := Normalize(term.text)
		for from := 0; ; {
			i := strings.Index(normalized.text[from:], needle)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(needle)
			from = start + 1
			if !wordBoundary(normalized.text, needle, start, end) || end-1 >= len(normalized.spans) {
				continue
			}
			byteStart := normalized.spans[start].start
			byteEnd := normalized.spans[end-1].end
			quote := text[byteStart:byteEnd]
			if term.exactCase && norm.NFKC.String(quote) != norm.NFKC.String(term.text) {
				continue
			}
			out = append(out, Match{BrandID: brand.ID, Start: byteStart, End: byteEnd, Quote: quote})
		}
	}
	return out
}

// Normalize folds case and Unicode forms so that two spellings of the same
// name compare equal.
func Normalize(value string) string {
	return normalize(strings.TrimSpace(value)).text
}

type normalizedText struct {
	text  string
	spans []span // for each byte of text, the byte range in the original
}

type span struct{ start, end int }

var folder = cases.Fold()

func normalize(value string) normalizedText {
	var b strings.Builder
	var spans []span
	for start := 0; start < len(value); {
		end := nextCluster(value, start)
		part := folder.String(norm.NFKC.String(value[start:end]))
		before := b.Len()
		b.WriteString(part)
		for i := before; i < b.Len(); i++ {
			spans = append(spans, span{start, end})
		}
		start = end
	}
	return normalizedText{text: b.String(), spans: spans}
}

// nextCluster returns the end of the letter at start, including any accents
// stored as separate combining marks, so a match never cuts a letter in two.
func nextCluster(value string, start int) int {
	_, size := utf8.DecodeRuneInString(value[start:])
	end := start + size
	for end < len(value) {
		r, n := utf8.DecodeRuneInString(value[end:])
		if !unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me) {
			break
		}
		end += n
	}
	return end
}

func wordBoundary(text, needle string, start, end int) bool {
	if r, _ := utf8.DecodeRuneInString(needle); isWord(r) && start > 0 {
		if before, _ := utf8.DecodeLastRuneInString(text[:start]); isWord(before) {
			return false
		}
	}
	if r, _ := utf8.DecodeLastRuneInString(needle); isWord(r) && end < len(text) {
		if after, _ := utf8.DecodeRuneInString(text[end:]); isWord(after) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

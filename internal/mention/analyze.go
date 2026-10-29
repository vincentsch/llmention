package mention

import "strings"

// Result describes one brand in one answer.
type Result struct {
	BrandID       string
	Mentioned     bool // the answer names the brand
	NamedInPrompt bool // the question already named the brand
	Rank          int  // 1 for first place; 0 when not mentioned or the place is unclear
	Match         Match
}

// Counted reports whether the mention counts toward visibility. A mention
// only counts when the question did not name the brand itself, because an
// assistant repeats names from the question even when it knows nothing about
// them ("I'm not familiar with Tabslip").
func (r Result) Counted() bool { return r.Mentioned && !r.NamedInPrompt }

// Answer is the analysis of one answer.
type Answer struct {
	Brands []Result // in the order of the brands passed to Analyze
	List   []ListItem
}

// Analyze checks one answer for every brand. The rank is the list position
// when the answer is a list and the brand has its own entry. In prose it is
// the order in which the tracked brands first appear.
//
// Brands named in the prompt are left out of the ranking, so in an answer to
// "Alternatives to Helicone?" a first line about Helicone does not push
// everyone else down one place.
func Analyze(prompt, answer string, brands []Brand) Answer {
	out := Answer{Brands: make([]Result, len(brands)), List: ParseList(answer)}
	matches := Find(answer, brands)
	byID := map[string]Match{}
	for _, m := range matches {
		byID[m.BrandID] = m
	}
	named := NamedIn(prompt, brands)
	for i, brand := range brands {
		m, ok := byID[brand.ID]
		out.Brands[i] = Result{
			BrandID:       brand.ID,
			Mentioned:     ok,
			NamedInPrompt: named[brand.ID],
			Match:         m,
		}
	}
	if len(out.List) >= 2 {
		positions := listPositions(out.List, brands, named)
		for i, brand := range brands {
			if out.Brands[i].Mentioned && !named[brand.ID] {
				out.Brands[i].Rank = positions[brand.ID]
			}
		}
		return out
	}
	order := 0
	for _, m := range matches {
		if named[m.BrandID] {
			continue
		}
		order++
		for i := range out.Brands {
			if out.Brands[i].BrandID == m.BrandID {
				out.Brands[i].Rank = order
			}
		}
	}
	return out
}

// NamedIn reports which brands a text names. It matches all brands together,
// like Find, so a prompt about "Acme Books" does not also name "Acme".
func NamedIn(text string, brands []Brand) map[string]bool {
	out := map[string]bool{}
	for _, m := range Find(text, brands) {
		out[m.BrandID] = true
	}
	return out
}

// listPositions finds each brand's list entry. Entries about a brand named in
// the prompt are not counted as places. A brand's entry is the first one whose
// name, or failing that the start of the line, matches it, with all brands
// matched together so "Acme Books" in an entry is not also "Acme".
func listPositions(items []ListItem, brands []Brand, named map[string]bool) map[string]int {
	type entry struct {
		item  ListItem
		owner string
	}
	var entries []entry
	for _, item := range items {
		owner := ""
		if item.Name != "" {
			if m := Find(item.Name, brands); len(m) > 0 {
				owner = m[0].BrandID
			}
		}
		if owner == "" {
			// Fall back to the start of the line when the name was not
			// recognized, for example "Helicone, the hosted proxy, ...".
			head := item.Text
			if len(head) > 60 {
				head = head[:60]
			}
			if m := Find(head, brands); len(m) > 0 && m[0].Start <= 4 {
				owner = m[0].BrandID
			}
		}
		if owner != "" && named[owner] {
			continue
		}
		entries = append(entries, entry{item, owner})
	}
	out := map[string]int{}
	for i, e := range entries {
		if e.owner != "" && out[e.owner] == 0 {
			out[e.owner] = i + 1
		}
	}
	return out
}

// Unknown returns names in the answer that match none of the brands, taken
// from list entries, the first column of tables and bold text. Each name
// appears once. looksLikeName filters out most section labels, but not every
// name it lets through is a product, so callers treat these as candidates.
func (a Answer) Unknown(answer string, brands []Brand) []string {
	var candidates []string
	for _, item := range a.List {
		if item.Name != "" {
			candidates = append(candidates, item.Name)
		}
	}
	candidates = append(candidates, TableNames(answer)...)
	candidates = append(candidates, BoldNames(answer)...)
	var out []string
	seen := map[string]bool{}
	for _, name := range candidates {
		key := Normalize(name)
		if seen[key] || matchesAny(name, brands) {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(name))
	}
	return out
}

func matchesAny(name string, brands []Brand) bool {
	for _, brand := range brands {
		if Contains(name, brand) {
			return true
		}
		for _, term := range brand.Terms() {
			if Contains(term, Brand{Name: name}) {
				return true
			}
		}
	}
	return false
}

// Excerpt returns the sentence around a match as plain text, without
// Markdown emphasis, list markers or table pipes, shortened to about maxLen
// bytes, for showing what an assistant said.
func Excerpt(text string, m Match, maxLen int) string {
	lineStart := strings.LastIndexByte(text[:m.Start], '\n') + 1
	lineEnd := len(text)
	if i := strings.IndexByte(text[m.End:], '\n'); i >= 0 {
		lineEnd = m.End + i
	}
	start, end := lineStart, lineEnd
	// A table row is quoted whole, because its cells are not sentences.
	if !strings.HasPrefix(strings.TrimSpace(text[lineStart:lineEnd]), "|") {
		for _, sep := range []string{". ", "! ", "? "} {
			if i := strings.LastIndex(text[lineStart:m.Start], sep); i >= 0 {
				start = max(start, lineStart+i+len(sep))
			}
			if i := strings.Index(text[m.End:lineEnd], sep); i >= 0 {
				end = min(end, m.End+i+1)
			}
		}
	}
	sentence := strings.NewReplacer("**", "", "__", "", "`", "").Replace(text[start:end])
	sentence = plainLine(sentence)
	focus := max(0, strings.Index(sentence, m.Quote))
	return Shorten(sentence, focus, maxLen)
}

// plainLine turns one line of Markdown into text for a quote. It drops
// heading and list markers, and writes a table row as "first cell: other
// cells; separated; by semicolons".
func plainLine(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "|") {
		var cells []string
		for _, cell := range strings.Split(strings.Trim(s, "|"), "|") {
			if cell = strings.TrimSpace(cell); cell != "" {
				cells = append(cells, cell)
			}
		}
		if len(cells) < 2 {
			return strings.Join(cells, "")
		}
		return cells[0] + ": " + strings.Join(cells[1:], "; ")
	}
	s = strings.TrimSpace(strings.TrimLeft(s, "#>"))
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, marker) {
			s = s[len(marker):]
			break
		}
	}
	if i := strings.Index(s, ". "); i > 0 && i <= 3 && strings.Trim(s[:i], "0123456789") == "" {
		s = s[i+2:]
	}
	return strings.TrimSpace(s)
}

// Shorten trims s to about maxLen bytes, keeping the part around focus and
// marking cuts with an ellipsis. It never splits a UTF-8 character.
func Shorten(s string, focus, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	start := max(0, focus-maxLen/3)
	end := min(len(s), start+maxLen)
	start = max(0, end-maxLen)
	for start > 0 && !utf8Start(s[start]) {
		start--
	}
	for end < len(s) && !utf8Start(s[end]) {
		end++
	}
	out := strings.TrimSpace(s[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

package report

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/scorecard"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Diff compares two runs on what they have in common: the same prompts asked
// of the same engines in the same modes, scored with the same brand names.
// Only answers that came back complete in both runs are compared, so a
// failed request cannot move a rate up or down. Whatever differs between the
// runs is listed instead of silently changing the numbers.
type Diff struct {
	Old            RunInfo         `json:"old"`
	New            RunInfo         `json:"new"`
	Brand          string          `json:"brand"`
	SharedPrompts  int             `json:"shared_prompts"`
	AddedPrompts   []string        `json:"added_prompts,omitempty"`
	RemovedPrompts []string        `json:"removed_prompts,omitempty"`
	Reworded       []Rewording     `json:"reworded_prompts,omitempty"`
	ModelChanges   []ModelChange   `json:"model_changes,omitempty"`
	OnlyOld        []string        `json:"only_in_old,omitempty"` // engine · mode pairs
	OnlyNew        []string        `json:"only_in_new,omitempty"`
	Compared       int             `json:"compared_answers"`
	LeftOut        int             `json:"left_out_answers"`
	Rows           []DiffRow       `json:"rows"`
	Prompts        []PromptChange  `json:"prompt_changes"`
	Competitors    []CompetitorRow `json:"competitors,omitempty"`
}

// Rewording is a prompt whose text changed while its id stayed the same, so a
// change in its answers may come from the new wording.
type Rewording struct {
	ID  string `json:"id"`
	Old string `json:"old"`
	New string `json:"new"`
}

// ModelChange is an engine whose model changed between the runs.
type ModelChange struct {
	Engine string `json:"engine"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// DiffRow compares your numbers for one engine and mode.
type DiffRow struct {
	Engine          string   `json:"engine"`
	Mode            string   `json:"mode"`
	OldRate         *float64 `json:"old_mention_rate"`
	NewRate         *float64 `json:"new_mention_rate"`
	OldShareOfVoice *float64 `json:"old_share_of_voice"`
	NewShareOfVoice *float64 `json:"new_share_of_voice"`
	OldRank         *float64 `json:"old_avg_rank"`
	NewRank         *float64 `json:"new_avg_rank"`
}

// CompetitorRow compares one brand's mention rate across all shared cells.
type CompetitorRow struct {
	Name    string   `json:"name"`
	You     bool     `json:"you,omitempty"`
	OldRate *float64 `json:"old_mention_rate"`
	NewRate *float64 `json:"new_mention_rate"`
}

// PromptChange is a prompt where an engine started or stopped naming you.
type PromptChange struct {
	Engine   string `json:"engine"`
	Mode     string `json:"mode"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

// BuildDiff compares two runs' visibility answers.
func BuildDiff(oldCard, newCard *scorecard.Scorecard, oldAnswers, newAnswers []visibility.Answer, brands visibility.Brands) Diff {
	d := Diff{Old: runInfo(oldCard), New: runInfo(newCard), Brand: brands.You.Name}
	oldPrompts, newPrompts := promptTexts(oldAnswers), promptTexts(newAnswers)
	for id, text := range newPrompts {
		if _, ok := oldPrompts[id]; !ok {
			d.AddedPrompts = append(d.AddedPrompts, text)
		}
	}
	for id, text := range oldPrompts {
		newText, ok := newPrompts[id]
		if !ok {
			d.RemovedPrompts = append(d.RemovedPrompts, text)
			continue
		}
		d.SharedPrompts++
		if newText != text {
			d.Reworded = append(d.Reworded, Rewording{ID: id, Old: text, New: newText})
		}
	}
	slices.Sort(d.AddedPrompts)
	slices.Sort(d.RemovedPrompts)
	slices.SortFunc(d.Reworded, func(a, b Rewording) int { return strings.Compare(a.ID, b.ID) })
	for _, e := range newCard.Engines {
		if old, ok := oldCard.Engine(e.ID); ok && old.Model != e.Model {
			d.ModelChanges = append(d.ModelChanges, ModelChange{Engine: e.ID, Old: old.Model, New: e.Model})
		}
	}
	oldCells, newCells := cellKeys(oldAnswers), cellKeys(newAnswers)
	for _, k := range oldCells {
		if !slices.Contains(newCells, k) {
			d.OnlyOld = append(d.OnlyOld, k[0]+" · "+config.ModeLabel(k[1]))
		}
	}
	for _, k := range newCells {
		if !slices.Contains(oldCells, k) {
			d.OnlyNew = append(d.OnlyNew, k[0]+" · "+config.ModeLabel(k[1]))
		}
	}
	// Use only the answers both runs have, for the same prompt, engine, mode
	// and repetition, and only when both came back complete.
	shared := func(a visibility.Answer, prompts map[string]string, cells [][2]string) bool {
		_, ok := prompts[a.PromptID]
		return ok && slices.Contains(cells, [2]string{a.Engine, a.Mode})
	}
	oldByKey := map[string]visibility.Answer{}
	for _, a := range oldAnswers {
		if shared(a, newPrompts, newCells) {
			oldByKey[a.Key()] = a
		}
	}
	matched := map[string]bool{}
	newKeys := map[string]bool{}
	var keptNew []visibility.Answer
	for _, a := range newAnswers {
		newKeys[a.Key()] = true
		if !shared(a, oldPrompts, oldCells) {
			continue
		}
		old, ok := oldByKey[a.Key()]
		if ok && old.Counted() && a.Counted() {
			matched[a.Key()] = true
			keptNew = append(keptNew, a)
		} else {
			d.LeftOut++
		}
	}
	var keptOld []visibility.Answer
	for _, a := range oldAnswers {
		if matched[a.Key()] {
			keptOld = append(keptOld, a)
		} else if shared(a, newPrompts, newCells) && !newKeys[a.Key()] {
			d.LeftOut++ // only the older run has this answer
		}
	}
	d.Compared = len(keptNew)
	oldSummary := visibility.Summarize(brands, keptOld)
	newSummary := visibility.Summarize(brands, keptNew)
	for _, nc := range newSummary.Cells {
		oc := findCell(oldSummary.Cells, nc.Engine, nc.Mode)
		if oc == nil {
			continue
		}
		row := DiffRow{Engine: nc.Engine, Mode: nc.Mode, OldShareOfVoice: oc.You().ShareOfVoice, NewShareOfVoice: nc.You().ShareOfVoice}
		row.OldRate, row.NewRate = optional(oc.You().MentionRate()), optional(nc.You().MentionRate())
		row.OldRank, row.NewRank = optional(oc.You().AvgRank()), optional(nc.You().AvgRank())
		d.Rows = append(d.Rows, row)
	}
	oldTotals, newTotals := oldSummary.Totals(), newSummary.Totals()
	for i := range newTotals {
		if i >= len(oldTotals) {
			break
		}
		d.Competitors = append(d.Competitors, CompetitorRow{
			Name: newTotals[i].Name, You: newTotals[i].You,
			OldRate: optional(oldTotals[i].MentionRate()), NewRate: optional(newTotals[i].MentionRate()),
		})
	}
	for _, np := range newSummary.Prompts {
		pi := slices.IndexFunc(oldSummary.Prompts, func(p visibility.PromptRow) bool { return p.ID == np.ID })
		if pi < 0 || np.NamesYou {
			continue
		}
		op := oldSummary.Prompts[pi]
		for ci, nc := range newSummary.Cells {
			oci := slices.IndexFunc(oldSummary.Cells, func(c visibility.Cell) bool { return c.Engine == nc.Engine && c.Mode == nc.Mode })
			if oci < 0 || ci >= len(np.Cells) || oci >= len(op.Cells) {
				continue
			}
			before, after := op.Cells[oci], np.Cells[ci]
			if before.Answers == 0 || after.Answers == 0 || (before.Mentioned > 0) == (after.Mentioned > 0) {
				continue
			}
			d.Prompts = append(d.Prompts, PromptChange{
				Engine: nc.Engine, Mode: nc.Mode, PromptID: np.ID, Prompt: np.Prompt,
				Before: outcome(before, brands.You.Name), After: outcome(after, brands.You.Name),
			})
		}
	}
	return d
}

func promptTexts(answers []visibility.Answer) map[string]string {
	out := map[string]string{}
	for _, a := range answers {
		out[a.PromptID] = a.Prompt
	}
	return out
}

func cellKeys(answers []visibility.Answer) [][2]string {
	var out [][2]string
	for _, a := range answers {
		k := [2]string{a.Engine, a.Mode}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func findCell(cells []visibility.Cell, engine, mode string) *visibility.Cell {
	for i := range cells {
		if cells[i].Engine == engine && cells[i].Mode == mode {
			return &cells[i]
		}
	}
	return nil
}

func optional(v float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &v
}

func outcome(c visibility.PromptCell, brand string) string {
	if c.Mentioned == 0 {
		if len(c.Competitors) > 0 {
			return "named " + config.JoinAnd(c.Competitors) + ", not " + brand
		}
		return "did not name " + brand
	}
	if c.BestRank > 0 {
		return fmt.Sprintf("named %s at #%d", brand, c.BestRank)
	}
	return "named " + brand
}

// WriteDiffText prints a comparison of two runs.
func WriteDiffText(w io.Writer, d Diff, style Style) {
	fmt.Fprintf(w, "%s: run %s (%s) compared with run %s (%s)\n", d.Brand, d.New.ID, localTime(d.New.CreatedAt), d.Old.ID, localTime(d.Old.CreatedAt))
	fmt.Fprintf(w, "Compared on the %s both runs asked, scored with the same brand names.\n", plural(d.SharedPrompts, "prompt"))
	for _, n := range diffNotes(d) {
		fmt.Fprintln(w, "Note: "+n)
	}
	if len(d.Rows) == 0 {
		fmt.Fprintln(w, "\nThe runs share no engine, mode and prompt with an answer in both, so there is nothing to compare.")
		return
	}
	fmt.Fprintf(w, "\n%s\n", style.heading("Mention rate of "+d.Brand))
	tw := table(w)
	fmt.Fprintln(tw, "  Engine\tMode\tBefore\tNow\tChange\tShare of voice now")
	for _, r := range d.Rows {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", r.Engine, config.ModeLabel(r.Mode), ptrPercent(r.OldRate), ptrPercent(r.NewRate), change(r.OldRate, r.NewRate), ptrPercent(r.NewShareOfVoice))
	}
	tw.Flush()
	if len(d.Competitors) > 1 {
		fmt.Fprintf(w, "\n%s\n", style.heading("All brands, every engine and mode together"))
		tw = table(w)
		fmt.Fprintln(tw, "  Brand\tBefore\tNow\tChange")
		for _, c := range d.Competitors {
			name := c.Name
			if c.You {
				name += " (you)"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", name, ptrPercent(c.OldRate), ptrPercent(c.NewRate), change(c.OldRate, c.NewRate))
		}
		tw.Flush()
	}
	if len(d.Prompts) > 0 {
		fmt.Fprintf(w, "\n%s\n", style.heading("Answers that changed"))
		for i, p := range d.Prompts {
			if i == 12 {
				fmt.Fprintf(w, "  …and %d more (see --json)\n", len(d.Prompts)-12)
				break
			}
			fmt.Fprintf(w, "  %s · %s · %q\n    before: %s\n    now:    %s\n", p.Engine, config.ModeLabel(p.Mode), shorten(p.Prompt, 70), p.Before, p.After)
		}
	} else {
		fmt.Fprintf(w, "\nNo answer started or stopped naming %s.\n", d.Brand)
	}
}

// diffNotes lists what differs between the runs and is left out of, or may
// explain part of, the comparison.
func diffNotes(d Diff) []string {
	var out []string
	for _, m := range d.ModelChanges {
		out = append(out, fmt.Sprintf("%s changed model from %s to %s, so part of any change may come from the new model.", m.Engine, m.Old, m.New))
	}
	for _, r := range d.Reworded {
		out = append(out, fmt.Sprintf("Prompt %s was reworded, so part of any change may come from the new wording. Before: %q. Now: %q.", r.ID, shorten(r.Old, 60), shorten(r.New, 60)))
	}
	if len(d.AddedPrompts) > 0 {
		n := len(d.AddedPrompts)
		out = append(out, fmt.Sprintf("%s only in the newer run, so %s left out of the comparison.", pick(n, "1 prompt is", fmt.Sprintf("%d prompts are", n)), pick(n, "it is", "they are")))
	}
	if len(d.RemovedPrompts) > 0 {
		n := len(d.RemovedPrompts)
		out = append(out, fmt.Sprintf("%s only in the older run, so %s left out of the comparison.", pick(n, "1 prompt is", fmt.Sprintf("%d prompts are", n)), pick(n, "it is", "they are")))
	}
	for _, c := range d.OnlyNew {
		out = append(out, c+" is new in this run.")
	}
	for _, c := range d.OnlyOld {
		out = append(out, c+" is missing from this run.")
	}
	if d.LeftOut > 0 {
		out = append(out, fmt.Sprintf("%s left out, because one of the runs has no complete answer there.", pick(d.LeftOut, "1 answer was", fmt.Sprintf("%d answers were", d.LeftOut))))
	}
	return out
}

func ptrPercent(v *float64) string {
	if v == nil {
		return "–"
	}
	return percent(*v, true)
}

func change(old, now *float64) string {
	if old == nil || now == nil {
		return ""
	}
	// Subtract the rounded values the table shows, so the columns add up.
	delta := math.Round(*now*100) - math.Round(*old*100)
	switch {
	case delta > 0:
		return fmt.Sprintf("+%.0f pts", delta)
	case delta < 0:
		return fmt.Sprintf("%.0f pts", delta)
	}
	return "same"
}

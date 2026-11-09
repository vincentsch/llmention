package evaluate

import (
	"github.com/vincentsch/llmention/internal/mention"
	"github.com/vincentsch/llmention/internal/suite"
)

// mentionAnalysis is what one answer says about the brand and competitors.
type mentionAnalysis struct {
	Eligible    bool
	Brand       mention.Result
	Competitors map[string]mention.Result // by competitor name
	Evidence    []EvidenceRecord
}

const brandEntityID = "brand"

// namesATrackedBrand reports whether the prompt named the brand or any
// competitor.
func (m *mentionAnalysis) namesATrackedBrand() bool {
	if m.Brand.NamedInPrompt {
		return true
	}
	for _, c := range m.Competitors {
		if c.NamedInPrompt {
			return true
		}
	}
	return false
}

func evaluateMention(resultID, declarationID, version string, item suite.Item, promptText, answerText string) (EvaluationRecord, mentionAnalysis) {
	evalID := evaluationID(resultID, declarationID, EvaluatorMentionMatch, version)
	brands := mentionBrands(item)
	answer := mention.Analyze(promptText, answerText, brands)
	analysis := mentionAnalysis{Eligible: true, Competitors: map[string]mention.Result{}}
	payload := MentionMatchPayload{EvidenceIDsByEntityID: map[string][]string{}}
	for i, result := range answer.Brands {
		brand := brands[i]
		row := MentionEntityRow{
			EntityID:      brand.ID,
			Name:          brand.Name,
			EntityType:    "competitor",
			Mentioned:     result.Mentioned,
			NamedInPrompt: result.NamedInPrompt,
		}
		if result.Rank > 0 {
			rank := result.Rank
			row.Rank = &rank
		}
		if result.Mentioned {
			m := result.Match
			evidence := EvidenceRecord{
				ID:              evidenceID(evalID, "answer_text", m.Start, m.End, m.Quote),
				Source:          "answer_text",
				ByteStart:       m.Start,
				ByteEnd:         m.End,
				Quote:           m.Quote,
				Normalized:      mention.Normalize(m.Quote),
				NormalizedValue: mention.Normalize(m.Quote),
				EntityID:        brand.ID,
			}
			analysis.Evidence = append(analysis.Evidence, evidence)
			row.EvidenceIDs = []string{evidence.ID}
			payload.EvidenceIDsByEntityID[brand.ID] = row.EvidenceIDs
			payload.OrderedMentionedEntityIDs = append(payload.OrderedMentionedEntityIDs, brand.ID)
		}
		if brand.ID == brandEntityID {
			row.EntityType = "brand"
			analysis.Brand = result
			payload.Brand = row
			continue
		}
		analysis.Competitors[brand.Name] = result
		payload.Competitors = append(payload.Competitors, row)
	}
	record := EvaluationRecord{
		ID:               evalID,
		ResultID:         resultID,
		DeclarationID:    declarationID,
		EvaluatorType:    EvaluatorMentionMatch,
		EvaluatorVersion: version,
		Status:           EvaluationStatusCompleted,
		PayloadType:      EvaluatorMentionMatch,
		Payload:          payload,
		Values:           map[string]MetricScalar{"brand_mentioned": BooleanScalar(analysis.Brand.Counted())},
		Evidence:         analysis.Evidence,
	}
	if analysis.Brand.Rank > 0 {
		record.Values["brand_rank"] = IntegerScalar(int64(analysis.Brand.Rank))
	}
	return record, analysis
}

// mentionBrands lists the brand first, then the competitors. Domains count
// as names, so "tabslip.com" is a mention of Tabslip.
func mentionBrands(item suite.Item) []mention.Brand {
	var out []mention.Brand
	if item.Brand != nil {
		domains := append(append([]string(nil), item.Brand.Domains...), item.OwnedDomains...)
		out = append(out, mention.Brand{ID: brandEntityID, Name: item.Brand.Name, Aliases: item.Brand.Aliases, Domains: domains, MatchCase: item.Brand.MatchCase})
	}
	for _, c := range item.Competitors {
		out = append(out, mention.Brand{ID: "competitor:" + c.Name, Name: c.Name, Aliases: c.Aliases, Domains: c.Domains, MatchCase: c.MatchCase})
	}
	return out
}

// counted reports whether a result counts as a mention of the brand. The
// answer must name it and the prompt must not.
func (a *mentionAnalysis) brandCounted() bool { return a != nil && a.Brand.Counted() }

// precedes reports whether a competitor came before the brand, either higher
// in a list or earlier in prose.
func precedes(competitor, brand mention.Result) bool {
	if !competitor.Counted() {
		return false
	}
	if !brand.Counted() {
		return true
	}
	if competitor.Rank > 0 && brand.Rank > 0 {
		return competitor.Rank < brand.Rank
	}
	return competitor.Match.Start < brand.Match.Start
}

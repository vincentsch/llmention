package benchmark

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/engine"
)

func KnowledgeCutoffLabel(rawCutoff, eventDate string) (label string, warning string) {
	rawCutoff = strings.TrimSpace(rawCutoff)
	if rawCutoff == "" {
		return CutoffUnknown, ""
	}
	event, err := time.Parse("2006-01-02", eventDate)
	if err != nil {
		return CutoffUnknown, ""
	}
	if cutoff, err := time.Parse("2006-01-02", rawCutoff); err == nil {
		if cutoff.Before(event) {
			return CutoffAfter, ""
		}
		return CutoffBefore, ""
	}
	if cutoff, err := time.Parse("2006-01", rawCutoff); err == nil {
		eventMonth := time.Date(event.Year(), event.Month(), 1, 0, 0, 0, 0, time.UTC)
		if eventMonth.Before(cutoff) {
			return CutoffBefore, ""
		}
		if eventMonth.After(cutoff) {
			return CutoffAfter, ""
		}
		return CutoffUnknown, ""
	}
	return CutoffUnknown, WarningInvalidKnowledgeCutoff
}

func CutoffHonest(expected, cutoffLabel, answerStatus string) *bool {
	switch expected {
	case ExpectedShouldKnow, ExpectedCorrectWithSource:
		return boolPtr(answerStatus == AnswerStatusCorrect)
	case ExpectedAbstainOrUnknown:
		if cutoffLabel == CutoffBefore {
			return boolPtr(answerStatus == AnswerStatusCorrect)
		}
		return boolPtr(answerStatus == AnswerStatusCorrect || answerStatus == AnswerStatusNotAttempted)
	default:
		return nil
	}
}

func DeriveSourceEvidence(answer *engine.Answer, caps engine.CapabilityProfile, mode string, expectation SourceExpectation) SourceEvidenceStatus {
	required := slices.Contains(expectation.RequiredForModes, mode)
	out := SourceEvidenceStatus{
		RequirementStatus: SourceStatusNotRequired,
		FreshnessStatus:   FreshnessNotRequired,
	}
	if !required {
		return out
	}
	citationExposed := caps.ExposesCitations
	consultedExposed := caps.ExposesConsultedSources
	freshnessReference := strings.TrimSpace(expectation.FreshnessReferenceDate)
	if !citationExposed && !consultedExposed {
		out.RequirementStatus = SourceStatusUnavailable
		if freshnessReference != "" {
			out.FreshnessStatus = FreshnessUnavailable
		}
		return out
	}
	out.RequirementStatus = SourceStatusMissing
	var citationDates, consultedDates []string
	if answer != nil {
		if citationExposed {
			for _, citation := range answer.Citations {
				if _, _, err := domain.NormalizeSourceIdentity(citation.URL, citation.Domain); err == nil {
					citationDates = append(citationDates, citation.Metadata["date"])
				}
			}
		}
		if consultedExposed {
			for _, source := range answer.SourcesConsulted {
				if _, _, err := domain.NormalizeSourceIdentity(source.URL, source.Domain); err == nil {
					consultedDates = append(consultedDates, source.Metadata["date"])
				}
			}
		}
	}
	if len(citationDates)+len(consultedDates) > 0 {
		out.RequirementStatus = SourceStatusMet
	}
	if freshnessReference == "" {
		out.FreshnessStatus = FreshnessNotRequired
		return out
	}
	ref, err := time.Parse("2006-01-02", freshnessReference)
	if err != nil {
		out.FreshnessStatus = FreshnessNoValidDate
		return out
	}
	freshest := time.Time{}
	channelSet := map[string]struct{}{}
	for _, date := range citationDates {
		out.recordSourceDate("citation", date, ref, &freshest, channelSet)
	}
	for _, date := range consultedDates {
		out.recordSourceDate("consulted", date, ref, &freshest, channelSet)
	}
	for channel := range channelSet {
		out.DatedSourceChannels = append(out.DatedSourceChannels, channel)
	}
	sort.Strings(out.DatedSourceChannels)
	if out.ValidDatedSourceCount == 0 {
		out.FreshnessStatus = FreshnessNoValidDate
		return out
	}
	out.FreshestNormalizedSourceDate = freshest.Format("2006-01-02")
	if freshest.Before(ref) {
		out.FreshnessStatus = FreshnessStale
	} else {
		out.FreshnessStatus = FreshnessFresh
	}
	out.Warnings = uniqueSorted(out.Warnings)
	return out
}

func (s *SourceEvidenceStatus) recordSourceDate(channel, rawDate string, _ time.Time, freshest *time.Time, channelSet map[string]struct{}) {
	rawDate = strings.TrimSpace(rawDate)
	if rawDate == "" {
		return
	}
	parsed, err := time.Parse("2006-01-02", rawDate)
	if err != nil {
		s.InvalidDatedSourceCount++
		s.Warnings = append(s.Warnings, WarningInvalidSourceDateMetadata)
		return
	}
	s.ValidDatedSourceCount++
	channelSet[channel] = struct{}{}
	if freshest.IsZero() || parsed.After(*freshest) {
		*freshest = parsed
	}
}

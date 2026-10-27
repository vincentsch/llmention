package evaluate

import (
	"fmt"
	"strings"

	"github.com/vincentsch/llmention/internal/domain"
	"github.com/vincentsch/llmention/internal/engine"
	"github.com/vincentsch/llmention/internal/suite"
)

type sourceAnalysis struct {
	CitationSupported  bool
	ConsultedSupported bool
	ToolCallsSupported bool
	UnavailableReason  string
	Citations          []domainObservation
	Consulted          []domainObservation
	SearchQueries      []string
	Warnings           []EvaluationWarning
}

type domainObservation struct {
	Domain   string
	Raw      string
	Channel  string
	Evidence EvidenceRecord
}

func evaluateSources(resultID, declarationID, version string, item suite.Item, answer *engine.Answer, caps engine.CapabilityProfile) (EvaluationRecord, sourceAnalysis) {
	evalID := evaluationID(resultID, declarationID, EvaluatorSourceDomain, version)
	analysis := sourceAnalysis{
		CitationSupported:  caps.ExposesCitations,
		ConsultedSupported: caps.ExposesConsultedSources,
		ToolCallsSupported: caps.ExposesToolCalls,
	}
	record := EvaluationRecord{
		ID:               evalID,
		ResultID:         resultID,
		DeclarationID:    declarationID,
		EvaluatorType:    EvaluatorSourceDomain,
		EvaluatorVersion: version,
		Status:           EvaluationStatusCompleted,
		Values:           map[string]MetricScalar{},
	}
	if answer == nil {
		record.Status = EvaluationStatusSkipped
		record.ReasonCode = ReasonEvaluatorNotApplicable
		analysis.UnavailableReason = ReasonEvaluatorNotApplicable
		record.PayloadType = EvaluatorSourceDomain
		record.Payload = sourcePayload(item, analysis)
		return record, analysis
	}
	if analysis.CitationSupported {
		for i, citation := range answer.Citations {
			obs, warning, ok := citationObservation(evalID, i, citation)
			if warning != nil {
				analysis.Warnings = append(analysis.Warnings, *warning)
			}
			if ok {
				analysis.Citations = append(analysis.Citations, obs)
				record.Evidence = append(record.Evidence, obs.Evidence)
			}
		}
	}
	if analysis.ConsultedSupported {
		for i, source := range answer.SourcesConsulted {
			obs, warning, ok := consultedObservation(evalID, i, source)
			if warning != nil {
				analysis.Warnings = append(analysis.Warnings, *warning)
			}
			if ok {
				analysis.Consulted = append(analysis.Consulted, obs)
				record.Evidence = append(record.Evidence, obs.Evidence)
			}
		}
	}
	if analysis.ToolCallsSupported {
		for _, call := range answer.ToolCalls {
			if strings.TrimSpace(call.Query) == "" {
				continue
			}
			analysis.SearchQueries = append(analysis.SearchQueries, call.Query)
		}
	}
	record.Warnings = analysis.Warnings
	record.Values["citation_domain_observations"] = IntegerScalar(int64(len(analysis.Citations)))
	record.Values["consulted_domain_observations"] = IntegerScalar(int64(len(analysis.Consulted)))
	record.Values["search_query_count"] = IntegerScalar(int64(len(analysis.SearchQueries)))
	record.PayloadType = EvaluatorSourceDomain
	record.Payload = sourcePayload(item, analysis)
	_ = item
	return record, analysis
}

func citationObservation(evalID string, index int, citation engine.Citation) (domainObservation, *EvaluationWarning, bool) {
	return sourceObservation(evalID, "citation", index, citation.URL, citation.Domain)
}

func consultedObservation(evalID string, index int, source engine.Source) (domainObservation, *EvaluationWarning, bool) {
	return sourceObservation(evalID, "consulted", index, source.URL, source.Domain)
}

func sourceObservation(evalID, channel string, index int, rawURL, rawDomain string) (domainObservation, *EvaluationWarning, bool) {
	observed, code, err := domain.NormalizeSourceIdentity(rawURL, rawDomain)
	if err != nil {
		warn := EvaluationWarning{Code: code, Message: fmt.Sprintf("%s source %d: %v", channel, index, err)}
		return domainObservation{}, &warn, false
	}

	raw := rawDomain
	if raw == "" {
		raw = rawURL
	}
	evidence := EvidenceRecord{
		Source:          channel,
		ByteStart:       0,
		ByteEnd:         len(raw),
		Quote:           raw,
		Normalized:      observed,
		NormalizedValue: observed,
		Domain:          observed,
		SourceChannel:   channel,
	}
	evidence.ID = evidenceID(evalID, channel, evidence.ByteStart, evidence.ByteEnd, evidence.Quote)
	return domainObservation{Domain: observed, Raw: raw, Channel: channel, Evidence: evidence}, nil, true
}

func sourcePayload(item suite.Item, analysis sourceAnalysis) SourceDomainPayload {
	payload := SourceDomainPayload{
		ChannelAvailability: []SourceChannelAvailability{
			sourceAvailability("citation", analysis.CitationSupported, analysis.UnavailableReason),
			sourceAvailability("consulted", analysis.ConsultedSupported, analysis.UnavailableReason),
			sourceAvailability("tool_call", analysis.ToolCallsSupported, analysis.UnavailableReason),
		},
	}
	counts := map[string]map[string]int{}
	evidence := map[string]map[string][]string{}
	for _, observation := range append(append([]domainObservation(nil), analysis.Citations...), analysis.Consulted...) {
		if counts[observation.Channel] == nil {
			counts[observation.Channel] = map[string]int{}
		}
		if evidence[observation.Channel] == nil {
			evidence[observation.Channel] = map[string][]string{}
		}
		counts[observation.Channel][observation.Domain]++
		evidence[observation.Channel][observation.Domain] = append(evidence[observation.Channel][observation.Domain], observation.Evidence.ID)
		payload.EvidenceIDs = append(payload.EvidenceIDs, observation.Evidence.ID)
	}
	for _, channel := range []string{"citation", "consulted"} {
		var domains []string
		for domain := range counts[channel] {
			domains = append(domains, domain)
		}
		sortStrings(domains)
		for _, domain := range domains {
			flags := sourcePolicyFlags(item, domain)
			ids := uniqueStrings(evidence[channel][domain])
			payload.Observations = append(payload.Observations, SourceObservation{
				Channel:          channel,
				Domain:           domain,
				Count:            counts[channel][domain],
				EvidenceIDs:      ids,
				OwnedDomain:      flags.OwnedDomain,
				CompetitorDomain: flags.CompetitorDomain,
				PreferredDomain:  flags.PreferredDomain,
				BlockedDomain:    flags.BlockedDomain,
			})
			payload.Counts = append(payload.Counts, SourceDomainCount{
				Channel: channel,
				Domain:  domain,
				Count:   counts[channel][domain],
			})
			payload.PolicyFlags = append(payload.PolicyFlags, flags)
		}
	}
	payload.EvidenceIDs = uniqueStrings(payload.EvidenceIDs)
	return payload
}

func sourceAvailability(channel string, available bool, reason string) SourceChannelAvailability {
	if reason != "" {
		return SourceChannelAvailability{Channel: channel, Available: false, UnavailableReason: reason}
	}
	row := SourceChannelAvailability{Channel: channel, Available: available}
	if !available {
		row.UnavailableReason = ReasonSourceChannelUnavailable
	}
	return row
}

func sourcePolicyFlags(item suite.Item, host string) SourcePolicyFlag {
	flag := SourcePolicyFlag{Domain: host}
	flag.OwnedDomain = policyMatches(host, item.OwnedDomains)
	flag.CompetitorDomain = policyMatches(host, item.CompetitorDomains)
	for _, competitor := range item.Competitors {
		if policyMatches(host, competitor.Domains) {
			flag.CompetitorDomain = true
			break
		}
	}
	flag.PreferredDomain = policyMatches(host, item.PreferredDomains)
	flag.BlockedDomain = policyMatches(host, item.BlockedDomains)
	return flag
}

func hostFromURL(raw string) (string, error) {
	host, _, err := domain.NormalizeSourceIdentity(raw, "")
	return host, err
}

func policyMatches(host string, policies []string) bool {
	for _, policy := range policies {
		ok, err := domain.MatchesScoringPolicy(host, policy)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func distinctDomains(observations []domainObservation) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, observation := range observations {
		if _, ok := seen[observation.Domain]; ok {
			continue
		}
		seen[observation.Domain] = struct{}{}
		out = append(out, observation.Domain)
	}
	sortStrings(out)
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

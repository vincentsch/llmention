// Package domain normalizes host names so that a citation of www.example.com
// matches a configured domain of example.com, and the other way round.
package domain

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// NormalizeScoringDomain turns a site into the form llmention compares: the
// sites in llmention.yaml and the sites the answers cite. It takes a host
// name or a URL and drops the port and a leading "www.". localhost and IP
// addresses are fine. A wildcard is only allowed at the start, as in
// *.example.com.
//
// Dropping "www." on both sides means a site written as www.example.com and a
// citation of example.com (or the reverse) count as the same site.
func NormalizeScoringDomain(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("domain is empty")
	}

	host, err := hostFromScoringInput(raw)
	if err != nil {
		return "", err
	}

	wildcard := false
	if strings.HasPrefix(host, "*.") {
		wildcard = true
		host = strings.TrimPrefix(host, "*.")
		if host == "" {
			return "", fmt.Errorf("wildcard domain %q is not valid; write it as *.example.com", value)
		}
	}
	if strings.Contains(host, "*") {
		return "", fmt.Errorf("domain %q can only have * at the start, as in *.example.com", value)
	}

	host, err = normalizeHostName(host)
	if err != nil {
		return "", fmt.Errorf("domain %q is not valid: %w", value, err)
	}
	if wildcard {
		if net.ParseIP(host) != nil || host == "localhost" || !strings.Contains(host, ".") {
			return "", fmt.Errorf("wildcard domain %q must be in the form *.example.com", value)
		}
		return "*." + host, nil
	}
	if rest, ok := strings.CutPrefix(host, "www."); ok && strings.Contains(rest, ".") {
		host = rest
	}
	return host, nil
}

// NormalizeScoringDomains runs NormalizeScoringDomain on a list and drops
// repeats. The order stays the same.
func NormalizeScoringDomains(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := NormalizeScoringDomain(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// MatchesScoringPolicy reports whether a cited host belongs to a site from
// the configuration. example.com matches example.com and its subdomains.
// *.example.com matches only the subdomains.
func MatchesScoringPolicy(host, policy string) (bool, error) {
	observed, err := NormalizeObservedHost(host)
	if err != nil {
		return false, err
	}
	normalizedPolicy, err := NormalizeScoringDomain(policy)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(normalizedPolicy, "*.") {
		base := strings.TrimPrefix(normalizedPolicy, "*.")
		return observed != base && strings.HasSuffix(observed, "."+base), nil
	}
	if observed == normalizedPolicy {
		return true, nil
	}
	if net.ParseIP(normalizedPolicy) != nil || normalizedPolicy == "localhost" {
		return false, nil
	}
	return strings.HasSuffix(observed, "."+normalizedPolicy), nil
}

// PoliciesOverlap reports whether some host would match both sites, for
// example example.com and docs.example.com.
func PoliciesOverlap(a, b string) (bool, error) {
	left, err := NormalizeScoringDomain(a)
	if err != nil {
		return false, err
	}
	right, err := NormalizeScoringDomain(b)
	if err != nil {
		return false, err
	}
	if left == right {
		return true, nil
	}
	leftWildcard := strings.HasPrefix(left, "*.")
	rightWildcard := strings.HasPrefix(right, "*.")
	leftBase := strings.TrimPrefix(left, "*.")
	rightBase := strings.TrimPrefix(right, "*.")
	if net.ParseIP(leftBase) != nil || net.ParseIP(rightBase) != nil || leftBase == "localhost" || rightBase == "localhost" {
		return false, nil
	}
	if !leftWildcard && !rightWildcard {
		return strings.HasSuffix(leftBase, "."+rightBase) || strings.HasSuffix(rightBase, "."+leftBase), nil
	}
	if leftWildcard && rightWildcard {
		return leftBase == rightBase || strings.HasSuffix(leftBase, "."+rightBase) || strings.HasSuffix(rightBase, "."+leftBase), nil
	}
	if leftWildcard {
		return leftBase == rightBase || strings.HasSuffix(leftBase, "."+rightBase) || strings.HasSuffix(rightBase, "."+leftBase), nil
	}
	return rightBase == leftBase || strings.HasSuffix(leftBase, "."+rightBase) || strings.HasSuffix(rightBase, "."+leftBase), nil
}

// NormalizeObservedHost normalizes a host that a provider cited or read. A
// real host has no wildcard.
func NormalizeObservedHost(value string) (string, error) {
	host, err := NormalizeScoringDomain(value)
	if err != nil {
		return "", err
	}
	if strings.Contains(host, "*") {
		return "", fmt.Errorf("source host %q must not contain a wildcard", value)
	}
	return host, nil
}

// NormalizeEndpointHost checks and normalizes the host of a base_url or a
// webhook. It takes a host name or a URL and keeps the port when there is
// one. localhost and IP addresses are fine. Wildcards are not.
func NormalizeEndpointHost(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("the host is empty")
	}
	if strings.Contains(raw, "*") {
		return "", fmt.Errorf("host %q must not contain a wildcard", value)
	}

	host, port, hasPort, err := endpointHostPort(raw)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeHostName(host)
	if err != nil {
		return "", fmt.Errorf("host %q is not valid: %w", host, err)
	}
	host = normalized
	if !hasPort {
		return host, nil
	}
	if err := validatePort(port); err != nil {
		return "", fmt.Errorf("host %q has a port that is not valid: %w", value, err)
	}
	return net.JoinHostPort(host, port), nil
}

func hostFromScoringInput(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("domain %q is not a valid URL: %w", raw, err)
		}
		if u.User != nil {
			return "", fmt.Errorf("domain %q must not include a user name or password", raw)
		}
		if u.Host == "" {
			return "", fmt.Errorf("domain %q has no host name", raw)
		}
		host := u.Hostname()
		if host == "" {
			return "", fmt.Errorf("domain %q has no host name", raw)
		}
		return host, nil
	}
	if strings.ContainsAny(raw, "/?#") {
		return "", fmt.Errorf("domain %q must be a host name such as example.com, or a full URL", raw)
	}
	host := raw
	if strings.HasPrefix(host, "[") {
		if strings.Contains(host, "]:") {
			parsedHost, _, err := net.SplitHostPort(host)
			if err != nil {
				return "", fmt.Errorf("domain %q has a host or port that is not valid: %w", raw, err)
			}
			return parsedHost, nil
		}
		if strings.HasSuffix(host, "]") {
			return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "["), nil
		}
	}
	if strings.Count(host, ":") == 1 {
		left, right, ok := strings.Cut(host, ":")
		if !ok || left == "" || right == "" {
			return "", fmt.Errorf("domain %q has a host or port that is not valid", raw)
		}
		if err := validatePort(right); err != nil {
			return "", fmt.Errorf("domain %q has a port that is not valid: %w", raw, err)
		}
		return left, nil
	}
	return host, nil
}

func endpointHostPort(raw string) (host, port string, hasPort bool, err error) {
	if strings.Contains(raw, "://") {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", false, fmt.Errorf("%q is not a valid URL: %w", raw, parseErr)
		}
		if u.User != nil {
			return "", "", false, fmt.Errorf("URL %q must not include a user name or password", raw)
		}
		host = u.Hostname()
		if host == "" {
			return "", "", false, fmt.Errorf("URL %q has no host name", raw)
		}
		port = u.Port()
		return host, port, port != "", nil
	}
	if strings.ContainsAny(raw, "/?#") {
		return "", "", false, fmt.Errorf("host %q must be a host name such as api.example.com, or a full URL", raw)
	}
	if strings.HasPrefix(raw, "[") {
		if strings.Contains(raw, "]:") {
			host, port, err := net.SplitHostPort(raw)
			if err != nil {
				return "", "", false, fmt.Errorf("host %q has a host or port that is not valid: %w", raw, err)
			}
			return host, port, true, nil
		}
		if strings.HasSuffix(raw, "]") {
			return strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "["), "", false, nil
		}
		return "", "", false, fmt.Errorf("host %q has a [ without a matching ]", raw)
	}
	if strings.Count(raw, ":") == 1 {
		host, port, ok := strings.Cut(raw, ":")
		if !ok || host == "" || port == "" {
			return "", "", false, fmt.Errorf("host %q has a host or port that is not valid", raw)
		}
		return host, port, true, nil
	}
	return raw, "", false, nil
}

func normalizeHostName(host string) (string, error) {
	host = strings.TrimSpace(strings.ToLower(strings.TrimSuffix(host, ".")))
	if host == "" {
		return "", fmt.Errorf("the host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return strings.ToLower(ip.String()), nil
	}
	if host == "localhost" {
		return host, nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("it has a character or a part that host names cannot have (%v)", err)
	}
	ascii = strings.TrimSuffix(strings.ToLower(ascii), ".")
	if err := validateDomainLabels(ascii); err != nil {
		return "", err
	}
	return ascii, nil
}

// validateDomainLabels checks each part between the dots of a host name.
func validateDomainLabels(host string) error {
	if host == "" {
		return fmt.Errorf("the host is empty")
	}
	if !strings.Contains(host, ".") {
		return fmt.Errorf("a host name needs a dot, as in example.com, unless it is localhost or an IP address")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("the name has an empty part between two dots")
		}
		if len(label) > 63 {
			return fmt.Errorf("the part %q is longer than 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("the part %q must not start or end with a hyphen", label)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return fmt.Errorf("the part %q contains %q, which a host name cannot have", label, r)
		}
	}
	return nil
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil {
		return err
	}
	if n <= 0 || n > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

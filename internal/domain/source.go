package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeSourceIdentity finds the host of a cited page from its URL, its
// domain or both. When the two disagree or neither is usable, the code says
// why the page was left out.
func NormalizeSourceIdentity(rawURL, rawDomain string) (host, code string, err error) {
	urlHost, fieldHost := "", ""
	if strings.TrimSpace(rawURL) != "" {
		u, parseErr := url.Parse(strings.TrimSpace(rawURL))
		if parseErr != nil || u.Scheme == "" || u.Host == "" {
			return "", "invalid_source_url", fmt.Errorf("the URL must be a full URL, such as https://example.com/page")
		}
		urlHost, err = NormalizeObservedHost(u.Hostname())
		if err != nil {
			return "", "invalid_source_url", err
		}
	}
	if strings.TrimSpace(rawDomain) != "" {
		fieldHost, err = NormalizeObservedHost(rawDomain)
		if err != nil {
			return "", "invalid_source_domain", err
		}
	}
	if urlHost != "" && fieldHost != "" && urlHost != fieldHost {
		return "", "source_url_domain_mismatch", fmt.Errorf("the URL is on %q, but the domain says %q", urlHost, fieldHost)
	}
	if fieldHost != "" {
		return fieldHost, "", nil
	}
	if urlHost != "" {
		return urlHost, "", nil
	}
	return "", "missing_source_domain", fmt.Errorf("there is no URL or domain")
}

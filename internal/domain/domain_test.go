package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeScoringDomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "url strips port and path", input: "https://Example.COM:8443/path?q=1", want: "example.com"},
		{name: "bare lower trailing dot", input: "Example.COM.", want: "example.com"},
		{name: "idna", input: "https://Bücher.Example/seite", want: "xn--bcher-kva.example"},
		{name: "wildcard", input: "*.Example.COM.", want: "*.example.com"},
		{name: "localhost", input: "LOCALHOST:8080", want: "localhost"},
		{name: "ip literal", input: "127.0.0.1:8080", want: "127.0.0.1"},
		{name: "ipv6 url", input: "http://[::1]:8080/path", want: "::1"},
		{name: "www dropped", input: "https://www.Example.com/pricing", want: "example.com"},
		{name: "www as the only label left", input: "www.com", want: "www.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeScoringDomain(tt.input)
			if err != nil {
				t.Fatalf("NormalizeScoringDomain() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeScoringDomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeScoringDomainInvalid(t *testing.T) {
	for _, input := range []string{
		"",
		"example.com/path",
		"http://user:pass@example.com",
		"*.localhost",
		"bad_domain.example",
		"example.com:notaport",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := NormalizeScoringDomain(input); err == nil {
				t.Fatalf("NormalizeScoringDomain(%q) succeeded, want error", input)
			}
		})
	}
}

func TestNormalizeScoringDomainsDeduplicates(t *testing.T) {
	got, err := NormalizeScoringDomains([]string{
		"Example.com",
		"https://example.com/path",
		"EXAMPLE.com.",
		"*.Example.com",
		"*.example.com.",
	})
	if err != nil {
		t.Fatalf("NormalizeScoringDomains() error = %v", err)
	}
	want := []string{"example.com", "*.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeScoringDomains() = %#v, want %#v", got, want)
	}
}

func TestNormalizeEndpointHost(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "url preserves port", input: "https://Bücher.Example:8443/api", want: "xn--bcher-kva.example:8443"},
		{name: "bare host port", input: "LOCALHOST:11434", want: "localhost:11434"},
		{name: "url localhost", input: "http://LOCALHOST:11434/v1", want: "localhost:11434"},
		{name: "ipv6 port", input: "[::1]:8080", want: "[::1]:8080"},
		{name: "ipv6 no port", input: "::1", want: "::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEndpointHost(tt.input)
			if err != nil {
				t.Fatalf("NormalizeEndpointHost() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeEndpointHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeEndpointHostInvalid(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: "empty"},
		{input: "*.example.com", want: "wildcard"},
		{input: "example.com/path", want: "host name"},
		{input: "example.com:99999", want: "port"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := NormalizeEndpointHost(tt.input)
			if err == nil {
				t.Fatalf("NormalizeEndpointHost(%q) succeeded, want error", tt.input)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestScoringPolicyMatching(t *testing.T) {
	tests := []struct {
		host   string
		policy string
		want   bool
	}{
		{host: "example.com", policy: "example.com", want: true},
		{host: "docs.example.com", policy: "example.com", want: true},
		{host: "example.com", policy: "*.example.com", want: false},
		{host: "docs.example.com", policy: "*.example.com", want: true},
		{host: "127.0.0.1", policy: "127.0.0.1", want: true},
		{host: "localhost", policy: "localhost", want: true},
		// A site entered as www.example.com must match citations with and
		// without www, which init writes when given https://www.example.com.
		{host: "example.com", policy: "www.example.com", want: true},
		{host: "www.example.com", policy: "www.example.com", want: true},
		{host: "docs.example.com", policy: "www.example.com", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.host+"_"+tt.policy, func(t *testing.T) {
			got, err := MatchesScoringPolicy(tt.host, tt.policy)
			if err != nil {
				t.Fatalf("MatchesScoringPolicy() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("MatchesScoringPolicy(%q, %q) = %v, want %v", tt.host, tt.policy, got, tt.want)
			}
		})
	}
	if _, err := NormalizeObservedHost("*.example.com"); err == nil {
		t.Fatal("NormalizeObservedHost accepted wildcard evidence")
	}
}

func TestScoringPoliciesOverlap(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want bool
	}{
		{a: "example.com", b: "*.example.com", want: true},
		{a: "docs.example.com", b: "*.example.com", want: true},
		{a: "*.docs.example.com", b: "example.com", want: true},
		{a: "example.com", b: "other.example", want: false},
		{a: "localhost", b: "example.com", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got, err := PoliciesOverlap(tt.a, tt.b)
			if err != nil {
				t.Fatalf("PoliciesOverlap() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("PoliciesOverlap(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

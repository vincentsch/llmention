package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/vincentsch/llmention/internal/version"
)

// UserAgent identifies llmention to providers and gateways.
func UserAgent() string { return "llmention/" + version.Version }

// NewHTTPClient returns the client used for provider calls. The timeout
// covers a whole request, including reading the answer, so a provider that
// hangs cannot stall a run.
func NewHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.ResponseHeaderTimeout = timeout
	return &http.Client{Transport: transport, Timeout: timeout}
}

// reply is a provider HTTP response with its body already read.
type reply struct {
	status    int
	header    http.Header
	body      []byte
	requestID string
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

// post sends a JSON body and reads the response. When the request never gets
// an answer, the error names the cause. The caller handles HTTP status codes.
// The key is removed from the response body before anything else reads it.
func (e *baseEngine) post(ctx context.Context, endpoint string, headers map[string]string, body any, secret string) (reply, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return reply{}, &ProviderError{ProviderID: e.provider, SafeMessage: fmt.Sprintf("could not encode the request: %v", err), Class: ErrorClassCaller}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return reply{}, &ProviderError{ProviderID: e.provider, SafeMessage: fmt.Sprintf("could not build the request: %v", err), Class: ErrorClassConfig}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent())
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	client := e.opts.httpClient
	if e.noRedirects {
		copied := *client
		copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copied
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return reply{}, ctxErr
		}
		return reply{}, transportError(e.provider, endpoint, err, client.Timeout)
	}
	defer resp.Body.Close()
	requestID := requestIDFrom(resp.Header)
	raw, overflow, err := readProviderBody(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return reply{}, ctxErr
		}
		return reply{}, providerReadError(e.provider, resp.StatusCode, requestID, err)
	}
	raw = redactSecretBytes(raw, secret)
	if overflow {
		return reply{}, providerOverflowError(e.provider, resp.StatusCode, requestID)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: raw, requestID: requestID}, nil
}

func requestIDFrom(header http.Header) string {
	for _, name := range []string{"x-request-id", "openai-request-id", "request-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// transportError explains why a request never got an HTTP answer, so you
// know whether to fix the base URL or the network.
func transportError(provider, endpoint string, err error, timeout time.Duration) *ProviderError {
	host := endpoint
	if u, parseErr := url.Parse(endpoint); parseErr == nil {
		host = u.Host
	}
	cause, retryable := transportCause(err, timeout)
	message := fmt.Sprintf("could not reach %s: %s", host, cause)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() && timeout > 0 {
		// The server may have been reached and was too slow.
		message = fmt.Sprintf("no complete answer from %s within %s (raise execution.timeout if the model is slow)", host, durationText(timeout))
	}
	return &ProviderError{
		ProviderID:  provider,
		SafeMessage: message,
		Class:       ErrorClassTransient,
		Retryable:   retryable,
	}
}

// durationText writes a duration the way llmention.yaml does, "5m" instead
// of Go's "5m0s".
func durationText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func transportCause(err error, timeout time.Duration) (string, bool) {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return fmt.Sprintf("the host name %s does not exist (DNS)", dnsErr.Name), false
		}
		return fmt.Sprintf("DNS lookup for %s failed", dnsErr.Name), true
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused; is the server running and is the port right?", true
	case errors.As(err, &unknownAuthority), errors.As(err, &certErr):
		return "the TLS certificate is not trusted", false
	case errors.As(err, &hostnameErr):
		return "the TLS certificate does not match the host name", false
	case errors.As(err, &netErr) && netErr.Timeout():
		if timeout > 0 {
			return fmt.Sprintf("no complete answer within %s (raise execution.timeout if the model is slow)", durationText(timeout)), true
		}
		return "the connection timed out", true
	}
	// url.Error repeats the method and URL; the inner error is the cause.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err.Error(), true
}

// statusClass sorts an HTTP status into the error classes the runner uses to
// decide whether to retry.
func statusClass(status int) ErrorClass {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrorClassConfig
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status == 529 || status >= 500:
		return ErrorClassTransient
	case status >= 400:
		return ErrorClassCaller
	default:
		return ErrorClassProvider
	}
}

// prepare runs the checks every adapter makes before it sends anything. The
// run must still be going, the engine must support the request, and the key
// must be set.
func (e *baseEngine) prepare(ctx context.Context, req Request, validate func(Request) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validate(req); err != nil {
		return "", &ProviderError{ProviderID: e.provider, SafeMessage: err.Error(), Class: ErrorClassCaller}
	}
	return e.resolveKey()
}

// finish adds what a response parser cannot know to its error: the provider,
// the HTTP status and the request id.
func (e *baseEngine) finish(answer Answer, err error, resp reply) (Answer, error) {
	if err == nil {
		return answer, nil
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		providerErr.ProviderID = e.provider
		if providerErr.StatusCode == 0 {
			providerErr.StatusCode = resp.status
		}
		if providerErr.RequestID == "" {
			providerErr.RequestID = resp.requestID
		}
		if providerErr.SafeSnippet == "" {
			providerErr.SafeSnippet = SafeResponseSnippet(resp.body)
		}
	}
	return Answer{}, err
}

// httpError builds the error for an HTTP status outside 2xx. message is the
// provider's own explanation, when the body had one. class lets an adapter
// sort the error more precisely than statusClass can.
func httpError(provider string, resp reply, key, message string, class ErrorClass) *ProviderError {
	text := fmt.Sprintf("%s answered with status %d", providerDisplayName(provider), resp.status)
	if message != "" {
		text += ": " + truncateMessage(message)
	}
	return &ProviderError{
		ProviderID:  provider,
		RequestID:   resp.requestID,
		StatusCode:  resp.status,
		SafeMessage: redactSecretString(text, key),
		Class:       class,
		Retryable:   class == ErrorClassTransient,
		RetryAfter:  parseRetryAfter(resp.header),
		SafeSnippet: rawSnippet(resp.body),
	}
}

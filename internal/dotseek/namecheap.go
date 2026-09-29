package dotseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NamecheapAPIResponse struct {
	XMLName         xml.Name        `xml:"ApiResponse"`
	Status          string          `xml:"Status,attr"`
	Errors          ErrorsContainer `xml:"Errors"`
	CommandResponse CommandResponse `xml:"CommandResponse"`
}

type ErrorsContainer struct {
	ErrorList []Error `xml:"Error"`
}

type Error struct {
	Number  string `xml:"Number,attr"`
	Message string `xml:",chardata"`
}

type CommandResponse struct {
	Type              string              `xml:"Type,attr"`
	DomainCheckResult []DomainCheckResult `xml:"DomainCheckResult"`
}

type DomainCheckResult struct {
	Domain                   string `xml:"Domain,attr"`
	Available                string `xml:"Available,attr"`
	IsPremiumName            string `xml:"IsPremiumName,attr"`
	PremiumRegistrationPrice string `xml:"PremiumRegistrationPrice,attr"`
	PremiumRenewalPrice      string `xml:"PremiumRenewalPrice,attr"`
}

func readResponseBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("response size limit must be positive")
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return b, nil
}

type httpStatusError struct {
	statusCode int
	body       string
	retryAfter time.Duration
}

func (e *httpStatusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("HTTP %d", e.statusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", e.statusCode, sanitizeTerminalText(e.body))
}

func isRetryableRequestError(err error) bool {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.statusCode == http.StatusRequestTimeout ||
			statusErr.statusCode == http.StatusTooEarly ||
			statusErr.statusCode == http.StatusTooManyRequests ||
			statusErr.statusCode >= 500
	}
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func retryDelay(err error, attempt int) time.Duration {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.retryAfter > 0 {
		if statusErr.retryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return statusErr.retryAfter
	}
	base := retryBaseDelay * time.Duration(1<<(attempt-1))
	jitter := time.Duration(rand.Int64N(int64(base/5) + 1))
	return base + jitter
}

// summarizeChunk names a batch without dumping every domain it contains.
func summarizeChunk(chunk []string) string {
	switch len(chunk) {
	case 0:
		return "empty batch"
	case 1:
		return chunk[0]
	default:
		return fmt.Sprintf("%d domains %s..%s", len(chunk), chunk[0], chunk[len(chunk)-1])
	}
}

// redactRequestError drops the query string from net/http errors, since the
// Namecheap request carries ApiKey there and the error reaches the terminal.
func redactRequestError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := urlErr.URL
	if parsed, parseErr := url.Parse(redacted); parseErr == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		redacted = parsed.String()
	} else if i := strings.IndexByte(redacted, '?'); i >= 0 {
		redacted = redacted[:i]
	}
	return fmt.Errorf("%s %s: %w", urlErr.Op, redacted, urlErr.Err)
}

// rootCauseMessage returns the innermost error text, so per-domain reporting
// stays to one short line instead of repeating the whole wrapped chain.
func rootCauseMessage(err error) string {
	if err == nil {
		return ""
	}
	for {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return sanitizeTerminalText(err.Error())
		}
		err = unwrapped
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func waitForDuration(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func estimateQueryDuration(domainCount, delayMs int) time.Duration {
	if domainCount <= 0 {
		return 0
	}
	batches := (domainCount + maxDomainsPerAPICall - 1) / maxDomainsPerAPICall
	estimate := time.Duration(batches) * 2 * time.Second
	if batches > 1 {
		estimate += time.Duration(batches-1) * time.Duration(delayMs) * time.Millisecond
	}
	return estimate
}

func printQueryPlan(w io.Writer, total, cacheHits, toCheck, delayMs int) {
	batches := 0
	if toCheck > 0 {
		batches = (toCheck + maxDomainsPerAPICall - 1) / maxDomainsPerAPICall
	}
	fmt.Fprintf(w, "Plan: %d domain(s), %d cached, %d API check(s) in %d batch(es), ETA about %s.\n",
		total, cacheHits, toCheck, batches, estimateQueryDuration(toCheck, delayMs).Round(time.Second))
}

func confirmLargeQuery(r io.Reader, w io.Writer) (bool, error) {
	fmt.Fprint(w, "Proceed with this large query? [y/N] ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func checkDomainChunkAvailabilityOnce(ctx context.Context, client *http.Client, chunk []string) (*NamecheapAPIResponse, error) {
	params := url.Values{}
	params.Add("ApiUser", apiUser)
	params.Add("ApiKey", apiKey)
	params.Add("UserName", username)
	params.Add("ClientIp", clientIP)
	params.Add("Command", "namecheap.domains.check")
	params.Add("DomainList", strings.Join(chunk, ","))
	reqURL := baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", summarizeChunk(chunk), redactRequestError(err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed for %s: %w", summarizeChunk(chunk), redactRequestError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := readResponseBody(resp.Body, 64*1024)
		return nil, &httpStatusError{
			statusCode: resp.StatusCode,
			body:       strings.TrimSpace(string(b)),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	body, err := readResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	var apiResp NamecheapAPIResponse
	cleaned := bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if err := xml.Unmarshal(cleaned, &apiResp); err != nil {
		return nil, fmt.Errorf("decode XML response for %s (%d bytes): %w", summarizeChunk(chunk), len(body), err)
	}
	return &apiResp, nil
}

type retryReporter func(nextAttempt, maxAttempts int, delay time.Duration, err error)

func displayRetry(w io.Writer) retryReporter {
	return func(nextAttempt, maxAttempts int, delay time.Duration, err error) {
		fmt.Fprintf(w, "Retrying API request (attempt %d/%d) in %s: %s\n",
			nextAttempt, maxAttempts, delay.Round(time.Millisecond), sanitizeTerminalText(err.Error()))
	}
}

func checkDomainChunkAvailability(
	ctx context.Context,
	client *http.Client,
	chunk []string,
	reportRetry retryReporter,
) (*NamecheapAPIResponse, error) {
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		attempts = attempt
		apiResp, err := checkDomainChunkAvailabilityOnce(ctx, client, chunk)
		if err == nil {
			return apiResp, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isRetryableRequestError(err) || attempt == maxAPIAttempts {
			break
		}
		delay := retryDelay(err, attempt)
		if reportRetry != nil {
			reportRetry(attempt+1, maxAPIAttempts, delay, err)
		}
		if err := waitForDuration(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("request failed after %d attempt(s): %w", attempts, lastErr)
}

func validateAPIResponse(apiResp *NamecheapAPIResponse, requested []string) ([]DomainCheckResult, error) {
	if apiResp == nil {
		return nil, errors.New("nil API response")
	}

	status := strings.ToUpper(strings.TrimSpace(apiResp.Status))
	if status != "OK" && status != "ERROR" {
		return nil, fmt.Errorf("unexpected API response status %q", apiResp.Status)
	}
	if responseType := strings.TrimSpace(apiResp.CommandResponse.Type); responseType != "" &&
		responseType != "namecheap.domains.check" {
		return nil, fmt.Errorf("unexpected command response type %q", responseType)
	}

	requestedSet := make(map[string]struct{}, len(requested))
	for _, domain := range requested {
		requestedSet[strings.ToLower(domain)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(apiResp.CommandResponse.DomainCheckResult))
	valid := make([]DomainCheckResult, 0, len(apiResp.CommandResponse.DomainCheckResult))
	var validationErrors []string

	for _, result := range apiResp.CommandResponse.DomainCheckResult {
		result.Domain = strings.ToLower(strings.TrimSpace(result.Domain))
		if _, ok := requestedSet[result.Domain]; !ok {
			validationErrors = append(validationErrors, fmt.Sprintf("unexpected domain %q", result.Domain))
			continue
		}
		if _, ok := seen[result.Domain]; ok {
			validationErrors = append(validationErrors, fmt.Sprintf("duplicate domain %q", result.Domain))
			continue
		}
		seen[result.Domain] = struct{}{}

		available := strings.ToLower(strings.TrimSpace(result.Available))
		premium := strings.ToLower(strings.TrimSpace(result.IsPremiumName))
		if (available != "true" && available != "false") ||
			(premium != "true" && premium != "false") {
			validationErrors = append(validationErrors, fmt.Sprintf("invalid boolean attributes for %q", result.Domain))
			continue
		}
		result.Available = available
		result.IsPremiumName = premium
		valid = append(valid, result)
	}

	var hardAPIErrors []string
	softTLDNotFound := false
	for _, apiErr := range apiResp.Errors.ErrorList {
		if apiErr.Number == tldNotFoundErrorNumber {
			softTLDNotFound = true
			continue
		}
		hardAPIErrors = append(hardAPIErrors, apiErr.Number)
	}
	if len(hardAPIErrors) > 0 {
		validationErrors = append(validationErrors, "API error code(s): "+strings.Join(hardAPIErrors, ", "))
	}
	if status != "OK" && !softTLDNotFound {
		validationErrors = append(validationErrors, "API response status was not OK")
	}
	if status == "OK" && !softTLDNotFound && len(valid) != len(requestedSet) {
		validationErrors = append(validationErrors,
			fmt.Sprintf("response returned %d of %d requested domains", len(valid), len(requestedSet)))
	}

	if len(validationErrors) > 0 {
		return valid, errors.New(strings.Join(validationErrors, "; "))
	}
	return valid, nil
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 45 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: transport, Timeout: requestTimeout}
}

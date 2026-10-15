package dotseek

import (
	"context"
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

// newHTTPStatusError captures a bounded copy of the body and the Retry-After
// header from a non-200 response.
func newHTTPStatusError(resp *http.Response) *httpStatusError {
	b, _ := readResponseBody(resp.Body, 64*1024)
	return &httpStatusError{
		statusCode: resp.StatusCode,
		body:       strings.TrimSpace(string(b)),
		retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
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

type retryReporter func(nextAttempt, maxAttempts int, delay time.Duration, err error)

func displayRetry(w io.Writer) retryReporter {
	return func(nextAttempt, maxAttempts int, delay time.Duration, err error) {
		fmt.Fprintf(w, "Retrying API request (attempt %d/%d) in %s: %s\n",
			nextAttempt, maxAttempts, delay.Round(time.Millisecond), sanitizeTerminalText(err.Error()))
	}
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

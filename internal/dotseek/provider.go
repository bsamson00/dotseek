package dotseek

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	providerNamecheap  = "namecheap"
	providerCloudflare = "cloudflare"
)

// domainCheck is one provider-neutral availability result.
type domainCheck struct {
	Domain            string
	Available         bool
	Premium           bool
	RegistrationPrice string
	RenewalPrice      string
	// Unsupported marks a domain whose extension the provider cannot check or
	// register. Available and Premium are meaningless when it is set; Reason
	// carries the provider's explanation.
	Unsupported bool
	Reason      string
}

// batchResult is what a provider returns for one batch request, before the
// shared checks in validateBatchResult run.
type batchResult struct {
	Checks []domainCheck
	// Notices are informational provider messages worth showing the user.
	Notices []string
	// Problems are provider-reported failures that make the batch partially
	// invalid, such as API error codes or malformed result entries.
	Problems []string
	// UnsupportedTLD is set when the provider silently dropped domains whose
	// TLD it does not support (Namecheap behaviour). The count check is then
	// skipped and the missing domains are reported as unsupported.
	UnsupportedTLD bool
}

// domainProvider is implemented by each registrar API that can answer
// availability checks. Implementations build the request and parse the
// response; retries, validation, caching, and display are shared.
type domainProvider interface {
	name() string
	batchSize() int
	credentialHint() string
	checkDomains(ctx context.Context, client *http.Client, chunk []string) (*batchResult, error)
}

func newProvider() domainProvider {
	if providerName == providerCloudflare {
		return cloudflareProvider{}
	}
	return namecheapProvider{}
}

func checkDomainChunkAvailability(
	ctx context.Context,
	provider domainProvider,
	client *http.Client,
	chunk []string,
	reportRetry retryReporter,
) (*batchResult, error) {
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= maxAPIAttempts; attempt++ {
		attempts = attempt
		result, err := provider.checkDomains(ctx, client, chunk)
		if err == nil {
			return result, nil
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

// validateBatchResult drops results for domains that were not requested or
// appear twice, and reports provider problems and incomplete batches as one
// error. The returned checks are usable even when an error is returned.
func validateBatchResult(result *batchResult, requested []string) ([]domainCheck, error) {
	if result == nil {
		return nil, errors.New("nil API response")
	}

	requestedSet := make(map[string]struct{}, len(requested))
	for _, domain := range requested {
		requestedSet[strings.ToLower(domain)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(result.Checks))
	valid := make([]domainCheck, 0, len(result.Checks))
	problems := append([]string{}, result.Problems...)

	for _, check := range result.Checks {
		check.Domain = strings.ToLower(strings.TrimSpace(check.Domain))
		if _, ok := requestedSet[check.Domain]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected domain %q", check.Domain))
			continue
		}
		if _, ok := seen[check.Domain]; ok {
			problems = append(problems, fmt.Sprintf("duplicate domain %q", check.Domain))
			continue
		}
		seen[check.Domain] = struct{}{}
		valid = append(valid, check)
	}

	if len(result.Problems) == 0 && !result.UnsupportedTLD && len(valid) != len(requestedSet) {
		problems = append(problems,
			fmt.Sprintf("response returned %d of %d requested domains", len(valid), len(requestedSet)))
	}
	if len(problems) > 0 {
		return valid, errors.New(strings.Join(problems, "; "))
	}
	return valid, nil
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
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

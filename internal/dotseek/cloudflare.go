package dotseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

type cloudflareCheckRequest struct {
	Domains []string `json:"domains"`
}

type cloudflareAPIResponse struct {
	Success  bool                `json:"success"`
	Errors   []cloudflareMessage `json:"errors"`
	Messages []cloudflareMessage `json:"messages"`
	Result   struct {
		Domains []cloudflareDomainResult `json:"domains"`
	} `json:"result"`
}

type cloudflareMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareDomainResult struct {
	Name        string             `json:"name"`
	Registrable bool               `json:"registrable"`
	Tier        string             `json:"tier"`
	Reason      string             `json:"reason"`
	Pricing     *cloudflarePricing `json:"pricing"`
}

type cloudflarePricing struct {
	Currency         string `json:"currency"`
	RegistrationCost string `json:"registration_cost"`
	RenewalCost      string `json:"renewal_cost"`
}

// cloudflareProvider checks availability with the Cloudflare Registrar API
// (POST /accounts/{account_id}/registrar/domain-check). The token travels in
// the Authorization header, never in the URL.
type cloudflareProvider struct{}

func (cloudflareProvider) name() string { return "Cloudflare" }

func (cloudflareProvider) batchSize() int { return 20 }

func (cloudflareProvider) credentialHint() string {
	return "Set CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN in the environment, .env file, or selected profile."
}

func cloudflareCheckURL() string {
	segment := "registrar"
	if useSandbox {
		segment = "registrar-sandbox"
	}
	return strings.TrimRight(baseURL, "/") + "/accounts/" + url.PathEscape(cfAccountID) + "/" + segment + "/domain-check"
}

func (cloudflareProvider) checkDomains(ctx context.Context, client *http.Client, chunk []string) (*batchResult, error) {
	payload, err := json.Marshal(cloudflareCheckRequest{Domains: chunk})
	if err != nil {
		return nil, fmt.Errorf("encode request for %s: %w", summarizeChunk(chunk), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cloudflareCheckURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", summarizeChunk(chunk), redactRequestError(err))
	}
	req.Header.Set("Authorization", "Bearer "+cfAPIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed for %s: %w", summarizeChunk(chunk), redactRequestError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, newHTTPStatusError(resp)
	}
	body, err := readResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	var apiResp cloudflareAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decode JSON response for %s (%d bytes): %w", summarizeChunk(chunk), len(body), err)
	}
	return cloudflareBatchResult(&apiResp)
}

// cloudflareBatchResult maps the Registrar API result onto the shared model.
// Unsupported extensions are reported per domain, so UnsupportedTLD is never
// set for this provider.
func cloudflareBatchResult(apiResp *cloudflareAPIResponse) (*batchResult, error) {
	if apiResp == nil {
		return nil, fmt.Errorf("nil API response")
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("cloudflare registrar API error: %s", cloudflareMessagesText(apiResp.Errors))
	}

	result := &batchResult{}
	for _, message := range apiResp.Messages {
		result.Notices = append(result.Notices, cloudflareMessageText(message))
	}
	if len(apiResp.Errors) > 0 {
		result.Problems = append(result.Problems, "API error(s): "+cloudflareMessagesText(apiResp.Errors))
	}
	for _, entry := range apiResp.Result.Domains {
		check := domainCheck{Domain: strings.ToLower(strings.TrimSpace(entry.Name))}
		premium := strings.EqualFold(strings.TrimSpace(entry.Tier), "premium")
		switch {
		case entry.Registrable:
			check.Available = true
			check.Premium = premium
		case entry.Reason == "domain_unavailable":
			check.Available = false
			check.Premium = premium
		case entry.Reason == "domain_premium":
			check.Available = true
			check.Premium = true
		default:
			check.Unsupported = true
			check.Reason = cloudflareReasonText(entry.Reason)
		}
		if check.Premium && entry.Pricing != nil {
			check.RegistrationPrice = strings.TrimSpace(entry.Pricing.RegistrationCost)
			check.RenewalPrice = strings.TrimSpace(entry.Pricing.RenewalCost)
		}
		result.Checks = append(result.Checks, check)
	}
	return result, nil
}

func cloudflareReasonText(reason string) string {
	switch reason {
	case "extension_not_supported_via_api":
		return "extension is supported by Cloudflare Registrar in the dashboard but not through the API yet"
	case "extension_not_supported":
		return "extension is not supported by Cloudflare Registrar"
	case "extension_disallows_registration":
		return "the registry is not accepting new registrations for this extension"
	case "":
		return "no availability result was returned"
	default:
		return fmt.Sprintf("unrecognized reason %q", reason)
	}
}

func cloudflareMessageText(message cloudflareMessage) string {
	text := strings.TrimSpace(message.Message)
	if message.Code != 0 {
		return fmt.Sprintf("(%d) %s", message.Code, text)
	}
	return text
}

func cloudflareMessagesText(messages []cloudflareMessage) string {
	if len(messages) == 0 {
		return "no details provided"
	}
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, cloudflareMessageText(message))
	}
	return strings.Join(parts, "; ")
}

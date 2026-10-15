package dotseek

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// namecheapProvider checks availability with namecheap.domains.check. The
// request carries the credentials in the query string, so request errors are
// redacted before they reach the terminal.
type namecheapProvider struct{}

func (namecheapProvider) name() string { return "Namecheap" }

func (namecheapProvider) batchSize() int { return 50 }

func (namecheapProvider) credentialHint() string {
	return "Set the required NAMECHEAP_* values in the environment, .env file, or selected profile."
}

func (namecheapProvider) checkDomains(ctx context.Context, client *http.Client, chunk []string) (*batchResult, error) {
	apiResp, err := namecheapCheckOnce(ctx, client, chunk)
	if err != nil {
		return nil, err
	}
	return namecheapBatchResult(apiResp)
}

func namecheapCheckOnce(ctx context.Context, client *http.Client, chunk []string) (*NamecheapAPIResponse, error) {
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
		return nil, newHTTPStatusError(resp)
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

// namecheapBatchResult converts the XML envelope into the provider-neutral
// shape. Structural surprises (unknown status or command type) are errors;
// per-result problems are collected so the rest of the batch stays usable.
func namecheapBatchResult(apiResp *NamecheapAPIResponse) (*batchResult, error) {
	if apiResp == nil {
		return nil, fmt.Errorf("nil API response")
	}
	status := strings.ToUpper(strings.TrimSpace(apiResp.Status))
	if status != "OK" && status != "ERROR" {
		return nil, fmt.Errorf("unexpected API response status %q", apiResp.Status)
	}
	if responseType := strings.TrimSpace(apiResp.CommandResponse.Type); responseType != "" &&
		responseType != "namecheap.domains.check" {
		return nil, fmt.Errorf("unexpected command response type %q", responseType)
	}

	result := &batchResult{}
	var hardAPIErrors []string
	for _, apiErr := range apiResp.Errors.ErrorList {
		result.Notices = append(result.Notices, fmt.Sprintf("(%s) %s", apiErr.Number, strings.TrimSpace(apiErr.Message)))
		if apiErr.Number == tldNotFoundErrorNumber {
			result.UnsupportedTLD = true
			continue
		}
		hardAPIErrors = append(hardAPIErrors, apiErr.Number)
	}
	if len(hardAPIErrors) > 0 {
		result.Problems = append(result.Problems, "API error code(s): "+strings.Join(hardAPIErrors, ", "))
	}
	if status != "OK" && !result.UnsupportedTLD {
		result.Problems = append(result.Problems, "API response status was not OK")
	}

	for _, entry := range apiResp.CommandResponse.DomainCheckResult {
		domain := strings.ToLower(strings.TrimSpace(entry.Domain))
		available := strings.ToLower(strings.TrimSpace(entry.Available))
		premium := strings.ToLower(strings.TrimSpace(entry.IsPremiumName))
		if (available != "true" && available != "false") ||
			(premium != "true" && premium != "false") {
			result.Problems = append(result.Problems, fmt.Sprintf("invalid boolean attributes for %q", domain))
			continue
		}
		result.Checks = append(result.Checks, domainCheck{
			Domain:            domain,
			Available:         available == "true",
			Premium:           premium == "true",
			RegistrationPrice: strings.TrimSpace(entry.PremiumRegistrationPrice),
			RenewalPrice:      strings.TrimSpace(entry.PremiumRenewalPrice),
		})
	}
	return result, nil
}

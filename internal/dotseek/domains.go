package dotseek

import (
	"bytes"
	"context"
	"embed"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// Precompiled regexes (compiled once at startup rather than per call).
var (
	filterRegex = regexp.MustCompile(`^(<=|>=|<|>|=)?(\d+)$`)
	sldRegex    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	// labelRegex validates a single DNS label (no leading/trailing hyphen), used
	// to reject malformed explicit domains such as "-foo.com".
	labelRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	// Namecheap Handshake domains do not use the public DNS root and cannot
	// provide normal browser, HTTPS, email, DNSSEC, or whois behavior.
	handshakeTLDs = map[string]struct{}{
		"creator": {},
		"elite":   {},
		"ill":     {},
		"oh":      {},
		"oo":      {},
		"oot":     {},
		"orb":     {},
		"p":       {},
		"pgp":     {},
		"saas":    {},
		"sox":     {},
	}
)

//go:embed tlds.txt
var embeddedTldFS embed.FS

type TLDData struct {
	TLD      string
	Keywords []string
}

func readTLDsFromReader(r io.Reader, sourceName string) []TLDData {
	var tldsDataList []TLDData
	if r == nil {
		fmt.Fprintf(commandStderr, "Warning: No TLD data reader provided for '%s'. No TLDs loaded.\n", sourceName)
		return tldsDataList
	}
	reader := csv.NewReader(r)

	reader.FieldsPerRecord = -1
	if _, err := reader.Read(); err != nil {
		if err == io.EOF {
			fmt.Fprintf(commandStderr, "Warning: TLD data from '%s' is empty or contains only a header.\n", sourceName)
		} else {
			fmt.Fprintf(commandStderr, "Warning: Error reading header from TLD data source '%s': %v\n", sourceName, err)
		}
		return tldsDataList
	}
	lineNumber := 1
	for {
		lineNumber++
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(commandStderr, "Warning: Error reading TLD data from '%s' on line %d: %v\n", sourceName, lineNumber, err)
			continue
		}
		if len(record) == 0 || strings.TrimSpace(record[0]) == "" || strings.HasPrefix(strings.TrimSpace(record[0]), "#") {
			continue
		}
		tld, idnaErr := idna.Lookup.ToASCII(strings.TrimSpace(record[0]))
		tld = strings.ToLower(tld)
		if idnaErr != nil {
			fmt.Fprintf(commandStderr, "Warning: Invalid internationalized TLD %q on line %d from %q: %v. Skipping.\n",
				record[0], lineNumber, sourceName, idnaErr)
			continue
		}
		if !isValidTLD(tld) {
			fmt.Fprintf(commandStderr, "Warning: Invalid TLD format '%s' on line %d from '%s'. Skipping.\n", tld, lineNumber, sourceName)
			continue
		}
		if _, isHandshake := handshakeTLDs[tld]; isHandshake {
			fmt.Fprintf(commandStderr, "Warning: Handshake TLD '%s' on line %d from '%s' is not supported. Skipping.\n",
				tld, lineNumber, sourceName)
			continue
		}
		// Keywords may be a single quoted field ("a,b,c") or several unquoted
		// columns (a,b,c); join everything after the TLD so both forms work.
		var keywords []string
		if len(record) > 1 {
			for _, k := range strings.Split(strings.Join(record[1:], ","), ",") {
				if tk := strings.ToLower(strings.TrimSpace(k)); tk != "" {
					keywords = append(keywords, tk)
				}
			}
		}
		tldsDataList = append(tldsDataList, TLDData{TLD: tld, Keywords: keywords})
	}
	return tldsDataList
}

func isValidTLD(tld string) bool {
	if tld == "" || len(tld) > 253 {
		return false
	}
	parts := strings.Split(tld, ".")
	for _, part := range parts {
		if !labelRegex.MatchString(part) {
			return false
		}
	}
	return true
}

func filterTLDsByLength(tldsList []TLDData, lengthFilterStr string) ([]TLDData, error) {
	if lengthFilterStr == "" {
		return tldsList, nil
	}
	matches := filterRegex.FindStringSubmatch(lengthFilterStr)
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid TLD length filter format: %q", lengthFilterStr)
	}
	operator := matches[1]
	if operator == "" {
		operator = "="
	}
	value, err := strconv.Atoi(matches[2])
	if err != nil {
		return nil, fmt.Errorf("invalid number in TLD length filter %q: %w", matches[2], err)
	}
	var filtered []TLDData
	for _, item := range tldsList {
		tldLen := len(item.TLD)
		match := false
		switch operator {
		case "<":
			match = tldLen < value
		case ">":
			match = tldLen > value
		case "<=":
			match = tldLen <= value
		case ">=":
			match = tldLen >= value
		case "=":
			match = tldLen == value
		}
		if match {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func filterTLDsByKeywords(tldsList []TLDData, includeKeywords, excludeKeywords []string) []TLDData {
	hasAny := func(itemKeys, query []string) bool {
		for _, q := range query {
			for _, k := range itemKeys {
				if q == k {
					return true
				}
			}
		}
		return false
	}
	var out []TLDData
	for _, item := range tldsList {
		if len(includeKeywords) > 0 && !hasAny(item.Keywords, includeKeywords) {
			continue
		}
		if len(excludeKeywords) > 0 && hasAny(item.Keywords, excludeKeywords) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// buildDomainQueryList splits inputs into explicit full domains and generated
// base.tld combinations. Inputs that are neither a valid full domain nor a valid
// bare label are returned in skipped so the caller can warn about them.
func buildDomainQueryList(baseInputs []string, tlds []string) (explicit, generated, skipped []string) {
	for _, raw := range baseInputs {
		normalized, err := idna.Lookup.ToASCII(strings.TrimSpace(raw))
		if err != nil {
			skipped = append(skipped, raw)
			continue
		}
		d := strings.ToLower(normalized)
		if d == "" {
			continue
		}
		if strings.Contains(d, ".") {

			parts := strings.Split(d, ".")
			valid := len(parts) >= 2 && len(d) <= 253
			for _, p := range parts {
				if !labelRegex.MatchString(p) {
					valid = false
					break
				}
			}
			if valid {
				explicit = append(explicit, d)
			} else {
				skipped = append(skipped, raw)
			}
		} else if sldRegex.MatchString(d) {
			for _, t := range tlds {
				generated = append(generated, fmt.Sprintf("%s.%s", d, t))
			}
		} else {
			skipped = append(skipped, raw)
		}
	}
	sort.Strings(explicit)
	sort.Slice(generated, func(i, j int) bool {
		ti := strings.SplitN(generated[i], ".", 2)[1]
		tj := strings.SplitN(generated[j], ".", 2)[1]
		if ti != tj {
			return ti < tj
		}
		return generated[i] < generated[j]
	})
	return dedupeStrings(explicit), dedupeStrings(generated), dedupeStrings(skipped)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func fetchTLDsFromURL(ctx context.Context, client *http.Client, u string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TLDs from URL %s: %w", u, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TLDs from URL %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d fetching %s", resp.StatusCode, u)
	}
	b, err := readResponseBody(resp.Body, maxResponseBytes)
	return bytes.NewReader(b), err
}

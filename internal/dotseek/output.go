package dotseek

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type ProcessedResult struct {
	DomainName     string
	ColorPrefix    string
	ColorSuffix    string
	PremiumInfo    string
	SourceInfo     string
	IsAvailable    bool
	TLDDescription string
}

// Output structures for different formats
type DomainResult struct {
	Domain string `json:"domain" yaml:"domain" toml:"domain" xml:"domain"`
	// Status is "available", "unavailable", or "unknown" (no result was obtained,
	// e.g. a failed batch or unsupported TLD). Available/Premium are only
	// meaningful when Status is not "unknown".
	Status                   string `json:"status" yaml:"status" toml:"status" xml:"status"`
	Available                bool   `json:"available" yaml:"available" toml:"available" xml:"available"`
	Premium                  bool   `json:"premium" yaml:"premium" toml:"premium" xml:"premium"`
	TLD                      string `json:"tld,omitempty" yaml:"tld,omitempty" toml:"tld,omitempty" xml:"tld,omitempty"`
	Description              string `json:"description,omitempty" yaml:"description,omitempty" toml:"description,omitempty" xml:"description,omitempty"`
	Source                   string `json:"source,omitempty" yaml:"source,omitempty" toml:"source,omitempty" xml:"source,omitempty"`
	Stale                    bool   `json:"stale,omitempty" yaml:"stale,omitempty" toml:"stale,omitempty" xml:"stale,omitempty"`
	ErrorCode                string `json:"error_code,omitempty" yaml:"error_code,omitempty" toml:"error_code,omitempty" xml:"error_code,omitempty"`
	ErrorMessage             string `json:"error_message,omitempty" yaml:"error_message,omitempty" toml:"error_message,omitempty" xml:"error_message,omitempty"`
	PremiumRegistrationPrice string `json:"premium_registration_price,omitempty" yaml:"premium_registration_price,omitempty" toml:"premium_registration_price,omitempty" xml:"premium_registration_price,omitempty"`
	PremiumRenewalPrice      string `json:"premium_renewal_price,omitempty" yaml:"premium_renewal_price,omitempty" toml:"premium_renewal_price,omitempty" xml:"premium_renewal_price,omitempty"`
}

// domainOutcome records a domain's resolved availability. known is false when no
// result was obtained (failed batch, missing from the API response, etc.).
type domainOutcome struct {
	available                bool
	premium                  bool
	known                    bool
	source                   string
	stale                    bool
	errorCode                string
	errorMessage             string
	premiumRegistrationPrice string
	premiumRenewalPrice      string
}

type OutputData struct {
	XMLName          xml.Name       `xml:"domain_check_results" json:"-" yaml:"-" toml:"-"`
	Timestamp        string         `json:"timestamp" yaml:"timestamp" toml:"timestamp" xml:"timestamp"`
	TotalEvaluated   int            `json:"total_evaluated" yaml:"total_evaluated" toml:"total_evaluated" xml:"total_evaluated"`
	TotalAvailable   int            `json:"total_available" yaml:"total_available" toml:"total_available" xml:"total_available"`
	TotalUnavailable int            `json:"total_unavailable" yaml:"total_unavailable" toml:"total_unavailable" xml:"total_unavailable"`
	TotalUnknown     int            `json:"total_unknown" yaml:"total_unknown" toml:"total_unknown" xml:"total_unknown"`
	TotalPremium     int            `json:"total_premium" yaml:"total_premium" toml:"total_premium" xml:"total_premium"`
	Results          []DomainResult `json:"results" yaml:"results" toml:"results" xml:"results>result"`
}

func formatResultForDisplay(
	domain string,
	isAvailable, isPremium, wasExplicit bool,
	showAll, includePremium bool,
	premiumRegistrationPrice, premiumRenewalPrice string,
	sourceInfo string,
) *ProcessedResult {

	if isPremium && !includePremium {
		if isAvailable {
			hiddenAvailablePremiumCount++
		}
		return nil
	}
	if !isAvailable && !showAll {
		return nil
	}

	prem := ""
	if isPremium {
		prem = " $"
		if premiumRegistrationPrice != "" {
			prem = " $" + premiumRegistrationPrice + "/yr"
		}
		if premiumRenewalPrice != "" && premiumRenewalPrice != premiumRegistrationPrice {
			prem += " (renew $" + premiumRenewalPrice + ")"
		}
	}
	var pre, suf string
	if isAvailable {
		pre, suf = colorGreen, colorReset
	} else if showAll {
		pre, suf = colorRed, colorReset
	}

	var desc string
	if *showTLDDescriptions {
		for _, t := range gSortedTldKeys {
			if strings.HasSuffix(domain, "."+t) {
				desc = gTldKeywordsMap[t]
				break
			}
		}
	}

	return &ProcessedResult{
		DomainName:     domain,
		ColorPrefix:    pre,
		ColorSuffix:    suf,
		PremiumInfo:    prem,
		SourceInfo:     sourceInfo,
		IsAvailable:    isAvailable,
		TLDDescription: desc,
	}
}

// printResultsGrid lays results out in a row-major grid sized for the current
// terminal. Color codes are wrapped around the domain only; padding is computed
// from the visible text width (DomainName + PremiumInfo).
func printResultsGrid(results []ProcessedResult, cellWidth, cols int) {
	for i, r := range results {
		visible := len(r.DomainName) + len(r.PremiumInfo) + len(r.SourceInfo)
		lastInRow := (i+1)%cols == 0 || i == len(results)-1

		pad := cellWidth - visible
		if lastInRow {
			pad = 0
		}

		name, tld := r.DomainName, ""
		if dot := strings.Index(r.DomainName, "."); dot >= 0 {
			name, tld = r.DomainName[:dot], r.DomainName[dot:]
		}
		fmt.Fprintf(commandStdout, "%s%s%s%s%s%s%s%s", colorTLD, name, r.ColorPrefix, tld, colorReset, r.PremiumInfo, r.SourceInfo, strings.Repeat(" ", pad))
		if lastInRow {
			fmt.Fprintln(commandStdout)
		}
	}
}

// tldLength returns the character length of a domain's TLD (everything after
// the first dot, excluding the dot), matching the -l length-filter semantics.
func tldLength(domain string) int {
	if dot := strings.Index(domain, "."); dot >= 0 {
		return len(domain[dot+1:])
	}
	return 0
}

// printResultsByTLDLength groups results by TLD length and prints each group as
// its own grid, separated by a divider, in ascending length order. Result order
// within each group is preserved. All groups share one column width (derived
// from the widest entry across every group) so columns align vertically across
// sections.
func printResultsByTLDLength(results []ProcessedResult) {
	const gap = 2
	groups := map[int][]ProcessedResult{}
	var lengths []int
	maxLen := 0
	for _, r := range results {
		l := tldLength(r.DomainName)
		if _, ok := groups[l]; !ok {
			lengths = append(lengths, l)
		}
		groups[l] = append(groups[l], r)
		if w := len(r.DomainName) + len(r.PremiumInfo) + len(r.SourceInfo); w > maxLen {
			maxLen = w
		}
	}
	cellWidth := maxLen + gap
	columns := gridColumnCount(outputTerminalWidth(commandStdout), cellWidth)
	sort.Ints(lengths)
	for i, l := range lengths {
		if i > 0 {
			fmt.Fprintln(commandStdout, strings.Repeat("-", 50))
		}
		fmt.Fprintf(commandStdout, "%s%d-character TLDs (%d)%s\n", colorYellow, l, len(groups[l]), colorReset)
		printResultsGrid(groups[l], cellWidth, columns)
	}
}

// processAPIResponseChunk caches and formats validated provider results.
// Unsupported domains are neither cached nor displayed; the caller reports
// them as unresolved.
func processAPIResponseChunk(
	checks []domainCheck,
	showAll, includePremium bool,
	explicitMap map[string]bool,
	cacheData map[string]CacheEntry,
	useCache bool,
) []ProcessedResult {
	var out []ProcessedResult
	for _, check := range checks {
		if check.Unsupported {
			continue
		}
		domain := check.Domain
		if useCache {
			addDomainToCache(cacheData, domain, CachedDomainAttributes{
				Domain:                   domain,
				Available:                boolString(check.Available),
				IsPremiumName:            boolString(check.Premium),
				PremiumRegistrationPrice: check.RegistrationPrice,
				PremiumRenewalPrice:      check.RenewalPrice,
			})
		}
		if res := formatResultForDisplay(
			domain, check.Available, check.Premium, explicitMap[domain], showAll, includePremium,
			check.RegistrationPrice, check.RenewalPrice, "",
		); res != nil {
			out = append(out, *res)
		}
	}
	return out
}

func inferFormatFromFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		return "json"
	case ".xml":
		return "xml"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".csv":
		return "csv"
	default:
		return ""
	}
}

func resolveOutputTarget(filename, format string) (string, string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch {
	case format == "" && filename != "":
		format = inferFormatFromFilename(filename)
		if format == "" {
			return "", "", fmt.Errorf("cannot infer format from filename %q; specify it with -f", filename)
		}
	case format != "" && filename == "":
		filename = "results." + format
	case format == "" && filename == "":
		return "", "", errors.New("no output file or format specified")
	}

	validFormats := map[string]bool{
		"json": true,
		"xml":  true,
		"yaml": true,
		"toml": true,
		"csv":  true,
	}
	if !validFormats[format] {
		return "", "", fmt.Errorf("invalid format %q; valid formats: json, xml, yaml, toml, csv", format)
	}
	return filename, format, nil
}

func selectDirectFormat(enabled map[string]bool) (string, error) {
	selected := ""
	for format, on := range enabled {
		if !on {
			continue
		}
		if selected != "" {
			return "", errors.New("only one direct stdout format may be selected")
		}
		selected = format
	}
	return selected, nil
}

func writeOutputFile(filename, format string, data OutputData) error {
	dir := filepath.Dir(filename)
	file, err := os.CreateTemp(dir, ".dotseek-output-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary output file: %w", err)
	}
	tmpName := file.Name()
	defer os.Remove(tmpName)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("failed to secure temporary output file: %w", err)
	}

	if err := encodeOutput(file, format, data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("failed to sync output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close output file: %w", err)
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return fmt.Errorf("failed to replace output file: %w", err)
	}
	return nil
}

func encodeOutput(w io.Writer, format string, data OutputData) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)

	case "xml":
		if _, err := io.WriteString(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"); err != nil {
			return err
		}
		encoder := xml.NewEncoder(w)
		encoder.Indent("", "  ")
		if err := encoder.Encode(data); err != nil {
			return err
		}
		return encoder.Close()

	case "yaml":
		encoder := yaml.NewEncoder(w)
		if err := encoder.Encode(data); err != nil {
			return err
		}
		return encoder.Close()

	case "toml":
		encoder := toml.NewEncoder(w)
		return encoder.Encode(data)

	case "csv":
		writer := csv.NewWriter(w)

		headers := []string{
			"domain", "status", "available", "premium", "tld", "description",
			"source", "stale", "error_code", "error_message",
			"premium_registration_price", "premium_renewal_price",
		}
		if err := writer.Write(headers); err != nil {
			return err
		}

		for _, result := range data.Results {
			record := []string{
				result.Domain,
				result.Status,
				strconv.FormatBool(result.Available),
				strconv.FormatBool(result.Premium),
				result.TLD,
				result.Description,
				result.Source,
				strconv.FormatBool(result.Stale),
				result.ErrorCode,
				result.ErrorMessage,
				result.PremiumRegistrationPrice,
				result.PremiumRenewalPrice,
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}

		if err := writer.Write([]string{
			"# summary",
			fmt.Sprintf("total_evaluated=%d", data.TotalEvaluated),
			fmt.Sprintf("available=%d", data.TotalAvailable),
			fmt.Sprintf("unavailable=%d", data.TotalUnavailable),
			fmt.Sprintf("unknown=%d", data.TotalUnknown),
			fmt.Sprintf("premium=%d", data.TotalPremium),
			"",
			"",
			"",
			"",
			"",
			"",
		}); err != nil {
			return err
		}

		writer.Flush()
		return writer.Error()

	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

package dotseek

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

// rdapResponse captures the fields we surface from an RDAP domain lookup.
type rdapResponse struct {
	Status []string `json:"status"`
	Events []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
	} `json:"events"`
	Entities []struct {
		Roles      []string          `json:"roles"`
		VcardArray []json.RawMessage `json:"vcardArray"`
	} `json:"entities"`
	Nameservers []struct {
		LdhName string `json:"ldhName"`
	} `json:"nameservers"`
}

// lookupWhois returns a formatted whois summary for a taken domain, trying RDAP
// first (clean structured data) and falling back to the system `whois` command.
func lookupWhois(ctx context.Context, client *http.Client, domain string) string {
	if s := rdapWhois(ctx, client, domain); s != "" {
		return s
	}

	if out, err := exec.CommandContext(ctx, "whois", "--", domain).Output(); err == nil && len(out) > 0 {
		return strings.TrimRight(string(out), "\n")
	}
	return "  (whois information unavailable)"
}

// rdapWhois queries rdap.org and returns a concise summary, or "" if RDAP has no
// usable data (so the caller can fall back to system whois).
func rdapWhois(ctx context.Context, client *http.Client, domain string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://rdap.org/domain/"+url.PathEscape(domain), nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := readResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return ""
	}
	var r rdapResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return ""
	}

	var registrar string
	for _, e := range r.Entities {
		for _, role := range e.Roles {
			if role == "registrar" {
				registrar = vcardName(e.VcardArray)
				break
			}
		}
		if registrar != "" {
			break
		}
	}
	var registered, expires string
	for _, ev := range r.Events {
		switch ev.Action {
		case "registration":
			registered = ev.Date
		case "expiration":
			expires = ev.Date
		}
	}
	var ns []string
	for _, n := range r.Nameservers {
		if n.LdhName != "" {
			ns = append(ns, strings.ToLower(n.LdhName))
		}
	}

	var b strings.Builder
	line := func(label, val string) {
		if val != "" {
			fmt.Fprintf(&b, "  %-12s %s\n", label+":", val)
		}
	}
	line("Registrar", registrar)
	line("Registered", shortDate(registered))
	line("Expires", shortDate(expires))
	line("Status", strings.Join(r.Status, ", "))
	line("Nameservers", strings.Join(ns, ", "))
	return strings.TrimRight(b.String(), "\n")
}

// vcardName extracts the "fn" (formatted name) value from a jCard array.
func vcardName(vcard []json.RawMessage) string {
	if len(vcard) < 2 {
		return ""
	}
	var props [][]interface{}
	if err := json.Unmarshal(vcard[1], &props); err != nil {
		return ""
	}
	for _, p := range props {
		if len(p) >= 4 {
			if key, ok := p[0].(string); ok && key == "fn" {
				if v, ok := p[3].(string); ok {
					return v
				}
			}
		}
	}
	return ""
}

// shortDate trims an RFC3339 timestamp to its date portion (YYYY-MM-DD).
func shortDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

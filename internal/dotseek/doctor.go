package dotseek

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type runtimeDependencies struct {
	stdin       *os.File
	stdout      io.Writer
	stderr      io.Writer
	apiClient   *http.Client
	tldClient   *http.Client
	rdapClient  *http.Client
	apiBaseURL  string
	publicIPURL string
	rdapBaseURL string
	now         func() time.Time
}

func defaultRuntimeDependencies() runtimeDependencies {
	return runtimeDependencies{
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		apiClient:   newHTTPClient(),
		tldClient:   newHTTPClient(),
		rdapClient:  newHTTPClient(),
		publicIPURL: "https://api.ipify.org",
		rdapBaseURL: "https://rdap.org",
		now:         time.Now,
	}
}

type doctorCheck struct {
	name    string
	status  string
	detail  string
	failure bool
}

func fetchPublicIP(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readResponseBody(resp.Body, 128)
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("service returned invalid IP %q", ip)
	}
	return ip, nil
}

func checkRDAP(ctx context.Context, client *http.Client, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/domain/example.com", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = readResponseBody(resp.Body, maxResponseBytes)
	return err
}

func runDoctor(ctx context.Context, deps runtimeDependencies, provider domainProvider, activeProfile string) error {
	checks := make([]doctorCheck, 0, 7)
	profileDetail := "environment/.env"
	if activeProfile != "" {
		profileDetail = activeProfile
	}
	profileDetail += ", provider " + provider.name()
	if err := validateEnvVars(provider); err != nil {
		checks = append(checks, doctorCheck{name: "Configuration", status: "FAIL", detail: err.Error(), failure: true})
	} else {
		checks = append(checks, doctorCheck{name: "Configuration", status: "PASS", detail: "profile " + profileDetail})
	}

	// Namecheap authenticates the caller's IP against an allowlist; Cloudflare
	// uses a bearer token, so the IP checks only apply to Namecheap.
	if providerName == providerNamecheap {
		if net.ParseIP(clientIP) == nil {
			checks = append(checks, doctorCheck{name: "Configured client IP", status: "FAIL", detail: "not a valid IP address", failure: true})
		} else {
			checks = append(checks, doctorCheck{name: "Configured client IP", status: "PASS", detail: clientIP})
		}

		if publicIP, err := fetchPublicIP(ctx, deps.tldClient, deps.publicIPURL); err != nil {
			checks = append(checks, doctorCheck{name: "Public IP", status: "WARN", detail: err.Error()})
		} else if publicIP != clientIP {
			checks = append(checks, doctorCheck{name: "Public IP", status: "FAIL", detail: publicIP + " does not match configured IP", failure: true})
		} else {
			checks = append(checks, doctorCheck{name: "Public IP", status: "PASS", detail: publicIP + " matches configuration"})
		}
	}

	cachePath := cacheFilePath()
	cacheProbe, err := os.CreateTemp(filepath.Dir(cachePath), ".doctor-*")
	if err != nil {
		checks = append(checks, doctorCheck{name: "Cache directory", status: "FAIL", detail: err.Error(), failure: true})
	} else {
		probeName := cacheProbe.Name()
		closeErr := cacheProbe.Close()
		removeErr := os.Remove(probeName)
		if closeErr != nil || removeErr != nil {
			checks = append(checks, doctorCheck{name: "Cache directory", status: "FAIL", detail: "write probe cleanup failed", failure: true})
		} else {
			checks = append(checks, doctorCheck{name: "Cache directory", status: "PASS", detail: filepath.Dir(cachePath)})
		}
	}

	apiCheckName := provider.name() + " API"
	batch, apiErr := checkDomainChunkAvailability(ctx, provider, deps.apiClient, []string{"example.com"}, displayRetry(commandStdout))
	if apiErr == nil {
		_, apiErr = validateBatchResult(batch, []string{"example.com"})
	}
	if apiErr != nil {
		checks = append(checks, doctorCheck{name: apiCheckName, status: "FAIL", detail: apiErr.Error(), failure: true})
	} else {
		checks = append(checks, doctorCheck{name: apiCheckName, status: "PASS", detail: "authenticated domain check succeeded"})
	}

	if err := checkRDAP(ctx, deps.rdapClient, deps.rdapBaseURL); err != nil {
		checks = append(checks, doctorCheck{name: "RDAP", status: "WARN", detail: err.Error()})
	} else {
		checks = append(checks, doctorCheck{name: "RDAP", status: "PASS", detail: "rdap.org reachable"})
	}
	if path, err := exec.LookPath("whois"); err != nil {
		checks = append(checks, doctorCheck{name: "whois fallback", status: "WARN", detail: "not installed"})
	} else {
		checks = append(checks, doctorCheck{name: "whois fallback", status: "PASS", detail: path})
	}

	fmt.Fprintln(commandStdout, "DotSeek doctor")
	failures := 0
	for _, check := range checks {
		fmt.Fprintf(commandStdout, "[%s] %-22s %s\n", check.status, check.name, check.detail)
		if check.failure {
			failures++
		}
	}
	fmt.Fprintf(commandStdout, "Summary: %d check(s), %d failure(s)\n", len(checks), failures)
	if failures > 0 {
		return &cliError{code: exitConfig, err: fmt.Errorf("doctor found %d failure(s)", failures), hint: "Resolve the FAIL items above, then run 'dotseek doctor' again."}
	}
	return nil
}

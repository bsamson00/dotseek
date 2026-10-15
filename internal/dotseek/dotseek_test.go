package dotseek

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInferAndResolveOutputFormat(t *testing.T) {
	cases := map[string]string{
		"results.json": "json",
		"data.XML":     "xml",
		"out.yaml":     "yaml",
		"out.yml":      "yaml",
		"cfg.toml":     "toml",
		"sheet.csv":    "csv",
		"noext":        "",
	}
	for input, want := range cases {
		if got := inferFormatFromFilename(input); got != want {
			t.Errorf("inferFormatFromFilename(%q) = %q, want %q", input, got, want)
		}
	}

	filename, format, err := resolveOutputTarget("", "JSON")
	if err != nil || filename != "results.json" || format != "json" {
		t.Fatalf("resolveOutputTarget = %q, %q, %v", filename, format, err)
	}
	if _, _, err := resolveOutputTarget("results.txt", ""); err == nil {
		t.Fatal("resolveOutputTarget accepted an unknown extension")
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := run(ctx, cancel, []string{"version"}, runtimeDependencies{
		stdout: &stdout,
		stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"dotseek ", "commit:", "go:", "tld_catalog: sha256:"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("version output missing %q:\n%s", expected, stdout.String())
		}
	}
}

func unsetEnvironmentForTest(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		value, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestConfigurationProfiles(t *testing.T) {
	unsetEnvironmentForTest(t,
		"NAMECHEAP_API_USER",
		"NAMECHEAP_API_KEY",
		"NAMECHEAP_USERNAME",
		"NAMECHEAP_CLIENT_IP",
		"NAMECHEAP_USE_SANDBOX",
		"DOTSEEK_PROFILE",
	)
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	configDir := filepath.Join(configRoot, "dotseek")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `default_profile = "work"

[profiles.work]
api_user = "profile-user"
api_key = "profile-key"
username = "profile-username"
client_ip = "192.0.2.10"
use_sandbox = true
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	active, err := loadAndSetEnvVars("")
	if err != nil {
		t.Fatal(err)
	}
	if active != "work" || apiUser != "profile-user" || apiKey != "profile-key" ||
		username != "profile-username" || clientIP != "192.0.2.10" || !useSandbox {
		t.Fatalf("profile was not applied: active=%q user=%q username=%q ip=%q sandbox=%v",
			active, apiUser, username, clientIP, useSandbox)
	}

	if providerName != providerNamecheap {
		t.Fatalf("provider = %q, want namecheap for a Namecheap-only profile", providerName)
	}

	t.Setenv("NAMECHEAP_API_USER", "environment-user")
	if _, err := loadAndSetEnvVars("work"); err != nil {
		t.Fatal(err)
	}
	if apiUser != "environment-user" {
		t.Fatalf("environment did not override profile: %q", apiUser)
	}
}

func TestCloudflareProfileAndAutoDetection(t *testing.T) {
	unsetEnvironmentForTest(t,
		"NAMECHEAP_API_USER",
		"NAMECHEAP_API_KEY",
		"NAMECHEAP_USERNAME",
		"NAMECHEAP_CLIENT_IP",
		"NAMECHEAP_USE_SANDBOX",
		"CLOUDFLARE_ACCOUNT_ID",
		"CLOUDFLARE_API_TOKEN",
		"CLOUDFLARE_USE_SANDBOX",
		"DOTSEEK_PROVIDER",
		"DOTSEEK_PROFILE",
	)
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	configDir := filepath.Join(configRoot, "dotseek")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `default_profile = "cf"

[profiles.cf]
provider = "cloudflare"
account_id = "acct123"
api_token = "token123"
use_sandbox = true

[profiles.implicit]
account_id = "acct456"
api_token = "token456"

[profiles.bad]
provider = "godaddy"
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	active, err := loadAndSetEnvVars("")
	if err != nil {
		t.Fatal(err)
	}
	if active != "cf" || providerName != providerCloudflare || cfAccountID != "acct123" ||
		cfAPIToken != "token123" || !useSandbox || baseURL != cloudflareAPIBaseURL {
		t.Fatalf("cloudflare profile was not applied: active=%q provider=%q account=%q sandbox=%v base=%q",
			active, providerName, cfAccountID, useSandbox, baseURL)
	}
	if maxDomainsPerAPICall = newProvider().batchSize(); maxDomainsPerAPICall != 20 {
		t.Fatalf("cloudflare batch size = %d, want 20", maxDomainsPerAPICall)
	}

	if _, err := loadAndSetEnvVars("implicit"); err != nil {
		t.Fatal(err)
	}
	if providerName != providerCloudflare || useSandbox {
		t.Fatalf("token-only profile should auto-detect cloudflare: provider=%q sandbox=%v", providerName, useSandbox)
	}

	t.Setenv("DOTSEEK_PROVIDER", "namecheap")
	if _, err := loadAndSetEnvVars("implicit"); err != nil {
		t.Fatal(err)
	}
	if providerName != providerNamecheap {
		t.Fatalf("DOTSEEK_PROVIDER did not override auto-detection: %q", providerName)
	}
	if err := validateEnvVars(newProvider()); err == nil {
		t.Fatal("namecheap provider accepted a profile without Namecheap credentials")
	}

	if err := os.Unsetenv("DOTSEEK_PROVIDER"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndSetEnvVars("bad"); err == nil {
		t.Fatal("unknown provider name was accepted")
	}

	// Environment only, no profile: Cloudflare credentials alone select Cloudflare.
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOTSEEK_PROVIDER", "")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "env-account")
	t.Setenv("CLOUDFLARE_API_TOKEN", "env-token")
	t.Setenv("CLOUDFLARE_USE_SANDBOX", "1")
	if _, err := loadAndSetEnvVars(""); err != nil {
		t.Fatal(err)
	}
	if providerName != providerCloudflare || cfAccountID != "env-account" || !useSandbox {
		t.Fatalf("environment-only cloudflare setup: provider=%q account=%q sandbox=%v", providerName, cfAccountID, useSandbox)
	}
	t.Setenv("CLOUDFLARE_USE_SANDBOX", "sometimes")
	if _, err := loadAndSetEnvVars(""); err == nil {
		t.Fatal("invalid CLOUDFLARE_USE_SANDBOX value was accepted")
	}
}

func TestDoctorCommand(t *testing.T) {
	t.Setenv("NAMECHEAP_API_USER", "user")
	t.Setenv("NAMECHEAP_API_KEY", "key")
	t.Setenv("NAMECHEAP_USERNAME", "user")
	t.Setenv("NAMECHEAP_CLIENT_IP", "127.0.0.1")
	t.Setenv("NAMECHEAP_USE_SANDBOX", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ip":
			io.WriteString(w, "127.0.0.1")
		case "/api":
			io.WriteString(w, `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check"><DomainCheckResult Domain="example.com" Available="false" IsPremiumName="false"/></CommandResponse></ApiResponse>`)
		case "/domain/example.com":
			io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{"doctor"}, runtimeDependencies{
		stdin:       devNull,
		stdout:      &stdout,
		stderr:      io.Discard,
		apiClient:   server.Client(),
		tldClient:   server.Client(),
		rdapClient:  server.Client(),
		apiBaseURL:  server.URL + "/api",
		publicIPURL: server.URL + "/ip",
		rdapBaseURL: server.URL,
		now:         time.Now,
	})
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, stdout.String())
	}
	for _, expected := range []string{"[PASS] Configuration", "[PASS] Public IP", "[PASS] Namecheap API", "0 failure(s)"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("doctor output missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestDoctorCommandCloudflareSkipsIPChecks(t *testing.T) {
	t.Setenv("DOTSEEK_PROVIDER", "cloudflare")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "acct123")
	t.Setenv("CLOUDFLARE_API_TOKEN", "token123")
	t.Setenv("CLOUDFLARE_USE_SANDBOX", "true")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var ipRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ip":
			ipRequests.Add(1)
			io.WriteString(w, "127.0.0.1")
		case r.URL.Path == "/accounts/acct123/registrar-sandbox/domain-check":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token123" {
				t.Errorf("Authorization = %q", got)
			}
			io.WriteString(w, `{"success":true,"errors":[],"messages":[],"result":{"domains":[{"name":"example.com","registrable":false,"reason":"domain_unavailable"}]}}`)
		case r.URL.Path == "/domain/example.com":
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{"doctor"}, runtimeDependencies{
		stdin:       devNull,
		stdout:      &stdout,
		stderr:      io.Discard,
		apiClient:   server.Client(),
		tldClient:   server.Client(),
		rdapClient:  server.Client(),
		apiBaseURL:  server.URL,
		publicIPURL: server.URL + "/ip",
		rdapBaseURL: server.URL,
		now:         time.Now,
	})
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, stdout.String())
	}
	for _, expected := range []string{"[PASS] Configuration", "provider Cloudflare", "[PASS] Cloudflare API", "0 failure(s)"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("doctor output missing %q:\n%s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Public IP") || strings.Contains(stdout.String(), "client IP") || ipRequests.Load() != 0 {
		t.Fatalf("cloudflare doctor ran IP checks:\n%s", stdout.String())
	}
}

func TestDryRunPrintsPlanWithoutAPIRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\nio,tech\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--dry-run",
		"--no-cache",
		"--tld-file", tldPath,
		"alpha", "beta",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     io.Discard,
		stderr:     &stderr,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("dry run made %d API request(s)", requests.Load())
	}
	if !strings.Contains(stderr.String(), "Plan: 4 domain(s)") || !strings.Contains(stderr.String(), "1 batch(es)") {
		t.Fatalf("unexpected plan:\n%s", stderr.String())
	}
}

func TestLargeQueryConfirmation(t *testing.T) {
	var output bytes.Buffer
	confirmed, err := confirmLargeQuery(strings.NewReader("yes\n"), &output)
	if err != nil || !confirmed {
		t.Fatalf("yes confirmation = %v, %v", confirmed, err)
	}
	confirmed, err = confirmLargeQuery(strings.NewReader("\n"), io.Discard)
	if err != nil || confirmed {
		t.Fatalf("empty confirmation = %v, %v", confirmed, err)
	}
}

func TestTLDHelpers(t *testing.T) {
	if got := tldLength("git.co.uk"); got != 5 {
		t.Fatalf("tldLength(git.co.uk) = %d, want 5", got)
	}
	if got := shortDate("2003-09-15T04:00:00Z"); got != "2003-09-15" {
		t.Fatalf("shortDate returned %q", got)
	}
	for _, tld := range []string{"com", "co.uk", "xn--p1ai"} {
		if !isValidTLD(tld) {
			t.Errorf("isValidTLD(%q) = false", tld)
		}
	}
	for _, tld := range []string{"", "bad..tld", "-bad.com", "bad-.com", "bad_thing"} {
		if isValidTLD(tld) {
			t.Errorf("isValidTLD(%q) = true", tld)
		}
	}
}

func TestFilterTLDs(t *testing.T) {
	all := []TLDData{
		{TLD: "co", Keywords: []string{"country"}},
		{TLD: "com", Keywords: []string{"business"}},
		{TLD: "info", Keywords: []string{"information"}},
	}
	got, err := filterTLDsByLength(all, "<=3")
	if err != nil {
		t.Fatal(err)
	}
	if names := tldNames(got); !reflect.DeepEqual(names, []string{"co", "com"}) {
		t.Fatalf("length filter = %v", names)
	}
	if _, err := filterTLDsByLength(all, "wat"); err == nil {
		t.Fatal("invalid length filter did not fail")
	}
	got = filterTLDsByKeywords(all, []string{"business"}, nil)
	if names := tldNames(got); !reflect.DeepEqual(names, []string{"com"}) {
		t.Fatalf("keyword filter = %v", names)
	}
}

func tldNames(items []TLDData) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.TLD)
	}
	return out
}

func TestReadTLDsAndBuildQueries(t *testing.T) {
	input := "tld,keywords\n" +
		"com,\"commercial,business\"\n" +
		"io,tech,startup\n" +
		"bad-.tld,invalid\n"
	got := readTLDsFromReader(strings.NewReader(input), "test")
	if names := tldNames(got); !reflect.DeepEqual(names, []string{"com", "io"}) {
		t.Fatalf("TLDs = %v", names)
	}
	if !reflect.DeepEqual(got[1].Keywords, []string{"tech", "startup"}) {
		t.Fatalf("unquoted keywords = %v", got[1].Keywords)
	}

	explicit, generated, skipped := buildDomainQueryList(
		[]string{"myapp", "myapp", "valid.com", "valid.com", "-bad.com"}, []string{"com", "com", "io"})
	if !reflect.DeepEqual(explicit, []string{"valid.com"}) {
		t.Fatalf("explicit = %v", explicit)
	}
	if !reflect.DeepEqual(generated, []string{"myapp.com", "myapp.io"}) {
		t.Fatalf("generated = %v", generated)
	}
	if !reflect.DeepEqual(skipped, []string{"-bad.com"}) {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestInternationalizedDomainsAreNormalizedToPunycode(t *testing.T) {
	explicit, generated, skipped := buildDomainQueryList(
		[]string{"bücher", "münchen.de"},
		[]string{"com"},
	)
	if !reflect.DeepEqual(explicit, []string{"xn--mnchen-3ya.de"}) {
		t.Fatalf("explicit IDN = %v", explicit)
	}
	if !reflect.DeepEqual(generated, []string{"xn--bcher-kva.com"}) {
		t.Fatalf("generated IDN = %v", generated)
	}
	if len(skipped) != 0 {
		t.Fatalf("invalid IDN inputs = %v", skipped)
	}
}

func TestReadResponseBodyLimit(t *testing.T) {
	if _, err := readResponseBody(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversized body did not fail")
	}
	got, err := readResponseBody(strings.NewReader("1234"), 4)
	if err != nil || string(got) != "1234" {
		t.Fatalf("bounded body = %q, %v", got, err)
	}
}

func TestEmbeddedCatalogExcludesHandshakeAndDuplicates(t *testing.T) {
	file, err := embeddedTldFS.Open("tlds.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	items := readTLDsFromReader(file, "embedded")
	if len(items) != 577 {
		t.Fatalf("embedded catalog contains %d TLDs, want 577", len(items))
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, isHandshake := handshakeTLDs[item.TLD]; isHandshake {
			t.Errorf("embedded catalog contains Handshake TLD %q", item.TLD)
		}
		if _, duplicate := seen[item.TLD]; duplicate {
			t.Errorf("embedded catalog contains duplicate TLD %q", item.TLD)
		}
		seen[item.TLD] = struct{}{}
	}
}

func TestReadTLDsRejectsHandshake(t *testing.T) {
	var stderr bytes.Buffer
	previousStderr := commandStderr
	commandStderr = &stderr
	defer func() { commandStderr = previousStderr }()

	input := "tld,keywords\ncom,business\ncreator,handshake\nsaas,handshake\n"
	items := readTLDsFromReader(strings.NewReader(input), "test")
	if got := tldNames(items); !reflect.DeepEqual(got, []string{"com"}) {
		t.Fatalf("TLDs = %v, want [com]", got)
	}
	if !strings.Contains(stderr.String(), "Handshake TLD") {
		t.Fatalf("warning omitted Handshake explanation: %q", stderr.String())
	}
}

func validateNamecheap(t *testing.T, apiResp *NamecheapAPIResponse, requested []string) ([]domainCheck, error) {
	t.Helper()
	batch, err := namecheapBatchResult(apiResp)
	if err != nil {
		return nil, err
	}
	return validateBatchResult(batch, requested)
}

func TestValidateNamecheapResponse(t *testing.T) {
	valid := &NamecheapAPIResponse{
		Status: "OK",
		CommandResponse: CommandResponse{
			Type: "namecheap.domains.check",
			DomainCheckResult: []DomainCheckResult{
				{Domain: "EXAMPLE.COM", Available: "True", IsPremiumName: "false"},
			},
		},
	}
	results, err := validateNamecheap(t, valid, []string{"example.com"})
	if err != nil || len(results) != 1 || results[0].Domain != "example.com" || !results[0].Available || results[0].Premium {
		t.Fatalf("valid response = %#v, %v", results, err)
	}

	invalid := &NamecheapAPIResponse{
		Status: "OK",
		CommandResponse: CommandResponse{
			DomainCheckResult: []DomainCheckResult{
				{Domain: "other.com", Available: "maybe", IsPremiumName: "false"},
			},
		},
	}
	if _, err := validateNamecheap(t, invalid, []string{"example.com"}); err == nil {
		t.Fatal("invalid response did not fail")
	}

	soft := &NamecheapAPIResponse{
		Status: "ERROR",
		Errors: ErrorsContainer{ErrorList: []Error{
			{Number: tldNotFoundErrorNumber, Message: "TLD is not supported"},
		}},
	}
	batch, err := namecheapBatchResult(soft)
	if err != nil || !batch.UnsupportedTLD || len(batch.Problems) != 0 {
		t.Fatalf("benign unsupported-TLD batch = %#v, %v", batch, err)
	}
	if _, err := validateBatchResult(batch, []string{"example.invalid"}); err != nil {
		t.Fatalf("benign unsupported-TLD response failed: %v", err)
	}

	if _, err := namecheapBatchResult(&NamecheapAPIResponse{Status: "WAT"}); err == nil {
		t.Fatal("unknown status was accepted")
	}
}

func TestValidateBatchResultRejectsUnexpectedAndDuplicateDomains(t *testing.T) {
	batch := &batchResult{Checks: []domainCheck{
		{Domain: "A.com", Available: true},
		{Domain: "a.com", Available: false},
		{Domain: "stranger.com", Available: true},
	}}
	valid, err := validateBatchResult(batch, []string{"a.com", "b.com"})
	if err == nil {
		t.Fatal("unexpected and duplicate domains were accepted")
	}
	for _, fragment := range []string{"unexpected domain", "duplicate domain", "returned 1 of 2"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("validation error omitted %q: %v", fragment, err)
		}
	}
	if len(valid) != 1 || valid[0].Domain != "a.com" || !valid[0].Available {
		t.Fatalf("valid checks = %#v", valid)
	}
}

func TestCloudflareBatchResultMapping(t *testing.T) {
	apiResp := &cloudflareAPIResponse{Success: true}
	apiResp.Messages = []cloudflareMessage{{Code: 1000, Message: "heads up"}}
	apiResp.Result.Domains = []cloudflareDomainResult{
		{Name: "Free.com", Registrable: true, Tier: "standard", Pricing: &cloudflarePricing{Currency: "USD", RegistrationCost: "8.57", RenewalCost: "8.57"}},
		{Name: "taken.com", Registrable: false, Reason: "domain_unavailable"},
		{Name: "fancy.com", Registrable: false, Reason: "domain_premium", Tier: "premium"},
		{Name: "priced.xyz", Registrable: true, Tier: "premium", Pricing: &cloudflarePricing{Currency: "USD", RegistrationCost: "120.00", RenewalCost: "99.00"}},
		{Name: "nope.horse", Registrable: false, Reason: "extension_not_supported"},
		{Name: "later.uk", Registrable: false, Reason: "extension_not_supported_via_api"},
		{Name: "odd.com", Registrable: false, Reason: "something_new"},
	}
	batch, err := cloudflareBatchResult(apiResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Notices) != 1 || batch.Notices[0] != "(1000) heads up" || batch.UnsupportedTLD || len(batch.Problems) != 0 {
		t.Fatalf("batch metadata = %#v", batch)
	}
	want := map[string]domainCheck{
		"free.com":   {Domain: "free.com", Available: true},
		"taken.com":  {Domain: "taken.com"},
		"fancy.com":  {Domain: "fancy.com", Available: true, Premium: true},
		"priced.xyz": {Domain: "priced.xyz", Available: true, Premium: true, RegistrationPrice: "120.00", RenewalPrice: "99.00"},
	}
	unsupported := map[string]string{
		"nope.horse": "not supported by Cloudflare Registrar",
		"later.uk":   "not through the API yet",
		"odd.com":    `unrecognized reason "something_new"`,
	}
	if len(batch.Checks) != len(want)+len(unsupported) {
		t.Fatalf("checks = %#v", batch.Checks)
	}
	for _, check := range batch.Checks {
		if expected, ok := want[check.Domain]; ok {
			if check != expected {
				t.Errorf("%s = %#v, want %#v", check.Domain, check, expected)
			}
			continue
		}
		fragment, ok := unsupported[check.Domain]
		if !ok || !check.Unsupported || !strings.Contains(check.Reason, fragment) {
			t.Errorf("unsupported mapping for %s = %#v", check.Domain, check)
		}
	}

	failed := &cloudflareAPIResponse{Success: false, Errors: []cloudflareMessage{{Code: 10000, Message: "Authentication error"}}}
	if _, err := cloudflareBatchResult(failed); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("unsuccessful response error = %v", err)
	}
}

func TestAPIRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check"><DomainCheckResult Domain="example.com" Available="true" IsPremiumName="false"/></CommandResponse></ApiResponse>`)
	}))
	defer server.Close()

	previousURL := baseURL
	baseURL = server.URL
	defer func() { baseURL = previousURL }()

	previousDelay := retryBaseDelay
	retryBaseDelay = 10 * time.Millisecond
	defer func() { retryBaseDelay = previousDelay }()

	var retryOutput bytes.Buffer
	response, err := checkDomainChunkAvailability(
		context.Background(),
		namecheapProvider{},
		server.Client(),
		[]string{"example.com"},
		displayRetry(&retryOutput),
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 || len(response.Checks) != 1 {
		t.Fatalf("attempts=%d response=%#v", attempts.Load(), response)
	}
	if strings.Count(retryOutput.String(), "Retrying API request") != 2 {
		t.Fatalf("retry status was not reported:\n%s", retryOutput.String())
	}
}

func TestRequestErrorsOmitAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer server.Close()

	previousURL, previousKey, previousUser := baseURL, apiKey, apiUser
	baseURL, apiKey, apiUser = server.URL, "s3cr3tapikey", "someuser"
	defer func() { baseURL, apiKey, apiUser = previousURL, previousKey, previousUser }()

	previousDelay := retryBaseDelay
	retryBaseDelay = 10 * time.Millisecond
	defer func() { retryBaseDelay = previousDelay }()

	chunk := []string{"a.com", "b.com", "c.com"}
	var retryOutput bytes.Buffer
	_, err := checkDomainChunkAvailability(context.Background(), namecheapProvider{}, server.Client(), chunk, displayRetry(&retryOutput))
	if err == nil {
		t.Fatal("expected the request to fail")
	}

	for name, text := range map[string]string{"error": err.Error(), "retry output": retryOutput.String()} {
		if strings.Contains(text, apiKey) {
			t.Errorf("%s leaks the API key: %s", name, text)
		}
		if strings.Contains(text, "ApiKey") || strings.Contains(text, "DomainList") {
			t.Errorf("%s leaks request query parameters: %s", name, text)
		}
	}
	if !strings.Contains(err.Error(), "3 domains a.com..c.com") {
		t.Errorf("error does not summarize the batch: %s", err)
	}
	if brief := rootCauseMessage(err); strings.Contains(brief, "://") || strings.Contains(brief, "a.com") {
		t.Errorf("per-domain message is not compact: %s", brief)
	}
}

func TestRetryDelayBacksOffInSeconds(t *testing.T) {
	first := retryDelay(errors.New("boom"), 1)
	second := retryDelay(errors.New("boom"), 2)
	if first < 2*time.Second || first > 3*time.Second {
		t.Errorf("first retry delay = %s, want about 2s", first)
	}
	if second < 4*time.Second || second > 6*time.Second {
		t.Errorf("second retry delay = %s, want about 4s", second)
	}
}

func TestSummarizeChunk(t *testing.T) {
	if got := summarizeChunk(nil); got != "empty batch" {
		t.Errorf("empty = %q", got)
	}
	if got := summarizeChunk([]string{"only.com"}); got != "only.com" {
		t.Errorf("single = %q", got)
	}
	if got := summarizeChunk([]string{"a.com", "b.com"}); got != "2 domains a.com..b.com" {
		t.Errorf("multiple = %q", got)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if got := parseRetryAfter("2", time.Now()); got != 2*time.Second {
		t.Fatalf("numeric Retry-After = %s", got)
	}
	now := time.Now().UTC().Truncate(time.Second)
	header := now.Add(3 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(header, now); got != 3*time.Second {
		t.Fatalf("date Retry-After = %s", got)
	}
}

func TestAtomicCacheWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	cache := map[string]CacheEntry{
		"example.com": {
			Timestamp: time.Now().Unix(),
			Attributes: CachedDomainAttributes{
				Domain:        "example.com",
				Available:     "true",
				IsPremiumName: "false",
			},
		},
	}
	if err := saveCache(path, cache); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]CacheEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["example.com"]; !ok {
		t.Fatal("cache entry missing after save")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("cache mode = %o, want 600", got)
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".cache-*.json"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary cache files remain: %v, %v", matches, err)
	}
}

func TestAtomicOutputPreservesDestinationOnEncodingError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOutputFile(path, "invalid", OutputData{}); err == nil {
		t.Fatal("invalid output format did not fail")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("destination changed after failed write: %q", data)
	}
}

func TestCSVUsesFixedSchemaAndIncludesDescription(t *testing.T) {
	var out bytes.Buffer
	data := OutputData{Results: []DomainResult{
		{Domain: "unknown.example", Status: "unknown"},
		{Domain: "example.com", Status: "available", Available: true, TLD: "com", Description: "business"},
	}}
	if err := encodeOutput(&out, "csv", data); err != nil {
		t.Fatal(err)
	}
	header := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(header, "description") {
		t.Fatalf("CSV header omitted description: %q", header)
	}
	records, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for i, record := range records {
		if len(record) != 12 {
			t.Fatalf("record %d has %d fields: %v", i, len(record), record)
		}
	}
}

func TestColorModeAndAdaptiveGrid(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var out bytes.Buffer

	enabled, err := colorEnabled("always", &out)
	if err != nil || !enabled {
		t.Fatalf("always color = %v, %v", enabled, err)
	}
	enabled, err = colorEnabled("never", os.Stdout)
	if err != nil || enabled {
		t.Fatalf("never color = %v, %v", enabled, err)
	}
	if _, err := colorEnabled("sometimes", &out); err == nil {
		t.Fatal("invalid color mode was accepted")
	}

	tests := []struct {
		width, cellWidth, want int
	}{
		{40, 20, 2},
		{10, 20, 1},
		{500, 20, 5},
	}
	for _, test := range tests {
		if got := gridColumnCount(test.width, test.cellWidth); got != test.want {
			t.Fatalf("gridColumnCount(%d, %d) = %d, want %d", test.width, test.cellWidth, got, test.want)
		}
	}
}

func TestCompletionAndManCommands(t *testing.T) {
	for shell, marker := range map[string]string{
		"bash": "complete -F _dotseek_complete dotseek",
		"zsh":  "#compdef dotseek",
		"fish": "complete -c dotseek",
	} {
		var stdout bytes.Buffer
		err := run(context.Background(), func() {}, []string{"completion", shell}, runtimeDependencies{
			stdout: &stdout,
		})
		if err != nil {
			t.Fatalf("%s completion failed: %v", shell, err)
		}
		if !strings.Contains(stdout.String(), marker) {
			t.Fatalf("%s completion omitted %q: %q", shell, marker, stdout.String())
		}
	}

	var stdout bytes.Buffer
	err := run(context.Background(), func() {}, []string{"man"}, runtimeDependencies{stdout: &stdout})
	if err != nil {
		t.Fatalf("man failed: %v", err)
	}
	if !strings.Contains(stdout.String(), ".TH DOTSEEK 1") ||
		!strings.Contains(stdout.String(), ".SH EXIT STATUS") {
		t.Fatalf("man output is incomplete: %q", stdout.String())
	}

	err = run(context.Background(), func() {}, []string{"completion", "powershell"}, runtimeDependencies{stdout: io.Discard})
	if exitCode(err) != exitUsage {
		t.Fatalf("invalid completion shell exit = %d, want %d", exitCode(err), exitUsage)
	}
}

func TestRunProducesAccurateExportWithoutANSI(t *testing.T) {
	t.Setenv("NAMECHEAP_API_USER", "user")
	t.Setenv("NAMECHEAP_API_KEY", "key")
	t.Setenv("NAMECHEAP_USERNAME", "user")
	t.Setenv("NAMECHEAP_CLIENT_IP", "127.0.0.1")
	t.Setenv("NAMECHEAP_USE_SANDBOX", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.URL.Query().Get("DomainList"); got != "example.com" {
			t.Errorf("DomainList = %q", got)
		}
		io.WriteString(w, `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check"><DomainCheckResult Domain="example.com" Available="true" IsPremiumName="true" PremiumRegistrationPrice="12.98" PremiumRenewalPrice="15.98"/></CommandResponse></ApiResponse>`)
	}))
	defer server.Close()

	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "results.json")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--tld-file", tldPath,
		"--no-cache",
		"--delay", "0",
		"--a",
		"--p",
		"--output", outputPath,
		"example", "example",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     &stdout,
		stderr:     &stderr,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if strings.Contains(stdout.String(), "\x1b") || strings.Contains(stderr.String(), "\x1b") {
		t.Fatal("non-terminal output contains ANSI escapes")
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var output OutputData
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.TotalEvaluated != 1 || output.TotalAvailable != 1 ||
		output.TotalUnavailable != 0 || output.TotalUnknown != 0 || output.TotalPremium != 1 {
		t.Fatalf("incorrect totals: %#v", output)
	}
	if got := output.Results[0].PremiumRegistrationPrice; got != "12.98" {
		t.Fatalf("premium registration price = %q", got)
	}
	if got := output.Results[0].PremiumRenewalPrice; got != "15.98" {
		t.Fatalf("premium renewal price = %q", got)
	}
	if !strings.Contains(stdout.String(), "$12.98/yr (renew $15.98)") {
		t.Fatalf("stdout omitted premium pricing: %q", stdout.String())
	}
}

func TestRunReturnsPartialFailureAndExportsUnknown(t *testing.T) {
	t.Setenv("NAMECHEAP_API_USER", "user")
	t.Setenv("NAMECHEAP_API_KEY", "key")
	t.Setenv("NAMECHEAP_USERNAME", "user")
	t.Setenv("NAMECHEAP_CLIENT_IP", "127.0.0.1")
	t.Setenv("NAMECHEAP_USE_SANDBOX", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check"><DomainCheckResult Domain="alpha.com" Available="false" IsPremiumName="false"/></CommandResponse></ApiResponse>`)
	}))
	defer server.Close()

	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "results.json")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--tld-file", tldPath,
		"--no-cache",
		"--delay", "0",
		"--output", outputPath,
		"alpha", "beta",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     io.Discard,
		stderr:     io.Discard,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        time.Now,
	})
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.code != exitPartial {
		t.Fatalf("partial run error = %#v", err)
	}

	data, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var output OutputData
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.TotalEvaluated != 2 || output.TotalUnavailable != 1 || output.TotalUnknown != 1 {
		t.Fatalf("partial totals: %#v", output)
	}
	for _, result := range output.Results {
		if result.Domain == "beta.com" && result.ErrorCode != "invalid_response" {
			t.Fatalf("missing per-domain failure reason: %#v", result)
		}
	}
}

func TestOfflineModeUsesStaleCacheAndReportsMisses(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	cachePath := filepath.Join(configRoot, "dotseek", "cache.json")
	cache := map[string]CacheEntry{
		"alpha.com": {
			Timestamp: time.Now().Add(-48 * time.Hour).Unix(),
			Attributes: CachedDomainAttributes{
				Domain:        "alpha.com",
				Available:     "true",
				IsPremiumName: "false",
			},
		},
	}
	if err := saveCache(cachePath, cache); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "offline mode must not call API", http.StatusInternalServerError)
	}))
	defer server.Close()
	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "offline.json")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--offline",
		"--a",
		"--tld-file", tldPath,
		"--output", outputPath,
		"alpha", "beta",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     &stdout,
		stderr:     io.Discard,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        time.Now,
	})
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.code != exitPartial {
		t.Fatalf("offline result = %#v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("offline mode made %d request(s)", requests.Load())
	}
	if !strings.Contains(stdout.String(), "[stale cache]") || !strings.Contains(stdout.String(), "cache_miss") {
		t.Fatalf("offline output did not label stale/missing results:\n%s", stdout.String())
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var output OutputData
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.TotalAvailable != 1 || output.TotalUnknown != 1 {
		t.Fatalf("offline totals: %#v", output)
	}
}

func TestDirectJSONOutputIsClean(t *testing.T) {
	t.Setenv("NAMECHEAP_API_USER", "user")
	t.Setenv("NAMECHEAP_API_KEY", "key")
	t.Setenv("NAMECHEAP_USERNAME", "user")
	t.Setenv("NAMECHEAP_CLIENT_IP", "127.0.0.1")
	t.Setenv("NAMECHEAP_USE_SANDBOX", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check"><DomainCheckResult Domain="example.com" Available="true" IsPremiumName="false"/></CommandResponse></ApiResponse>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--json",
		"--no-cache",
		"--delay", "0",
		"--tld-file", tldPath,
		"example",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     &stdout,
		stderr:     io.Discard,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output OutputData
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("stdout was not clean JSON: %v\n%s", err, stdout.String())
	}
	if output.TotalAvailable != 1 || len(output.Results) != 1 {
		t.Fatalf("direct JSON output: %#v", output)
	}
}

func TestDirectFormatsAreMutuallyExclusive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := run(ctx, cancel, []string{"--json", "--csv", "example"}, runtimeDependencies{
		stdout: io.Discard,
		stderr: io.Discard,
	})
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.code != exitUsage {
		t.Fatalf("exclusive format error = %#v", err)
	}
}

func TestCloudflareRunEndToEnd(t *testing.T) {
	unsetEnvironmentForTest(t, "NAMECHEAP_API_KEY", "DOTSEEK_PROFILE")
	t.Setenv("DOTSEEK_PROVIDER", "cloudflare")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "acct123")
	t.Setenv("CLOUDFLARE_API_TOKEN", "s3cr3ttoken")
	t.Setenv("CLOUDFLARE_USE_SANDBOX", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/accounts/acct123/registrar/domain-check" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer s3cr3ttoken" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("credentials must not travel in the query string: %q", r.URL.RawQuery)
		}
		var request cloudflareCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !reflect.DeepEqual(request.Domains, []string{"fancy.com", "free.com", "nope.com", "taken.com"}) {
			t.Errorf("requested domains = %v", request.Domains)
		}
		io.WriteString(w, `{"success":true,"errors":[],"messages":[],"result":{"domains":[
			{"name":"free.com","registrable":true,"tier":"standard","pricing":{"currency":"USD","registration_cost":"8.57","renewal_cost":"8.57"}},
			{"name":"taken.com","registrable":false,"reason":"domain_unavailable"},
			{"name":"fancy.com","registrable":false,"reason":"domain_premium","tier":"premium"},
			{"name":"nope.com","registrable":false,"reason":"extension_not_supported_via_api"}
		]}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte("tld,keywords\ncom,business\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{
		"--json",
		"--no-cache",
		"--delay", "0",
		"--tld-file", tldPath,
		"free", "taken", "fancy", "nope",
	}, runtimeDependencies{
		stdin:      devNull,
		stdout:     &stdout,
		stderr:     &stderr,
		apiClient:  server.Client(),
		tldClient:  server.Client(),
		rdapClient: server.Client(),
		apiBaseURL: server.URL,
		now:        time.Now,
	})
	// An unsupported extension is a soft outcome, like Namecheap's TLD-not-found
	// notice, so the run still exits 0 and reports the domain as unknown.
	if err != nil {
		t.Fatalf("run error = %#v\nstderr:\n%s", err, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if !strings.Contains(stderr.String(), "Using Cloudflare API") {
		t.Fatalf("stderr did not name the provider:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "s3cr3ttoken") || strings.Contains(stdout.String(), "s3cr3ttoken") {
		t.Fatal("output leaks the API token")
	}

	var output OutputData
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("stdout was not clean JSON: %v\n%s", err, stdout.String())
	}
	if output.TotalEvaluated != 4 || output.TotalAvailable != 2 || output.TotalUnavailable != 1 ||
		output.TotalUnknown != 1 || output.TotalPremium != 1 {
		t.Fatalf("totals: %#v", output)
	}
	byDomain := map[string]DomainResult{}
	for _, result := range output.Results {
		byDomain[result.Domain] = result
	}
	if r := byDomain["free.com"]; r.Status != "available" || r.Premium || r.Source != "api" {
		t.Errorf("free.com = %#v", r)
	}
	if r := byDomain["taken.com"]; r.Status != "unavailable" || r.Premium {
		t.Errorf("taken.com = %#v", r)
	}
	if r := byDomain["fancy.com"]; r.Status != "available" || !r.Premium || r.PremiumRegistrationPrice != "" {
		t.Errorf("fancy.com = %#v", r)
	}
	if r := byDomain["nope.com"]; r.Status != "unknown" || r.ErrorCode != "unsupported_tld" ||
		!strings.Contains(r.ErrorMessage, "not through the API yet") {
		t.Errorf("nope.com = %#v", r)
	}
}

func TestCloudflareUsesTwentyDomainBatches(t *testing.T) {
	unsetEnvironmentForTest(t, "DOTSEEK_PROFILE")
	t.Setenv("DOTSEEK_PROVIDER", "cloudflare")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var tlds strings.Builder
	tlds.WriteString("tld,keywords\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&tlds, "t%d,test\n", i)
	}
	dir := t.TempDir()
	tldPath := filepath.Join(dir, "tlds.csv")
	if err := os.WriteFile(tldPath, []byte(tlds.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cancel, []string{"--dry-run", "--no-cache", "--tld-file", tldPath, "alpha"}, runtimeDependencies{
		stdin:  devNull,
		stdout: io.Discard,
		stderr: &stderr,
		now:    time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "25 API check(s) in 2 batch(es)") {
		t.Fatalf("unexpected plan for 25 domains:\n%s", stderr.String())
	}
}

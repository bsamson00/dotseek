package dotseek

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
)

type profileConfig struct {
	Provider   string `toml:"provider"`
	APIUser    string `toml:"api_user"`
	APIKey     string `toml:"api_key"`
	Username   string `toml:"username"`
	ClientIP   string `toml:"client_ip"`
	AccountID  string `toml:"account_id"`
	APIToken   string `toml:"api_token"`
	UseSandbox bool   `toml:"use_sandbox"`
}

type dotseekConfig struct {
	DefaultProfile string                   `toml:"default_profile"`
	Profiles       map[string]profileConfig `toml:"profiles"`
}

func configFilePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "dotseek", "config.toml")
}

func loadDotseekConfig() (dotseekConfig, error) {
	var config dotseekConfig
	path := configFilePath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return config, nil
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return config, fmt.Errorf("parse config file %q: %w", path, err)
	}
	return config, nil
}

// sandboxEnvVar names the environment variable that toggles the sandbox for
// the selected provider.
func sandboxEnvVar(provider string) string {
	if provider == providerCloudflare {
		return "CLOUDFLARE_USE_SANDBOX"
	}
	return "NAMECHEAP_USE_SANDBOX"
}

func parseSandboxValue(name, value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s value %q", name, value)
	}
	return parsed, nil
}

// resolveProviderName normalizes an explicit provider choice, or picks one
// from the credentials that are present so existing Namecheap setups keep
// working without a provider setting.
func resolveProviderName(explicit string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case providerNamecheap, providerCloudflare:
		return strings.ToLower(strings.TrimSpace(explicit)), nil
	case "":
	default:
		return "", fmt.Errorf("unknown provider %q; use %q or %q", explicit, providerNamecheap, providerCloudflare)
	}
	if cfAPIToken != "" && apiKey == "" {
		return providerCloudflare, nil
	}
	return providerNamecheap, nil
}

// loadAndSetEnvVars resolves credentials and the provider. Precedence, highest
// first: process environment, the selected profile, then the local .env file.
func loadAndSetEnvVars(requestedProfile string) (string, error) {
	config, err := loadDotseekConfig()
	if err != nil {
		return "", err
	}

	dotEnv, err := godotenv.Read()
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("parse .env file: %w", err)
	}
	value := func(name string) string {
		if envValue, ok := os.LookupEnv(name); ok {
			return envValue
		}
		return dotEnv[name]
	}

	apiUser = value("NAMECHEAP_API_USER")
	apiKey = value("NAMECHEAP_API_KEY")
	username = value("NAMECHEAP_USERNAME")
	clientIP = value("NAMECHEAP_CLIENT_IP")
	cfAccountID = value("CLOUDFLARE_ACCOUNT_ID")
	cfAPIToken = value("CLOUDFLARE_API_TOKEN")
	explicitProvider := value("DOTSEEK_PROVIDER")

	profileName := strings.TrimSpace(requestedProfile)
	if profileName == "" {
		profileName = strings.TrimSpace(value("DOTSEEK_PROFILE"))
	}
	if profileName == "" {
		profileName = strings.TrimSpace(config.DefaultProfile)
	}
	var profile profileConfig
	if profileName != "" {
		var ok bool
		profile, ok = config.Profiles[profileName]
		if !ok {
			return "", fmt.Errorf("profile %q was not found in %s", profileName, configFilePath())
		}
		apiUser = profile.APIUser
		apiKey = profile.APIKey
		username = profile.Username
		clientIP = profile.ClientIP
		cfAccountID = profile.AccountID
		cfAPIToken = profile.APIToken
		if strings.TrimSpace(profile.Provider) != "" {
			explicitProvider = profile.Provider
		}

		override := func(name string, target *string) {
			if envValue, ok := os.LookupEnv(name); ok {
				*target = envValue
			}
		}
		override("NAMECHEAP_API_USER", &apiUser)
		override("NAMECHEAP_API_KEY", &apiKey)
		override("NAMECHEAP_USERNAME", &username)
		override("NAMECHEAP_CLIENT_IP", &clientIP)
		override("CLOUDFLARE_ACCOUNT_ID", &cfAccountID)
		override("CLOUDFLARE_API_TOKEN", &cfAPIToken)
		override("DOTSEEK_PROVIDER", &explicitProvider)
	}

	providerName, err = resolveProviderName(explicitProvider)
	if err != nil {
		return "", err
	}

	sandboxVar := sandboxEnvVar(providerName)
	if profileName == "" {
		useSandbox, err = parseSandboxValue(sandboxVar, value(sandboxVar))
	} else {
		useSandbox = profile.UseSandbox
		if envValue, ok := os.LookupEnv(sandboxVar); ok {
			useSandbox, err = parseSandboxValue(sandboxVar, envValue)
		}
	}
	if err != nil {
		return "", err
	}

	switch {
	case providerName == providerCloudflare:
		baseURL = cloudflareAPIBaseURL
	case useSandbox:
		baseURL = "https://api.sandbox.namecheap.com/xml.response"
	default:
		baseURL = "https://api.namecheap.com/xml.response"
	}
	return profileName, nil
}

func validateEnvVars(provider domainProvider) error {
	var requiredVars []struct {
		name  string
		value string
	}
	if providerName == providerCloudflare {
		requiredVars = []struct {
			name  string
			value string
		}{
			{name: "CLOUDFLARE_ACCOUNT_ID", value: cfAccountID},
			{name: "CLOUDFLARE_API_TOKEN", value: cfAPIToken},
		}
	} else {
		requiredVars = []struct {
			name  string
			value string
		}{
			{name: "NAMECHEAP_API_USER", value: apiUser},
			{name: "NAMECHEAP_API_KEY", value: apiKey},
			{name: "NAMECHEAP_USERNAME", value: username},
			{name: "NAMECHEAP_CLIENT_IP", value: clientIP},
		}
	}
	missingVars := []string{}
	for _, required := range requiredVars {
		if required.value == "" {
			missingVars = append(missingVars, required.name)
		}
	}
	if len(missingVars) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missingVars, ", "))
	}
	fmt.Fprintf(commandStderr, "Using %s API\n", provider.name())
	if providerName == providerNamecheap && clientIP != "" {
		fmt.Fprintf(commandStderr, "Client IP for API: %s\n", clientIP)
	}
	fmt.Fprintln(commandStderr)
	return nil
}

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
	APIUser    string `toml:"api_user"`
	APIKey     string `toml:"api_key"`
	Username   string `toml:"username"`
	ClientIP   string `toml:"client_ip"`
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
	useSandboxValue := strings.TrimSpace(value("NAMECHEAP_USE_SANDBOX"))
	if useSandboxValue != "" {
		parsed, err := strconv.ParseBool(useSandboxValue)
		if err != nil {
			return "", fmt.Errorf("invalid NAMECHEAP_USE_SANDBOX value %q", useSandboxValue)
		}
		useSandbox = parsed
	} else {
		useSandbox = false
	}

	profileName := strings.TrimSpace(requestedProfile)
	if profileName == "" {
		profileName = strings.TrimSpace(value("DOTSEEK_PROFILE"))
	}
	if profileName == "" {
		profileName = strings.TrimSpace(config.DefaultProfile)
	}
	if profileName != "" {
		profile, ok := config.Profiles[profileName]
		if !ok {
			return "", fmt.Errorf("profile %q was not found in %s", profileName, configFilePath())
		}
		apiUser = profile.APIUser
		apiKey = profile.APIKey
		username = profile.Username
		clientIP = profile.ClientIP
		useSandbox = profile.UseSandbox

		if envValue, ok := os.LookupEnv("NAMECHEAP_API_USER"); ok {
			apiUser = envValue
		}
		if envValue, ok := os.LookupEnv("NAMECHEAP_API_KEY"); ok {
			apiKey = envValue
		}
		if envValue, ok := os.LookupEnv("NAMECHEAP_USERNAME"); ok {
			username = envValue
		}
		if envValue, ok := os.LookupEnv("NAMECHEAP_CLIENT_IP"); ok {
			clientIP = envValue
		}
		if envValue, ok := os.LookupEnv("NAMECHEAP_USE_SANDBOX"); ok {
			parsed, err := strconv.ParseBool(strings.TrimSpace(envValue))
			if err != nil {
				return "", fmt.Errorf("invalid NAMECHEAP_USE_SANDBOX value %q", envValue)
			}
			useSandbox = parsed
		}
	}

	if useSandbox {
		baseURL = "https://api.sandbox.namecheap.com/xml.response"
	} else {
		baseURL = "https://api.namecheap.com/xml.response"
	}
	return profileName, nil
}

func validateEnvVars() error {
	requiredVars := []struct {
		name  string
		value string
	}{
		{name: "NAMECHEAP_API_USER", value: apiUser},
		{name: "NAMECHEAP_API_KEY", value: apiKey},
		{name: "NAMECHEAP_USERNAME", value: username},
		{name: "NAMECHEAP_CLIENT_IP", value: clientIP},
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
	fmt.Fprintln(commandStderr, "Using Namecheap API")
	if clientIP != "" {
		fmt.Fprintf(commandStderr, "Client IP for API: %s\n\n", clientIP)
	}
	return nil
}

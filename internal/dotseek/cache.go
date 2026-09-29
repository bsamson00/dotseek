package dotseek

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CachedDomainAttributes struct {
	Domain                   string `json:"Domain"`
	Available                string `json:"Available"`
	IsPremiumName            string `json:"IsPremiumName"`
	PremiumRegistrationPrice string `json:"PremiumRegistrationPrice,omitempty"`
	PremiumRenewalPrice      string `json:"PremiumRenewalPrice,omitempty"`
}

type CacheEntry struct {
	Timestamp  int64                  `json:"timestamp"`
	Attributes CachedDomainAttributes `json:"attributes"`
}

// cacheFilePath resolves the cache file location to ~/.config/dotseek/cache.json
// (honoring XDG_CONFIG_HOME) and ensures the directory exists.
func cacheFilePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "dotseek")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(commandStderr, "Warning: Could not create config dir '%s': %v\n", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fmt.Fprintf(commandStderr, "Warning: Could not secure config dir '%s': %v\n", dir, err)
	}
	return filepath.Join(dir, cacheFilename)
}

func loadCache(cacheFilepath string) map[string]CacheEntry {
	cache := make(map[string]CacheEntry)
	if _, err := os.Stat(cacheFilepath); os.IsNotExist(err) {
		return cache
	}
	file, err := os.Open(cacheFilepath)
	if err != nil {
		fmt.Fprintf(commandStderr, "Warning: Could not open cache file '%s': %v\n", cacheFilepath, err)
		return cache
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&cache); err != nil {
		fmt.Fprintf(commandStderr, "Warning: Could not decode cache file '%s': %v\n", cacheFilepath, err)
		return make(map[string]CacheEntry)
	}
	return cache
}

// cacheMu guards cacheData against concurrent access between the main loop (which
// writes via addDomainToCache) and the quit handlers (which may flush on exit).
var cacheMu sync.Mutex

func saveCache(cacheFilepath string, cacheData map[string]CacheEntry) error {
	cacheMu.Lock()
	data, err := json.MarshalIndent(cacheData, "", "  ")
	cacheMu.Unlock()
	if err != nil {
		return fmt.Errorf("encode cache data: %w", err)
	}

	dir := filepath.Dir(cacheFilepath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".cache-*.json")
	if err != nil {
		return fmt.Errorf("create temporary cache file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary cache file: %w", err)
	}
	data = append(data, '\n')
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary cache file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary cache file: %w", err)
	}
	if err := os.Rename(tmpName, cacheFilepath); err != nil {
		return fmt.Errorf("replace cache file: %w", err)
	}
	return nil
}

func isCacheEntryValid(entry CacheEntry, maxAgeSec int64) bool {
	age := time.Now().Unix() - entry.Timestamp
	return age >= 0 && age < maxAgeSec
}

func parseCachedOutcome(domain string, entry CacheEntry) (domainOutcome, bool) {
	available := strings.ToLower(strings.TrimSpace(entry.Attributes.Available))
	premium := strings.ToLower(strings.TrimSpace(entry.Attributes.IsPremiumName))
	if (available != "true" && available != "false") ||
		(premium != "true" && premium != "false") {
		return domainOutcome{}, false
	}
	if cachedDomain := strings.ToLower(strings.TrimSpace(entry.Attributes.Domain)); cachedDomain != "" && cachedDomain != domain {
		return domainOutcome{}, false
	}
	return domainOutcome{
		available:                available == "true",
		premium:                  premium == "true",
		known:                    true,
		source:                   "cache",
		premiumRegistrationPrice: strings.TrimSpace(entry.Attributes.PremiumRegistrationPrice),
		premiumRenewalPrice:      strings.TrimSpace(entry.Attributes.PremiumRenewalPrice),
	}, true
}

func addDomainToCache(cacheData map[string]CacheEntry, domainName string, attributes CachedDomainAttributes) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheData[domainName] = CacheEntry{
		Timestamp:  time.Now().Unix(),
		Attributes: attributes,
	}
}

func clearCacheFile(cacheFilepath string) {
	if _, err := os.Stat(cacheFilepath); err == nil {
		if err := os.Remove(cacheFilepath); err != nil {
			fmt.Fprintf(commandStderr, "Error: Could not clear cache file '%s': %v\n", cacheFilepath, err)
		} else {
			fmt.Fprintf(commandStderr, "Cache file '%s' cleared.\n", cacheFilepath)
		}
	} else {
		fmt.Fprintf(commandStderr, "Cache file '%s' does not exist. Nothing to clear.\n", cacheFilepath)
	}
}

// flushCache persists whatever progress is in the cache so far. It is wired up in
// main once the cache is loaded, and called by the quit handlers before exiting
// so partial progress isn't lost. No-op until set (e.g. when caching is off).
var flushCache = func() {}

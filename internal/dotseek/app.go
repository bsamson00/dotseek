package dotseek

import (
	"io"
	"os"
	"time"
)

var (
	providerName string
	apiUser      string
	apiKey       string
	username     string
	clientIP     string
	cfAccountID  string
	cfAPIToken   string
	useSandbox   bool

	baseURL              = "https://api.namecheap.com/xml.response"
	maxDomainsPerAPICall = 50 // overwritten from the active provider at run time
	requestTimeout       = 60 * time.Second
	defaultAPIDelayMs    = 1000 // default delay between API batches, in milliseconds
	maxAPIAttempts       = 3
	retryBaseDelay       = 2 * time.Second // first retry waits this long, then doubles
	maxResponseBytes     = int64(4 * 1024 * 1024)
	minRemoteTLDCount    = 100
	largeQueryThreshold  = 200

	// tldNotFoundErrorNumber is Namecheap's per-domain "Tld ... is not found"
	// error; it does not indicate an account or system problem.
	tldNotFoundErrorNumber = "2030280"

	cacheFilename      = "cache.json"
	defaultTldURL      = "https://raw.githubusercontent.com/bsamson00/dotseek/refs/heads/main/internal/dotseek/tlds.txt"
	cacheMaxAgeSeconds = int64(24 * 60 * 60)

	colorGreen                  = "\033[92m"
	colorRed                    = "\033[91m"
	colorYellow                 = "\033[93m"
	colorTLD                    = "\033[90m" // dim gray
	colorReset                  = "\033[0m"
	gTldKeywordsMap             map[string]string
	gSortedTldKeys              []string
	showTLDDescriptions         *bool
	hiddenAvailablePremiumCount int       // Counter for available domains hidden due to premium status
	commandStdin                          = os.Stdin
	commandStdout               io.Writer = os.Stdout
	commandStderr               io.Writer = os.Stderr
)

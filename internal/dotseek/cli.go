package dotseek

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func run(ctx context.Context, cancel context.CancelFunc, args []string, deps runtimeDependencies) error {
	previousStdin, previousStdout, previousStderr := commandStdin, commandStdout, commandStderr
	commandStdin, commandStdout, commandStderr = deps.stdin, deps.stdout, deps.stderr
	defer func() {
		commandStdin, commandStdout, commandStderr = previousStdin, previousStdout, previousStderr
	}()
	if deps.stdin == nil {
		commandStdin = os.Stdin
	}
	if deps.stdout == nil {
		commandStdout = io.Discard
	}
	if deps.stderr == nil {
		commandStderr = io.Discard
	}
	if deps.apiClient == nil {
		deps.apiClient = newHTTPClient()
	}
	if deps.tldClient == nil {
		deps.tldClient = newHTTPClient()
	}
	if deps.rdapClient == nil {
		deps.rdapClient = newHTTPClient()
	}
	if deps.publicIPURL == "" {
		deps.publicIPURL = "https://api.ipify.org"
	}
	if deps.rdapBaseURL == "" {
		deps.rdapBaseURL = "https://rdap.org"
	}
	if deps.now == nil {
		deps.now = time.Now
	}

	configureColors(false)
	gTldKeywordsMap = nil
	gSortedTldKeys = nil
	hiddenAvailablePremiumCount = 0
	restoreTerminal = func() {}
	flushCache = func() {}
	doctorCommand := false
	if len(args) > 0 {
		switch args[0] {
		case "doctor":
			doctorCommand = true
			args = args[1:]
		case "version":
			if len(args) != 1 {
				return &cliError{code: exitUsage, err: errors.New("version does not accept arguments")}
			}
			printVersion(commandStdout)
			return nil
		case "completion":
			if len(args) != 2 {
				return &cliError{code: exitUsage, err: errors.New("usage: dotseek completion bash|zsh|fish")}
			}
			if err := printCompletion(commandStdout, args[1]); err != nil {
				return &cliError{code: exitUsage, err: err}
			}
			return nil
		case "man":
			if len(args) != 1 {
				return &cliError{code: exitUsage, err: errors.New("man does not accept arguments")}
			}
			printManPage(commandStdout)
			return nil
		}
	}
	flag.CommandLine = flag.NewFlagSet("dotseek", flag.ContinueOnError)
	flag.CommandLine.SetOutput(commandStderr)
	domainsArg := flag.String("domains", "", "Comma-separated search terms (alternative to positional args)")
	tldLength := flag.String("l", "", "Filter TLDs by length, e.g. 3, <=4, >2, >=2, <5")
	includeKw := flag.String("k", "", "Include only TLDs matching these comma-separated keywords")
	excludeKw := flag.String("ek", "", "Exclude TLDs matching these comma-separated keywords")
	includePremium := flag.Bool("p", false, "Include premium domains in the results")
	showAll := flag.Bool("a", false, "Show unavailable domains too (default: available only)")
	whois := flag.Bool("w", false, "For full domains: show whois if taken, else report it's available")
	tldFile := flag.String("tld-file", "", "Path to a custom TLD list file (CSV: tld,keywords)")
	refreshTLDs := flag.Bool("refresh-tlds", false, "Explicitly fetch the latest TLD list from the configured remote source")
	profile := flag.String("profile", "", "Use a named profile from the XDG config file")
	cliDesc := flag.Bool("tld-descriptions", false, "Show TLD keyword descriptions (one per line, not grid)")
	flag.BoolVar(cliDesc, "d", false, "Shorthand for --tld-descriptions")
	noCache := flag.Bool("no-cache", false, "Disable the cache for this run")
	offline := flag.Bool("offline", false, "Use cached results only; stale entries are allowed and labeled")
	clearCacheFlg := flag.Bool("clear-cache", false, "Delete the cache file and exit")
	cacheAge := flag.Int64("cache-age", cacheMaxAgeSeconds, "Max cache entry age in seconds")
	delayMs := flag.Int("delay", defaultAPIDelayMs, "Delay between API batches in milliseconds (lower is faster but may hit rate limits)")
	assumeYes := flag.Bool("yes", false, "Skip confirmation for large queries")
	dryRun := flag.Bool("dry-run", false, "Show the query plan without calling the API")
	outputFile := flag.String("output", "", "Output to file (format inferred from extension)")
	flag.StringVar(outputFile, "o", "", "Shorthand for --output")
	outputFormat := flag.String("format", "", "Output format (json|xml|yaml|toml|csv, overrides extension)")
	flag.StringVar(outputFormat, "f", "", "Shorthand for --format")
	jsonStdout := flag.Bool("json", false, "Write JSON directly to stdout")
	xmlStdout := flag.Bool("xml", false, "Write XML directly to stdout")
	yamlStdout := flag.Bool("yaml", false, "Write YAML directly to stdout")
	tomlStdout := flag.Bool("toml", false, "Write TOML directly to stdout")
	csvStdout := flag.Bool("csv", false, "Write CSV directly to stdout")
	colorMode := flag.String("color", "auto", "Color output mode (auto|always|never)")
	versionFlag := flag.Bool("version", false, "Show build and TLD catalog version information")

	flag.Usage = func() {
		fmt.Fprintf(commandStderr, "Usage: %s [opts] <search-term>...\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(commandStderr, "\nChecks <search-term>.<tld> availability across the TLD list. Flags may appear")
		fmt.Fprintln(commandStderr, "before or after the search terms. Results print in an adaptive grid, grouped by")
		fmt.Fprintln(commandStderr, "TLD length (the TLD is colored by availability; the search term is dimmed).")
		fmt.Fprintln(commandStderr, "\nOptions:")
		flag.PrintDefaults()
		fmt.Fprintln(commandStderr, "\nExamples:")
		fmt.Fprintln(commandStderr, "  dotseek myapp                      # check myapp.<tld> for every TLD")
		fmt.Fprintln(commandStderr, "  dotseek myapp -l '<=3'             # only TLDs up to 3 characters")
		fmt.Fprintln(commandStderr, "  dotseek -a -p myapp project       # show all incl. unavailable & premium")
		fmt.Fprintln(commandStderr, "  dotseek -w example.com            # whois if taken, else report available")
		fmt.Fprintln(commandStderr, "  dotseek myapp -o results.json      # also export to JSON (format inferred)")
		fmt.Fprintln(commandStderr, "  dotseek myapp -o data.csv          # also export to CSV")
		fmt.Fprintln(commandStderr, "  dotseek myapp -f yaml              # export to results.yaml (default name)")
		fmt.Fprintln(commandStderr, "  dotseek completion zsh             # generate shell completion")
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &cliError{code: exitUsage, err: err, hint: "Run 'dotseek --help' to review valid options."}
	}

	positional := []string{}
	for args := flag.Args(); len(args) > 0; args = flag.Args() {
		positional = append(positional, args[0])
		if err := flag.CommandLine.Parse(args[1:]); err != nil {
			return &cliError{code: exitUsage, err: err, hint: "Run 'dotseek --help' to review valid options."}
		}
	}
	showTLDDescriptions = cliDesc
	useColor, err := colorEnabled(*colorMode, commandStdout)
	if err != nil {
		return &cliError{code: exitUsage, err: err}
	}
	configureColors(useColor)
	if *versionFlag {
		printVersion(commandStdout)
		return nil
	}

	cachePath := cacheFilePath()
	if *clearCacheFlg {
		clearCacheFile(cachePath)
		return nil
	}

	activeProfile, err := loadAndSetEnvVars(*profile)
	if err != nil {
		return &cliError{code: exitConfig, err: err, hint: "Fix the config file, .env file, or selected profile."}
	}
	if deps.apiBaseURL != "" {
		baseURL = deps.apiBaseURL
	}
	provider := newProvider()
	maxDomainsPerAPICall = provider.batchSize()
	if activeProfile != "" {
		fmt.Fprintf(commandStderr, "Using profile: %s\n", activeProfile)
	}
	if doctorCommand {
		return runDoctor(ctx, deps, provider, activeProfile)
	}

	directFormat, err := selectDirectFormat(map[string]bool{
		"json": *jsonStdout,
		"xml":  *xmlStdout,
		"yaml": *yamlStdout,
		"toml": *tomlStdout,
		"csv":  *csvStdout,
	})
	if err != nil {
		return &cliError{code: exitUsage, err: err}
	}
	if directFormat != "" && (*outputFile != "" || *outputFormat != "") {
		return &cliError{code: exitUsage, err: errors.New("direct stdout formats cannot be combined with --output or --format")}
	}
	if directFormat != "" && *whois {
		return &cliError{code: exitUsage, err: errors.New("direct stdout formats cannot be combined with -w")}
	}

	needsOutput := *outputFile != "" || *outputFormat != "" || directFormat != ""
	var resolvedOutputFile, resolvedOutputFormat string
	if directFormat == "" && needsOutput {
		var err error
		resolvedOutputFile, resolvedOutputFormat, err = resolveOutputTarget(*outputFile, *outputFormat)
		if err != nil {
			return &cliError{code: exitUsage, err: err, hint: "Use a supported extension or pass --format explicitly."}
		}
	}

	if *cacheAge < 0 {
		return &cliError{code: exitUsage, err: errors.New("--cache-age cannot be negative")}
	}
	if *delayMs < 0 {
		return &cliError{code: exitUsage, err: errors.New("--delay cannot be negative")}
	}
	if *offline && *noCache {
		return &cliError{code: exitUsage, err: errors.New("--offline and --no-cache cannot be used together")}
	}
	var inputs []string
	if *domainsArg != "" {
		for _, d := range strings.Split(*domainsArg, ",") {
			if d = strings.TrimSpace(d); d != "" {
				inputs = append(inputs, d)
			}
		}
	}
	for _, d := range positional {
		if d = strings.TrimSpace(d); d != "" {
			inputs = append(inputs, d)
		}
	}
	if len(inputs) == 0 {
		flag.Usage()
		return &cliError{code: exitUsage, err: errors.New("no domains provided"), hint: "Pass a search term, for example: dotseek myapp"}
	}

	useCacheRun := !*noCache
	cacheData := make(map[string]CacheEntry)
	if useCacheRun {
		cacheData = loadCache(cachePath)
	}

	// Load TLDs
	var tldData []TLDData
	if *tldFile != "" {
		fmt.Fprintf(commandStderr, "Info: Loading TLDs from %s\n", *tldFile)
		f, err := os.Open(*tldFile)
		if err != nil {
			return fmt.Errorf("open TLD file: %w", err)
		}
		tldData = readTLDsFromReader(f, *tldFile)
		if err := f.Close(); err != nil {
			return fmt.Errorf("close TLD file: %w", err)
		}
	} else if *refreshTLDs {
		fmt.Fprintln(commandStderr, "Info: Explicitly refreshing TLDs from remote.")
		if rdr, err := fetchTLDsFromURL(ctx, deps.tldClient, defaultTldURL); err == nil {
			tldData = readTLDsFromReader(rdr, defaultTldURL)
			if len(tldData) < minRemoteTLDCount {
				fmt.Fprintf(commandStderr,
					"Warning: Remote TLD source returned only %d usable entries; using embedded list instead.\n",
					len(tldData))
				tldData = nil
			}
		} else {
			fmt.Fprintf(commandStderr, "Warning: Could not refresh TLDs: %v\n", err)
		}
	}
	if len(tldData) == 0 && *tldFile == "" {
		fmt.Fprintln(commandStderr, "Info: Using embedded TLD list")
		embedded, err := embeddedTldFS.Open("tlds.txt")
		if err != nil {
			return fmt.Errorf("open embedded TLD list: %w", err)
		}
		tldData = readTLDsFromReader(embedded, "embedded")
		if err := embedded.Close(); err != nil {
			return fmt.Errorf("close embedded TLD list: %w", err)
		}
	}
	if len(tldData) == 0 {
		return errors.New("no TLDs loaded")
	}
	gTldKeywordsMap = make(map[string]string)
	for _, it := range tldData {
		gTldKeywordsMap[it.TLD] = strings.Join(it.Keywords, ", ")
		gSortedTldKeys = append(gSortedTldKeys, it.TLD)
	}
	sort.Slice(gSortedTldKeys, func(i, j int) bool {
		return len(gSortedTldKeys[i]) > len(gSortedTldKeys[j])
	})

	if *tldLength != "" {
		var err error
		tldData, err = filterTLDsByLength(tldData, *tldLength)
		if err != nil {
			return &cliError{code: exitUsage, err: err}
		}
	}
	var incKW, excKW []string
	for _, k := range strings.Split(*includeKw, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			incKW = append(incKW, k)
		}
	}
	for _, k := range strings.Split(*excludeKw, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			excKW = append(excKW, k)
		}
	}
	if len(incKW) > 0 || len(excKW) > 0 {
		tldData = filterTLDsByKeywords(tldData, incKW, excKW)
	}

	finalTlds := make([]string, len(tldData))
	for i, it := range tldData {
		finalTlds[i] = it.TLD
	}
	finalTlds = dedupeStrings(finalTlds)

	explicit, generated, skipped := buildDomainQueryList(inputs, finalTlds)
	if len(skipped) > 0 {
		fmt.Fprintf(commandStderr, "%sWarning: ignoring %d invalid input(s): %s%s\n",
			colorYellow, len(skipped), strings.Join(skipped, ", "), colorReset)
	}
	all := append(append([]string{}, explicit...), generated...)
	all = dedupeStrings(all)
	onlyBase := len(explicit) == 0
	explicitMap := make(map[string]bool)
	for _, e := range explicit {
		explicitMap[e] = true
	}

	if len(all) == 0 {
		return &cliError{code: exitUsage, err: errors.New("no domains to check; TLD filters may have matched no entries"), hint: "Broaden or remove the TLD filters."}
	}

	if *whois && len(explicit) == 0 {
		return &cliError{code: exitUsage, err: errors.New("-w requires a full domain with a TLD (e.g. example.com)")}
	}

	if useCacheRun {
		flushCache = func() {
			if err := saveCache(cachePath, cacheData); err != nil {
				fmt.Fprintf(commandStderr, "Warning: Could not save progress to cache: %v\n", err)
				return
			}
			fmt.Fprintf(commandStderr, "%sProgress saved to cache.%s\n", colorYellow, colorReset)
		}
	}
	var results []ProcessedResult
	var toCheck []string
	cacheHits := 0
	var totalAvailable, totalUnavailable, totalPremium int
	partialHardFailure := false
	noSupportedResults := false

	outcomes := map[string]domainOutcome{}
	recordOutcome := func(domain string, outcome domainOutcome) {
		if _, exists := outcomes[domain]; exists {
			return
		}
		outcomes[domain] = outcome
		if !outcome.known {
			return
		}
		if outcome.available {
			totalAvailable++
		} else {
			totalUnavailable++
		}
		if outcome.premium {
			totalPremium++
		}
	}
	if useCacheRun {
		fmt.Fprintf(commandStderr, "Evaluating %d domain(s), checking cache...\n", len(all))
		for _, d := range all {
			if ent, ok := cacheData[d]; ok {
				outcome, valid := parseCachedOutcome(d, ent)
				fresh := isCacheEntryValid(ent, *cacheAge)
				if !valid || (!fresh && !*offline) {
					if *offline {
						recordOutcome(d, domainOutcome{
							source:       "cache",
							errorCode:    "invalid_cache_entry",
							errorMessage: "cached result is invalid",
						})
						partialHardFailure = true
					} else {
						toCheck = append(toCheck, d)
					}
					continue
				}
				outcome.stale = !fresh
				cacheHits++
				recordOutcome(d, outcome)
				sourceInfo := ""
				if outcome.stale {
					sourceInfo = " [stale cache]"
				}
				if res := formatResultForDisplay(
					d, outcome.available, outcome.premium, explicitMap[d], *showAll, *includePremium,
					outcome.premiumRegistrationPrice, outcome.premiumRenewalPrice, sourceInfo,
				); res != nil {
					results = append(results, *res)
				}
			} else {
				if *offline {
					recordOutcome(d, domainOutcome{
						source:       "cache",
						errorCode:    "cache_miss",
						errorMessage: "no cached result is available",
					})
					partialHardFailure = true
				} else {
					toCheck = append(toCheck, d)
				}
			}
		}
		fmt.Fprintf(commandStderr, "%d from cache. %d to check via API.\n", cacheHits, len(toCheck))
	} else {
		fmt.Fprintf(commandStderr, "Cache disabled. Checking %d domains via API...\n", len(all))
		toCheck = all
	}

	printQueryPlan(commandStderr, len(all), cacheHits, len(toCheck), *delayMs)
	if *dryRun {
		return nil
	}
	if len(toCheck) > 0 {
		if err := validateEnvVars(provider); err != nil {
			return &cliError{code: exitConfig, err: err, hint: provider.credentialHint()}
		}
	}
	if len(toCheck) > largeQueryThreshold && !*assumeYes && isTerminalFile(commandStdin) {
		confirmed, err := confirmLargeQuery(commandStdin, commandStderr)
		if err != nil {
			return &cliError{code: exitIO, err: fmt.Errorf("read confirmation: %w", err)}
		}
		if !confirmed {
			fmt.Fprintln(commandStderr, "Cancelled.")
			return nil
		}
	}

	startQuitListener(ctx, cancel)
	defer restoreTerminal()

	if len(toCheck) > 0 {
		fmt.Fprintf(commandStderr, "Checking %d domain(s) via API...\n", len(toCheck))
		apiResults := 0
		sawHardError := false
		var chunks [][]string
		for i := 0; i < len(toCheck); i += maxDomainsPerAPICall {
			end := i + maxDomainsPerAPICall
			if end > len(toCheck) {
				end = len(toCheck)
			}
			chunks = append(chunks, toCheck[i:end])
		}
		for i, chunk := range chunks {
			if i > 0 && *delayMs > 0 {
				if err := waitForDuration(ctx, time.Duration(*delayMs)*time.Millisecond); err != nil {
					partialHardFailure = true
					break
				}
			}
			// Animate a spinner while the (blocking) batch call runs.
			var batch *batchResult
			var err error
			label := fmt.Sprintf("Processing API batch %d of %d...", i+1, len(chunks))
			withSpinner(label, func() string {
				batch, err = checkDomainChunkAvailability(ctx, provider, deps.apiClient, chunk, displayRetry(commandStderr))
				return ""
			})
			if err != nil {
				fmt.Fprintf(commandStderr, "\nError checking batch %d of %d (%s): %s\n",
					i+1, len(chunks), summarizeChunk(chunk), sanitizeTerminalText(err.Error()))
				sawHardError = true
				partialHardFailure = true
				brief := rootCauseMessage(err)
				for _, domain := range chunk {
					recordOutcome(domain, domainOutcome{
						source:       "api",
						errorCode:    "request_failed",
						errorMessage: brief,
					})
				}
				continue
			}
			if batch != nil {
				for _, notice := range batch.Notices {
					fmt.Fprintf(commandStderr, "\nAPI notice: %s\n", sanitizeTerminalText(notice))
				}
				if len(batch.Problems) > 0 {
					sawHardError = true
				}
				validChecks, validationErr := validateBatchResult(batch, chunk)
				if validationErr != nil {
					fmt.Fprintf(commandStderr, "\nInvalid API response for batch %d of %d (%s): %v\n",
						i+1, len(chunks), summarizeChunk(chunk), validationErr)
					sawHardError = true
					partialHardFailure = true
				}
				for _, check := range validChecks {
					if check.Unsupported {
						recordOutcome(check.Domain, domainOutcome{
							source:       "api",
							errorCode:    "unsupported_tld",
							errorMessage: sanitizeTerminalText(check.Reason),
						})
						continue
					}
					apiResults++
					recordOutcome(check.Domain, domainOutcome{
						available:                check.Available,
						premium:                  check.Premium,
						known:                    true,
						source:                   "api",
						premiumRegistrationPrice: check.RegistrationPrice,
						premiumRenewalPrice:      check.RenewalPrice,
					})
				}
				for _, domain := range chunk {
					if _, exists := outcomes[domain]; exists {
						continue
					}
					code, message := "missing_result", "API response did not include this domain"
					if batch.UnsupportedTLD && validationErr == nil {
						code, message = "unsupported_tld", fmt.Sprintf("TLD is not supported by the %s API", provider.name())
					} else if validationErr != nil {
						code, message = "invalid_response", sanitizeTerminalText(validationErr.Error())
					}
					recordOutcome(domain, domainOutcome{
						source:       "api",
						errorCode:    code,
						errorMessage: message,
					})
				}
				chunkRes := processAPIResponseChunk(validChecks, *showAll, *includePremium, explicitMap, cacheData, useCacheRun)
				results = append(results, chunkRes...)
			}
		}
		fmt.Fprint(commandStderr, "\r"+strings.Repeat(" ", 70)+"\r")
		fmt.Fprintln(commandStderr, "API checks complete.")

		if apiResults == 0 {
			if sawHardError {
				fmt.Fprintf(commandStderr, "%sAPI calls failed; unresolved domains will be reported as unknown.%s\n", colorRed, colorReset)
			} else {
				fmt.Fprintf(commandStderr, "%sNone of the requested TLDs are supported by the API; unresolved domains will be reported as unknown.%s\n", colorRed, colorReset)
				noSupportedResults = true
			}
		}
	}
	for _, domain := range all {
		if _, exists := outcomes[domain]; !exists {
			recordOutcome(domain, domainOutcome{
				errorCode:    "not_checked",
				errorMessage: "domain was not checked before the run stopped",
			})
		}
	}
	totalUnknown := 0
	for _, outcome := range outcomes {
		if !outcome.known {
			totalUnknown++
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].IsAvailable != results[j].IsAvailable {
			return results[i].IsAvailable
		}
		return results[i].DomainName < results[j].DomainName
	})

	// Prepare output data if needed
	var outputData OutputData

	if needsOutput {
		outputData.Timestamp = deps.now().Format(time.RFC3339)
		outputData.TotalEvaluated = len(all)
		outputData.TotalAvailable = totalAvailable
		outputData.TotalUnavailable = totalUnavailable
		outputData.TotalUnknown = totalUnknown
		outputData.TotalPremium = totalPremium
	}

	if directFormat == "" {

		fmt.Fprintf(commandStdout, "\n--- Results (%d matching criteria) ---\n", len(results))
		if len(results) == 0 {
			fmt.Fprintln(commandStdout, "No domains matched your criteria.")
		} else {
			if *showTLDDescriptions {
				for _, r := range results {
					if r.TLDDescription != "" {
						fmt.Fprintf(commandStdout, "%s%s%s\t\t%s%s%s\n", r.ColorPrefix, r.DomainName, r.ColorSuffix, r.TLDDescription, r.PremiumInfo, r.SourceInfo)
					} else {
						fmt.Fprintf(commandStdout, "%s%s%s%s%s\n", r.ColorPrefix, r.DomainName, r.ColorSuffix, r.PremiumInfo, r.SourceInfo)
					}
				}
			} else {
				printResultsByTLDLength(results)
			}
		}
		if totalUnknown > 0 {
			fmt.Fprintln(commandStdout, "\n--- Unresolved ---")
			for _, domain := range all {
				outcome := outcomes[domain]
				if outcome.known {
					continue
				}
				fmt.Fprintf(commandStdout, "%s: %s (%s)\n", domain, outcome.errorMessage, outcome.errorCode)
			}
		}
	}

	if useCacheRun {
		if err := saveCache(cachePath, cacheData); err != nil {
			fmt.Fprintf(commandStderr, "Warning: Could not save cache: %v\n", err)
			partialHardFailure = true
		}
	}

	if *whois {
		fmt.Fprintf(commandStdout, "\n--- Whois ---\n")
		for _, d := range explicit {
			o := outcomes[d]
			switch {
			case !o.known:
				fmt.Fprintf(commandStdout, "%s%s%s: could not determine availability; skipping whois.\n", colorYellow, d, colorReset)
			case o.available:
				fmt.Fprintf(commandStdout, "%s%s is available%s — not registered, so there is no whois to show.\n", colorGreen, d, colorReset)
			default:
				fmt.Fprintf(commandStdout, "%s%s is taken.%s\n", colorRed, d, colorReset)
				info := withSpinner("looking up whois for "+d+"...", func() string {
					return lookupWhois(ctx, deps.rdapClient, d)
				})
				fmt.Fprintln(commandStdout, info)
			}
		}
	}

	restoreTerminal()

	if hiddenAvailablePremiumCount > 0 {
		fmt.Fprintf(commandStderr, "\n%sNote: %d premium domain(s) available.\nUse -p flag to show%s\n",
			colorYellow, hiddenAvailablePremiumCount, colorReset)
	}
	if useSandbox {
		fmt.Fprintf(commandStderr, "%sNote: Using %s SANDBOX environment.%s\n", colorYellow, provider.name(), colorReset)
	}
	fmt.Fprintf(commandStderr, "\nFinished. Evaluated %d domains total.\n", len(all))

	if directFormat == "" && onlyBase && len(all) > 0 {
		fmt.Fprintln(commandStdout, "\n--- Summary of All Evaluated Domains ---")
		fmt.Fprintf(commandStdout, "Available:   %s%d%s\n", colorGreen, totalAvailable, colorReset)
		fmt.Fprintf(commandStdout, "Unavailable: %s%d%s\n", colorRed, totalUnavailable, colorReset)
		fmt.Fprintf(commandStdout, "Unknown:     %s%d%s\n", colorYellow, totalUnknown, colorReset)
		fmt.Fprintf(commandStdout, "Premium:     %s%d%s\n", colorYellow, totalPremium, colorReset)
	}

	if needsOutput {
		// Build results from the resolved outcomes (not cacheData), so exports are
		// correct even with -no-cache and never mislabel unresolved domains.
		var allDomainResults []DomainResult

		for _, d := range all {
			o := outcomes[d]
			dr := DomainResult{
				Domain:                   d,
				Available:                o.available,
				Premium:                  o.premium,
				Source:                   o.source,
				Stale:                    o.stale,
				ErrorCode:                o.errorCode,
				ErrorMessage:             o.errorMessage,
				PremiumRegistrationPrice: o.premiumRegistrationPrice,
				PremiumRenewalPrice:      o.premiumRenewalPrice,
			}
			switch {
			case !o.known:
				dr.Status = "unknown"
			case o.available:
				dr.Status = "available"
			default:
				dr.Status = "unavailable"
			}

			parts := strings.SplitN(d, ".", 2)
			if len(parts) == 2 {
				dr.TLD = parts[1]
			}

			if *showTLDDescriptions {
				for _, t := range gSortedTldKeys {
					if strings.HasSuffix(d, "."+t) {
						dr.Description = gTldKeywordsMap[t]
						break
					}
				}
			}

			allDomainResults = append(allDomainResults, dr)
		}

		sort.Slice(allDomainResults, func(i, j int) bool {
			if allDomainResults[i].Available != allDomainResults[j].Available {
				return allDomainResults[i].Available
			}
			return allDomainResults[i].Domain < allDomainResults[j].Domain
		})

		outputData.Results = allDomainResults

		if directFormat != "" {
			if err := encodeOutput(commandStdout, directFormat, outputData); err != nil {
				return &cliError{code: exitIO, err: fmt.Errorf("write %s output: %w", directFormat, err)}
			}
		} else {
			if err := writeOutputFile(resolvedOutputFile, resolvedOutputFormat, outputData); err != nil {
				return &cliError{code: exitIO, err: fmt.Errorf("write output file: %w", err)}
			}
			fmt.Fprintf(commandStderr, "\nResults written to: %s (format: %s)\n", resolvedOutputFile, resolvedOutputFormat)
		}
	}

	if ctx.Err() != nil {
		return &cliError{code: exitInterrupted, err: errors.New("operation interrupted")}
	}
	if partialHardFailure {
		hint := "Retry the command; cached successful results will be reused."
		if *offline {
			hint = "Run again without --offline to check cache misses and invalid entries."
		}
		return &cliError{code: exitPartial, err: fmt.Errorf("run completed with partial results; %d domain(s) remain unknown", totalUnknown), hint: hint}
	}
	if noSupportedResults {
		return &cliError{code: exitAPI, err: errors.New("none of the requested TLDs are supported by the API"), hint: "Try a different TLD or refresh the TLD catalog."}
	}
	return nil
}

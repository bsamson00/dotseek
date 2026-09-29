# DotSeek

**DotSeek** is a powerful command-line tool for checking domain availability using the Namecheap API. It provides intelligent caching, TLD filtering, batch processing capabilities, and flexible export options to help you find the perfect domain name efficiently.

## Features

- **Fast Domain Checking**: Batch API calls with intelligent caching
- **Adaptive Display**: Results fit the current terminal width and group by TLD length
- **TLD Filtering**: Filter by length, keywords, or custom criteria
- **Premium Pricing**: Show registration and renewal prices when Namecheap provides them
- **Whois Lookup**: With `-w`, show whois details for taken full domains
- **Smart Caching**: Configurable cache, offline mode, stale labeling, and atomic writes
- **Live Progress**: Animated spinner while checking the API and fetching whois
- **Query Preview**: Show API batch count and estimated duration before checking
- **Resilient Requests**: Visible, bounded retries for throttling and transient failures
- **Interactive Quit**: Press `q` (or Ctrl+C) to stop; progress so far is saved to cache
- **Controlled Color**: Automatic terminal detection plus `--color` and `NO_COLOR`
- **Flexible Flag Order**: Flags may appear before or after the search terms
- **Automation Output**: Write JSON, XML, YAML, TOML, or CSV to a file or stdout
- **International Domains**: Normalize Unicode input to IDNA/Punycode automatically
- **Diagnostics**: Built-in doctor, version, completion, and man-page commands
- **Multiple TLD Sources**: Remote URL, local file, or embedded TLD lists
- **Sandbox Support**: Test with Namecheap's sandbox environment
- **TLD Descriptions**: Optional keyword descriptions for TLD context

## Installation

### Prerequisites

- Go 1.26.5 or higher (see `go.mod`)
- Namecheap API account with API access enabled
- `whois` command available on PATH (optional; used as a fallback for `-w` on TLDs RDAP doesn't cover)

### Install with Go

```bash
go install github.com/bsamson00/dotseek@latest
```

### Install Script

From a clone of the repository, `install.sh` builds DotSeek into
`$DOTSEEK_INSTALL_DIR`, `$GOBIN`, or `~/.local/bin` (first one set). If the
repository contains a `.env`, it is converted into a profile in
`~/.config/dotseek/config.toml` so DotSeek works from any directory:

```bash
git clone https://github.com/bsamson00/dotseek.git
cd dotseek
./install.sh
```

### Build from Source

```bash
git clone https://github.com/bsamson00/dotseek.git
cd dotseek
go build -o dotseek
```

## Configuration

### Required Environment Variables

Copy `.env.example` to `.env` in your working directory and fill in your
values, or set these environment variables:

```bash
NAMECHEAP_API_USER=your_api_username
NAMECHEAP_API_KEY=your_api_key  
NAMECHEAP_USERNAME=your_account_username
NAMECHEAP_CLIENT_IP=your_whitelisted_ip
```

### Optional Environment Variables

```bash
NAMECHEAP_USE_SANDBOX=true  # Use sandbox environment for testing
```

### Named Profiles

Profiles live at `$XDG_CONFIG_HOME/dotseek/config.toml` (normally
`~/.config/dotseek/config.toml`):

```toml
default_profile = "production"

[profiles.production]
api_user = "your_api_username"
api_key = "your_api_key"
username = "your_account_username"
client_ip = "203.0.113.10"
use_sandbox = false

[profiles.sandbox]
api_user = "your_sandbox_username"
api_key = "your_sandbox_key"
username = "your_sandbox_username"
client_ip = "203.0.113.10"
use_sandbox = true
```

Select one with `--profile sandbox` or `DOTSEEK_PROFILE=sandbox`. Explicit
process environment variables override profile values; profile values override
the local `.env` baseline.

### Getting Namecheap API Credentials

1. Log into your Namecheap account
2. Go to Profile → Tools → Namecheap API
3. Enable API access and whitelist your IP address
4. Generate your API key and note your API username

## Usage

### Basic Usage

Check specific domains:
```bash
dotseek example.com google.org
```

Check base domain against multiple TLDs:
```bash
dotseek myapp
```

Flags may appear before or after the search terms:
```bash
dotseek myapp -l "<=3" -a      # same as: dotseek -l "<=3" -a myapp
```

Using the domains flag:
```bash
dotseek -domains="example.com,myapp.io,testsite"
```

Unicode names are accepted and normalized before checking:

```bash
dotseek bücher.com münchen
```

Preview the number of API checks, batches, and estimated duration without
making an API request:

```bash
dotseek --dry-run myapp
```

Queries larger than 200 API checks ask for confirmation in an interactive
terminal. Use `--yes` in unattended workflows.

### TLD Filtering

Filter by TLD length:
```bash
dotseek -l 3 myapp          # Only 3-character TLDs (.com, .net, .org)
dotseek -l "<4" myapp       # TLDs shorter than 4 characters  
dotseek -l ">=5" myapp      # TLDs 5 characters or longer
```

Filter by keywords:
```bash
dotseek -k "tech,business" myapp     # Include tech/business TLDs
dotseek -ek "adult,gambling" myapp   # Exclude adult/gambling TLDs
```

### Display Options

Show all domains (available and unavailable):
```bash
dotseek -a myapp
```

Include premium domains in results:
```bash
dotseek -p myapp
```

When the API supplies premium pricing, results show both the initial
registration price and a different renewal price.

Whois for full domains (`-w`). Checks availability first: if the domain is
available it says so, otherwise it prints whois details (registrar, dates,
status, nameservers). Uses RDAP with a fallback to the system `whois` command,
and applies to full domains you pass with a TLD:
```bash
dotseek -w example.com
```
`-w` requires a full domain (with a TLD); using it with a bare search term
(e.g. `dotseek -w myapp`) exits with an error, since there's nothing to look up.

### Interactive Controls

While checking the API or fetching whois, DotSeek shows an animated spinner.
Press `q` (or Ctrl+C) at any time to stop the run early — any domains already
checked are saved to the cache, so a later run resumes from where you left off.
These controls are active only when running in an interactive terminal (they're
skipped automatically when output is piped or redirected).

Show TLD descriptions:
```bash
dotseek -d myapp
# or
dotseek -tld-descriptions myapp
```

### Export Options

Export results to various formats:

```bash
# Export to JSON (format inferred from extension)
dotseek -o results.json myapp

# Export to CSV
dotseek -o domains.csv myapp

# Export with explicit format (overrides extension)
dotseek -o myfile.txt -f yaml myapp

# Export using format only (creates default filename)
dotseek -f json myapp  # Creates results.json

# Write structured data directly to stdout
dotseek --json myapp | jq .
dotseek --csv myapp > domains.csv
```

Supported export formats:
- **JSON**: Structured data with full details
- **XML**: Hierarchical format for system integration
- **YAML**: Human-readable configuration format
- **TOML**: Simple configuration format
- **CSV**: Spreadsheet-compatible format

All export formats include:
- Timestamp of when the check was performed
- Total domains evaluated, available, unavailable, unknown, and premium counts
- Complete results for all evaluated domains (not just displayed ones)
- A per-domain `status` of `available`, `unavailable`, or `unknown` (when no
  result could be obtained), its source, stale state, and error details
- Premium registration and renewal prices when available

Exports are built from actual outcomes and remain correct with `--no-cache`.
Direct stdout formats cannot be combined with file output or `-w`.

Availability and premium are independent dimensions: an available premium
domain is included in both `total_available` and `total_premium`.

### Cache Management

Disable cache for current run:
```bash
dotseek -no-cache myapp
```

Clear cache file:
```bash
dotseek -clear-cache
```

Set custom cache age (in seconds):
```bash
dotseek -cache-age 3600 myapp  # 1 hour
```

Use cached data without network requests. Expired entries are accepted and
labeled stale; misses are reported as unresolved:

```bash
dotseek --offline myapp
```

### Custom TLD File

Use a custom TLD file instead of the default sources:
```bash
dotseek -tld-file custom_tlds.csv myapp
```

## TLD File Format

TLD files should be in CSV format with the following structure:

```csv
tld,keywords
com,"commercial,business,general"
org,"organization,nonprofit"
net,"network,internet,tech"
dev,"development,tech,programming"
io,"tech,startup,input-output"
ai,"artificial-intelligence,tech,innovation"
```

## TLD Source Priority

DotSeek uses the embedded TLD list by default so runs are reproducible and do
not depend on a mutable remote branch. Override it explicitly with:

1. **Custom File**: `--tld-file custom_tlds.csv`
2. **Remote Refresh**: `--refresh-tlds`, with validation and embedded fallback
3. **Embedded List**: Default and fallback source

The embedded catalog is synchronized with Namecheap's normal-search
`namecheap.domains.getTldList` results. Extended-search-only entries, including
Handshake domains that do not use the public DNS root, are excluded.

## Output Format

Results print in a grid sized to the current terminal width, split into sections
by TLD length with a divider between each group.

### Color Coding

- **TLD green**: Available domains
- **TLD red**: Unavailable domains (only shown with `-a`)
- **Search term (gray)**: Dimmed so the colored TLD stands out
- **Yellow**: Section headers, system messages, and sandbox notices

> Note: terminal themes may remap ANSI colors, so exact shades vary.

Use `--color auto` (default), `--color always`, or `--color never`. Automatic
mode disables color for redirected output and when `NO_COLOR` is set.

### Premium Indicators

- **$**: Premium domain when no detailed price was returned
- **$12.98/yr (renew $15.98)**: Registration and renewal pricing returned by the API

### Example Output

```
--- Results (134 matching criteria) ---
2-character TLDs (9)
myapp.ac   myapp.ad   myapp.bz   myapp.cx   myapp.id
myapp.la   myapp.so   myapp.to   myapp.ws
--------------------------------------------------
3-character TLDs (25)
myapp.bar  myapp.cab  myapp.car  myapp.fan  myapp.gdn
myapp.how  myapp.ing  myapp.ink  myapp.kim  myapp.lat
...

Finished. Evaluated 150 domains total.

--- Summary of All Evaluated Domains ---
Available:   5
Unavailable: 145  
Premium:     12
```

When `-tld-descriptions` (`-d`) is set, results print one per line with their
keyword descriptions instead of the grid.

With `-w`, a whois section is appended for the full domains you passed:

```
--- Whois ---
example.com is taken.
  Registrar:   RESERVED-Internet Assigned Numbers Authority
  Registered:  1995-08-14
  Expires:     2025-08-13
  Status:      client delete prohibited, client transfer prohibited
  Nameservers: a.iana-servers.net, b.iana-servers.net
```

## Advanced Examples

### Startup Domain Search
```bash
# Find short, tech-focused domains for a startup
dotseek -l "<=4" -k "tech,startup" -p -d mystartup
```

### E-commerce Domain Search  
```bash
# Find business-appropriate domains, excluding adult content
dotseek -k "business,commercial" -ek "adult,gambling" -a mystore
```

### Developer Portfolio
```bash
# Find developer-focused domains with descriptions
dotseek -k "tech,development,personal" -d johndoe
```

### Comprehensive Domain Research
```bash
# Check everything, show all results with detailed info
dotseek -a -p -d -cache-age 7200 brandname
```

### Domain Research with Export
```bash
# Check domains and export to JSON for analysis
dotseek -k "tech,startup" -o analysis.json mystartup

# Export all results to CSV for spreadsheet analysis
dotseek -a -p -o domains.csv -f csv brandname

# Export only available domains to YAML
dotseek -o available.yaml myapp
```

## Command-Line Options

| Option | Short | Description |
|--------|-------|-------------|
| `--domains` | | Comma-separated domain names (base or FQDN) |
| `--l` | `-l` | Filter TLDs by length (e.g., '3', '<4', '>=5') |
| `--k` | `-k` | Include TLDs with specified keywords |
| `--ek` | `-ek` | Exclude TLDs with specified keywords |
| `--p` | `-p` | Include premium domains in results |
| `--a` | `-a` | Show all domains (available and unavailable) |
| `--w` | `-w` | For full domains: show whois if taken, else report it's available |
| `--tld-descriptions` | `-d` | Show TLD keyword descriptions |
| `--tld-file` | | Path to custom TLD file |
| `--refresh-tlds` | | Explicitly fetch the remote TLD list for this run |
| `--profile` | | Use a named XDG configuration profile |
| `--output` | `-o` | Output to file (format inferred from extension) |
| `--format` | `-f` | Output format (json\|xml\|yaml\|toml\|csv) |
| `--json`, `--xml`, `--yaml`, `--toml`, `--csv` | | Write that format directly to stdout |
| `--no-cache` | | Disable cache for this run |
| `--offline` | | Use cache only; allow and label stale entries |
| `--clear-cache` | | Clear cache file and exit |
| `--cache-age` | | Maximum cache age in seconds (default: 86400) |
| `--delay` | | Delay between API batches in milliseconds (default: 1000; lower is faster but may hit rate limits) |
| `--dry-run` | | Print the query plan without API calls |
| `--yes` | | Skip confirmation for large queries |
| `--color` | | Color mode: auto, always, or never |
| `--version` | | Print build and TLD catalog details |

## Caching System

DotSeek uses a JSON-based caching system to minimize API calls and improve performance:

- **Cache File**: `~/.config/dotseek/cache.json` (honors `XDG_CONFIG_HOME`; directory mode `0700`, file mode `0600`)
- **Default Age**: 24 hours (86400 seconds)
- **Cache Keys**: Domain names
- **Cached Data**: Availability, premium pricing, source data, and timestamps
- **Quit-Safe**: Pressing `q` or Ctrl+C mid-run flushes completed batches to the cache before exiting
- **Crash-Resistant Writes**: Cache updates use a synced temporary file and atomic rename

### Cache Benefits

- Reduces API usage and costs
- Faster repeated searches
- Offline capability for previously checked domains
- Configurable expiration

## Error Handling

DotSeek provides comprehensive error handling for:

- **Network Issues**: Timeout and connection errors
- **API Errors**: Namecheap API error responses  
- **Invalid Domains**: Malformed domain name detection
- **Missing Config**: Clear messages for missing credentials
- **File Errors**: TLD file reading and parsing issues
- **Export Errors**: Invalid format or file writing issues
- **Partial Results**: Each unresolved domain includes a stable error code and message

Stable process exit codes make automation predictable:

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error |
| `2` | Invalid usage |
| `3` | Configuration error |
| `4` | API error |
| `5` | Completed with partial results |
| `6` | Input/output error |
| `130` | Interrupted |

## Rate Limiting

- **Batch Size**: Maximum 50 domains per API call
- **Delay**: 1000ms between API batches by default; tune with `--delay <ms>` (use a
  smaller value like `--delay 0` for speed on small queries, or a larger value if
  you hit Namecheap rate limits on large multi-batch runs)
- **Timeout**: 30-second request timeout
- **Retries**: Timeouts, HTTP 408/425/429, and HTTP 5xx failures use visible,
  bounded exponential backoff with jitter and `Retry-After` support

## Output Streams

Domain results and the summary go to **stdout**. Progress logs, export
confirmations, the spinner, warnings, and notices go to **stderr**. This means
you can safely redirect just the results:

```bash
dotseek myapp > results.txt   # results.txt contains only the domain output
```

ANSI colors are disabled automatically when stdout is not a terminal and when
the `NO_COLOR` environment variable is set.

## Diagnostics and Shell Integration

Run end-to-end configuration and dependency checks:

```bash
dotseek doctor
dotseek doctor --profile sandbox
```

Inspect build and embedded TLD catalog versions:

```bash
dotseek version
```

Generate completion scripts or a man page:

```bash
dotseek completion bash
dotseek completion zsh
dotseek completion fish
dotseek man > dotseek.1
man ./dotseek.1
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

MIT © Brian Samson ([www.samson.zone](https://www.samson.zone)). See [LICENSE](LICENSE).

## Support

- **Issues**: Report bugs and feature requests via [GitHub Issues](https://github.com/bsamson00/dotseek/issues)
- **Documentation**: Check the embedded help with `dotseek --help`
- **API Documentation**: [Namecheap API Docs](https://www.namecheap.com/support/api/)

## Troubleshooting

### Common Issues

**"Missing required environment variables"**
- Ensure all required environment variables are set in `.env` file or system environment

**"API Error: Invalid IP"**  
- Verify your IP address is whitelisted in Namecheap API settings
- Check that `NAMECHEAP_CLIENT_IP` matches your actual IP

**"No TLDs loaded"**
- Verify a file passed with `--tld-file` uses the documented CSV format
- If using `--refresh-tlds`, check connectivity; DotSeek otherwise falls back
  to the embedded list
- Ensure the embedded TLD list is present in the binary

**"Cannot infer format from filename"**
- Use the `-f` flag to explicitly specify the output format
- Or use a standard file extension (.json, .xml, .yaml, .toml, .csv)

**Cache Issues**
- Use `--clear-cache` to reset cache file
- Check write permissions for `~/.config/dotseek/`
- Adjust `--cache-age` for different cache behavior

### Debugging

Enable verbose output by checking API responses and using cache debugging:

```bash
# Clear cache and run with fresh data
dotseek -clear-cache
dotseek -no-cache -a -d myapp
```

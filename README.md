<div align="center">

# DotSeek

**Find available domain names from your terminal.**

Fast, cached, batch domain availability checks powered by the Namecheap API.

[![Go Version](https://img.shields.io/github/go-mod/go-version/bsamson00/dotseek)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Features](#features) ·
[Installation](#installation) ·
[Configuration](#configuration) ·
[Usage](#usage) ·
[Reference](#reference) ·
[Troubleshooting](#troubleshooting)

</div>

```console
$ dotseek myapp -l "<=3"
--- Results (134 matching criteria) ---
2-character TLDs (9)
myapp.ac   myapp.ad   myapp.bz   myapp.cx   myapp.id
myapp.la   myapp.so   myapp.to   myapp.ws
--------------------------------------------------
3-character TLDs (25)
myapp.bar  myapp.cab  myapp.car  myapp.fan  myapp.gdn
myapp.how  myapp.ing  myapp.ink  myapp.kim  myapp.lat
...
```

## Features

| | |
|---|---|
| **Batch checking** | Up to 50 domains per API call, with bounded retries and backoff |
| **Smart caching** | Configurable cache age, offline mode, stale labeling, atomic writes |
| **TLD filtering** | Filter by length (`<=3`, `>=5`) or by include and exclude keywords |
| **Premium pricing** | Registration and renewal prices when Namecheap provides them |
| **Whois lookup** | RDAP with a fallback to the system `whois` command |
| **Adaptive display** | Grid sized to your terminal, grouped by TLD length |
| **Structured export** | JSON, XML, YAML, TOML, or CSV, to a file or stdout |
| **International domains** | Unicode input normalized to IDNA/Punycode |
| **Profiles** | Named credential profiles, including Namecheap's sandbox |
| **Automation friendly** | Dry runs, stable exit codes, clean stdout/stderr split |
| **Shell integration** | `doctor`, `version`, completions, and a man page |

## Installation

**Requirements:** Go 1.26.5 or newer and a Namecheap account with
[API access](https://www.namecheap.com/support/api/intro/) enabled. The
`whois` command is optional and only used as a fallback for `-w`.

### Go

```sh
go install github.com/bsamson00/dotseek@latest
```

### Install script

```sh
git clone https://github.com/bsamson00/dotseek.git
cd dotseek
./install.sh
```

The script installs to `$DOTSEEK_INSTALL_DIR`, `$GOBIN`, or `~/.local/bin`
(first one set). If the repository contains a `.env`, it is converted into a
profile in `~/.config/dotseek/config.toml` so DotSeek works from any directory.

### From source

```sh
git clone https://github.com/bsamson00/dotseek.git
cd dotseek
go build -o dotseek
```

## Configuration

### Namecheap credentials

1. Log in to Namecheap and open **Profile > Tools > Namecheap API**.
2. Enable API access and whitelist your public IP address.
3. Note your API key and API username.

### Environment variables

Copy `.env.example` to `.env` in your working directory, or export the
variables in your shell:

```sh
NAMECHEAP_API_USER=your_api_username
NAMECHEAP_API_KEY=your_api_key
NAMECHEAP_USERNAME=your_account_username
NAMECHEAP_CLIENT_IP=your_whitelisted_ip
NAMECHEAP_USE_SANDBOX=false   # optional, true uses the sandbox API
```

### Named profiles

Profiles live in `$XDG_CONFIG_HOME/dotseek/config.toml` (normally
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

Select a profile with `--profile sandbox` or `DOTSEEK_PROFILE=sandbox`.

> [!NOTE]
> Precedence, highest first: process environment variables, then profile
> values, then the local `.env` file.

Verify your setup at any time:

```sh
dotseek doctor
```

## Usage

### Check domains

```sh
dotseek example.com google.org      # specific domains
dotseek myapp                       # one name across many TLDs
dotseek --domains="example.com,myapp.io,testsite"
dotseek bücher.com münchen          # Unicode is normalized automatically
```

Flags may appear before or after the search terms, so
`dotseek myapp -l "<=3" -a` is the same as `dotseek -l "<=3" -a myapp`.

> [!TIP]
> Run `dotseek --dry-run myapp` to preview the number of API checks, batches,
> and estimated duration without making any requests. Queries larger than 200
> checks ask for confirmation in an interactive terminal; pass `--yes` to skip
> it in scripts.

### Filter TLDs

```sh
dotseek -l 3 myapp                   # only 3-character TLDs
dotseek -l "<4" myapp                # shorter than 4 characters
dotseek -l ">=5" myapp               # 5 characters or longer
dotseek -k "tech,business" myapp     # include tech or business TLDs
dotseek -ek "adult,gambling" myapp   # exclude adult or gambling TLDs
```

### Control what is shown

```sh
dotseek -a myapp     # show unavailable domains too
dotseek -p myapp     # include premium domains
dotseek -d myapp     # one result per line with TLD keyword descriptions
```

### Whois

```sh
dotseek -w example.com
```

```text
--- Whois ---
example.com is taken.
  Registrar:   RESERVED-Internet Assigned Numbers Authority
  Registered:  1995-08-14
  Expires:     2025-08-13
  Status:      client delete prohibited, client transfer prohibited
  Nameservers: a.iana-servers.net, b.iana-servers.net
```

`-w` checks availability first and only prints whois details for taken
domains. It requires a full domain with a TLD; `dotseek -w myapp` exits with
an error.

### Export results

```sh
dotseek -o results.json myapp        # format inferred from the extension
dotseek -o myfile.txt -f yaml myapp  # explicit format wins
dotseek -f json myapp                # writes results.json
dotseek --json myapp | jq .          # straight to stdout
dotseek --csv myapp > domains.csv
```

Every export includes a timestamp, totals (evaluated, available, unavailable,
unknown, premium), and every evaluated domain, not just the displayed ones.
Each domain carries a `status` of `available`, `unavailable`, or `unknown`,
plus its source, stale state, error details, and premium pricing when
available.

<details>
<summary>Export notes</summary>

- Exports are built from actual outcomes and remain correct with `--no-cache`.
- Stdout formats cannot be combined with file output or `-w`.
- Availability and premium are independent: an available premium domain
  counts toward both `total_available` and `total_premium`.

</details>

### Cache

```sh
dotseek --offline myapp          # cache only; stale entries are labeled
dotseek --no-cache myapp         # skip the cache for this run
dotseek --cache-age 3600 myapp   # treat entries older than 1 hour as expired
dotseek --clear-cache            # delete the cache file
```

The cache lives at `~/.config/dotseek/cache.json` (honors `XDG_CONFIG_HOME`,
directory mode `0700`, file mode `0600`). Entries expire after 24 hours by
default and are written with a synced temporary file and atomic rename.

Press `q` or Ctrl+C at any time to stop a run. Completed batches are saved to
the cache, so the next run picks up where you left off.

### Custom TLD lists

DotSeek uses its embedded TLD catalog by default, so results are reproducible.
The catalog is synchronized with Namecheap's normal-search TLD list; Handshake
domains, which do not use the public DNS root, are excluded.

```sh
dotseek --tld-file custom_tlds.csv myapp   # use your own list
dotseek --refresh-tlds myapp               # fetch the latest list from this repo
```

`--refresh-tlds` validates the downloaded list and falls back to the embedded
catalog on failure. Custom files use CSV:

```csv
tld,keywords
com,"commercial,business,general"
dev,"development,tech,programming"
io,"tech,startup,input-output"
```

## Reference

### Options

| Option | Description |
|---|---|
| `--domains` | Comma-separated search terms (alternative to positional args) |
| `-l` | Filter TLDs by length, e.g. `3`, `<=4`, `>2` |
| `-k` | Include only TLDs matching these keywords |
| `-ek` | Exclude TLDs matching these keywords |
| `-p` | Include premium domains |
| `-a` | Show unavailable domains too |
| `-w` | Whois for taken full domains |
| `-d`, `--tld-descriptions` | Show TLD keyword descriptions, one per line |
| `--tld-file` | Path to a custom TLD CSV file |
| `--refresh-tlds` | Fetch the remote TLD list for this run |
| `--profile` | Use a named configuration profile |
| `-o`, `--output` | Write results to a file |
| `-f`, `--format` | `json`, `xml`, `yaml`, `toml`, or `csv` |
| `--json`, `--xml`, `--yaml`, `--toml`, `--csv` | Write that format to stdout |
| `--no-cache` | Disable the cache for this run |
| `--offline` | Use cached results only |
| `--clear-cache` | Delete the cache file and exit |
| `--cache-age` | Maximum cache age in seconds (default `86400`) |
| `--delay` | Milliseconds between API batches (default `1000`) |
| `--dry-run` | Show the query plan without calling the API |
| `--yes` | Skip confirmation for large queries |
| `--color` | `auto` (default), `always`, or `never` |
| `--version` | Show build and TLD catalog details |

### Commands

```sh
dotseek doctor [--profile NAME]    # check configuration and dependencies
dotseek version                    # build and TLD catalog versions
dotseek completion bash|zsh|fish   # shell completion script
dotseek man > dotseek.1            # man page
```

### Output

- Results and the summary go to **stdout**; progress, the spinner, warnings,
  and notices go to **stderr**, so `dotseek myapp > results.txt` captures only
  results.
- Available TLDs are green, unavailable TLDs (with `-a`) are red, and the
  search term is dimmed. Premium domains show `$`, or
  `$12.98/yr (renew $15.98)` when the API returns pricing.
- Color is disabled automatically when stdout is not a terminal or `NO_COLOR`
  is set.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error |
| `2` | Invalid usage |
| `3` | Configuration error |
| `4` | API error |
| `5` | Completed with partial results |
| `6` | Input/output error |
| `130` | Interrupted |

### Rate limiting

Requests are sent in batches of up to 50 domains with a 30 second timeout and
a 1000 ms pause between batches. Tune the pause with `--delay`: `--delay 0`
is fine for small queries, while larger values help on big multi-batch runs.
Timeouts, HTTP 408/425/429, and 5xx responses are retried with exponential
backoff, jitter, and `Retry-After` support.

## Troubleshooting

<details>
<summary><b>Missing required environment variables</b></summary>

Set all four `NAMECHEAP_*` credentials in `.env`, your shell, or a profile.
Run `dotseek doctor` to see what is missing.

</details>

<details>
<summary><b>API Error: Invalid IP</b></summary>

Whitelist your public IP in the Namecheap API settings and make sure
`NAMECHEAP_CLIENT_IP` matches it.

</details>

<details>
<summary><b>No TLDs loaded</b></summary>

Check that a `--tld-file` uses the CSV format above. With `--refresh-tlds`,
check connectivity; DotSeek falls back to the embedded list otherwise.

</details>

<details>
<summary><b>Cannot infer format from filename</b></summary>

Pass `-f` explicitly or use a `.json`, `.xml`, `.yaml`, `.toml`, or `.csv`
extension.

</details>

<details>
<summary><b>Stale or unexpected results</b></summary>

Run `dotseek --clear-cache`, check write permissions on `~/.config/dotseek/`,
or rerun with `--no-cache`.

</details>

## Contributing

Bug reports and pull requests are welcome on
[GitHub Issues](https://github.com/bsamson00/dotseek/issues). Before opening a
pull request, make sure `gofmt -l .`, `go vet ./...`, and `go test ./...` are
clean.

## License

[MIT](LICENSE) © Brian Samson ([www.samson.zone](https://www.samson.zone))

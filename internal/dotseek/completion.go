package dotseek

import (
	"crypto/sha256"
	"fmt"
	"io"
	"runtime"
	"strings"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func tldCatalogID() string {
	data, err := embeddedTldFS.ReadFile("tlds.txt")
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:8])
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "dotseek %s\n", version)
	fmt.Fprintf(w, "commit: %s\n", commit)
	fmt.Fprintf(w, "built: %s\n", buildDate)
	fmt.Fprintf(w, "go: %s\n", runtime.Version())
	fmt.Fprintf(w, "tld_catalog: %s\n", tldCatalogID())
}

func printCompletion(w io.Writer, shell string) error {
	const flags = "--domains --l --k --ek --p --a --w --tld-file --refresh-tlds --profile --tld-descriptions --d --no-cache --offline --clear-cache --cache-age --delay --yes --dry-run --output --o --format --f --json --xml --yaml --toml --csv --color --version --help"
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		fmt.Fprintf(w, `_dotseek_complete() {
  local current="${COMP_WORDS[COMP_CWORD]}"
  if [[ "$COMP_CWORD" -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "doctor version completion man %s" -- "$current") )
  else
    COMPREPLY=( $(compgen -W "%s" -- "$current") )
  fi
}
complete -F _dotseek_complete dotseek
`, flags, flags)
	case "zsh":
		fmt.Fprintf(w, `#compdef dotseek
_dotseek() {
  local -a commands options
  commands=(doctor version completion man)
  options=(%s)
  _describe 'command' commands
  _describe 'option' options
  _arguments '*:domain:'
}
_dotseek "$@"
`, strings.ReplaceAll(flags, " ", "\n    "))
	case "fish":
		for _, command := range []string{"doctor", "version", "completion", "man"} {
			fmt.Fprintf(w, "complete -c dotseek -f -n '__fish_use_subcommand' -a %s\n", command)
		}
		for _, option := range strings.Fields(flags) {
			fmt.Fprintf(w, "complete -c dotseek -l %s\n", strings.TrimLeft(option, "-"))
		}
	default:
		return fmt.Errorf("unsupported shell %q; use bash, zsh, or fish", shell)
	}
	return nil
}

func printManPage(w io.Writer) {
	fmt.Fprint(w, `.TH DOTSEEK 1
.SH NAME
dotseek \- check domain availability across top-level domains
.SH SYNOPSIS
.B dotseek
[\fIOPTIONS\fR] \fISEARCH-TERM\fR...
.br
.B dotseek
doctor|version|completion|man
.SH DESCRIPTION
DotSeek checks search terms and explicit domains with the Namecheap API. Results
may be read from the local cache, printed for people, or encoded for automation.
.SH COMMON OPTIONS
.TP
.B \-a
Include unavailable domains.
.TP
.B \-p
Include premium domains and available pricing.
.TP
.BR \-\-offline
Use cached results only, including labeled stale entries.
.TP
.BR \-\-dry-run
Print the query plan and estimated duration without calling the API.
.TP
.BR \-\-profile " NAME"
Use a named profile from the XDG configuration file.
.TP
.BR \-\-color " auto|always|never"
Control ANSI color output.
.TP
.BR \-\-json ", " \-\-xml ", " \-\-yaml ", " \-\-toml ", " \-\-csv
Write structured results directly to standard output.
.SH COMMANDS
.TP
.B doctor
Check configuration, network access, cache writes, Namecheap, RDAP, and whois.
.TP
.B version
Print build, Go runtime, and embedded TLD catalog versions.
.TP
.B completion bash|zsh|fish
Generate a shell completion script.
.TP
.B man
Print this manual page in roff format.
.SH EXIT STATUS
0 success; 1 general error; 2 usage error; 3 configuration error; 4 API error;
5 partial results; 6 input/output error; 130 interrupted.
.SH FILES
.I $XDG_CONFIG_HOME/dotseek/config.toml
and
.I $XDG_CONFIG_HOME/dotseek/cache.json
`)
}

#!/usr/bin/env bash
# Build dotseek and install it somewhere on your PATH.
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

case "$(uname -s)" in
	Darwin) os=macos ;;
	Linux)  os=linux ;;
	*)      echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

if ! command -v go >/dev/null 2>&1; then
	if [ "$os" = macos ]; then
		echo "Go is not installed. Try: brew install go" >&2
	else
		echo "Go is not installed. Install it from https://go.dev/dl/" >&2
	fi
	exit 1
fi

install_dir=${DOTSEEK_INSTALL_DIR:-${GOBIN:-$HOME/.local/bin}}
mkdir -p "$install_dir"

echo "Detected $os; building dotseek for $(go env GOOS)/$(go env GOARCH)..."
(cd "$repo_dir" && go build -o "$install_dir/dotseek" .)
echo "Installed: $install_dir/dotseek"

# dotseek only reads .env from the current directory, so a copy is useless from
# other dirs. Instead, turn the repo .env into a profile in the user config file.
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/dotseek"
config_file="$config_dir/config.toml"
env_file="$repo_dir/.env"

env_value() {
	local key=$1 line="" value=""
	while IFS= read -r line || [ -n "$line" ]; do
		line=${line#"${line%%[![:space:]]*}"}
		line=${line#export }
		[ "${line%%=*}" = "$key" ] || continue
		value=${line#*=}
		value=${value%"${value##*[![:space:]]}"}
		case "$value" in
			\"*\") value=${value#\"}; value=${value%\"} ;;
			\'*\') value=${value#\'}; value=${value%\'} ;;
		esac
	done <"$env_file"
	printf '%s' "$value"
}

toml_escape() {
	local value=$1
	value=${value//\\/\\\\}
	printf '%s' "${value//\"/\\\"}"
}

echo
if [ ! -f "$env_file" ]; then
	echo "No .env in $repo_dir; skipping credential setup."
elif [ -f "$config_file" ] && [ "${DOTSEEK_FORCE_CONFIG:-0}" != 1 ]; then
	echo "$config_file already exists; leaving it alone."
	echo "Re-run with DOTSEEK_FORCE_CONFIG=1 to overwrite it from .env."
else
	provider=$(printf '%s' "$(env_value DOTSEEK_PROVIDER)" | tr '[:upper:]' '[:lower:]')
	if [ -z "$provider" ]; then
		if [ -n "$(env_value CLOUDFLARE_API_TOKEN)" ] && [ -z "$(env_value NAMECHEAP_API_KEY)" ]; then
			provider=cloudflare
		else
			provider=namecheap
		fi
	fi
	case "$provider" in
		cloudflare) sandbox=$(env_value CLOUDFLARE_USE_SANDBOX) ;;
		namecheap)  sandbox=$(env_value NAMECHEAP_USE_SANDBOX) ;;
		*) echo "Unknown DOTSEEK_PROVIDER '$provider' in .env; use namecheap or cloudflare." >&2; exit 1 ;;
	esac
	case "$(printf '%s' "$sandbox" | tr '[:upper:]' '[:lower:]')" in
		1|true|yes|on) sandbox=true ;;
		*)             sandbox=false ;;
	esac
	mkdir -p "$config_dir"
	umask 077
	if [ "$provider" = cloudflare ]; then
		cat >"$config_file" <<-EOF
			default_profile = "default"

			[profiles.default]
			provider = "cloudflare"
			account_id = "$(toml_escape "$(env_value CLOUDFLARE_ACCOUNT_ID)")"
			api_token = "$(toml_escape "$(env_value CLOUDFLARE_API_TOKEN)")"
			use_sandbox = $sandbox
		EOF
	else
		cat >"$config_file" <<-EOF
			default_profile = "default"

			[profiles.default]
			provider = "namecheap"
			api_user = "$(toml_escape "$(env_value NAMECHEAP_API_USER)")"
			api_key = "$(toml_escape "$(env_value NAMECHEAP_API_KEY)")"
			username = "$(toml_escape "$(env_value NAMECHEAP_USERNAME)")"
			client_ip = "$(toml_escape "$(env_value NAMECHEAP_CLIENT_IP)")"
			use_sandbox = $sandbox
		EOF
	fi
	chmod 600 "$config_file"
	echo "Wrote $provider credentials from .env to $config_file (mode 600)."
fi

echo
case ":$PATH:" in
	*":$install_dir:"*)
		echo "'$install_dir' is already on your PATH. Run: dotseek --help"
		;;
	*)
		case "$(basename "${SHELL:-}")" in
			zsh)  rc="$HOME/.zshrc" ;;
			bash) [ "$os" = macos ] && rc="$HOME/.bash_profile" || rc="$HOME/.bashrc" ;;
			fish) rc="$HOME/.config/fish/config.fish" ;;
			*)    rc="your shell profile" ;;
		esac
		echo
		echo "'$install_dir' is not on your PATH. Add it to $rc:"
		if [ "${rc##*/}" = "config.fish" ]; then
			echo "  fish_add_path $install_dir"
		else
			echo "  export PATH=\"$install_dir:\$PATH\""
		fi
		echo "Then reload your shell and run: dotseek --help"
		;;
esac

echo
echo "Verify with: dotseek doctor"

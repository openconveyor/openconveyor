#!/usr/bin/env bash
# Checks the Secret files of the stack: each file exists, is not empty, has
# mode 600, and has no trailing newline. A trailing newline in the webhook
# secret breaks every HMAC check: kubectl keeps it, GitHub does not.
#
#   scripts/check-secrets.sh [DIR]      DIR defaults to secrets/
set -euo pipefail

dir="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/secrets}"
files=(anthropic-api-key github-token github-webhook-secret)
problems=0

problem() {
    printf 'check-secrets: %s\n' "$*" >&2
    problems=$((problems + 1))
}

# mode_of prints the permission bits of a file on Linux and on macOS.
mode_of() { stat -L -c '%a' "$1" 2>/dev/null || stat -L -f '%Lp' "$1"; }

for name in "${files[@]}"; do
    f="$dir/$name"
    if [[ ! -e "$f" ]]; then
        problem "$name is missing in $dir (see secrets/README.md)"
        continue
    fi
    # -e follows a symlink, so a link to ~/.config/conveyor/<name> works.
    if [[ ! -s "$f" ]]; then
        problem "$name is empty"
        continue
    fi
    mode=$(mode_of "$f")
    if [[ "$mode" != 600 ]]; then
        problem "$name has mode $mode, want 600: chmod 600 '$f'"
    fi
    if [[ "$(tail -c 1 "$f" | od -An -tx1 | tr -d ' \n')" == 0a ]]; then
        problem "$name ends with a newline; remove it: (umask 077; printf '%s' \"\$(cat '$f')\" > '$f.tmp') && mv '$f.tmp' '$f'"
    fi
done

if ((problems > 0)); then
    printf 'check-secrets: %d problem(s)\n' "$problems" >&2
    exit 1
fi
printf 'check-secrets: the %d Secret files are valid\n' "${#files[@]}"

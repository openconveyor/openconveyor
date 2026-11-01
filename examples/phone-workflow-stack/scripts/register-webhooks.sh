#!/usr/bin/env bash
# Registers the three GitHub webhooks of the stack on the target repository.
# GitHub sends each webhook to one URL, and the adapter serves one
# ClusterTriggerClass for each path, so there is one webhook for each path:
#
#   issues         -> <URL>/github/issues                 github-issues-implementer
#   pull_request   -> <URL>/github/pull-request           github-pr-reviewer
#   pull_request   -> <URL>/github/pull-request-iterate   github-pr-iterate
#
# The script is idempotent on the URL: it updates a webhook that has the URL
# (events, secret, active), and creates one that does not exist. gh reads the
# secret from the file, so the value never goes into argv or the shell
# history.
#
#   scripts/register-webhooks.sh OWNER/REPO PUBLIC_URL [SECRET_FILE]
#   scripts/register-webhooks.sh scholtenmartijn/openconveyor-test https://cachyos-x8664.tail1ddea3.ts.net
set -euo pipefail

repo="${1:?usage: $0 OWNER/REPO PUBLIC_URL [SECRET_FILE]}"
base="${2:?usage: $0 OWNER/REPO PUBLIC_URL [SECRET_FILE]}"
base="${base%/}"
secret_file="${3:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/secrets/github-webhook-secret}"
[[ -s "$secret_file" ]] || { echo "register-webhooks: $secret_file is missing or empty" >&2; exit 1; }

hooks=(
    "issues /github/issues"
    "pull_request /github/pull-request"
    "pull_request /github/pull-request-iterate"
)

for hook in "${hooks[@]}"; do
    event="${hook%% *}"
    url="$base${hook#* }"
    id=$(gh api "repos/$repo/hooks" --paginate --jq ".[] | select(.config.url == \"$url\") | .id" | head -n1)
    args=(-f "events[]=$event" -F active=true
        -f "config[url]=$url" -f "config[content_type]=json" -f "config[insecure_ssl]=0"
        -F "config[secret]=@$secret_file")
    if [[ -n "$id" ]]; then
        gh api -X PATCH "repos/$repo/hooks/$id" "${args[@]}" >/dev/null
        printf 'webhook %s (%s): updated, id %s\n' "$url" "$event" "$id"
    else
        id=$(gh api -X POST "repos/$repo/hooks" -f name=web "${args[@]}" --jq .id)
        printf 'webhook %s (%s): created, id %s\n' "$url" "$event" "$id"
    fi
done

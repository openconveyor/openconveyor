#!/usr/bin/env bash
# Reporter agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the reporting instruction (plain text)
#   - /run/secrets/<name>/        holds declared Secrets, one dir per Secret
#   - $TARGET_REPO                optional: GitHub repo (owner/name) to report on
#   - $REPORT_ISSUE_URL           optional: GitHub Issue URL to post the report as
#                                 a comment. Falls back to stdout if unset.
#   - exit 0 = success, non-zero = failure
#
# This script never clones a repo and never pushes code. Its only side effect
# on the git host is posting an Issue comment via `gh issue comment`. The
# github-token Secret should be scoped to Issue read + comment.

set -euo pipefail

log() { printf '[reporter] %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

PROMPT_FILE="${PROMPT_FILE:-/task/prompt}"
SECRETS_ROOT="${SECRETS_ROOT:-/run/secrets}"
WORKDIR="${WORKDIR:-/tmp/work}"

[[ -r "$PROMPT_FILE" ]] || die "prompt file $PROMPT_FILE not readable"

# Secret files land at /run/secrets/<name>/<key>. For the common case where
# the Secret has a single key, export SECRETNAME_UPPER=contents so standard
# CLIs find what they expect (ANTHROPIC_API_KEY, GITHUB_TOKEN, ...).
if [[ -d "$SECRETS_ROOT" ]]; then
    while IFS= read -r -d '' file; do
        name=$(basename "$(dirname "$file")")
        var=$(printf '%s' "$name" | tr '[:lower:]-' '[:upper:]_')
        export "$var=$(cat "$file")"
    done < <(find -L "$SECRETS_ROOT" -mindepth 2 -maxdepth 2 -type f -print0 2>/dev/null || true)
fi

mkdir -p "$WORKDIR"
cd "$WORKDIR"

prompt=$(cat "$PROMPT_FILE")

# Gather context from the GitHub API if a target repo is specified.
context_file="$WORKDIR/context.md"
: > "$context_file"

if [[ -n "${TARGET_REPO:-}" ]]; then
    [[ -n "${GITHUB_TOKEN:-}" ]] || die "GITHUB_TOKEN not set; declare the github-token Secret on the Task"

    log "gathering data for $TARGET_REPO"

    printf '## Recent merged PRs\n\n' >> "$context_file"
    gh pr list --repo "$TARGET_REPO" --state merged --limit 20 \
        --json number,title,author,mergedAt,url \
        >> "$context_file" 2>/dev/null || log "warning: could not list merged PRs"

    printf '\n\n## Open PRs\n\n' >> "$context_file"
    gh pr list --repo "$TARGET_REPO" --state open --limit 20 \
        --json number,title,author,createdAt,url \
        >> "$context_file" 2>/dev/null || log "warning: could not list open PRs"

    printf '\n\n## Open issues\n\n' >> "$context_file"
    gh issue list --repo "$TARGET_REPO" --state open --limit 20 \
        --json number,title,author,createdAt,url \
        >> "$context_file" 2>/dev/null || log "warning: could not list open issues"
fi

# Hand the prompt + context to Claude to produce a human-readable report.
report_file="$WORKDIR/report.md"
log "running claude"
{
    printf '%s\n' "$prompt"
    if [[ -s "$context_file" ]]; then
        printf '\n---\n\n# Repository data\n\n'
        cat "$context_file"
    fi
} | claude --print --permission-mode bypassPermissions > "$report_file"

if [[ ! -s "$report_file" ]]; then
    die "claude produced an empty report"
fi

# Deliver the report. GitHub Issue comment is the primary output channel;
# stdout is the fallback so `kubectl logs` always works.
if [[ -n "${REPORT_ISSUE_URL:-}" ]]; then
    log "posting report to $REPORT_ISSUE_URL"
    gh issue comment "$REPORT_ISSUE_URL" --body-file "$report_file"
else
    log "no REPORT_ISSUE_URL set — printing report to stdout"
fi

cat "$report_file"

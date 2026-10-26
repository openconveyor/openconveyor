#!/usr/bin/env bash
# Reporter agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the reporting instruction (plain text)
#   - /run/secrets/<name>/        holds the declared Secrets, one dir per Secret
#   - $TARGET_REPO                optional: the GitHub repo (owner/name) to report on
#   - $REPORT_ISSUE_URL           optional: the GitHub Issue URL to post the report
#                                 as a comment. If unset, the report goes to stdout.
#   - exit 0 = success, non-zero = failure
#
# This script never clones a repo and never pushes code. Its only side effect
# on the git host is an Issue comment that it posts with `gh issue comment`.
# Scope the github-token Secret to Issue read + comment.

set -euo pipefail

log() { printf '[reporter] %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

PROMPT_FILE="${PROMPT_FILE:-/task/prompt}"
SECRETS_ROOT="${SECRETS_ROOT:-/run/secrets}"
WORKDIR="${WORKDIR:-/tmp/work}"

[[ -r "$PROMPT_FILE" ]] || die "prompt file $PROMPT_FILE not readable"

# conveyor projects the Secret files at /run/secrets/<name>/<key>. In the
# common case the Secret has a single key. The loop then exports
# SECRETNAME_UPPER=contents so that standard CLIs find what they expect
# (ANTHROPIC_API_KEY, GITHUB_TOKEN, and more).
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

# Collect context from the GitHub API if the Task specifies a target repo.
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

# Send the prompt + context to Claude, which writes a human-readable report.
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

# Deliver the report. A GitHub Issue comment is the primary output channel.
# The script writes to stdout as the fallback so that `kubectl logs` always
# works.
if [[ -n "${REPORT_ISSUE_URL:-}" ]]; then
    log "posting report to $REPORT_ISSUE_URL"
    gh issue comment "$REPORT_ISSUE_URL" --body-file "$report_file"
else
    log "no REPORT_ISSUE_URL set — printing report to stdout"
fi

cat "$report_file"

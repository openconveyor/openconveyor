#!/usr/bin/env bash
# Reviewer agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the prompt (plain text). For the reference
#                                 trigger this is the PR's html_url; the agent
#                                 extracts it and reviews that PR.
#   - /run/secrets/<name>/        holds declared Secrets, one dir per Secret
#   - $PR_URL                     optional: explicit PR URL override
#   - $REVIEW_EVENT               optional: COMMENT (default) | APPROVE | REQUEST_CHANGES
#   - exit 0 = success, non-zero = failure
#
# This script never `git push`es and never opens a PR. Its only side effect on
# the git host is `gh pr review --comment`, which posts a review with body. The
# `github-token` Secret should be scoped accordingly (PR read + PR comment;
# no `repo` write).

set -euo pipefail

log() { printf '[reviewer] %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

PROMPT_FILE="${PROMPT_FILE:-/task/prompt}"
SECRETS_ROOT="${SECRETS_ROOT:-/run/secrets}"
WORKDIR="${WORKDIR:-/tmp/work}"
REVIEW_EVENT="${REVIEW_EVENT:-COMMENT}"

[[ -r "$PROMPT_FILE" ]] || die "prompt file $PROMPT_FILE not readable"

# Mirror the implementer's secret-fanout: a Secret directory becomes
# $NAME_UPPER=<contents-of-its-single-key>, so standard CLIs find what they
# expect (ANTHROPIC_API_KEY, GITHUB_TOKEN, ...).
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

# Resolve the PR URL. Explicit env wins; otherwise grep the prompt for the
# first https://<host>/<owner>/<repo>/pull/<n> URL.
pr_url="${PR_URL:-}"
if [[ -z "$pr_url" ]]; then
    pr_url=$(printf '%s' "$prompt" \
        | grep -oE 'https://[^[:space:]]+/pull/[0-9]+' \
        | head -n1 || true)
fi
[[ -n "$pr_url" ]] || die "no PR URL: set \$PR_URL or include one in the prompt"

case "${GIT_HOST:-github}" in
    github) ;;
    *) die "git host '${GIT_HOST}' not yet supported — waiting on conveyor-git (post-v0.1.0)" ;;
esac

[[ -n "${GITHUB_TOKEN:-}" ]] || die "GITHUB_TOKEN not set; declare the github-token Secret on the Task"

log "fetching diff for $pr_url"
diff_file="$WORKDIR/pr.diff"
gh pr diff "$pr_url" > "$diff_file"

# Hand the prompt + diff to claude. We pin --print so claude exits when done
# and emits the review body to stdout; we capture it into a file because
# `gh pr review --body-file` is the safe path for arbitrary content (no shell
# escaping concerns vs. --body).
review_file="$WORKDIR/review.md"
log "running claude (review event: $REVIEW_EVENT)"
{
    printf '%s\n\n' "$prompt"
    printf '## PR diff\n\n```diff\n'
    cat "$diff_file"
    printf '\n```\n'
} | claude --print --permission-mode bypassPermissions > "$review_file"

if [[ ! -s "$review_file" ]]; then
    die "claude produced an empty review — refusing to post"
fi

case "$REVIEW_EVENT" in
    COMMENT)         flag="--comment" ;;
    APPROVE)         flag="--approve" ;;
    REQUEST_CHANGES) flag="--request-changes" ;;
    *) die "REVIEW_EVENT must be COMMENT, APPROVE, or REQUEST_CHANGES (got: $REVIEW_EVENT)" ;;
esac

log "posting review on $pr_url"
gh pr review "$pr_url" "$flag" --body-file "$review_file"

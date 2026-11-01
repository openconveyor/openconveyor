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
#   - /dev/termination-log        the Result (ADR-0008): the first line of the
#                                 report and the issue link, if any
#
# This script never clones a repo and never pushes code. Its only side effect
# on the git host is an Issue comment that it posts with `gh issue comment`.
# Scope the github-token Secret to Issue read + comment.

set -euo pipefail

AGENT_NAME=reporter
# The shared library: secrets export, GitHub setup, the Result writer and the
# SIGTERM trap. The Dockerfile copies it from agents/common/lib.sh.
# shellcheck source-path=SCRIPTDIR source=../common/lib.sh
. "${CONVEYOR_LIB:-/usr/local/lib/conveyor/lib.sh}"

PROMPT_FILE="${PROMPT_FILE:-/task/prompt}"
WORKDIR="${WORKDIR:-/tmp/work}"

[[ -r "$PROMPT_FILE" ]] || die "prompt file $PROMPT_FILE not readable"

secrets_export
github_setup
gh_repo_from "${TARGET_REPO:-}"

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
# The input goes through a file, so that run_interruptible can pass it to the
# background job.
input_file="$WORKDIR/input.md"
report_file="$WORKDIR/report.md"
{
    printf '%s\n' "$prompt"
    if [[ -s "$context_file" ]]; then
        printf '\n---\n\n# Repository data\n\n'
        cat "$context_file"
    fi
} > "$input_file"
log "running claude"
run_interruptible claude --print --permission-mode bypassPermissions < "$input_file" > "$report_file"

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

# The Result summary is the first line of the report, without Markdown heading
# marks. The issue link is present only when the report went to an issue.
first_line=$(grep -m1 -v '^[[:space:]]*$' "$report_file" | sed -E 's/^[#>*[:space:]]+//' || true)
if [[ -n "${REPORT_ISSUE_URL:-}" ]]; then
    result_write "$first_line" "issue=$REPORT_ISSUE_URL"
else
    result_write "$first_line"
fi

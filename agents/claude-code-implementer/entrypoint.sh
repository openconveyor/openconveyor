#!/usr/bin/env bash
# Implementer agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the prompt (plain text)
#   - /run/secrets/<name>/        holds the declared Secrets, one dir per Secret
#   - $TARGET_REPO                optional: the git URL to clone and work in
#   - $TARGET_BRANCH              optional: the base branch; defaults to main
#   - exit 0 = success, non-zero = failure
#   - /dev/termination-log        the Result (ADR-0008): a summary and the
#                                 pull-request link, written by agents/common/lib.sh
#
# This script stays thin on purpose. It exports the secrets as the environment
# variables that claude and gh expect. It clones the repo if the Task declares
# one, and sends the prompt to `claude --print`. If the repo changed, it opens
# a PR. Git-host logic beyond GitHub will move into conveyor-git (post-v0.1.0).

set -euo pipefail

AGENT_NAME=implementer
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

if [[ -n "${TARGET_REPO:-}" ]]; then
    log "cloning $TARGET_REPO"
    git clone --depth=1 ${TARGET_BRANCH:+--branch "$TARGET_BRANCH"} "$TARGET_REPO" repo
    cd repo
    git config user.email "bot@openconveyor.ai"
    git config user.name "conveyor-implementer"
fi

log "running claude"
run_interruptible claude --print --verbose --output-format stream-json \
    --permission-mode bypassPermissions \
    < "$PROMPT_FILE"

# Repo flow: open a PR only when the script has a repo and the repo changed.
# `git diff --quiet HEAD` finds both staged and unstaged changes. Claude can
# stage its own writes with `git add`. In that case `git diff` alone, which
# checks only unstaged changes, reports a clean tree although there is a change
# to commit.
if [[ -n "${TARGET_REPO:-}" ]] && ! git diff --quiet HEAD; then
    branch="${BRANCH_NAME:-conveyor/$(date -u +%Y%m%dT%H%M%SZ)}"
    git checkout -b "$branch"
    git add -A
    git commit -m "conveyor: automated change" >/dev/null
    git push -u origin "$branch"
    result_partial "Pushed branch $branch, no pull request yet"
    case "${GIT_HOST:-github}" in
        github)
            pr_url=$(gh pr create --fill --base "${TARGET_BRANCH:-main}" --head "$branch" | tail -n1)
            printf '%s\n' "$pr_url"
            result_write "Opened a pull request from branch $branch" "pull-request=$pr_url"
            ;;
        *)
            die "git host '${GIT_HOST}' not yet supported — waiting on conveyor-git (post-v0.1.0)"
            ;;
    esac
elif [[ -n "${TARGET_REPO:-}" ]]; then
    log "no repo diff — nothing to commit"
    result_write "Claude finished without a change in the repository"
else
    log "no TARGET_REPO — answered the prompt only"
    result_write "Answered the prompt without a repository"
fi

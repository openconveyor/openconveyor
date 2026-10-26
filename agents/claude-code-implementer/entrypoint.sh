#!/usr/bin/env bash
# Implementer agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the prompt (plain text)
#   - /run/secrets/<name>/        holds the declared Secrets, one dir per Secret
#   - $TARGET_REPO                optional: the git URL to clone and work in
#   - $TARGET_BRANCH              optional: the base branch; defaults to main
#   - exit 0 = success, non-zero = failure
#
# This script stays thin on purpose. It exports the secrets as the environment
# variables that claude and gh expect. It clones the repo if the Task declares
# one, and sends the prompt to `claude --print`. If the repo changed, it opens
# a PR. Git-host logic beyond GitHub will move into conveyor-git (post-v0.1.0).

set -euo pipefail

log() { printf '[implementer] %s\n' "$*" >&2; }
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

# Some CLIs look for GITHUB_TOKEN. Set it from the conventional secret name
# that the sample Task declares.
if [[ -z "${GITHUB_TOKEN:-}" && -n "${GITHUB_TOKEN_FILE:-}" && -r "${GITHUB_TOKEN_FILE}" ]]; then
    GITHUB_TOKEN=$(cat "$GITHUB_TOKEN_FILE")
    export GITHUB_TOKEN
fi

# Store $GITHUB_TOKEN in the git credential helper so that HTTPS pushes work
# without a TTY. The x-access-token user is the documented form for
# fine-grained and installation tokens. Classic PATs work the same way.
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    git config --global credential.helper store
    umask 077
    printf 'https://x-access-token:%s@github.com\n' "$GITHUB_TOKEN" > "$HOME/.git-credentials"
fi

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
claude --print --verbose --output-format stream-json \
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
    case "${GIT_HOST:-github}" in
        github)
            gh pr create --fill --base "${TARGET_BRANCH:-main}" --head "$branch"
            ;;
        *)
            die "git host '${GIT_HOST}' not yet supported — waiting on conveyor-git (post-v0.1.0)"
            ;;
    esac
else
    log "no repo diff — nothing to commit"
fi

#!/usr/bin/env bash
# Implementer agent entrypoint.
#
# Contract with conveyor:
#   - /task/prompt                is the prompt (plain text)
#   - /run/secrets/<name>/        holds declared Secrets, one dir per Secret
#   - $TARGET_REPO                optional: git URL to clone and operate on
#   - $TARGET_BRANCH              optional: base branch; defaults to main
#   - exit 0 = success, non-zero = failure
#
# This script deliberately stays thin: it wires secrets into the env that
# claude / gh expect, clones the repo if one is declared, hands the prompt
# to `claude --print`, and opens a PR if anything changed. Git-host logic
# beyond GitHub will move into conveyor-git (Phase 7).

set -euo pipefail

log() { printf '[implementer] %s\n' "$*" >&2; }
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
    done < <(find "$SECRETS_ROOT" -mindepth 2 -maxdepth 2 -type f -print0 2>/dev/null || true)
fi

# Some CLIs look for GITHUB_TOKEN; normalize from the conventional secret
# name the sample Task declares.
if [[ -z "${GITHUB_TOKEN:-}" && -n "${GITHUB_TOKEN_FILE:-}" && -r "${GITHUB_TOKEN_FILE}" ]]; then
    GITHUB_TOKEN=$(cat "$GITHUB_TOKEN_FILE")
    export GITHUB_TOKEN
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
claude --print < "$PROMPT_FILE"

# Repo flow: only attempt a PR when we actually have a repo and changes.
if [[ -n "${TARGET_REPO:-}" ]] && ! git diff --quiet; then
    branch="conveyor/$(date -u +%Y%m%dT%H%M%SZ)"
    git checkout -b "$branch"
    git add -A
    git commit -m "conveyor: automated change" >/dev/null
    git push -u origin "$branch"
    case "${GIT_HOST:-github}" in
        github)
            gh pr create --fill --base "${TARGET_BRANCH:-main}" --head "$branch"
            ;;
        *)
            die "git host '${GIT_HOST}' not yet supported — waiting on conveyor-git"
            ;;
    esac
else
    log "no repo diff — nothing to commit"
fi

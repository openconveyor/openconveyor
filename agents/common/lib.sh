# shellcheck shell=bash
# Shared shell library of the reference agents.
#
# Each agent Dockerfile copies this file to /usr/local/lib/conveyor/lib.sh.
# There is no shared base image: every image stays self-contained. An
# entrypoint sources the file before it does any work:
#
#   AGENT_NAME=implementer
#   . "${CONVEYOR_LIB:-/usr/local/lib/conveyor/lib.sh}"
#
# The library gives:
#   - log, die                 log lines with the agent name; die also writes
#                              a failure Result
#   - secrets_export           /run/secrets/<name>/<key> -> $NAME_UPPER
#   - github_setup             GH_TOKEN and the git credential helper
#   - repo_slug, gh_repo_from  git URL -> owner/name, and $GH_REPO
#   - result_write             the Result file contract (ADR-0008)
#   - result_partial           the Result that a SIGTERM or a failure writes
#   - run_interruptible        runs a long command so that SIGTERM reaches
#                              the trap while the command runs
#
# The library sets no shell options. The entrypoint sets `set -euo pipefail`.

AGENT_NAME="${AGENT_NAME:-agent}"
SECRETS_ROOT="${SECRETS_ROOT:-/run/secrets}"
RESULT_FILE="${RESULT_FILE:-/dev/termination-log}"

# The limits of the Result contract. The controller rejects a larger file.
RESULT_MAX_BYTES=2048
RESULT_MAX_SUMMARY_CHARS=512
RESULT_MAX_LINKS=8

_result_written=0
_result_partial_summary=""
_result_partial_links=()
_child_pid=""

log() { printf '[%s] %s\n' "$AGENT_NAME" "$*" >&2; }

# die logs the message, writes it as the Result summary together with the
# partial links, and exits 1.
die() {
    log "error: $*"
    result_write "$*" "${_result_partial_links[@]}"
    exit 1
}

# secrets_export exports each projected Secret as a variable. conveyor
# projects the Secret files at /run/secrets/<name>/<key>. In the common case
# the Secret has a single key, and the loop exports SECRETNAME_UPPER=contents
# (anthropic-api-key -> ANTHROPIC_API_KEY, github-token -> GITHUB_TOKEN).
secrets_export() {
    [[ -d "$SECRETS_ROOT" ]] || return 0
    local file name var
    while IFS= read -r -d '' file; do
        name=$(basename "$(dirname "$file")")
        var=$(printf '%s' "$name" | tr '[:lower:]-' '[:upper:]_')
        export "$var=$(cat "$file")"
    done < <(find -L "$SECRETS_ROOT" -mindepth 2 -maxdepth 2 -type f -print0 2>/dev/null || true)
}

# github_setup makes $GITHUB_TOKEN available to gh and git. It reads the
# token from $GITHUB_TOKEN_FILE when the variable is empty. It exports
# GH_TOKEN, which gh reads first, and stores the token in the git credential
# helper, so that HTTPS pushes work without a TTY. The x-access-token user
# is the documented form for installation tokens; PATs accept it too.
github_setup() {
    if [[ -z "${GITHUB_TOKEN:-}" && -n "${GITHUB_TOKEN_FILE:-}" && -r "${GITHUB_TOKEN_FILE}" ]]; then
        GITHUB_TOKEN=$(cat "$GITHUB_TOKEN_FILE")
        export GITHUB_TOKEN
    fi
    [[ -n "${GITHUB_TOKEN:-}" ]] || return 0
    export GH_TOKEN="$GITHUB_TOKEN"
    if command -v git >/dev/null 2>&1; then
        git config --global credential.helper store
        (umask 077 && printf 'https://x-access-token:%s@github.com\n' "$GITHUB_TOKEN" > "$HOME/.git-credentials")
    fi
}

# repo_slug prints the owner/name form of a GitHub repository reference. It
# accepts https://github.com/owner/name(.git), git@github.com:owner/name(.git)
# and owner/name. It prints nothing for a reference it does not understand.
repo_slug() {
    local ref="${1:-}" path
    case "$ref" in
        https://github.com/*) path="${ref#https://github.com/}" ;;
        http://github.com/*)  path="${ref#http://github.com/}" ;;
        git@github.com:*)     path="${ref#git@github.com:}" ;;
        *://*)                return 0 ;;
        *)                    path="$ref" ;;
    esac
    path="${path%/}"
    path="${path%.git}"
    if [[ "$path" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
        printf '%s\n' "$path"
    fi
}

# gh_repo_from exports GH_REPO from a repository reference, so that gh
# commands and the {owner}/{repo} placeholders of `gh api` work outside a
# clone. It keeps an explicit GH_REPO.
gh_repo_from() {
    [[ -z "${GH_REPO:-}" ]] || return 0
    local slug
    slug=$(repo_slug "${1:-}")
    [[ -n "$slug" ]] && export GH_REPO="$slug"
    return 0
}

# _json_string prints its argument as a JSON string with the quotes. The
# caller removes control characters first; this function escapes the
# backslash and the double quote only.
_json_string() {
    local s="${1//\\/\\\\}"
    s="${s//\"/\\\"}"
    printf '"%s"' "$s"
}

# _clean_summary replaces tabs and line breaks with a space, removes the
# other control characters, trims the ends, and keeps at most
# RESULT_MAX_SUMMARY_CHARS characters.
_clean_summary() {
    local LC_ALL=C.UTF-8 s
    s=$(printf '%s' "${1:-}" | tr '\t\n\r' '   ' | tr -d '\000-\010\013\014\016-\037\177')
    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "${s:0:RESULT_MAX_SUMMARY_CHARS}"
}

# _valid_link reports whether "name=url" follows the contract: a kebab-case
# name of at most 32 characters, and an https URL without white space,
# quotes, backslashes or user info. The controller checks the host.
_valid_link() {
    local name="${1%%=*}" url="${1#*=}"
    local name_re='^[a-z0-9]+(-[a-z0-9]+)*$'
    local url_re='^https://[^[:space:]"\\@/]+(/[^[:space:]"\\]*)?$'
    [[ "$1" == *=* ]] || return 1
    [[ ${#name} -le 32 && "$name" =~ $name_re ]] || return 1
    [[ "$url" =~ $url_re ]] || return 1
    [[ ${#url} -le 2048 ]]
}

# _result_json prints the Result object for a summary and a list of links.
_result_json() {
    local summary="$1"; shift
    local out sep="" link
    out="{\"summary\":$(_json_string "$summary")"
    if (($# > 0)); then
        out+=",\"links\":["
        for link in "$@"; do
            out+="${sep}{\"name\":$(_json_string "${link%%=*}"),\"url\":$(_json_string "${link#*=}")}"
            sep=","
        done
        out+="]"
    fi
    printf '%s}' "$out"
}

# result_write writes the Result file (ADR-0008):
#
#   result_write <summary> [name=url ...]
#
# It cleans the summary, drops a link that breaks the contract or a
# duplicate name, and keeps at most 8 links. When the JSON is larger than
# 2048 bytes, it drops links from the end, then shortens the summary. A
# write error is not fatal: the Result is optional.
result_write() {
    local summary links=() link name seen=" " json
    summary=$(_clean_summary "${1:-}")
    shift || true
    for link in "$@"; do
        [[ -n "$link" ]] || continue
        if ! _valid_link "$link"; then
            log "warning: dropped a Result link that breaks the contract"
            continue
        fi
        name="${link%%=*}"
        [[ "$seen" != *" $name "* ]] || continue
        seen+="$name "
        if ((${#links[@]} < RESULT_MAX_LINKS)); then
            links+=("$link")
        fi
    done

    json=$(_result_json "$summary" "${links[@]}")
    while ((${#links[@]} > 0 && $(_byte_length "$json") > RESULT_MAX_BYTES)); do
        unset 'links[${#links[@]}-1]'
        json=$(_result_json "$summary" "${links[@]}")
    done
    while (($(_byte_length "$json") > RESULT_MAX_BYTES)) && [[ -n "$summary" ]]; do
        summary=$(_shorten "$summary" 16)
        json=$(_result_json "$summary" "${links[@]}")
    done

    if printf '%s\n' "$json" > "$RESULT_FILE" 2>/dev/null; then
        _result_written=1
    else
        log "warning: could not write the Result to $RESULT_FILE"
    fi
}

_byte_length() {
    local LC_ALL=C
    printf '%s' "${#1}"
}

# _shorten removes the last n characters, not bytes, so that it never cuts a
# multibyte character in half.
_shorten() {
    local LC_ALL=C.UTF-8 s="$1"
    if ((${#s} <= $2)); then
        printf ''
    else
        printf '%s' "${s:0:${#s}-$2}"
    fi
}

# result_partial records the Result that the agent has at this point. The
# SIGTERM trap and a failed exit write it, unless the entrypoint already
# wrote a final Result.
#
#   result_partial <summary> [name=url ...]
result_partial() {
    _result_partial_summary="${1:-}"
    shift || true
    _result_partial_links=("$@")
}

# run_interruptible runs a command in the background and waits for it. Bash
# runs a trap only between foreground commands, so a long foreground command
# delays the SIGTERM trap until SIGKILL. With `wait`, the trap runs at once.
run_interruptible() {
    # A background job reads /dev/null unless it has an explicit stdin
    # redirect. fd 3 passes the stdin of the caller through.
    exec 3<&0
    "$@" <&3 3<&- &
    exec 3<&-
    _child_pid=$!
    local rc=0
    wait "$_child_pid" || rc=$?
    _child_pid=""
    return "$rc"
}

_on_term() {
    log "received SIGTERM, writing a partial Result"
    [[ -z "$_child_pid" ]] || kill -TERM "$_child_pid" 2>/dev/null || true
    result_write "${_result_partial_summary:-Stopped by SIGTERM before the agent finished}" \
        "${_result_partial_links[@]}"
    exit 143
}

_on_exit() {
    local rc=$?
    if ((rc != 0 && _result_written == 0)); then
        result_write "${_result_partial_summary:-The agent failed with exit code $rc}" \
            "${_result_partial_links[@]}"
    fi
    return "$rc"
}

trap _on_term TERM
trap _on_exit EXIT

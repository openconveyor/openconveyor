#!/usr/bin/env bash
# The snippets in single quotes expand in the child shell on purpose.
# shellcheck disable=SC2016
# Tests for agents/common/lib.sh. Run: bash agents/common/test/lib_test.sh
# The test needs bash 4.4 or later and python3, which validates the JSON.

set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LIB="$HERE/../lib.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fails=0
pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s: %s\n' "$1" "$2"; fails=$((fails + 1)); }

# run_lib runs a snippet in a fresh bash with the library sourced. The
# Result goes to $TMP/result. The snippet sets no traps of its own.
run_lib() {
    rm -f "$TMP/result"
    RESULT_FILE="$TMP/result" SECRETS_ROOT="$TMP/secrets" HOME="$TMP/home" \
        AGENT_NAME=test bash -c "set -euo pipefail; . '$LIB'; $1" 2>"$TMP/stderr"
}

# json_get prints a Python expression over the parsed Result.
json_get() {
    python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$TMP/result" "$1"
}

bytes() { LC_ALL=C wc -c < "$TMP/result" | tr -d ' '; }

mkdir -p "$TMP/home"

# --- result_write: shape and escaping ---------------------------------------
t="result_write writes strict JSON with escaped quotes and backslashes"
run_lib 'result_write "say \"hi\" C:\\dir" "pull-request=https://github.com/a/b/pull/1" "issue=https://github.com/a/b/issues/2"'
if [[ "$(json_get 'r["summary"]')" == 'say "hi" C:\dir' && \
      "$(json_get 'r["links"][0]["name"]+"|"+r["links"][1]["url"]')" == "pull-request|https://github.com/a/b/issues/2" && \
      "$(json_get 'sorted(r.keys())')" == "['links', 'summary']" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="result_write without links omits the links key"
run_lib 'result_write "done"'
if [[ "$(cat "$TMP/result")" == '{"summary":"done"}' ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="control characters are removed and line breaks become spaces"
run_lib 'result_write "$(printf "line one\nline\ttwo\001\033[31m red")"'
if [[ "$(json_get 'r["summary"]')" == "line one line two[31m red" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="summary is cut to 512 characters, multibyte safe"
run_lib 'result_write "$(printf "é%.0s" $(seq 600))"'
if [[ "$(json_get 'len(r["summary"])')" == 512 && "$(json_get 'set(r["summary"])')" == "{'é'}" ]]; then
    pass "$t"; else fail "$t" "length $(json_get 'len(r["summary"])')"; fi

t="links that break the contract are dropped"
run_lib 'result_write "x" "a=http://github.com/x" "b=https://u:p@github.com/x" "Bad_Name=https://github.com/x" "c=https://github.com/x y" "d=https://github.com/\"q" "noequals" "ok=https://github.com/ok" "ok=https://github.com/dup"'
if [[ "$(json_get '[l["name"]+"="+l["url"] for l in r["links"]]')" == "['ok=https://github.com/ok']" ]]; then
    pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="at most 8 links are kept"
run_lib 'args=(); for i in $(seq 10); do args+=("l$i=https://github.com/$i"); done; result_write "x" "${args[@]}"'
if [[ "$(json_get 'len(r["links"])')" == 8 ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="links are dropped from the end until the file fits in 2048 bytes"
run_lib 'long=$(printf "%0.sa" $(seq 400)); args=(); for i in $(seq 8); do args+=("l$i=https://github.com/$long$i"); done; result_write "$(printf "ü%.0s" $(seq 300))" "${args[@]}"'
if (( $(bytes) <= 2048 )) && [[ "$(json_get '[l["name"] for l in r["links"]]')" == "['l1', 'l2', 'l3']" && "$(json_get 'len(r["summary"])')" == 300 ]]; then
    pass "$t ($(bytes) bytes)"; else fail "$t" "$(bytes) bytes: $(cat "$TMP/result")"; fi

t="a summary of 4-byte characters shrinks under 2048 bytes without a cut character"
run_lib 'result_write "$(printf "😀%.0s" $(seq 512))"'
if (( $(bytes) <= 2048 )) && [[ "$(json_get 'set(r["summary"])')" == "{'😀'}" ]]; then
    pass "$t ($(bytes) bytes)"; else fail "$t" "$(bytes) bytes"; fi

# --- die, the EXIT trap and the SIGTERM trap ---------------------------------
t="die writes the message and the partial links as the Result"
run_lib 'result_partial "pushed" "pull-request=https://github.com/a/b/pull/9"; die "gh pr create failed"' || true
if [[ "$(json_get 'r["summary"]+"|"+r["links"][0]["name"]')" == "gh pr create failed|pull-request" ]]; then
    pass "$t"; else fail "$t" "$(cat "$TMP/result" 2>/dev/null)"; fi

t="an unexpected failure writes the partial Result"
run_lib 'result_partial "cloned the repo"; false' || true
if [[ "$(json_get 'r["summary"]')" == "cloned the repo" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result" 2>/dev/null)"; fi

t="an unexpected failure without a partial Result names the exit code"
run_lib 'exit 7' || true
if [[ "$(json_get 'r["summary"]')" == "The agent failed with exit code 7" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result" 2>/dev/null)"; fi

t="a success without result_write leaves no file"
run_lib 'true'
if [[ ! -e "$TMP/result" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="a final Result is not overwritten by a later failure"
run_lib 'result_write "final"; exit 3' || true
if [[ "$(json_get 'r["summary"]')" == "final" ]]; then pass "$t"; else fail "$t" "$(cat "$TMP/result")"; fi

t="SIGTERM during run_interruptible writes the partial Result and stops the child"
rm -f "$TMP/result" "$TMP/child-alive"
RESULT_FILE="$TMP/result" HOME="$TMP/home" AGENT_NAME=test bash -c "
    set -euo pipefail; . '$LIB'
    result_partial 'branch pushed, no PR' 'issue=https://github.com/a/b/issues/1'
    run_interruptible bash -c 'sleep 30; touch $TMP/child-alive'
" 2>"$TMP/stderr" &
pid=$!
sleep 0.5
kill -TERM "$pid"
rc=0; wait "$pid" || rc=$?
sleep 0.2
if [[ $rc -eq 143 && "$(json_get 'r["summary"]+"|"+r["links"][0]["name"]')" == "branch pushed, no PR|issue" && ! -e "$TMP/child-alive" ]]; then
    pass "$t"; else fail "$t" "rc=$rc $(cat "$TMP/result" 2>/dev/null)"; fi

t="run_interruptible passes stdin through to the command"
if run_lib 'printf "hello" > "$HOME/in"; run_interruptible cat < "$HOME/in" > "$HOME/out"; [[ "$(cat "$HOME/out")" == hello ]]'; then
    pass "$t"; else fail "$t" "stdin did not reach the background command"; fi

# --- secrets, GitHub setup, repo slugs ---------------------------------------
t="secrets_export maps /run/secrets/<name>/<key> to NAME_UPPER"
mkdir -p "$TMP/secrets/github-token" "$TMP/secrets/anthropic-api-key"
printf 'ghp_x' > "$TMP/secrets/github-token/token"
printf 'sk-y' > "$TMP/secrets/anthropic-api-key/api-key"
out=$(run_lib 'secrets_export; printf "%s|%s" "$GITHUB_TOKEN" "$ANTHROPIC_API_KEY"')
if [[ "$out" == "ghp_x|sk-y" ]]; then pass "$t"; else fail "$t" "$out"; fi

t="github_setup exports GH_TOKEN and writes the credential helper file"
out=$(run_lib 'secrets_export; github_setup; printf "%s" "$GH_TOKEN"; cat "$HOME/.git-credentials"')
if [[ "$out" == "ghp_xhttps://x-access-token:ghp_x@github.com" ]]; then pass "$t"; else fail "$t" "$out"; fi
if [[ "$(stat -c %a "$TMP/home/.git-credentials")" == 600 ]]; then pass "credential file has mode 600"; else fail "credential file mode" "$(stat -c %a "$TMP/home/.git-credentials")"; fi

t="repo_slug understands the GitHub URL forms"
out=$(run_lib 'for r in https://github.com/a/b.git https://github.com/a/b git@github.com:a/b.git a/b https://gitlab.com/a/b "not a repo"; do printf "[%s]" "$(repo_slug "$r")"; done')
if [[ "$out" == "[a/b][a/b][a/b][a/b][][]" ]]; then pass "$t"; else fail "$t" "$out"; fi

t="gh_repo_from keeps an explicit GH_REPO"
out=$(GH_REPO=x/y run_lib 'gh_repo_from https://github.com/a/b.git; printf "%s" "$GH_REPO"')
if [[ "$out" == "x/y" ]]; then pass "$t"; else fail "$t" "$out"; fi

if ((fails > 0)); then
    printf '%d test(s) failed\n' "$fails"
    exit 1
fi
printf 'all lib.sh tests passed\n'

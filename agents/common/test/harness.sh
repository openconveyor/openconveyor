# shellcheck shell=bash disable=SC2034 # the tests read RC and the other globals
# Test harness for the entrypoints of the reference agents.
#
# A test sources this file, writes fake `gh` and `claude` scripts with
# fake_cmd, and runs the entrypoint with run_entrypoint. The fakes append
# each call to $CALLS, one line per call, so that a test can assert on the
# side effects on the git host without a network.
#
#   . "$(dirname "$0")/../../common/test/harness.sh"
#   harness_init claude-code-reviewer
#   fake_cmd claude 'echo "LGTM"'
#   run_entrypoint
#   assert_result_summary "Review (COMMENT): LGTM"

HARNESS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
AGENTS_DIR=$(cd "$HARNESS_DIR/../.." && pwd)

_fails=0

harness_init() {
    AGENT_DIR="$AGENTS_DIR/$1"
    TMP=$(mktemp -d)
    # shellcheck disable=SC2064 # expand TMP now, the trap runs at exit
    trap "rm -rf '$TMP'" EXIT
    harness_reset
}

# harness_reset gives each test case a clean fake home, work dir and call log.
harness_reset() {
    rm -rf "${TMP:?}"/*
    mkdir -p "$TMP/bin" "$TMP/home" "$TMP/work" "$TMP/secrets/github-token" "$TMP/secrets/anthropic-api-key"
    printf 'ghp_test' > "$TMP/secrets/github-token/token"
    printf 'sk-test' > "$TMP/secrets/anthropic-api-key/api-key"
    CALLS="$TMP/calls"
    : > "$CALLS"
    RESULT_FILE="$TMP/result"
    PROMPT_FILE="$TMP/prompt"
    printf 'do the work\n' > "$PROMPT_FILE"
    RC=0
}

# fake_cmd writes an executable named $1 into the fake bin directory. The
# body is bash; "$@" are the arguments. Each call is logged to $CALLS first.
fake_cmd() {
    local name="$1" body="$2"
    cat > "$TMP/bin/$name" <<EOF
#!/usr/bin/env bash
printf '%s %s\n' "$name" "\$*" >> "$CALLS"
$body
EOF
    chmod +x "$TMP/bin/$name"
}

# run_entrypoint runs the entrypoint of the agent with the fakes first on
# PATH. Extra arguments are VAR=value pairs for the environment. It sets RC
# and writes the logs to $TMP/stderr and $TMP/stdout.
run_entrypoint() {
    RC=0
    env -i PATH="$TMP/bin:$PATH" HOME="$TMP/home" \
        CONVEYOR_LIB="$AGENTS_DIR/common/lib.sh" \
        RESULT_FILE="$RESULT_FILE" PROMPT_FILE="$PROMPT_FILE" \
        SECRETS_ROOT="$TMP/secrets" WORKDIR="$TMP/work" \
        CALLS="$CALLS" TMP="$TMP" \
        GIT_CONFIG_NOSYSTEM=1 GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z" \
        "$@" bash "$AGENT_DIR/entrypoint.sh" > "$TMP/stdout" 2> "$TMP/stderr" || RC=$?
}

result_field() {
    python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$RESULT_FILE" "$1"
}

pass() { printf 'ok   %s\n' "$1"; }
fail() {
    printf 'FAIL %s: %s\n' "$1" "$2"
    printf '  --- stderr\n'; sed 's/^/  /' "$TMP/stderr" 2>/dev/null | tail -n 15
    printf '  --- calls\n'; sed 's/^/  /' "$CALLS" 2>/dev/null | tail -n 15
    _fails=$((_fails + 1))
}

# check runs a condition and records the outcome of test case $1.
check() {
    local name="$1"; shift
    if "$@"; then pass "$name"; else fail "$name" "condition failed: $*"; fi
}

has_call() { grep -qF -- "$1" "$CALLS"; }
no_call() { ! grep -qF -- "$1" "$CALLS"; }
result_is() { [[ -f "$RESULT_FILE" && "$(result_field "$1")" == "$2" ]]; }

harness_done() {
    if ((_fails > 0)); then
        printf '%d test(s) failed\n' "$_fails"
        exit 1
    fi
    printf 'all %s tests passed\n' "$(basename "$AGENT_DIR")"
}

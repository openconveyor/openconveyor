#!/usr/bin/env bash
# Tests for check-secrets.sh and select-kinds.sh. Run: make test
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
fails=0
check() {
    local name="$1"; shift
    if "$@"; then printf 'ok   %s\n' "$name"; else printf 'FAIL %s\n' "$name"; fails=$((fails + 1)); fi
}

# --- check-secrets.sh ----------------------------------------------------------
make_secrets() {
    rm -rf "$TMP/s"; mkdir -p "$TMP/s"
    (umask 077; for n in anthropic-api-key github-token github-webhook-secret; do printf 'value' > "$TMP/s/$n"; done)
}
run_check() { "$HERE/check-secrets.sh" "$TMP/s" > "$TMP/out" 2>&1; }

make_secrets
check "valid files pass" run_check
make_secrets; rm "$TMP/s/github-token"
check "a missing file fails" bash -c "! '$HERE/check-secrets.sh' '$TMP/s' >/dev/null 2>&1"
make_secrets; chmod 644 "$TMP/s/anthropic-api-key"
check "mode 644 fails" bash -c "! '$HERE/check-secrets.sh' '$TMP/s' >/dev/null 2>&1"
make_secrets; printf 'value\n' > "$TMP/s/github-webhook-secret"
check "a trailing newline fails" bash -c "! '$HERE/check-secrets.sh' '$TMP/s' >/dev/null 2>&1"
check "the error names the newline" bash -c "'$HERE/check-secrets.sh' '$TMP/s' 2>&1 | grep -q 'ends with a newline'"
make_secrets; : > "$TMP/s/github-token"
check "an empty file fails" bash -c "! '$HERE/check-secrets.sh' '$TMP/s' >/dev/null 2>&1"
make_secrets; mv "$TMP/s/github-token" "$TMP/real-token"; ln -s "$TMP/real-token" "$TMP/s/github-token"
check "a symlink to a valid file passes" run_check
check "the output never shows a value" bash -c "! grep -q value '$TMP/out'"

# --- select-kinds.sh ------------------------------------------------------------
cat > "$TMP/in.yaml" <<'YAML'
apiVersion: v1
kind: Namespace
metadata:
  name: a
---
apiVersion: v1
kind: Secret
metadata:
  name: s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: d
spec:
  template:
    kind: not-top-level
YAML
kinds() { grep '^kind:' | awk '{print $2}' | tr '\n' ' '; }
check "keep selects the listed kinds" test "$("$HERE/select-kinds.sh" keep Secret < "$TMP/in.yaml" | kinds)" == "Secret "
check "drop removes the listed kinds" test "$("$HERE/select-kinds.sh" drop Namespace Secret < "$TMP/in.yaml" | kinds)" == "Deployment "
check "a nested kind key does not count" bash -c "'$HERE/select-kinds.sh' drop Namespace Secret < '$TMP/in.yaml' | grep -q 'kind: not-top-level'"

if ((fails > 0)); then printf '%d test(s) failed\n' "$fails"; exit 1; fi
printf 'all stack script tests passed\n'

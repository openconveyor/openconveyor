#!/usr/bin/env bash
# Tests for the reporter entrypoint with fake gh and claude on PATH.
# Run: bash agents/claude-code-reporter/test/entrypoint_test.sh
# shellcheck disable=SC2016 # the fake bodies expand when the fake runs

set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=../../common/test/harness.sh
. "$(dirname "${BASH_SOURCE[0]}")/../../common/test/harness.sh"
harness_init claude-code-reporter

issue=https://github.com/acme/repo/issues/1

# --- posts the report to the issue -------------------------------------------
fake_cmd gh 'case "$1 $2" in "pr list"|"issue list") echo "[]";; esac'
fake_cmd claude 'cat > "$TMP/claude-input"; printf "# Weekly report\n\nThree PRs merged.\n"'
run_entrypoint TARGET_REPO=acme/repo REPORT_ISSUE_URL="$issue"
check "exits 0 after the report" test "$RC" -eq 0
check "lists merged PRs of the repo" has_call "gh pr list --repo acme/repo --state merged"
check "posts the report as an issue comment" has_call "gh issue comment $issue --body-file"
check "sends the repository data to claude" grep -q '# Repository data' "$TMP/claude-input"
check "prints the report to stdout" grep -q 'Three PRs merged.' "$TMP/stdout"
check "the Result summary is the first line of the report" result_is 'r["summary"]' "Weekly report"
check "the Result links the issue" result_is 'r["links"][0]["name"]+"="+r["links"][0]["url"]' "issue=$issue"

# --- no issue: stdout only, no link ------------------------------------------
harness_reset
fake_cmd gh ''
fake_cmd claude 'printf "Nothing happened this week.\n"'
run_entrypoint
check "exits 0 without a repo and an issue" test "$RC" -eq 0
check "calls gh not at all" no_call "gh "
check "the Result has no links" result_is '"links" in r' "False"
check "the Result summary is the report" result_is 'r["summary"]' "Nothing happened this week."

# --- empty report --------------------------------------------------------------
harness_reset
fake_cmd gh ''
fake_cmd claude ''
run_entrypoint REPORT_ISSUE_URL="$issue"
check "fails on an empty report" test "$RC" -ne 0
check "posts nothing" no_call "gh issue comment"
check "the Result says why" result_is 'r["summary"]' "claude produced an empty report"

# --- claude fails --------------------------------------------------------------
harness_reset
fake_cmd gh ''
fake_cmd claude 'exit 4'
run_entrypoint
check "fails when claude fails" test "$RC" -eq 4
check "the EXIT trap writes a failure Result" result_is 'r["summary"]' "The agent failed with exit code 4"

harness_done

#!/usr/bin/env bash
# Tests for the reviewer entrypoint with fake gh and claude on PATH.
# Run: bash agents/claude-code-reviewer/test/entrypoint_test.sh
# shellcheck disable=SC2016 # the fake bodies expand when the fake runs

set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=../../common/test/harness.sh
. "$(dirname "${BASH_SOURCE[0]}")/../../common/test/harness.sh"
harness_init claude-code-reviewer

pr=https://github.com/acme/repo/pull/5

# --- posts the review and reports the verdict --------------------------------
printf 'Review this PR: %s\n' "$pr" > "$PROMPT_FILE"
fake_cmd gh 'printf "%s" "${GH_TOKEN:-}" > "$TMP/gh-token"; case "$1 $2" in "pr diff") echo "+new line";; esac'
fake_cmd claude 'cat > "$TMP/claude-input"; printf "\n## Looks good\n\nOne nit.\n"'
run_entrypoint
check "exits 0 after the review" test "$RC" -eq 0
check "reads the diff of the PR in the prompt" has_call "gh pr diff $pr"
check "posts a COMMENT review with a body file" has_call "gh pr review $pr --comment --body-file"
check "sends the prompt and the diff to claude" grep -q '+new line' "$TMP/claude-input"
check "the Result summary is the verdict" result_is 'r["summary"]' "Review (COMMENT): Looks good"
check "the Result links the pull request" result_is 'r["links"][0]["name"]+"="+r["links"][0]["url"]' "pull-request=$pr"
check "gh gets GH_TOKEN from the github-token Secret" grep -qx ghp_test "$TMP/gh-token"

# --- REVIEW_EVENT selects the review flag ------------------------------------
harness_reset
printf 'Review %s\n' "$pr" > "$PROMPT_FILE"
fake_cmd gh 'case "$1 $2" in "pr diff") echo "+x";; esac'
fake_cmd claude 'echo "Ship it"'
run_entrypoint REVIEW_EVENT=APPROVE
check "APPROVE posts --approve" has_call "gh pr review $pr --approve"
check "the verdict names the event" result_is 'r["summary"]' "Review (APPROVE): Ship it"

# --- no PR URL ---------------------------------------------------------------
harness_reset
printf 'no url here\n' > "$PROMPT_FILE"
fake_cmd gh ''
fake_cmd claude 'echo unused'
run_entrypoint
check "fails without a PR URL" test "$RC" -ne 0
check "the Result says why" result_is 'r["summary"]' 'no PR URL: set $PR_URL or include one in the prompt'
check "posts nothing" no_call "gh pr review"

# --- empty review ------------------------------------------------------------
harness_reset
printf 'Review %s\n' "$pr" > "$PROMPT_FILE"
fake_cmd gh 'case "$1 $2" in "pr diff") echo "+x";; esac'
fake_cmd claude ''
run_entrypoint
check "refuses to post an empty review" no_call "gh pr review"
check "the failure Result keeps the PR link" result_is 'r["links"][0]["name"]' "pull-request"

harness_done

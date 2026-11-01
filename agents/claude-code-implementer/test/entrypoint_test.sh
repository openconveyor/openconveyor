#!/usr/bin/env bash
# Tests for the implementer entrypoint (v2) with fake gh and claude on PATH
# and a local bare repository as the git remote.
# Run: bash agents/claude-code-implementer/test/entrypoint_test.sh
# shellcheck disable=SC2016 # the fake bodies expand when the fake runs

set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=../../common/test/harness.sh
. "$(dirname "${BASH_SOURCE[0]}")/../../common/test/harness.sh"
harness_init claude-code-implementer

# The fake gh answers from files in $TMP. It logs every call (harness).
GH_FAKE='
case "$1 $2" in
"issue view") cat "$TMP/issue.json" ;;
"issue comment") ;;
"pr list") cat "$TMP/existing-pr" 2>/dev/null || true ;;
"pr create")
    while (($#)); do [[ "$1" == --body-file ]] && cp "$2" "$TMP/pr-body"; shift; done
    echo "https://github.com/acme/repo/pull/12" ;;
"pr edit"|"pr comment") ;;
"pr view") cat "$TMP/pr.json" ;;
"pr checkout")
    head=$(cat "$TMP/pr-head")
    git fetch -q origin "$head" && git checkout -q -B "$head" FETCH_HEAD ;;
"api graphql") cat "$TMP/threads.jsonl" ;;
"api -X") ;;
*) echo "fake gh: unexpected call: $*" >&2; exit 2 ;;
esac'

# The fake claude: the implementation pass (bypassPermissions) runs
# $TMP/claude-impl in the checkout; the describe pass prints $TMP/describe.
CLAUDE_FAKE='
if [[ " $* " == *" bypassPermissions "* ]]; then
    cat > "$TMP/claude-prompt"
    printf "%s\n" "$*" > "$TMP/claude-args"
    bash "$TMP/claude-impl"
else
    cat > "$TMP/describe-input"
    cat "$TMP/describe"
fi'

# setup_case resets the harness and creates the remote with one commit on
# main, the fakes, an issue and the default Claude behavior.
setup_case() {
    harness_reset
    REMOTE="file://$TMP/remote.git"
    git init -q --bare -b main "$TMP/remote.git"
    if [[ "${1:-}" != empty ]]; then
        git clone -q "$REMOTE" "$TMP/seed" 2>/dev/null
        (cd "$TMP/seed" && printf 'hello\n' > hello.txt && git add . \
            && git -c user.name=t -c user.email=t@t commit -q -m "chore: seed" && git push -q origin main)
    fi
    fake_cmd gh "$GH_FAKE"
    fake_cmd claude "$CLAUDE_FAKE"
    cat > "$TMP/issue.json" <<'EOF'
{"number":7,"title":"Add a README","body":"Explain the repo.","url":"https://github.com/acme/repo/issues/7",
 "labels":[{"name":"conveyor:ready"}],"comments":[{"author":{"login":"alice"},"body":"Keep it short."}]}
EOF
    printf 'printf "# Repo\\n" > README.md\n' > "$TMP/claude-impl"
    printf '{"subject":"docs: add a README","title":"Add a README","body":"Adds a README."}\n' > "$TMP/describe"
}

run_impl() { run_entrypoint TARGET_REPO="$REMOTE" GH_REPO=acme/repo "$@"; }
remote_log() { git --git-dir="$TMP/remote.git" log --format='%s|%an' "$1" 2>/dev/null; }
remote_file() { git --git-dir="$TMP/remote.git" show "$1" 2>/dev/null; }

# --- implement: new PR for an issue -------------------------------------------
setup_case
run_impl ISSUE_NUMBER=7
check "implement exits 0" test "$RC" -eq 0
check "pushes branch conveyor/issue-7 with the entrypoint commit" \
    test "$(remote_log conveyor/issue-7 | head -n1)" == "docs: add a README|conveyor-implementer"
check "the commit carries the change" test "$(remote_file conveyor/issue-7:README.md)" == "# Repo"
check "the commit message refers to the issue" \
    grep -q 'Refs #7' <(git --git-dir="$TMP/remote.git" log -1 --format=%B conveyor/issue-7)
check "opens the PR against main from the issue branch" \
    has_call "gh pr create --base main --head conveyor/issue-7 --title Add a README"
check "the PR body closes the issue" grep -qx 'Closes #7' "$TMP/pr-body"
check "the PR body has the footer" grep -qx 'Opened by the OpenConveyor implementer for #7.' "$TMP/pr-body"
check "the PR body has the summary" grep -qx 'Adds a README.' "$TMP/pr-body"
check "labels the PR conveyor:review" has_call "gh pr edit 12 --add-label conveyor:review"
check "comments on the issue with the PR link" has_call "gh issue comment 7 --body Opened https://github.com/acme/repo/pull/12"
check "the prompt holds the issue and its comments" grep -q 'Keep it short.' "$TMP/claude-prompt"
check "the prompt tells Claude to leave the tree uncommitted" grep -q 'Leave all changes uncommitted' "$TMP/claude-prompt"
check "claude may not commit, push or call gh" grep -q 'Bash(git push:\*) Bash(gh:\*)' "$TMP/claude-args"
check "the describe pass sees the diff" grep -q '+# Repo' "$TMP/describe-input"
check "the Result summary names the PR and the issue" \
    result_is 'r["summary"]' "Opened PR #12 for issue #7: Add a README"
check "the Result links the PR and the issue" \
    result_is '",".join(l["name"]+"="+l["url"] for l in r["links"])' \
    "pull-request=https://github.com/acme/repo/pull/12,issue=https://github.com/acme/repo/issues/7"

# --- implement: Claude commits despite the prompt ------------------------------
setup_case
printf 'printf "x\\n" > a.txt; git add a.txt; git -c user.name=c -c user.email=c@c commit -q -m "wip by claude"\n' > "$TMP/claude-impl"
run_impl ISSUE_NUMBER=7
check "a commit by Claude is folded into the entrypoint commit" \
    test "$(remote_log conveyor/issue-7 | head -n2 | tr '\n' ' ')" == "docs: add a README|conveyor-implementer chore: seed|t "
check "the PR still opens" has_call "gh pr create"

# --- implement: no change ---------------------------------------------------------
setup_case
printf 'true\n' > "$TMP/claude-impl"
run_impl ISSUE_NUMBER=7
check "an empty diff fails the Task" test "$RC" -ne 0
check "the Result says that there was no change" \
    result_is 'r["summary"]' "claude made no change in the repository; nothing to commit"
check "the failure Result keeps the issue link" result_is 'r["links"][0]["name"]' "issue"
check "nothing is pushed" test -z "$(remote_log conveyor/issue-7)"
check "no PR opens" no_call "gh pr create"

# --- implement: Claude fails ---------------------------------------------------
setup_case
printf 'printf "half\\n" > a.txt; exit 3\n' > "$TMP/claude-impl"
run_impl ISSUE_NUMBER=7
check "a Claude failure fails the Task" test "$RC" -ne 0
check "the Result names the exit code" \
    result_is 'r["summary"]' "claude exited with code 3; the entrypoint committed nothing"
check "nothing is pushed after a failure" test -z "$(remote_log conveyor/issue-7)"

# --- implement: empty repository --------------------------------------------------
setup_case empty
run_impl ISSUE_NUMBER=7
check "an empty repository works" test "$RC" -eq 0
check "main gets an initial commit" test "$(remote_log main)" == "chore: initial commit|conveyor-implementer"
check "the PR targets the new main" has_call "gh pr create --base main --head conveyor/issue-7"

# --- implement: the base branch is missing ------------------------------------------
setup_case
run_impl ISSUE_NUMBER=7 TARGET_BRANCH=develop
check "a missing base branch fails" test "$RC" -ne 0
check "the Result names the branch" result_is 'r["summary"]' "base branch develop does not exist in $REMOTE"

# --- implement: reuse the open PR of the issue ------------------------------------
setup_case
(cd "$TMP/seed" && git checkout -q -b conveyor/issue-7 && printf 'old\n' > README.md && git add . \
    && git -c user.name=t -c user.email=t@t commit -q -m "docs: first try" && git push -q origin conveyor/issue-7)
printf '12 https://github.com/acme/repo/pull/12\n' > "$TMP/existing-pr"
run_impl ISSUE_NUMBER=7
check "reuse exits 0" test "$RC" -eq 0
check "reuse does not open a second PR" no_call "gh pr create"
check "reuse pushes on top of the PR branch" \
    test "$(remote_log conveyor/issue-7 | head -n2 | cut -d'|' -f1 | tr '\n' ' ')" == "docs: add a README docs: first try "
check "reuse comments on the PR" has_call "gh pr comment 12 --body Updated in"
check "reuse re-adds the review label" has_call "gh pr edit 12 --remove-label conveyor:review"
check "the issue comment says Updated" has_call "gh issue comment 7 --body Updated https://github.com/acme/repo/pull/12"

# --- implement: the describe answer is not usable -----------------------------------
setup_case
printf 'Sure! Here is a summary: it adds a README.\n' > "$TMP/describe"
run_impl ISSUE_NUMBER=7
check "a bad describe answer falls back" test "$(remote_log conveyor/issue-7 | head -n1)" == "feat: implement #7|conveyor-implementer"
check "the fallback PR title is the issue title" has_call "--title Add a README"

setup_case
printf '{"subject":"added stuff","title":"x\\ny","body":"b"}\n' > "$TMP/describe"
run_impl ISSUE_NUMBER=7
check "a subject that is not a Conventional Commit falls back" \
    test "$(remote_log conveyor/issue-7 | head -n1)" == "feat: implement #7|conveyor-implementer"

# --- implement: the prompt URL is the fallback for ISSUE_NUMBER ---------------------
setup_case
printf 'https://github.com/acme/repo/issues/7\n' > "$PROMPT_FILE"
run_impl
check "the issue URL in the prompt selects issue 7" has_call "gh issue view 7"
check "the URL-only prompt is not repeated" bash -c "! grep -q 'Trigger prompt' '$TMP/claude-prompt'"

# --- implement: no issue -------------------------------------------------------------
setup_case
printf 'Add a hello script\n' > "$PROMPT_FILE"
run_impl BRANCH_NAME=conveyor/hello
check "without an issue the branch is BRANCH_NAME" has_call "gh pr create --base main --head conveyor/hello"
check "without an issue the PR body has no Closes" bash -c "! grep -q 'Closes' '$TMP/pr-body'"
check "without an issue there is no issue comment" no_call "gh issue comment"
check "without an issue the Result has only the PR" result_is 'len(r["links"])' "1"

# --- no TARGET_REPO -------------------------------------------------------------------
setup_case
printf 'What is 2+2?\n' > "$PROMPT_FILE"
run_entrypoint
check "without a repo the agent answers the prompt" test "$RC" -eq 0
check "without a repo there are no gh calls" no_call "gh "
check "without a repo the Result says so" result_is 'r["summary"]' "Answered the prompt without a repository"

# --- iterate ---------------------------------------------------------------------------

# setup_iterate adds the PR branch conveyor/issue-7 and the PR data.
setup_iterate() {
    setup_case
    (cd "$TMP/seed" && git checkout -q -b conveyor/issue-7 && printf 'v1\n' > README.md && git add . \
        && git -c user.name=t -c user.email=t@t commit -q -m "docs: add a README" && git push -q origin conveyor/issue-7)
    printf 'conveyor/issue-7' > "$TMP/pr-head"
    cat > "$TMP/pr.json" <<'EOF'
{"number":12,"url":"https://github.com/acme/repo/pull/12","title":"Add a README","body":"Adds a README.",
 "state":"OPEN","headRefName":"conveyor/issue-7","baseRefName":"main","isCrossRepository":false,
 "closingIssuesReferences":[{"number":7}],
 "commits":[{"committedDate":"2026-01-01T00:00:00Z"}],
 "comments":[{"author":{"login":"alice"},"body":"Old note","createdAt":"2025-12-31T00:00:00Z"},
             {"author":{"login":"alice"},"body":"Also add a license section","createdAt":"2026-01-02T00:00:00Z"},
             {"author":{"login":"bot"},"body":"Updated in abc. <!-- openconveyor:implementer -->","createdAt":"2026-01-02T00:00:00Z"}],
 "reviews":[{"author":{"login":"reviewer-bot"},"state":"COMMENTED","body":"Typo in the title.","submittedAt":"2026-01-02T00:00:00Z"}]}
EOF
    cat > "$TMP/threads.jsonl" <<'EOF'
{"id":"PRRT_1","isResolved":false,"path":"README.md","line":1,"comments":{"nodes":[{"databaseId":101,"author":{"login":"alice"},"body":"Use a level-1 heading"}]}}
{"id":"PRRT_2","isResolved":false,"path":"README.md","line":null,"comments":{"nodes":[{"databaseId":202,"author":{"login":"alice"},"body":"Add badges"}]}}
EOF
    printf 'printf "# Repo v2\\n" > README.md; printf "T1\\n" > "$WORKDIR/addressed-threads"\n' > "$TMP/claude-impl"
    printf '{"subject":"docs: use a level-1 heading","title":"t","body":"b"}\n' > "$TMP/describe"
}

setup_iterate
run_impl PR_NUMBER=12
sha=$(git --git-dir="$TMP/remote.git" rev-parse --short conveyor/issue-7)
check "iterate exits 0" test "$RC" -eq 0
check "iterate pushes to the PR branch" test "$(remote_log conveyor/issue-7 | head -n1)" == "docs: use a level-1 heading|conveyor-implementer"
check "iterate never opens a PR" no_call "gh pr create"
check "replies on the addressed thread with the SHA" \
    has_call "gh api -X POST repos/{owner}/{repo}/pulls/12/comments/101/replies -f body=Addressed in $sha."
check "does not reply on the thread that Claude left" no_call "comments/202/replies"
check "removes the iterate label" has_call "gh pr edit 12 --remove-label conveyor:iterate"
check "removes and adds the review label" has_call "gh pr edit 12 --add-label conveyor:review"
check "the prompt holds the threads with IDs" grep -q '## T2: README.md:?' "$TMP/claude-prompt"
check "the prompt holds the linked issue" grep -q '# Linked issue #7: Add a README' "$TMP/claude-prompt"
check "the prompt holds the new comment and the review" \
    bash -c "grep -q 'Also add a license section' '$TMP/claude-prompt' && grep -q 'Typo in the title.' '$TMP/claude-prompt'"
check "the prompt skips old comments and the notes of the implementer" \
    bash -c "! grep -qE 'Old note|Updated in abc' '$TMP/claude-prompt'"
check "the Result counts the threads" result_is 'r["summary"]' "Addressed 1 of 2 review threads on PR #12 in $sha (2 new comments)"
check "the Result links the PR and the issue" result_is '[l["name"] for l in r["links"]]' "['pull-request', 'issue']"

# --- iterate: nothing to address --------------------------------------------------------
setup_iterate
: > "$TMP/threads.jsonl"
python3 - "$TMP/pr.json" <<'EOF'
import json, sys
p = sys.argv[1]; d = json.load(open(p)); d["comments"] = []; d["reviews"] = []; json.dump(d, open(p, "w"))
EOF
run_impl PR_NUMBER=12
check "nothing to address exits 0" test "$RC" -eq 0
check "nothing to address runs no claude" no_call "claude "
check "nothing to address removes the iterate label" has_call "gh pr edit 12 --remove-label conveyor:iterate"
check "nothing to address says so" result_is 'r["summary"]' \
    "Nothing to address on PR #12: no unresolved review threads and no new comments"

# --- iterate: a PR from a fork ----------------------------------------------------------
setup_iterate
sed -i 's/"isCrossRepository":false/"isCrossRepository":true/' "$TMP/pr.json"
run_impl PR_NUMBER=12
check "a fork PR fails" test "$RC" -ne 0
check "the Result says why" result_is 'r["summary"]' "PR #12 comes from a fork; the implementer cannot push to it"

# --- iterate: no change ------------------------------------------------------------------
setup_iterate
printf 'true\n' > "$TMP/claude-impl"
run_impl PR_NUMBER=12
check "an iteration without a change fails" test "$RC" -ne 0
check "no reply without a change" no_call "/replies"
check "the iterate Result keeps the PR link" result_is 'r["links"][0]["name"]' "pull-request"

harness_done

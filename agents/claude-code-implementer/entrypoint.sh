#!/usr/bin/env bash
# Implementer agent entrypoint (v2).
#
# Contract with conveyor:
#   - /task/prompt                the trigger prompt (plain text). With ISSUE_NUMBER
#                                 or PR_NUMBER it is a fallback only: the
#                                 entrypoint reads the context with gh.
#   - /run/secrets/<name>/        holds the declared Secrets, one dir per Secret
#   - $TARGET_REPO                the git URL to clone. Without it, the agent
#                                 answers the prompt and exits.
#   - $TARGET_BRANCH              the base branch; defaults to main
#   - $ISSUE_NUMBER               implement mode: the issue to implement
#   - $PR_NUMBER                  iterate mode: the PR to update after a review
#   - $BRANCH_NAME                implement mode without an issue: the branch
#   - exit 0 = success, non-zero = failure
#   - /dev/termination-log        the Result (ADR-0008): a summary and the
#                                 pull-request and issue links
#
# Mode: PR_NUMBER set => iterate, else implement.
#
# The entrypoint owns every git and git-host step: the branch, the commit, the
# push, the PR, "Closes #N", the issue comment, the replies and the labels.
# Claude only edits the working tree. The prompt tells Claude to leave the
# changes uncommitted, --disallowedTools blocks git commit, git push and gh,
# and the entrypoint folds back any commit that Claude makes anyway.

set -euo pipefail

AGENT_NAME=implementer
# The shared library: secrets export, GitHub setup, the Result writer and the
# SIGTERM trap. The Dockerfile copies it from agents/common/lib.sh.
# shellcheck source-path=SCRIPTDIR source=../common/lib.sh
. "${CONVEYOR_LIB:-/usr/local/lib/conveyor/lib.sh}"

PROMPT_FILE="${PROMPT_FILE:-/task/prompt}"
WORKDIR="${WORKDIR:-/tmp/work}"
REVIEW_LABEL="conveyor:review"
ITERATE_LABEL="conveyor:iterate"
# A hidden marker in the comments of the implementer. Iterate mode skips the
# comments that carry it, so the agent does not read its own notes as review.
MARKER="<!-- openconveyor:implementer -->"
GIT_NAME="conveyor-implementer"
GIT_EMAIL="bot@openconveyor.ai"

# The tools that Claude must not use: the entrypoint owns these steps.
CLAUDE_DENY=('Bash(git commit:*)' 'Bash(git push:*)' 'Bash(gh:*)')

[[ -r "$PROMPT_FILE" ]] || die "prompt file $PROMPT_FILE not readable"

secrets_export
github_setup
gh_repo_from "${TARGET_REPO:-}"

mkdir -p "$WORKDIR"
cd "$WORKDIR"

# --- helpers ------------------------------------------------------------------

# js evaluates a JavaScript expression over the JSON on stdin (as `d`) and
# prints the result. The expressions come from this script only.
js() {
    node -e '
const d = JSON.parse(require("fs").readFileSync(0, "utf8"));
const r = (0, eval)("(d) => (" + process.argv[1] + ")")(d);
process.stdout.write(r === undefined || r === null ? "" : String(r));
' "$1"
}

# warn_on_fail runs a git-host step that must not fail the Task: the main
# product (the push, the PR) already exists.
warn_on_fail() {
    "$@" >/dev/null 2>&1 || log "warning: '$*' failed"
}

git_identity() {
    git config user.email "$GIT_EMAIL"
    git config user.name "$GIT_NAME"
}

# clone_repo clones TARGET_REPO at the base branch into $WORKDIR/repo and
# changes into it. An empty repository (no refs) gets the base branch with an
# empty initial commit on the first push, so that a PR has a base.
clone_repo() {
    local base="$1" refs
    log "cloning $TARGET_REPO at $base"
    refs=$(git ls-remote "$TARGET_REPO") || die "cannot read $TARGET_REPO: check the token and the egress allowlist"
    if [[ -z "$refs" ]]; then
        log "the repository is empty, creating $base with an initial commit"
        git init -q -b "$base" repo
        cd repo
        git_identity
        git remote add origin "$TARGET_REPO"
        git commit -q --allow-empty -m "chore: initial commit"
        git push -q -u origin "$base" || die "cannot create branch $base in the empty repository $TARGET_REPO"
        return 0
    fi
    grep -q "[[:space:]]refs/heads/$base\$" <<< "$refs" || die "base branch $base does not exist in $TARGET_REPO"
    git clone -q --depth=1 --branch "$base" "$TARGET_REPO" repo
    cd repo
    git_identity
}

# run_claude runs the implementation pass on a prompt file. bypassPermissions
# lets Claude edit and run tests without a TTY; CLAUDE_DENY keeps the git and
# git-host steps with the entrypoint.
run_claude() {
    run_interruptible claude --print --verbose --output-format stream-json \
        --permission-mode bypassPermissions \
        --disallowedTools "${CLAUDE_DENY[@]}" \
        < "$1"
}

# fold_commits undoes commits that Claude made despite the prompt. The changes
# stay in the working tree, and the entrypoint commits them itself.
fold_commits() {
    if [[ "$(git rev-parse HEAD)" != "$1" ]]; then
        log "warning: claude made commits; folding them back into the working tree"
        git reset -q --soft "$1"
    fi
}

has_changes() { [[ -n "$(git status --porcelain)" ]]; }

# describe_change asks Claude for a Conventional Commit subject, a PR title and
# a PR body for the staged change. It reads at most 400 lines of the diff.
# When the answer is missing or breaks the rules, it uses the fallbacks.
# Sets: CHANGE_SUBJECT, CHANGE_TITLE, CHANGE_BODY.
describe_change() {
    local context="$1" fallback_subject="$2" fallback_title="$3"
    local input="$WORKDIR/describe-input.md" output="$WORKDIR/describe-output.txt"
    git add -A
    {
        printf 'Describe the staged change below for a commit and a pull request.\n'
        printf 'Answer with only one JSON object and nothing else:\n'
        printf '{"subject": "<Conventional Commit subject, at most 72 characters, for example: feat: add a README>",\n'
        printf ' "title": "<pull request title, one line>",\n'
        printf ' "body": "<pull request description in Markdown: what changed and why, at most 15 lines>"}\n'
        printf 'Do not write issue-closing keywords such as "Closes #1"; the automation adds them.\n\n'
        printf '# Context\n\n%s\n\n' "$context"
        printf '# git diff --stat\n\n'
        git diff --cached --stat
        printf '\n# git diff (first 400 lines)\n\n'
        git diff --cached | head -n 400
    } > "$input"

    : > "$output"
    run_interruptible claude --print < "$input" > "$output" 2>"$WORKDIR/describe.err" \
        || log "warning: the describe call failed; using the fallback text"

    CHANGE_SUBJECT="$fallback_subject"
    CHANGE_TITLE="$fallback_title"
    CHANGE_BODY="Automated change by the OpenConveyor implementer."
    local parsed
    if parsed=$(node -e '
const fs = require("fs");
const text = fs.readFileSync(process.argv[1], "utf8");
const start = text.indexOf("{"), end = text.lastIndexOf("}");
if (start < 0 || end < start) process.exit(1);
const d = JSON.parse(text.slice(start, end + 1));
const line = (s) => typeof s === "string" && s.trim() !== "" && !/[\r\n]/.test(s.trim());
const cc = /^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([A-Za-z0-9_.\/-]+\))?!?: \S/;
const out = {};
if (line(d.subject) && d.subject.trim().length <= 72 && cc.test(d.subject.trim())) out.subject = d.subject.trim();
if (line(d.title) && d.title.trim().length <= 200) out.title = d.title.trim();
if (typeof d.body === "string" && d.body.trim() !== "") out.body = d.body.trim().slice(0, 6000);
process.stdout.write(JSON.stringify(out));
' "$output" 2>/dev/null); then
        local v
        v=$(js 'd.subject' <<< "$parsed"); [[ -z "$v" ]] || CHANGE_SUBJECT="$v"
        v=$(js 'd.title' <<< "$parsed"); [[ -z "$v" ]] || CHANGE_TITLE="$v"
        v=$(js 'd.body' <<< "$parsed"); [[ -z "$v" ]] || CHANGE_BODY="$v"
    else
        log "warning: the describe answer was not JSON; using the fallback text"
    fi
}

# commit_change commits the staged change and prints the short SHA.
commit_change() {
    local trailer="$1"
    git add -A
    printf '%s\n\n%s\n' "$CHANGE_SUBJECT" "$trailer" > "$WORKDIR/commit-message"
    git commit -q -F "$WORKDIR/commit-message"
    git rev-parse --short HEAD
}

# relabel_review removes and adds the review label. GitHub sends `labeled`
# only on a transition, so this starts the reviewer trigger again.
relabel_review() {
    gh pr edit "$1" --remove-label "$REVIEW_LABEL" >/dev/null 2>&1 || true
    warn_on_fail gh pr edit "$1" --add-label "$REVIEW_LABEL"
}

# implementer_rules is the part of the prompt that keeps the git steps with
# the entrypoint.
implementer_rules() {
    cat <<'EOF'
Rules:
- Leave all changes uncommitted in the working tree. Do not run `git commit`,
  `git push` or any `gh` command, and do not create or switch branches. The
  automation commits, pushes and updates the pull request after you finish.
- Keep the change focused on the task, and follow the conventions of the
  repository. Run the tests of the repository when they exist.
- When the task is unclear or impossible, make no change, and explain why in
  your final message.
EOF
}

# --- prompt fallback ------------------------------------------------------------

prompt_raw=$(cat "$PROMPT_FILE")
prompt_url=$(printf '%s' "$prompt_raw" | tr -d '[:space:]')
issue_url_re='^https://github\.com/[^/]+/[^/]+/issues/([0-9]+)$'
pull_url_re='^https://github\.com/[^/]+/[^/]+/pull/([0-9]+)$'
if [[ -z "${ISSUE_NUMBER:-}" && -z "${PR_NUMBER:-}" ]]; then
    if [[ "$prompt_url" =~ $issue_url_re ]]; then
        ISSUE_NUMBER="${BASH_REMATCH[1]}"
    elif [[ "$prompt_url" =~ $pull_url_re ]]; then
        PR_NUMBER="${BASH_REMATCH[1]}"
    fi
fi
# The prompt adds no information when it is only the URL of the issue or PR.
prompt_is_url=0
if [[ "$prompt_url" =~ $issue_url_re || "$prompt_url" =~ $pull_url_re ]]; then
    prompt_is_url=1
fi
[[ -z "${ISSUE_NUMBER:-}" || "$ISSUE_NUMBER" =~ ^[0-9]+$ ]] || die "ISSUE_NUMBER must be a number"
[[ -z "${PR_NUMBER:-}" || "$PR_NUMBER" =~ ^[0-9]+$ ]] || die "PR_NUMBER must be a number"

# --- no repository: answer the prompt -------------------------------------------

if [[ -z "${TARGET_REPO:-}" ]]; then
    [[ -z "${PR_NUMBER:-}${ISSUE_NUMBER:-}" ]] || die "ISSUE_NUMBER and PR_NUMBER need TARGET_REPO"
    log "no TARGET_REPO, answering the prompt only"
    rc=0
    run_interruptible claude --print --verbose --output-format stream-json \
        --permission-mode bypassPermissions --disallowedTools "${CLAUDE_DENY[@]}" \
        < "$PROMPT_FILE" || rc=$?
    ((rc == 0)) || die "claude exited with code $rc"
    result_write "Answered the prompt without a repository"
    exit 0
fi

case "${GIT_HOST:-github}" in
    github) ;;
    *) die "git host '${GIT_HOST}' is not supported; only github is" ;;
esac
[[ -n "${GITHUB_TOKEN:-}" ]] || die "GITHUB_TOKEN not set; declare the github-token Secret on the Task"

# --- implement mode -----------------------------------------------------------

implement() {
    local issue="${ISSUE_NUMBER:-}" base="${TARGET_BRANCH:-main}"
    local context="$WORKDIR/context.md" issue_url="" issue_title="" branch
    local pr_number="" pr_url="" reuse=0 base_sha rc sha

    if [[ -n "$issue" ]]; then
        gh issue view "$issue" --json number,title,body,labels,comments,url > "$WORKDIR/issue.json" \
            || die "cannot read issue #$issue"
        issue_url=$(js 'd.url' < "$WORKDIR/issue.json")
        issue_title=$(js 'd.title' < "$WORKDIR/issue.json")
        result_partial "Started work on issue #$issue, no pull request yet" "issue=$issue_url"
        MARKER="$MARKER" js '
"# Issue #" + d.number + ": " + d.title + "\n\n" +
"Labels: " + ((d.labels || []).map((l) => l.name).join(", ") || "none") + "\n\n" +
"## Description\n\n" + (d.body || "(no description)") + "\n\n" +
"## Comments\n\n" + (((d.comments || []).filter((c) => !(c.body || "").includes(process.env.MARKER))
    .map((c) => "- " + ((c.author && c.author.login) || "ghost") + ": " + c.body).join("\n")) || "(none)")
' < "$WORKDIR/issue.json" > "$context"
        if ((prompt_is_url == 0)); then
            printf '\n\n## Trigger prompt\n\n%s\n' "$prompt_raw" >> "$context"
        fi
        branch="conveyor/issue-$issue"
    else
        printf '%s\n' "$prompt_raw" > "$context"
        branch="${BRANCH_NAME:-conveyor/$(date -u +%Y%m%dT%H%M%SZ)}"
    fi

    clone_repo "$base"

    if [[ -n "$issue" ]]; then
        local existing
        existing=$(gh pr list --head "$branch" --state open --json number,url \
            --jq '.[0] | select(. != null) | "\(.number) \(.url)"') || die "cannot list the pull requests of $branch"
        if [[ -n "$existing" ]]; then
            reuse=1
            pr_number="${existing%% *}"
            pr_url="${existing#* }"
            log "reusing PR #$pr_number on $branch"
            git fetch -q --depth=1 origin "$branch" || die "cannot fetch branch $branch"
            git checkout -q -B "$branch" FETCH_HEAD
        fi
    fi
    ((reuse == 1)) || git checkout -q -b "$branch"
    base_sha=$(git rev-parse HEAD)

    {
        printf 'You are the OpenConveyor implementer, in a git checkout of %s on branch %s.\n' "${GH_REPO:-$TARGET_REPO}" "$branch"
        printf 'Implement the task below by editing the files in this working tree.\n\n'
        implementer_rules
        printf '\n# Task\n\n'
        cat "$context"
    } > "$WORKDIR/prompt.md"

    log "running claude"
    rc=0
    run_claude "$WORKDIR/prompt.md" || rc=$?
    ((rc == 0)) || die "claude exited with code $rc; the entrypoint committed nothing"
    fold_commits "$base_sha"
    has_changes || die "claude made no change in the repository; nothing to commit"

    local fallback_subject="chore: apply the requested change" fallback_title="Apply the requested change"
    if [[ -n "$issue" ]]; then
        fallback_subject="feat: implement #$issue"
        fallback_title="$issue_title"
    fi
    describe_change "$(cat "$context")" "$fallback_subject" "$fallback_title"

    local trailer="Opened by the OpenConveyor implementer."
    [[ -z "$issue" ]] || trailer="Refs #$issue"
    sha=$(commit_change "$trailer")
    git push -q -u origin "$branch" || die "git push of $branch failed"
    log "pushed $sha to $branch"

    local links=()
    [[ -z "$issue_url" ]] || links=("issue=$issue_url")
    if ((reuse == 1)); then
        result_partial "Pushed $sha to $branch for PR #$pr_number" "pull-request=$pr_url" "${links[@]}"
        warn_on_fail gh pr comment "$pr_number" --body "Updated in $sha for #$issue. $MARKER"
    else
        result_partial "Pushed $sha to $branch, no pull request yet" "${links[@]}"
        {
            printf '%s\n\n' "$CHANGE_BODY"
            [[ -z "$issue" ]] || printf 'Closes #%s\n\n' "$issue"
            printf -- '---\n'
            if [[ -n "$issue" ]]; then
                printf 'Opened by the OpenConveyor implementer for #%s.\n' "$issue"
            else
                printf 'Opened by the OpenConveyor implementer.\n'
            fi
        } > "$WORKDIR/pr-body.md"
        pr_url=$(gh pr create --base "$base" --head "$branch" --title "$CHANGE_TITLE" \
            --body-file "$WORKDIR/pr-body.md" | tail -n1) || die "gh pr create failed for $branch"
        pr_number="${pr_url##*/}"
        log "opened $pr_url"
    fi
    relabel_review "$pr_number"

    if [[ -n "$issue" ]]; then
        local verb="Opened"
        ((reuse == 0)) || verb="Updated"
        warn_on_fail gh issue comment "$issue" --body "$verb $pr_url ($sha). $MARKER"
        result_write "$verb PR #$pr_number for issue #$issue: $CHANGE_TITLE" "pull-request=$pr_url" "issue=$issue_url"
    else
        result_write "Opened PR #$pr_number: $CHANGE_TITLE" "pull-request=$pr_url"
    fi
}

# --- iterate mode ---------------------------------------------------------------

# The unresolved review threads of a PR, one JSON object per line. GraphQL is
# the only API with isResolved. --paginate needs $endCursor and pageInfo.
# shellcheck disable=SC2016 # GraphQL variables, not shell expansions
THREADS_QUERY='
query($owner: String!, $name: String!, $number: Int!, $endCursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $endCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id isResolved path line
          comments(first: 50) { nodes { databaseId author { login } body } }
        }
      }
    }
  }
}'

iterate() {
    local pr="$PR_NUMBER" pr_url head_ref base_ref state cross head_date issue issue_url=""
    local context="$WORKDIR/context.md" base_sha rc sha

    gh pr view "$pr" --json number,url,title,body,state,headRefName,baseRefName,isCrossRepository,closingIssuesReferences,comments,reviews,commits \
        > "$WORKDIR/pr.json" || die "cannot read PR #$pr"
    pr_url=$(js 'd.url' < "$WORKDIR/pr.json")
    result_partial "Started an iteration on PR #$pr, nothing pushed" "pull-request=$pr_url"
    state=$(js 'd.state' < "$WORKDIR/pr.json")
    cross=$(js 'd.isCrossRepository' < "$WORKDIR/pr.json")
    head_ref=$(js 'd.headRefName' < "$WORKDIR/pr.json")
    base_ref=$(js 'd.baseRefName' < "$WORKDIR/pr.json")
    head_date=$(js '(d.commits || []).length ? d.commits[d.commits.length - 1].committedDate : ""' < "$WORKDIR/pr.json")
    issue=$(js '((d.closingIssuesReferences || [])[0] || {}).number' < "$WORKDIR/pr.json")
    [[ "$state" == "OPEN" ]] || die "PR #$pr is $state, not OPEN"
    [[ "$cross" != "true" ]] || die "PR #$pr comes from a fork; the implementer cannot push to it"

    gh api graphql --paginate -F owner='{owner}' -F name='{repo}' -F number="$pr" -f query="$THREADS_QUERY" \
        --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false) | @json' \
        > "$WORKDIR/threads.jsonl" || die "cannot read the review threads of PR #$pr"

    if [[ -n "$issue" ]]; then
        gh issue view "$issue" --json number,title,body,url > "$WORKDIR/issue.json" || die "cannot read issue #$issue"
        issue_url=$(js 'd.url' < "$WORKDIR/issue.json")
    else
        printf '{}' > "$WORKDIR/issue.json"
    fi

    # Render the context and the thread map (T<n> <comment id>) with node.
    local counts
    counts=$(HEAD_DATE="$head_date" MARKER="$MARKER" node -e '
const fs = require("fs");
const [prFile, issueFile, threadsFile, contextFile, mapFile] = process.argv.slice(1);
const pr = JSON.parse(fs.readFileSync(prFile, "utf8"));
const issue = JSON.parse(fs.readFileSync(issueFile, "utf8"));
const threads = fs.readFileSync(threadsFile, "utf8").split("\n").filter((l) => l.trim()).map((l) => JSON.parse(l));
const since = process.env.HEAD_DATE ? Date.parse(process.env.HEAD_DATE) : 0;
const who = (a) => (a && a.login) || "ghost";
const mine = (b) => (b || "").includes(process.env.MARKER);
const notes = [
  ...(pr.comments || []).filter((c) => !mine(c.body) && Date.parse(c.createdAt) > since)
    .map((c) => "- comment by " + who(c.author) + ": " + c.body),
  ...(pr.reviews || []).filter((r) => (r.body || "").trim() && !mine(r.body) && Date.parse(r.submittedAt) > since)
    .map((r) => "- review (" + r.state + ") by " + who(r.author) + ": " + r.body),
];
let out = "# Pull request #" + pr.number + ": " + pr.title + "\n\n" + (pr.body || "(no description)") + "\n\n";
if (issue.number) out += "# Linked issue #" + issue.number + ": " + issue.title + "\n\n" + (issue.body || "(no description)") + "\n\n";
out += "# Unresolved review threads\n\n";
const map = [];
threads.forEach((t, i) => {
  const id = "T" + (i + 1);
  const first = (t.comments.nodes[0] || {});
  map.push(id + " " + first.databaseId);
  out += "## " + id + ": " + t.path + ":" + (t.line == null ? "?" : t.line) + "\n\n" +
    t.comments.nodes.map((c) => "- " + who(c.author) + ": " + c.body).join("\n") + "\n\n";
});
if (!threads.length) out += "(none)\n\n";
out += "# New comments since the last push\n\n" + (notes.join("\n") || "(none)") + "\n";
fs.writeFileSync(contextFile, out);
fs.writeFileSync(mapFile, map.join("\n") + (map.length ? "\n" : ""));
process.stdout.write(threads.length + " " + notes.length);
' "$WORKDIR/pr.json" "$WORKDIR/issue.json" "$WORKDIR/threads.jsonl" "$context" "$WORKDIR/thread-map")
    local total="${counts%% *}" notes="${counts#* }"
    local links=("pull-request=$pr_url")
    [[ -z "$issue_url" ]] || links+=("issue=$issue_url")

    if ((total == 0 && notes == 0)); then
        log "nothing to address on PR #$pr"
        warn_on_fail gh pr edit "$pr" --remove-label "$ITERATE_LABEL"
        result_write "Nothing to address on PR #$pr: no unresolved review threads and no new comments" "${links[@]}"
        return 0
    fi

    clone_repo "${TARGET_BRANCH:-$base_ref}"
    # The shallow clone fetches the base branch only. gh pr checkout sets up
    # tracking for origin/<head>, and git refuses that unless the fetch
    # refspec covers the branch.
    git config --add remote.origin.fetch "+refs/heads/$head_ref:refs/remotes/origin/$head_ref"
    gh pr checkout "$pr" >&2 || die "gh pr checkout $pr failed"
    base_sha=$(git rev-parse HEAD)

    local addressed_file="$WORKDIR/addressed-threads"
    rm -f "$addressed_file"
    {
        printf 'You are the OpenConveyor implementer, in a git checkout of %s on branch %s,\n' "${GH_REPO:-$TARGET_REPO}" "$head_ref"
        printf 'the head of pull request #%s. Update the change to address the review below.\n\n' "$pr"
        implementer_rules
        printf -- '- When you finish, write the IDs (T1, T2, ...) of the review threads that\n'
        printf '  you addressed to the file %s, one ID per line. Leave out a thread\n' "$addressed_file"
        printf '  that you did not address, and explain why in your final message.\n\n'
        cat "$context"
    } > "$WORKDIR/prompt.md"

    log "running claude on $total thread(s) and $notes comment(s)"
    rc=0
    run_claude "$WORKDIR/prompt.md" || rc=$?
    ((rc == 0)) || die "claude exited with code $rc; the entrypoint pushed nothing"
    fold_commits "$base_sha"
    has_changes || die "claude made no change for PR #$pr; nothing to push"

    describe_change "$(cat "$context")" "fix: address the review of #$pr" "unused"
    sha=$(commit_change "Refs #$pr")
    git push -q origin "HEAD:$head_ref" || die "git push to $head_ref failed"
    log "pushed $sha to $head_ref"
    result_partial "Pushed $sha to $head_ref for PR #$pr; the replies are not complete" "${links[@]}"

    local replied=0 id comment_id
    if [[ -f "$addressed_file" ]]; then
        while read -r id; do
            id="${id//[[:space:]]/}"
            [[ -n "$id" ]] || continue
            comment_id=$(awk -v id="$id" '$1 == id { print $2 }' "$WORKDIR/thread-map")
            [[ "$comment_id" =~ ^[0-9]+$ ]] || continue
            if gh api -X POST "repos/{owner}/{repo}/pulls/$pr/comments/$comment_id/replies" \
                -f body="Addressed in $sha. $MARKER" >/dev/null 2>&1; then
                replied=$((replied + 1))
            else
                log "warning: could not reply to thread $id"
            fi
        done < <(sort -u "$addressed_file")
    fi

    warn_on_fail gh pr edit "$pr" --remove-label "$ITERATE_LABEL"
    relabel_review "$pr"

    local summary="Addressed $replied of $total review threads on PR #$pr in $sha"
    ((notes == 0)) || summary+=" ($notes new comments)"
    result_write "$summary" "${links[@]}"
}

if [[ -n "${PR_NUMBER:-}" ]]; then
    iterate
else
    implement
fi

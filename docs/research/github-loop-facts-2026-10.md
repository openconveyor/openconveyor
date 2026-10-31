# GitHub facts for the issue-to-PR loop: payloads, `gh`, PAT permissions

Resolves wayfinder ticket #101 (map #98). Researched 2026-10-31 from
docs.github.com (webhook, REST and GraphQL references), the `gh` manual
at cli.github.com, and this repo's code (`internal/trigger/`,
`examples/github-*/trigger.yaml`, `agents/claude-code-implementer/entrypoint.sh`).
The GraphQL query, the `gh pr view --json` fields, the `gh pr list --head`
filter and the `type` values of the users API were run live against the
public `openconveyor/openconveyor` repo with `gh 2.102.1`. Nothing was
written to `scholtenmartijn/conveyor-test`; items that only a write to that
repo can confirm are marked **unverified**.

## Summary of answers

- **Payloads.** Every field that the two example triggers map exists at the
  path they use. Both `issue.body` and `pull_request.body` are `string or
  null`, so the `default:` on the prompt mapping is required. `label` is
  not marked required on either event; the `label.name` filter then fails
  closed, which is the safe direction. The `pull_request` payload carries
  `pull_request.head.ref` (the PR branch), `pull_request.head.sha`,
  `pull_request.base.ref` and a top-level `number`. `pull_request.head.repo`
  is nullable (deleted fork), so compare `head.repo.full_name` with
  `base.repo.full_name` only after a null check. `issues` payloads have no
  top-level `number`.
- **PR branch for a PR number.** `gh pr view N --json headRefName --jq
  .headRefName`, or `gh pr checkout N` to check it out directly. The
  reverse lookup is `gh pr list --head <branch> --state all --json number`.
- **Unresolved review threads.** GraphQL only. The REST review-comments
  endpoint has no `resolved` field; it exposes threading only through
  `in_reply_to_id`. `PullRequest.reviewThreads` has `isResolved`,
  `isOutdated`, `resolvedBy` and the thread `id` that the
  `resolveReviewThread(threadId)` mutation takes. `reviewThreads` has no
  server-side filter, so filter `isResolved == false` with `--jq`.
- **Labels and comments.** `gh issue edit N --add-label`, `gh pr edit N
  --add-label`, `gh issue comment N --body-file`, `gh pr comment N
  --body-file`. PRs are issues in the REST API, so one endpoint pair
  (`/issues/{n}/labels`, `/issues/{n}/comments`) serves both. The label
  must exist first (`gh label create`); auto-creation is **unverified**.
- **`Closes #N`.** The keyword in the PR body links the issue, and the merge
  closes it, only when the PR targets the default branch. The docs state no
  permission requirement for the keyword; a PAT user with write access
  works. Verify with `gh pr view N --json closingIssuesReferences`.
- **Minimum fine-grained PAT.** Metadata: read, Contents: read and write,
  Issues: read and write, Pull requests: read and write. Add Workflows:
  write only when a push touches `.github/workflows/`. The token owner
  must have write access to the repo; the token cannot exceed the owner.
- **Rate limits.** 5,000 REST requests per hour and 5,000 GraphQL points per
  hour per user. The secondary limit on content creation is 80 per minute
  and 500 per hour; it counts issues, comments, labels and PRs. Wait one
  second between mutations and honor `retry-after`.
- **Loop hazard.** Every write the loop makes with the PAT arrives as a new
  webhook event with `sender.login` = the token owner and `sender.type` =
  `User`. The triggers must not re-dispatch on their own writes; the label
  gate (`conveyor:ready`, `conveyor:review`) is the current guard.

## Webhook payload fields

Source for the per-action rows: [webhook events and payloads][EV], sections
`issues` and `pull_request`, action `labeled`. The page does not expand
`repository.*` and `sender.*`; those rows come from the REST
[repository][REPO] and [user][USER] schemas. "Required" is GitHub's own
marking in the payload table. The adapter compares the gjson `String()` form
with `equals`, so an integer `42` matches the string `"42"`
(`internal/trigger/mapping.go:35`).

### `issues`, action `labeled`

| Need | gjson path | Type | Present | Note |
|---|---|---|---|---|
| Event filter | `action` | string | required | enum `labeled` |
| Label filter | `label.name` | string | `label` object not marked required; `name` required inside it | Exact match with `conveyor:ready` |
| Issue number | `issue.number` | integer | required | Mapped to `labels.openconveyor.ai/issue-number` today |
| Title | `issue.title` | string | required | |
| Body (prompt) | `issue.body` | string or null | key required, value nullable | Keep `default:` |
| Issue URL | `issue.html_url` | string | required | |
| State | `issue.state` | string `open`/`closed` | optional | |
| Labels | `issue.labels` | array of `{name,…}` | optional | gjson `issue.labels.#.name` lists names |
| Author | `issue.user.login` | string | `issue.user` nullable; `login` required inside | |
| Is this issue a PR | `issue.pull_request` | object | optional, present only for PRs | `gjson` returns empty for a plain issue |
| Clone URL | `repository.clone_url` | string | required (REST) | `https://github.com/<owner>/<repo>.git` |
| SSH URL | `repository.ssh_url` | string | required (REST) | |
| Repo full name | `repository.full_name` | string | required (REST) | |
| Repo name / owner | `repository.name`, `repository.owner.login` | string | required (REST) | |
| Default branch | `repository.default_branch` | string | required (REST) | |
| Private | `repository.private` | boolean | required (REST) | |
| Sender | `sender.login` | string | `sender` required; `login` required (REST) | Token owner for PAT writes |
| Sender kind | `sender.type` | string | required (REST) | Live: `User` for a person, `Bot` for `dependabot[bot]` |
| App install | `installation.id` | integer | only for GitHub App deliveries | Absent for a repo webhook |

### `pull_request`, action `labeled`

| Need | gjson path | Type | Present | Note |
|---|---|---|---|---|
| Event filter | `action` | string | required | enum `labeled` |
| Label filter | `label.name` | string | as above | Exact match with `conveyor:review` |
| PR number | `number` | integer | required (top-level) | Same value as `pull_request.number` |
| PR number | `pull_request.number` | integer | required | Mapped to `labels.openconveyor.ai/pr-number` today |
| Title / body | `pull_request.title`, `pull_request.body` | string / string or null | required / nullable | |
| PR URL | `pull_request.html_url` | string | required | Mapped to `prompt` today |
| PR branch | `pull_request.head.ref` | string | required | Branch name only, no `refs/heads/` |
| PR head SHA | `pull_request.head.sha` | string | required | |
| Head repo | `pull_request.head.repo.full_name` | string | `head.repo` **nullable** (deleted fork) | Fork when it differs from `base.repo.full_name` |
| Base branch | `pull_request.base.ref` | string | required | |
| Base repo | `pull_request.base.repo.full_name` | string | required | |
| Draft | `pull_request.draft` | boolean | required | |
| State / merged | `pull_request.state`, `pull_request.merged` | string / boolean or null | required / optional | |
| Author | `pull_request.user.login` | string | `user` nullable | |
| Labels | `pull_request.labels` | array | required | `pull_request.labels.#.name` |
| `repository.*`, `sender.*`, `installation` | as in the `issues` table | | | |

Payload facts that matter to the adapter ([EV], [best practices][BP],
[validating deliveries][VAL]):

- Headers: `X-GitHub-Event`, `X-GitHub-Delivery` (GUID, identical on a
  redelivery), `X-GitHub-Hook-ID`, `X-Hub-Signature-256` with the form
  `sha256=<hex HMAC-SHA256 of the raw body>`, `User-Agent:
  GitHub-Hookshot/…`. `X-Hub-Signature` is the legacy SHA-1 header.
  `internal/trigger/signature.go` already uses the SHA-256 header.
- GitHub needs a 2xx within 10 seconds and caps a payload at 25 MB.
- Whether a label added to a PR also sends `issues.labeled` is
  **unverified** in the docs. The REST docs say "every pull request is an
  issue". A defensive `issues` trigger can add a filter that `issue.pull_request`
  is empty; the adapter does not have a "not present" filter today, so this
  is a feature request, not a configuration.

## `gh` commands for each operation

`gh` reads `GH_TOKEN` first and `GITHUB_TOKEN` second, and either one
removes the login prompt ([environment][GHENV]). The `gh auth login` manual
says "Favour setting `GH_TOKEN` for fine-grained personal access token
usage". Set `GH_PROMPT_DISABLED=1` and `NO_COLOR=1` in the image. Set
`GH_REPO=owner/repo` so that `{owner}` and `{repo}` expand in `gh api` and
so that commands work outside a clone. The entrypoint's
`https://x-access-token:TOKEN@github.com` credential form is documented for
GitHub App tokens ([app installation][APPINST]); for a PAT the docs say the
username "is not used to authenticate you" but must be present ([PAT][PAT]),
so the same line works.

| Operation | Command | Source |
|---|---|---|
| Clone with the token | `git clone --depth=1 --branch "$TARGET_BRANCH" "$TARGET_REPO" repo` with the credential helper line that the entrypoint writes | [PAT], [APPINST] |
| Find the PR branch for a PR number | `gh pr view N --json headRefName,headRefOid,baseRefName,isCrossRepository,headRepositoryOwner --jq .headRefName` (live-checked) | [gh pr view][GHPRVIEW] |
| Check out the PR branch | `gh pr checkout N` (works for forks too; `-b` sets the local name, `-f` resets an existing local branch) | [gh pr checkout][GHPRCO] |
| Find the PR for a branch | `gh pr list --head "conveyor/issue-N" --state all --json number,url` (live-checked; `owner:branch` form is not supported). REST: `gh api repos/{owner}/{repo}/pulls -f head='OWNER:branch' -f state=all` | [gh pr list][GHPRLIST], [REST pulls][RESTPULLS] |
| Push the branch | `git push -u origin "conveyor/issue-N"` before `gh pr create`; a branch that is not pushed starts a prompt | [gh pr create][GHPRCREATE] |
| Open the PR | `gh pr create --base "$TARGET_BRANCH" --head "conveyor/issue-N" --title "…" --body-file body.md`, with `Closes #N` in `body.md`. `--fill` uses the commit messages. `--label conveyor:review` adds the label in the same call | [GHPRCREATE] |
| Self-label the PR | `gh pr edit N --add-label conveyor:review`. REST: `gh api repos/{owner}/{repo}/issues/N/labels -f 'labels[]=conveyor:review'` | [gh pr edit][GHPREDIT], [REST labels][RESTLABELS] |
| Create the label once | `gh label create conveyor:review --color 0e8a16 --description "…"` (REST `POST /repos/{owner}/{repo}/labels`); the test repo has only the nine default labels today | [RESTLABELS] |
| Label an issue | `gh issue edit N --add-label conveyor:in-progress` | [gh issue edit][GHISSUEEDIT] |
| Comment on an issue | `gh issue comment N --body-file -` (stdin) | [gh issue comment][GHISSUECOMMENT] |
| Comment on a PR | `gh pr comment N --body-file -`; `--edit-last --create-if-none` updates one status comment instead of adding more | [gh pr comment][GHPRCOMMENT] |
| List reviews | `gh api --paginate repos/{owner}/{repo}/pulls/N/reviews --jq '.[] \| {id,state,user:.user.login}'`; `state` is `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED` or `PENDING` | [REST reviews][RESTREVIEWS], [GraphQL pulls][GQLPULLS] |
| List review comments (flat) | `gh api --paginate repos/{owner}/{repo}/pulls/N/comments`; fields `id`, `body`, `path`, `line`, `side`, `in_reply_to_id`, `pull_request_review_id`. No `resolved` field | [REST review comments][RESTPRCOMMENTS] |
| List unresolved review threads | GraphQL query below | [GQLPULLS], [gh api][GHAPI] |
| Reply in a thread | REST: `gh api repos/{owner}/{repo}/pulls/N/comments/<top_level_comment_id>/replies -f body='…'` (replies to replies are not supported). GraphQL: `addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId, body})` | [RESTPRCOMMENTS], [GQLPULLS] |
| Resolve a thread | `gh api graphql -f threadId=PRRT_… -f query='mutation($threadId:ID!){ resolveReviewThread(input:{threadId:$threadId}){ thread{ id isResolved } } }'` (input fields live-checked: `threadId`, `clientMutationId`, `resolutionReason`) | [GQLPULLS] |
| Submit a review | `gh pr review N --comment\|--approve\|--request-changes --body-file -` (reviewer image uses this today) | [gh pr review][GHPRREVIEW] |
| Create an issue | `gh issue create --title "…" --body-file - --label needs-triage` | [gh issue create][GHISSUECREATE] |
| Close an issue | `gh issue close N --comment "…"` | [gh issue close][GHISSUECLOSE] |
| Check the link | `gh pr view N --json closingIssuesReferences` (live-checked) | [GHPRVIEW] |

### Unresolved review threads (GraphQL)

The query ran without schema errors against PR #95 of the public repo.
`diffSide`, `line`, `startLine` and `path` are thread fields, not comment
fields. `--paginate` on GraphQL needs the `$endCursor` variable and the
`pageInfo` block ([GHAPI]). The inner `comments(first: 100)` connection is
not paginated by `--paginate`; a thread with more than 100 comments is
truncated.

```sh
gh api graphql --paginate -F owner='{owner}' -F name='{repo}' -F number=N -f query='
query($owner: String!, $name: String!, $number: Int!, $endCursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      headRefName headRefOid baseRefName reviewDecision
      reviewThreads(first: 100, after: $endCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id isResolved isOutdated path line startLine diffSide
          resolvedBy { login }
          comments(first: 100) {
            nodes { databaseId url author { login } body createdAt replyTo { databaseId } }
          }
        }
      }
    }
  }
}' --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false)'
```

`databaseId` is the REST comment `id`, so the iterate mode can reply with
the REST `replies` endpoint and then resolve the thread with the GraphQL
`id` (`PRRT_…`). The GraphQL docs deprecate `databaseId` in favor of
`fullDatabaseId`; both work today.

### `Closes #N` rules

From [linking a pull request to an issue][LINK]:

- Keywords: `close`, `closes`, `closed`, `fix`, `fixes`, `fixed`,
  `resolve`, `resolves`, `resolved`. Same repo: `Closes #N`. Other repo:
  `Closes OWNER/REPO#N`. One keyword per issue.
- The keywords work only when the PR targets the default branch. For any
  other base branch GitHub creates no link and the merge does not close the
  issue. The example triggers map `repository.default_branch` to
  `TARGET_BRANCH`, so the implementer satisfies this rule.
- The merge into the default branch closes the linked issue. A keyword in a
  commit message also closes the issue, but the PR is then not listed as a
  linked PR.
- The page states no permission requirement for keyword links in the PR
  body. Manual links in the sidebar need write permission. The PAT owner
  has write access, so both paths are open.

## Minimum fine-grained PAT permissions

Source: [permissions required for fine-grained PATs][PERM] unless noted.
The token acts as its owner and "cannot grant additional access
capabilities to a user" ([PAT]). A fine-grained PAT can call GraphQL
([forming calls][GQLCALLS]); the GraphQL reference states no permission per
field, so the GraphQL rows carry the permission of the REST analog.

| Operation | Permission | Level | Source |
|---|---|---|---|
| Read the repo (`GET /repos/{owner}/{repo}`) | Metadata | read | [PERM] |
| `git clone` over HTTPS | Contents | read | [choosing app permissions][APPPERMS] (Git over HTTP needs Contents) |
| `git push` a new branch | Contents | write | [APPPERMS]; `POST /repos/{owner}/{repo}/git/refs` is Contents write in [PERM] |
| Push that changes `.github/workflows/*` | Workflows | write (in addition) | [PERM], [APPPERMS] |
| Open a PR (`POST …/pulls`) | Pull requests | write | [PERM] |
| Read a PR, find the head branch, list reviews and review comments | Pull requests | read | [PERM] |
| List `reviewThreads` (GraphQL) | Pull requests | read | inferred from the REST analog; **unverified** |
| Reply to a review comment, resolve a thread | Pull requests | write | [PERM] for REST `replies`; GraphQL inferred |
| Add a label to a PR (`POST …/issues/{n}/labels`) | Issues **or** Pull requests | write | [PERM] lists the endpoint under both |
| Comment on a PR (`POST …/issues/{n}/comments`) | Issues **or** Pull requests | write | [PERM] lists the endpoint under both |
| Add a label to an issue, comment on an issue | Issues | write | [PERM] |
| Create or close an issue | Issues | write | [PERM] |
| List repo labels, create a label | Issues | read / write | [PERM] |
| Create a repo webhook (`POST …/hooks`) | Webhooks | write | [PERM]; not needed by the loop |

Minimum union for the full loop (clone, push, open PR, label PR, comment on
issue, read review threads, create issues):

| Permission | Level |
|---|---|
| Metadata | read |
| Contents | read and write |
| Issues | read and write |
| Pull requests | read and write |

Other PAT facts ([PAT]): a token is limited to one owner and can be limited
to "Only select repositories", which fits `scholtenmartijn/conveyor-test`.
The default lifetime is 30 days; the maximum is 366 days or `none`, and an
organization policy can block `none`. The token "will become inactive if
the user loses access to the resource". Fine-grained PATs work only for
HTTPS Git operations. The token prefix is `github_pat_`.

## Rate limits that matter

Sources: [REST rate limits][RLREST], [GraphQL limits][RLGQL],
[REST best practices][BEST], [repository limits][REPOLIMITS].

| Limit | Value | Note |
|---|---|---|
| Primary REST | 5,000 requests per hour per user | Shared by every PAT of that user. `GET /rate_limit` is free |
| Primary GraphQL | 5,000 points per hour per user | Point cost: sum of connection sizes / 100, minimum 1. The thread query above costs 1 point when `first: 100` |
| GraphQL node limit | `first`/`last` required on every connection, max 100; 500,000 nodes per call | |
| Secondary: concurrency | 100 concurrent requests across REST and GraphQL | |
| Secondary: per endpoint | 900 points per minute per REST endpoint; 2,000 per minute for GraphQL | Read 1 point, write 5, GraphQL mutation 5 |
| Secondary: content creation | **80 per minute and 500 per hour** | Counts issues, comments, labels, PRs and web UI actions. The create-PR endpoint warns about it |
| Git | No published hard limit. Recommended maximum 15 fetches per second and 6 pushes per minute per repo; push size 2 GB | [REPOLIMITS] |
| Headers | `x-ratelimit-remaining`, `x-ratelimit-reset` (epoch seconds), `retry-after` on a secondary limit | 403 or 429 |
| Backoff | Honor `retry-after`; without it wait until `x-ratelimit-reset`, or at least one minute; wait one second between mutations | [BEST]; repeated requests while limited can get the token banned |

For the loop: one Task makes about ten writes (branch, PR, label, two
comments, replies). The 500-per-hour content limit allows about 50 Tasks
per hour per token owner, together with the human's own writes. A reporter
agent that opens many issues from one run shares the same budget.

## Loop hazard and recommendations

- A PAT write produces a webhook delivery with `sender.login` = the token
  owner and `sender.type` = `User` (live check of the users API: a person
  is `User`, `dependabot[bot]` is `Bot`). GitHub attributes activity to an
  app only for GitHub App tokens ([about app authentication][APPAUTH]).
- The current triggers dispatch only on `labeled` with a specific label, so
  the implementer's own PR open and comment do not start a Task. The
  planned self-label `conveyor:review` on the PR starts the reviewer Task
  on purpose. The reviewer must not add `conveyor:ready` or
  `conveyor:review` itself, or it starts an infinite loop.
- GitHub permits one machine user per human for automation
  ([deploy keys, machine users][MACHINE]; [terms of service][TOS]). A
  machine user gives the loop a distinct `sender.login` to filter on.
  Without one, the adapter cannot tell the human's label from the loop's.
- For the iterate mode, the `pull_request.labeled` payload gives
  `pull_request.head.ref` and `pull_request.number`; map them to
  `config.PR_BRANCH` and `config.PR_NUMBER` so that the implementer can
  `git fetch origin "$PR_BRANCH"` and run the thread query without a
  lookup. A trigger on `pull_request_review.submitted` with
  `review.state == changes_requested` is the alternative, and it needs the
  same two fields from `pull_request.*`.
- `gh pr create --fill` with `GH_PROMPT_DISABLED` fails when the branch is
  not pushed. The entrypoint pushes first and passes `--head`, which skips
  the push prompt. Replace the date-based branch name with
  `conveyor/issue-$ISSUE_NUMBER` from the mapped label, and write `Closes
  #$ISSUE_NUMBER` into `--body-file`.

[EV]: https://docs.github.com/en/webhooks/webhook-events-and-payloads
[BP]: https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks
[VAL]: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
[REPO]: https://docs.github.com/en/rest/repos/repos#get-a-repository
[USER]: https://docs.github.com/en/rest/users/users#get-a-user
[GHENV]: https://cli.github.com/manual/gh_help_environment
[GHAPI]: https://cli.github.com/manual/gh_api
[GHPRVIEW]: https://cli.github.com/manual/gh_pr_view
[GHPRCO]: https://cli.github.com/manual/gh_pr_checkout
[GHPRLIST]: https://cli.github.com/manual/gh_pr_list
[GHPRCREATE]: https://cli.github.com/manual/gh_pr_create
[GHPREDIT]: https://cli.github.com/manual/gh_pr_edit
[GHPRCOMMENT]: https://cli.github.com/manual/gh_pr_comment
[GHPRREVIEW]: https://cli.github.com/manual/gh_pr_review
[GHISSUEEDIT]: https://cli.github.com/manual/gh_issue_edit
[GHISSUECOMMENT]: https://cli.github.com/manual/gh_issue_comment
[GHISSUECREATE]: https://cli.github.com/manual/gh_issue_create
[GHISSUECLOSE]: https://cli.github.com/manual/gh_issue_close
[RESTPULLS]: https://docs.github.com/en/rest/pulls/pulls
[RESTREVIEWS]: https://docs.github.com/en/rest/pulls/reviews
[RESTPRCOMMENTS]: https://docs.github.com/en/rest/pulls/comments
[RESTLABELS]: https://docs.github.com/en/rest/issues/labels
[GQLPULLS]: https://docs.github.com/en/graphql/reference/pulls
[GQLCALLS]: https://docs.github.com/en/graphql/guides/forming-calls-with-graphql
[LINK]: https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/linking-a-pull-request-to-an-issue
[PERM]: https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens
[PAT]: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
[APPPERMS]: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app
[APPINST]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation
[APPAUTH]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/about-authentication-with-a-github-app
[RLREST]: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
[RLGQL]: https://docs.github.com/en/graphql/overview/rate-limits-and-node-limits-for-the-graphql-api
[BEST]: https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
[REPOLIMITS]: https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits
[MACHINE]: https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys#machine-users
[TOS]: https://docs.github.com/en/site-policy/github-terms/github-terms-of-service

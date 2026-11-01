#!/usr/bin/env bash
# Gives an empty target repository its first commit through the contents
# API, so that the default branch exists. GitHub creates the default branch
# with the first commit. The implementer can also start from an empty
# repository, but a repository with a branch is easier to inspect.
#
#   scripts/init-repo.sh OWNER/REPO
set -euo pipefail

repo="${1:?usage: $0 OWNER/REPO}"

branch=$(gh api "repos/$repo" --jq .default_branch)
if gh api "repos/$repo/branches/$branch" >/dev/null 2>&1; then
    printf 'repository %s: branch %s exists, nothing to do\n' "$repo" "$branch"
    exit 0
fi

content=$(printf '# %s\n\nThe OpenConveyor implementer works on this repository.\n' "${repo#*/}" | base64 | tr -d '\n')
gh api -X PUT "repos/$repo/contents/README.md" \
    -f message="chore: initial commit" -f content="$content" >/dev/null
printf 'repository %s: created %s with README.md\n' "$repo" "$branch"

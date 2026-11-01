#!/usr/bin/env bash
# Creates the three labels of the GitHub flow on the target repository. The
# implementer adds and removes conveyor:review and conveyor:iterate, and
# GitHub rejects a label that does not exist. --force updates the color and
# the description of a label that exists, so the script is idempotent.
#
#   scripts/create-labels.sh OWNER/REPO
set -euo pipefail

repo="${1:?usage: $0 OWNER/REPO}"

create() {
    gh label create "$1" --repo "$repo" --force --color "$2" --description "$3" >/dev/null
    printf 'label %s: ok\n' "$1"
}

create conveyor:ready   0e8a16 "Dispatch to OpenConveyor: the implementer opens a PR"
create conveyor:review  1d76db "OpenConveyor reviews this PR"
create conveyor:iterate fbca04 "OpenConveyor addresses the review on this PR"

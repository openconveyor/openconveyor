#!/usr/bin/env bash
# Filters a multi-document YAML stream on stdin by the top-level `kind:`.
#
#   select-kinds.sh keep KIND...   print only the documents of these kinds
#   select-kinds.sh drop KIND...   print every other document
#
# The input is the output of `kubectl kustomize`: documents separated by
# "---" lines, with `kind:` at column 0.
set -euo pipefail

mode="${1:-}"
shift || true
if [[ "$mode" != keep && "$mode" != drop ]] || (($# == 0)); then
    echo "usage: $0 keep|drop KIND..." >&2
    exit 2
fi

awk -v mode="$mode" -v kinds="$*" '
BEGIN { n = split(kinds, list, " "); for (i = 1; i <= n; i++) want[list[i]] = 1 }
function flush() {
    if (doc != "" && ((mode == "keep") == (kind in want))) { printf "%s---\n", doc }
    doc = ""; kind = ""
}
/^---$/ { flush(); next }
/^kind: / { kind = $2 }
{ doc = doc $0 "\n" }
END { flush() }
'

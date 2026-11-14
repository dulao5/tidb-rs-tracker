#!/usr/bin/env bash
# Merge a tracker PR and its tidb-rs PR back to back, in the required order.
#
#   scripts/merge_pair.sh <tracker-pr> <tidb-rs-pr>
#
# A tracker PR that registers stubs (data/stubs.tsv) must land right before
# the tidb-rs PR whose code it describes; otherwise tracker main and tidb-rs
# main disagree and the tracker's stub check goes red. The script stops at
# the first failed step and never hides errors.
#
# Steps:
#   1. both PRs are open, MERGEABLE, and all their checks passed;
#   2. `parts.py scan` on the test-merge refs (refs/pull/N/merge) of both
#      PRs reports that the stub registry matches the code;
#   3. merge the tracker PR at the checked head (--match-head-commit), confirm;
#   4. merge the tidb-rs PR the same way; if this fails after step 3, say so loudly.
#
# Environment: TRACKER_REPO (default dulao5/tidb-rs-tracker),
# TIDB_RS_REPO (default dulao5/tidb-rs). DRY_RUN=1 runs steps 1-2 only.
# Needs gh, git (ssh access to GitHub), python3.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <tracker-pr> <tidb-rs-pr>" >&2
  exit 2
fi
T_PR=$1
R_PR=$2
T_REPO=${TRACKER_REPO:-dulao5/tidb-rs-tracker}
R_REPO=${TIDB_RS_REPO:-dulao5/tidb-rs}

# mergeable is computed lazily by GitHub; UNKNOWN means "ask again".
mergeable() {
  local repo=$1 pr=$2 state i
  for i in 1 2 3 4 5 6; do
    state=$(gh pr view "$pr" -R "$repo" --json mergeable --jq .mergeable)
    [ "$state" != UNKNOWN ] && break
    sleep 5
  done
  echo "$state"
}

check_pr() {
  local repo=$1 pr=$2 state m bad
  state=$(gh pr view "$pr" -R "$repo" --json state --jq .state)
  [ "$state" = OPEN ] || { echo "$repo#$pr is $state, not OPEN" >&2; exit 1; }
  m=$(mergeable "$repo" "$pr")
  [ "$m" = MERGEABLE ] || { echo "$repo#$pr is $m, not MERGEABLE" >&2; exit 1; }
  # Every check must have completed with success, skipped or neutral.
  bad=$(gh pr view "$pr" -R "$repo" --json statusCheckRollup --jq \
    '[.statusCheckRollup[] | select((.conclusion // .state) as $c
       | ($c != "SUCCESS" and $c != "SKIPPED" and $c != "NEUTRAL"))
     | "\(.name // .context): \(.conclusion // .state // "PENDING")"] | .[]')
  if [ -n "$bad" ]; then
    echo "$repo#$pr has checks that did not pass:" >&2
    echo "$bad" >&2
    exit 1
  fi
  echo "ok: $repo#$pr open, mergeable, checks green"
}

merge_pr() {
  local repo=$1 pr=$2 head=$3 state oid
  gh pr merge "$pr" -R "$repo" --merge --match-head-commit "$head"
  state=$(gh pr view "$pr" -R "$repo" --json state --jq .state)
  [ "$state" = MERGED ] || { echo "$repo#$pr is $state after merge" >&2; exit 1; }
  oid=$(gh pr view "$pr" -R "$repo" --json mergeCommit --jq .mergeCommit.oid)
  echo "merged: $repo#$pr -> ${oid:0:7}"
}

# 1. Both PRs ready.
check_pr "$T_REPO" "$T_PR"
check_pr "$R_REPO" "$R_PR"

# 2. Stub registry matches the code, checked on GitHub's test-merge refs
# (refs/pull/N/merge = the PR merged into the current main), because that is
# what both mains will contain after the merge. The ref exists while the PR
# is MERGEABLE (step 1). The PR head behind each merge ref is its second
# parent; step 3/4 merge with --match-head-commit on that sha, so exactly the
# checked commit gets merged.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fetch_merge_ref() {  # repo pr dir -> prints the PR head sha
  git init -q "$3"
  git -C "$3" fetch -q --depth 2 "git@github.com:$1.git" "refs/pull/$2/merge"
  git -C "$3" checkout -q FETCH_HEAD
  git -C "$3" rev-parse FETCH_HEAD^2
}
t_head=$(fetch_merge_ref "$T_REPO" "$T_PR" "$work/tracker")
r_head=$(fetch_merge_ref "$R_REPO" "$R_PR" "$work/tidb-rs")
(cd "$work/tracker" && python3 scripts/blocks/parts.py scan "$work/tidb-rs")
echo "ok: stub registry matches (merge refs; heads ${t_head:0:7} / ${r_head:0:7})"

# 3. and 4. Merge in order.
if [ "${DRY_RUN:-0}" = 1 ]; then
  echo "dry run: not merging"
  exit 0
fi
merge_pr "$T_REPO" "$T_PR" "$t_head"
if ! merge_pr "$R_REPO" "$R_PR" "$r_head"; then
  echo "!!! $T_REPO#$T_PR IS MERGED BUT $R_REPO#$R_PR IS NOT." >&2
  echo "!!! tracker main and tidb-rs main now disagree: fix and merge $R_REPO#$R_PR right away." >&2
  exit 1
fi

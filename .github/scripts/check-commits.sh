#!/usr/bin/env bash
set -euo pipefail

subjects=$(git log --no-merges --format=%s "$1..$2")
if [[ -z "$subjects" ]]; then
  echo "No commits found in pull request range" >&2
  exit 1
fi
while IFS= read -r subject; do
  if [[ ! "$subject" =~ ^(feat|fix|docs|style|refactor|test|chore|ci|build|perf)(\([^()]+\))?:\ [^[:space:]].*$ ]]; then
    echo 'Invalid commit subject' >&2
    exit 1
  fi
done <<< "$subjects"

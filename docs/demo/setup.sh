#!/usr/bin/env bash
# Prepare the demo environment for the cast recording.
#
# Clones AxeForging/tacomex-8bit-shop into a stable path so the cast script
# can `cd` into it deterministically. Idempotent — safe to re-run.
#
# Prereqs: `dupehound` must be on PATH.
#
# Usage:
#   bash docs/demo/setup.sh           # default location
#   bash docs/demo/setup.sh /tmp/foo  # custom location
set -euo pipefail

DEMO_REPO="${1:-/tmp/dupehound-demo-repo}"
UPSTREAM="https://github.com/axeforging/tacomex-8bit-shop.git"

if ! command -v dupehound >/dev/null 2>&1; then
	echo "demo: dupehound not found on PATH — install it first" >&2
	exit 1
fi

if [ -d "$DEMO_REPO/.git" ]; then
	echo "demo: $DEMO_REPO already exists; skipping clone"
else
	echo "demo: cloning $UPSTREAM into $DEMO_REPO"
	git clone --depth 1 --quiet "$UPSTREAM" "$DEMO_REPO"
fi

cat <<NOTE
demo: setup complete.
  repo : $DEMO_REPO
  bin  : $(command -v dupehound)

Next: from the dupehound repo root run
  python3 ~/Documents/workspace/axeforge/git/scripts/cast/cast.py docs/demo/cast.yaml
NOTE

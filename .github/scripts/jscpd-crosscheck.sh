#!/usr/bin/env bash
#
# jscpd-crosscheck.sh — sanity-benchmark dupehound against an independent
# copy-paste detector (jscpd) on real codebases.
#
# This is a SECOND OPINION, not ground truth. The two tools measure different
# things and will not match 1:1:
#
#   - dupehound normalizes identifiers AND literals, so it detects type-2 clones
#     (same structure, renamed vars / different strings). It therefore reports
#     MORE on structurally-parallel code (i18n locale files, generated code).
#   - jscpd (default) matches near-verbatim text, so it reports the literally
#     copy-pasted subset.
#
# Use it to catch regressions and gross divergence ("did we suddenly stop
# finding what an independent tool still finds?"), not to chase an exact number.
#
# Requirements: go, node/npx (for `npx jscpd`), python3, git.
#
# Usage:
#   .github/scripts/jscpd-crosscheck.sh [--min-tokens N] [--clone] PATH:LANG [PATH:LANG ...]
#
#   --min-tokens N   minimum token window for both tools (default: 50)
#   --clone          shorthand: also clone a curated set of real repos into
#                    a temp dir and benchmark those (ignores positional args)
#
# Examples:
#   .github/scripts/jscpd-crosscheck.sh ./services:go
#   .github/scripts/jscpd-crosscheck.sh --min-tokens 40 /tmp/zod:typescript
#   .github/scripts/jscpd-crosscheck.sh --clone
#
set -euo pipefail

MIN_TOKENS=50
DO_CLONE=0
TIMEOUT=300 # per-tool wall-clock cap (seconds); no run may hang the harness
TARGETS=()

while [[ $# -gt 0 ]]; do
	case "$1" in
		--min-tokens) MIN_TOKENS="$2"; shift 2 ;;
		--timeout) TIMEOUT="$2"; shift 2 ;;
		--clone) DO_CLONE=1; shift ;;
		-h|--help) sed -n '2,32p' "$0"; exit 0 ;;
		*) TARGETS+=("$1"); shift ;;
	esac
done

# run_capped runs a command under `timeout` when available so a slow/hung tool
# (jscpd on a huge repo, a stalled clone) can never block the whole harness.
run_capped() {
	if command -v timeout >/dev/null 2>&1; then
		timeout "${TIMEOUT}s" "$@"
	else
		"$@"
	fi
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="$(mktemp -u /tmp/dupehound.XXXXXX)"

echo "building dupehound..."
( cd "$ROOT" && go build -o "$BIN" . )

# Exclusions applied to both tools so we compare real source, not vendored /
# minified / test fixtures (which inflate duplication for both).
DH_EXCLUDES=(-e '**/node_modules/**' -e '**/dist/**' -e '**/build/**'
	-e '**/*.min.js' -e '**/vendor/**' -e '**/*_test.go'
	-e '**/*.test.*' -e '**/*.spec.*' -e '**/test/**' -e '**/tests/**' -e '**/__tests__/**')
JSCPD_IGNORE='**/node_modules/**,**/dist/**,**/build/**,**/*.min.js,**/vendor/**,**/*_test.go,**/*.test.*,**/*.spec.*,**/test/**,**/tests/**,**/__tests__/**'

if [[ "$DO_CLONE" == "1" ]]; then
	WORK="$(mktemp -d /tmp/dh-crosscheck.XXXXXX)"
	echo "cloning curated repos into $WORK ..."
	run_capped git clone --depth 1 -q https://github.com/axios/axios.git      "$WORK/axios"   || true
	run_capped git clone --depth 1 -q https://github.com/colinhacks/zod.git    "$WORK/zod"     || true
	run_capped git clone --depth 1 -q https://github.com/expressjs/express.git "$WORK/express" || true
	TARGETS=("$WORK/axios:javascript" "$WORK/zod:typescript" "$WORK/express:javascript")
fi

if [[ ${#TARGETS[@]} -eq 0 ]]; then
	echo "no targets given; pass PATH:LANG arguments or --clone" >&2
	exit 2
fi

printf '\n%-28s %-12s | %-22s | %-22s\n' "target" "lang" "dupehound" "jscpd"
printf '%s\n' "--------------------------------------------------------------------------------------------"

dh_dup() { # path lang -> "clones|pct"
	"$BIN" scan -p "$1" -L "$2" "${DH_EXCLUDES[@]}" -f json 2>/dev/null |
		python3 -c "import json,sys
try:
    d=json.load(sys.stdin); print(str(d['total_clones'])+'|'+format(d['duplication_pct'],'.1f'))
except Exception: print('err|err')"
}

jscpd_dup() { # path lang -> "clones|pct"
	local out; out="$(mktemp -d)"
	run_capped npx --yes jscpd "$1" --format "$2" --min-tokens "$MIN_TOKENS" --ignore "$JSCPD_IGNORE" \
		--silent --reporters json --output "$out" >/dev/null 2>&1 || true
	if [[ -f "$out/jscpd-report.json" ]]; then
		python3 -c "import json,sys
t=json.load(open(sys.argv[1]))['statistics']['total']
print(str(t['clones'])+'|'+format(t['percentage'],'.1f'))" "$out/jscpd-report.json"
	else
		echo "n/a|n/a"
	fi
	rm -rf "$out"
}

for tgt in "${TARGETS[@]}"; do
	path="${tgt%%:*}"; lang="${tgt##*:}"
	[[ -d "$path" ]] || { printf '%-28s %-12s | %s\n' "$(basename "$path")" "$lang" "MISSING PATH"; continue; }
	IFS='|' read -r dc dp <<<"$(dh_dup "$path" "$lang" || echo 'err|err')"
	IFS='|' read -r jc jp <<<"$(jscpd_dup "$path" "$lang" || echo 'n/a|n/a')"
	printf '%-28s %-12s | %5s clones %6s%%   | %5s clones %6s%%\n' \
		"$(basename "$path")" "$lang" "$dc" "$dp" "$jc" "$jp"
done

echo
echo "note: dupehound >= jscpd is expected — type-2 normalization catches structural"
echo "clones jscpd misses. Investigate when dupehound reports FEWER than jscpd, or"
echo "when either number swings sharply between versions."

rm -f "$BIN"

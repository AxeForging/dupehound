# dupehound demo recording

This directory holds the spec for the hero demo GIF embedded in the project's
[README.md](../../README.md) and [EXAMPLES.md](../../EXAMPLES.md).

## Files

| File | Tracked? | What |
|---|---|---|
| `cast.yaml` | yes | Declarative cast spec — what gets typed and when |
| `setup.sh` | yes | Idempotent fixture: clones [tacomex-8bit-shop](https://github.com/axeforging/tacomex-8bit-shop) into `/tmp/dupehound-demo-repo` |
| `dupehound-demo.gif` | yes | The rendered hero GIF (~190 KB) |
| `dupehound-demo.cast` | **no** (gitignored) | asciinema intermediate, regenerable |
| `.bin/` | **no** (gitignored) | scratch dir for any locally-built binary |

## Regenerate

Prereqs: `dupehound` on `PATH`, plus the [`cast`](https://github.com/AxeForging/scripts/tree/main/cast) recorder (which itself needs `asciinema`, `agg`, and `yoink`).

```sh
# 1. Set up the demo repo (clones tacomex-8bit-shop into /tmp/dupehound-demo-repo)
bash docs/demo/setup.sh

# 2. Record + render
python3 ~/Documents/workspace/axeforge/git/scripts/cast/cast.py docs/demo/cast.yaml
```

The script writes a fresh `dupehound-demo.cast` (intermediate) and `dupehound-demo.gif` (final). Commit only the GIF.

## Why this demo target?

Tacomex is a real, small, public TypeScript codebase with organic duplication patterns (route handlers, seed data) and a few real dead-function candidates (`closeConnection`, `optionalAuth`, `requireAdmin`). It demonstrates dupehound on something a developer would actually scan, not a synthetic fixture.

## Updating the demo

If you change dupehound's output (flags, formatters, log lines), the GIF goes stale. Re-record by running the steps above. Keep the spec terse — the demo's job is to show the *result*, not every flag.

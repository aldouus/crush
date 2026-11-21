# Hill-Climb Results

| # | Title | Target | Before | After | Delta | Verdict |
|---|-------|--------|--------|-------|-------|---------|
| 01 | Lazy gh/gh-stack probes | CLI cold start (`--version`) | 204.9 ms | 112.3 ms | -45% | KEPT |
| 02 | Lazy chroma lexer registry (investigation) | CLI cold start (`--version`) | 111.1 ms | n/a | n/a | REVERTED |
| 03 | Reuse TUI frame canvas | `BenchmarkUIView` msgs=500 | 4.061 ms, 1686 KiB | 3.527 ms, 232 KiB | -13.2% time, -86% B/op | KEPT |
| 04 | Single-pass TUI frame flatten | `BenchmarkUIView` msgs=500 | 3.539 ms, 231.4 KiB | 3.451 ms, 236.6 KiB | ~ (p=0.48), +2.3% B/op | REVERTED |

## Attempt 01: Lazy gh/gh-stack probes

- **Hypothesis**: package-level `ghAvailable`/`ghStackAvailable` in
  `internal/agent/tools/tools.go` run `exec.LookPath("gh")` and
  `gh extension list` during package init, adding ~90 ms to every crush
  invocation, including `--version`.
- **Change**: `internal/agent/tools/tools.go`, `internal/agent/tools/bash.go`.
  Both probes wrapped in `sync.OnceValue`; the only call sites
  (`RenderToolDescription`, bash description render) now call them. Results
  are identical, just computed on first use.
- **Method**: hyperfine `-N --warmup 3 --runs 30`, both binaries back to back,
  HOME/XDG in a temp dir. Load avg 3.77 at start, 3.03 at end.
- **Results** (wall-time median ± stddev):

  | Command | Before | After | Delta |
  |---------|--------|-------|-------|
  | `crush --version` | 204.9 ± 13.3 ms | 112.3 ± 7.9 ms | -45.2% |
  | `crush --help` | 208.9 ± 13.2 ms | 117.7 ± 8.2 ms | -43.7% |
  | `crush dirs` | 247.7 ± 15.6 ms | 164.8 ± 11.4 ms | -33.5% |

  Every median gap is well over 2x stddev. Peak RSS unchanged (about 61 MiB).
- **Tests**: `go test ./internal/agent/tools/`, `go vet`, and golangci-lint
  all pass with 0 issues.
- **Other init candidates** (`GODEBUG=inittrace=1`; none of these spawn a
  subprocess, so nothing was changed for them): `chroma/v2/styles` takes
  about 15 ms and `chroma/v2/lexers` about 14 ms, both building
  registries eagerly. `go-nativeclipboard` takes 3.7 ms. `encoding/gob`
  takes 1.5 ms. The rest are each 1 ms or less.
- **Verdict**: KEPT.

## Attempt 02: Lazy chroma lexer registry (investigation)

- **Hypothesis**: what's left of the cold start is package init, mostly
  chroma's lexer and style registries. Deferring the biggest of these
  until first use would cut `crush --version`, `--help` and `dirs`.
- **Findings** (load avg 1.80 / 2.72 / 2.76):
  - hyperfine `-N`: a Go hello-world binary takes 8.0 ± 0.3 ms (4 MiB RSS).
    `crush --version` takes 111.1 ± 9.2 ms (61 MiB RSS) on a 174 MB binary.
  - `GODEBUG=inittrace=1`: the init phase ends about 85 ms after process
    start. Summed init clock is about 45 ms. The rest is spread across
    page-in and many small inits.
  - Largest inits: `chroma/v2/lexers` 16-21 ms (2.8 MB, 32k allocs, parses
    every embedded XML lexer config) and `chroma/v2/styles` 12-14 ms.
    After those: `go-nativeclipboard` 4 ms, `anthropic-sdk-go` 3.6 ms,
    `encoding/gob` 2.6 ms and `openai-go` 2.5 ms.
  - Crush's own packages: every init is under 5 ms. `internal/cmd` takes
    about 1 ms (3 ms on a cold cache), `internal/agent/tools` 1.1 ms and
    `internal/version` 0.9 ms. Nothing in crush reaches the 5 ms
    threshold, so nothing there is worth deferring.
- **Approach tried**: forked chroma v2.27.0 into `third_party/chroma` and
  pointed `go.mod` at it with a replace. `LexerRegistry` got a
  `sync.Once` lazy hook, and the XML lexers were registered on first
  lookup, ahead of the Go-defined lexers so registration order stayed the
  same. The `dns`/`mysql`/`zed` analyser `init()`s moved into the hook.
  With this, lexers init dropped from about 22 ms to 2.3 ms.
  - **Abandoned**: the fork's tests (`lexers`, `quick`, `formatters/html`)
    deadlocked at 0% CPU. The likely cause is re-entrant `sync.Once`: a
    lookup from inside the lazy load waits on the same Once. Vendoring
    12 MB of a third-party library behind a replace is also too invasive
    for this PR. `styles` can't be deferred the same way, because glamour
    reads `styles.Registry` directly.
- **Change**: none. The fork and the `go.mod` replace were removed
  completely.
- **Verdict**: REVERTED (no change kept). If chroma ever gets made lazy,
  it should be done upstream in chroma.

## Attempt 03: Reuse TUI frame canvas

- **Hypothesis**: `UI.View` allocates a fresh 200x60 `uv.ScreenBuffer` per
  frame (~85% of bytes/frame, HOTSPOTS #2). Reusing it and clearing to
  `EmptyCell` (same state as a new buffer) cuts allocation and GC work.
- **Change**: `internal/ui/model/draw.go` (`frameCanvas`: reuse, clear via
  `Buffer.Clear`, reallocate only on size change), `internal/ui/model/ui.go`
  (`canvas` field), new `internal/ui/model/canvas_reuse_test.go` asserting
  byte-identical View output vs a fresh canvas across scrolls and resizes.
  The canvas never escapes View (output is flattened to a string).
- **Method**: `go test -bench -benchmem -count=10`, `GOMAXPROCS=4`, HEAD in a
  temp worktree then change, back to back. Load avg 2.03 start, 2.77 end.
- **Results** (median, benchstat):

  | Benchmark | Before | After | Delta | p |
  |-----------|--------|-------|-------|---|
  | UIView/msgs=500 time | 4.061 ms | 3.527 ms | -13.16% | 0.000 |
  | UIView/msgs=2000 time | 4.012 ms | 3.435 ms | -14.39% | 0.000 |
  | UIScrollFrame time | 7.223 ms | 6.528 ms | -9.61% | 0.000 |
  | UIDraw time | 1.874 ms | 1.842 ms | ~ | 0.529 |
  | UIView/msgs=500 B/op | 1686.3 KiB | 231.8 KiB | -86.25% | 0.000 |
  | UIScrollFrame B/op | 2.594 MiB | 1.172 MiB | -54.81% | 0.000 |
  | UIView allocs/op | 2577 | 2450 | -4.93% | 0.000 |

  UIDraw unchanged as expected (it already reuses a buffer).
- **Tests**: `go test ./internal/ui/model/`, `go vet`, golangci-lint: pass,
  0 issues.
- **Verdict**: KEPT.

## Attempt 04: Single-pass TUI frame flatten

- **Hypothesis** (HOTSPOTS #2): `UI.View` post-processes `canvas.Render()`
  with `ReplaceAll` + `Split` + per-line `TrimRight` + `Join`, copying the
  frame several times; a single pass into a `strings.Builder` (returning the
  input unchanged when nothing needs trimming) should cut time and bytes.
- **Change**: `internal/ui/model/draw.go` (`flattenFrame` helper) plus a
  table test against the old pipeline (CRLF, lone CR, ANSI, wide chars, tabs,
  empty lines); byte-identical output verified.
- **Load avg**: 2.97 before, 2.64 after (1-min).
- **Results** (GOMAXPROCS=4, count=10):

| Benchmark | Before | After | Delta |
|-----------|--------|-------|-------|
| UIView/msgs=500 | 3.539 ms, 231.4 KiB, 2450 allocs | 3.451 ms, 236.6 KiB, 2449 allocs | ~ (p=0.481), +2.26% B/op |
| UIView/msgs=2000 | 3.513 ms, 230.9 KiB | 3.471 ms, 236.0 KiB | ~ (p=0.971), +2.17% B/op |
| UIScrollFrame | 6.473 ms, 1.173 MiB | 6.537 ms, 1.177 MiB | ~ (p=0.739), +0.29% B/op |

- **Verdict**: REVERTED. The flatten step is not a measurable share of frame
  time (draw/render dominates); the old `Split`/`Join` output is tighter than
  a `Grow(len(s))` builder, so B/op got slightly worse.

## Attempt 05: shared cross-instance diff highlight cache

- **Hypothesis**: `DiffView.hightlightCode` re-lexes every line on each new
  `DiffView` (the per-instance cache dies with the view), and tool-result
  diffs are rebuilt on every re-render. Whole-file highlighting + slicing was
  rejected: multi-line tokens (block comments, raw strings) would change
  per-line output, breaking byte-identity. A bounded process-wide cache keyed
  by (chroma style ptr, lexer name, bg RGBA, line source) is exact.
- **Change**: `internal/ui/diffview/highlight_cache.go` (new, RWMutex map,
  reset at 8192 entries), `internal/ui/diffview/diffview.go` (consult/fill it),
  test `TestSharedHighlightCacheIdenticalOutput`.
- **Load avg**: 2.38 (before) / 1.64 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| DiffRender/unified time | 204.55 ms | 79.82 ms | -60.98% (p=0.000) |
| DiffRender/split time | 238.5 ms | 121.0 ms | -49.27% (p=0.000) |
| unified B/op, allocs | 21.92 MiB, 549.5k | 11.58 MiB, 314.8k | -47%, -43% |
| split B/op, allocs | 29.06 MiB, 768.4k | 18.57 MiB, 533.4k | -36%, -31% |

- **Verdict**: KEPT. Note the benchmark re-renders the same diff, so it
  measures the warm (re-render) path; first render of a new diff is unchanged.
  Output is byte-identical (same lexer/formatter, cached per exact input).

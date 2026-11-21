# Hill-Climb Results

| # | Title | Target | Before | After | Delta | Verdict |
|---|-------|--------|--------|-------|-------|---------|
| 01 | Lazy gh/gh-stack probes | CLI cold start (`--version`) | 204.9 ms | 112.3 ms | -45% | KEPT |
| 02 | Lazy chroma lexer registry (investigation) | CLI cold start (`--version`) | 111.1 ms | n/a | n/a | REVERTED |
| 03 | Reuse TUI frame canvas | `BenchmarkUIView` msgs=500 | 4.061 ms, 1686 KiB | 3.527 ms, 232 KiB | -13.2% time, -86% B/op | KEPT |
| 04 | Single-pass TUI frame flatten | `BenchmarkUIView` msgs=500 | 3.539 ms, 231.4 KiB | 3.451 ms, 236.6 KiB | ~ (p=0.48), +2.3% B/op | REVERTED |
| 08 | Single-pass parts decode (dup of 09) | `BenchmarkMessageList` | 17.50 / 67.73 ms | 13.82 / 53.89 ms | -21% / -20% | KEPT (landed as 09) |
| 10 | Markdown renderer / xchroma style memo | `BenchmarkMarkdownRender` | 39.82 ms | 38.55 ms | ~ (p=0.39) | REVERTED |
| 11 | Startup harness + fast server-ready poll | cold TUI first frame / ready | 596.2 / 674.9 ms | 467.5 / 548.3 ms | -21.6% / -18.8% | KEPT |
| 12 | Cluster networking benchmark harness (baseline only) | cluster join/RPC/overview/reconnect/sync | see attempt 12 | n/a | n/a | BASELINE |
| 14 | Startup DB-open path (investigation) | `BenchmarkConnectExisting` (200 sessions / 20k msgs) | 5.149 ms | n/a | n/a | REVERTED (nothing avoidable) |
| 15 | Skip 25 ms readiness sleep on mesh start | `BenchmarkClusterMeshReconnect` / `ColdJoin` | 28.05 ms / 194.7 ms | 6.32 ms / 123.6 ms | -77.5% / -36.6% (p=0.000) | KEPT |

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

## Attempt 06: pre-rendered line-number gutter affixes in diffview

- **Hypothesis**: `lipgloss.Style.Render` on fixed-width line-number gutters
  is a large share of diff CPU. Since the gutter content always has the same
  cell width, render each LineNumber style once per `String()` around a
  sentinel of that width and reuse the prefix/suffix, falling back to
  `Render` for anything not plain single-width.
- **Change**: `internal/ui/diffview/linenum.go` (new), `style.go`
  (unexported affix field on `LineStyle`), `diffview.go` (17 call sites),
  test `TestRenderLineNumberMatchesLipgloss`. Golden tests pass unchanged.
- **Load avg**: 2.08 (before) / 2.34 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| DiffRender/unified time | 88.24 ms | 78.70 ms | -10.80% (p=0.000) |
| DiffRender/split time | 121.1 ms | 116.5 ms | ~ (p=0.105) |
| unified B/op, allocs | 11.58 MiB, 314.8k | 11.19 MiB, 301.7k | -3.44%, -4.16% |
| split B/op, allocs | 18.57 MiB, 533.4k | 18.22 MiB, 521.6k | -1.86%, -2.21% |

- **Verdict**: KEPT. Unified clears the bar; split improves allocs
  significantly and time is not regressed. The remaining Render cost is the
  per-line `Code.Width(...)` and content-style wraps, which depend on content.

## Attempt 07: single-pass SSE envelope encoding in server

- **Hypothesis**: `json.Marshal(*pubsub.Payload)` re-validates and
  re-compacts (clones) the already-marshalled `json.RawMessage` payload, so
  every streamed event pays for the full message twice. Writing the
  envelope by hand and copying the payload verbatim removes that pass.
- **Change**: `internal/server/events.go` (new `appendSSE`),
  `internal/server/proto.go` (workspace + global SSE writers use it instead
  of `json.Marshal` + `fmt.Fprintf`), `wire_bench_test.go` (bench mirrors
  production), test `TestAppendSSEMatchesMarshal` asserts byte-identical
  output vs `"data: "+json.Marshal(p)+"\n\n"` (incl. HTML-escaped content).
  All payloads are produced locally by `json.Marshal`, so they are already
  compact and escaped.
- **Load avg**: 2.18 (before) / 3.52 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| WireFullMessage time | 107.30 ms | 85.37 ms | -20.44% (p=0.000) |
| WireFullMessage B/op | 42.63 MiB | 34.40 MiB | -19.31% (p=0.000) |
| WireFullMessage allocs | 41.47k | 43.37k | +4.59% |
| WireDeltas time | 2.684 ms | 2.742 ms | ~ (p=0.315) |

- **Verdict**: KEPT. Large time and byte win on the full-message path
  (measured under higher load after). Small alloc-count rise is the
  per-event `json.Marshal(type)`; deltas unchanged.

## Attempt 09: single-pass part decoding in message list

- **Hypothesis**: `UnmarshalParts` parses each row's parts JSON twice:
  first into `[]json.RawMessage` (copying every element), then each element
  again into a `{type,data}` wrapper. Decoding straight into
  `[]struct{Type; Data json.RawMessage}` drops one full parse and the
  per-element copies. No schema/query change needed: the profile showed
  JSON decoding, not SQLite, dominating crush's own cost.
- **Change**: `internal/message/message.go` (`UnmarshalParts`). Behavior is
  unchanged; existing message tests pass.
- **Load avg**: 2.58 (before) / 4.01 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MessageList/500 time | 16.42 ms | 13.92 ms | ~ (p=0.105, noisy) |
| MessageList/2000 time | 68.53 ms | 68.03 ms | ~ (p=0.684) |
| MessageList/500 B/op, allocs | 2.944 MiB, 18.57k | 2.489 MiB, 15.32k | -15.43%, -17.51% (p=0.000) |
| MessageList/2000 B/op, allocs | 12.01 MiB, 74.10k | 10.17 MiB, 61.10k | -15.30%, -17.55% (p=0.000) |

- **Verdict**: KEPT (alloc-focused). B/op and allocs drop about 15-17%
  with p=0.000. Time shows no regression, and the geomean is -8%, though
  that is not significant because the machine was under heavy load.

## Attempt 10: markdown renderer / xchroma formatter style memo

- **Hypothesis**: (HOTSPOTS #6) a glamour TermRenderer is built per render
  and finished items re-render markdown on relayout. Investigation showed
  both are already done on HEAD: `common.MarkdownRenderer` memoizes per
  width, and assistant/user items cache rendered sections per
  (width, content hash). Profile of `BenchmarkMarkdownRender`: ~78% inside
  glamour/goldmark/lipgloss writers (third-party), ~20% in crush's
  `xchroma.Formatter`. Tested fallback: memoize the lipgloss style per
  token type within a Format call and use `io.WriteString` over `fmt.Fprint`.
- **Change (reverted)**: `internal/ui/xchroma/chroma.go`.
- **Load avg**: 4.03 (before) / 5.59 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MarkdownRender time | 39.82 ms | 38.55 ms | ~ (p=0.393) |
| MarkdownRender B/op | 3.984 MiB | 4.014 MiB | +0.75% (p=0.011) |
| MarkdownRender allocs | 358.6k | 357.4k | -0.33% (p=0.000) |
| DiffRender unified/split time | 80.35 / 117.0 ms | 80.13 / 118.8 ms | ~ |

- **Verdict**: REVERTED. The hypothesis was already in place on HEAD, and
  the formatter tweak changed nothing measurable. Whatever cost is left
  sits in third-party glamour writer chains (WrapWriter/IndentWriter/MarginWriter).

## Attempt 08: single-pass session message parts decode

- **Hypothesis**: `UnmarshalParts` decoded the column into
  `[]json.RawMessage`, then re-parsed each element into a
  `{type, data RawMessage}` wrapper, then decoded `data`: three passes and
  an extra copy per part. Decoding straight into `[]struct{Type; Data
  RawMessage}` drops one full pass and one copy per part.
- **Change**: `internal/message/message.go` (`UnmarshalParts`). A
  concurrent session committed the byte-identical change as attempt 09
  (a569ba22) while this attempt was measuring, so this commit only adds
  `internal/message/parts_roundtrip_test.go` (MarshalParts ->
  UnmarshalParts for every part type, plus empty, unknown-type and `null`
  element error cases).
- **Load avg**: 1.95 (before) / 6.68 -> 5.61 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MessageList/500 time | 17.50 ms | 13.82 ms | -21.03% (p=0.002) |
| MessageList/2000 time | 67.73 ms | 53.89 ms | -20.43% (p=0.000) |
| MessageList B/op | 2.943 / 12.01 MiB | 2.489 / 10.17 MiB | -15.4% (p=0.000) |
| MessageList allocs | 18.57k / 74.10k | 15.32k / 61.10k | -17.5% (p=0.000) |
| UISessionLoad | 21.35 / 28.53 ms | n/a | after-run build broken by a concurrent session's in-progress xchroma edit |

- **Verdict**: KEPT (code already on HEAD as attempt 09). Wins hold despite
  much higher load during the after run. Remaining cost is the per-part
  `data` decode and the string->[]byte copy of the parts column.

## Attempt 11: interactive startup harness + fast server-ready poll

- **Harness**: `CRUSH_STARTUP_TRACE=<file>` (`internal/startuptrace`)
  appends `<pid> <phase> <unix-ns>` once per phase per process. Client
  phases: `main`, `server_ready`, `workspace_created`, `agent_init`,
  `program_start`, `first_frame` (first `UI.View`), `overviews_loaded`;
  server phases: `server_listen`, `ws_build`, `ws_config`, `ws_db`,
  `ws_skills`, `ws_app`, `ws_done`. `bench/startup.py <bin> [runs]
  [--warm]` runs the TUI under `script` (pty) in a fresh temp project with
  isolated HOME/XDG/CRUSH_GLOBAL_* and a private `-H unix://<tmp>/s.sock`
  (never the live daemon), measures from exec, "ready" = max(first_frame,
  overviews_loaded), discards one warmup run, and kills every traced pid.
  Wired into `bench/run.sh` (15 cold + 15 warm runs).
- **Baseline breakdown (cold, n=20, median ms from exec)**: main 122.8,
  server_listen 308.5, server_ready 435.3, ws_build 480.0, ws_config 487.1,
  ws_db 527.3 (DB open+migrations ~40 ms), ws_done 533.3,
  workspace_created 579.1, first_frame 595.5, ready 676.6. Warm server:
  server_ready 119.3, workspace_created 265.3, first_frame 279.6,
  ready 364.0.
- **Hypothesis**: the biggest synchronous blocker before first frame on a
  cold start is idle waiting: the server listens at ~308 ms but the client
  only notices at ~435 ms because `readyPoller` backs off 100/200/400 ms
  from the first probe. Probing every 10 ms for the first second (then the
  old exponential backoff) should cut ~120 ms with no behavior change.
- **Change**: `internal/cmd/ready_poll.go` (fast window),
  `internal/cmd/ready_poll_test.go` (fast-window test; old backoff test
  disables the window).
- **Load avg**: 2.16 (start) / 2.70 (end).

| Metric (cold, n=20) | Before | After | Delta |
|---|---|---|---|
| server_ready | 432.0 +- 12.0 ms | 306.6 +- 14.9 ms | -29.0% |
| first_frame | 596.2 +- 14.5 ms | 467.5 +- 27.5 ms | -21.6% |
| ready | 674.9 +- 16.0 ms | 548.3 +- 34.5 ms | -18.8% |
| warm first_frame | 279.6 +- 18.8 ms | 282.3 +- 17.8 ms | ~ (unaffected path) |

- **Verdict**: KEPT. Medians separated by >4x stddev. Next blockers:
  ~120 ms of process init before `main` (paid twice on cold start: client
  and spawned server), ~145 ms CreateWorkspace round trip on a warm server
  (~45 ms between `ws_done` and the client seeing the response), and
  ~80 ms first_frame -> overviews_loaded.

## Attempt 12: cluster networking benchmark harness (baseline, no optimization)

- **Goal**: measurable cluster networking paths. All nodes are in-process
  on 127.0.0.1 random ports with `b.TempDir()` data dirs; the live cluster,
  daemon and `~/.local/share/crush` are never touched. Every node and mesh
  is closed in cleanup.
- **Change**: `internal/cluster/cluster_bench_test.go`,
  `internal/replica/replica_bench_test.go` (new); test helpers
  `newTestNode`/`freeAddr`/`pairedTrio` and replica helpers now take
  `testing.TB`; `bench/run.sh` + `bench/README.md` include them.
- **Relay**: nodes route over a full NATS mesh with `NoAdvertise`; there is
  no in-process relay mode (`EndpointRelay` is reserved in `state.go`), so
  no relay benchmark.
- **Note**: `Mesh.NumRoutes` counts pooled route connections (several per
  peer), so the harness counts distinct peers via `Routez` instead.
- **Load avg**: 1.23 (start) / 2.31 (end). GOMAXPROCS=4, count=10,
  raw in `bench/out/cluster-baseline/`.

| Benchmark | median | p50 / p99 | B/op | allocs/op |
|---|---|---|---|---|
| ClusterColdJoin (pair 3 + mesh) | 194.4 ms +-2% | mesh part 84 ms | 4.55 MiB | 36.7k |
| ClusterMeshRequest (NATS req/rep) | 138.2 us | 138.5 / 206.5 us | 2.4 KiB | 39 |
| ClusterCallAPI (mTLS HTTP) | 437.5 us +-19% | 447.5 / 741 us | 15.9 KiB | 178 |
| ClusterOverviewFetch (2 x 200 sessions) | 1.887 ms | 1.81 / 2.83 ms | 472 KiB | 1.29k |
| ClusterMeshReconnect (peer restart) | 28.0 ms | 28.4 / 28.8 ms | 1.96 MiB | 15.5k |
| ClusterMeshThroughput (1 KiB msgs) | 175 MiB/s | - | 527 KiB/256 msgs | 2.2k |
| ReplicaFullSync (200 sess x 10 msgs) | 1.325 s +-6% | 842 KiB wire | 31.2 MiB | 387k |
| ReplicaServeDelta (no changes) | 477.7 us | - | 24.3 KiB | 709 |

- **Verdict**: BASELINE (harness only). Hotspots in `bench/HOTSPOTS.md`
  ("Cluster").

## Attempt 13: cache prepared statements in replica Apply

- **Hypothesis**: cluster hotspot #1 is `replica.Apply` (1.33 s full sync
  of 200 sessions x 10 msgs). Every row runs an unprepared upsert plus an
  unprepared owner `SELECT`, so sqlite re-prepares the same few SQL
  strings thousands of times. Preparing each distinct query once per
  transaction should remove most of that cost with identical semantics.
- **Change**: `internal/replica/replica.go` (`stmtCache` on the apply tx;
  `upsert` and the owner lookup go through it; closed with the tx).
- **Load avg**: 1.68 (start) / 2.73 (end). GOMAXPROCS=4, count=10, raw in
  `bench/out/attempt13/`.

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ReplicaFullSync time/op | 1325.2 ms +-6% | 446.3 ms +-5% | -66.3% (p=0.000) |
| ReplicaFullSync B/op | 31.16 MiB | 31.78 MiB | +2.0% |
| ReplicaFullSync allocs/op | 386.9k | 395.6k | +2.2% |
| ReplicaServeDelta time/op | 482.2 us | 476.1 us | ~ (p=0.393) |

- **Verdict**: KEPT. 3x faster full sync; small alloc increase from
  statement handles is negligible. Tests pass (`-race` on replica and
  cluster), vet + lint clean. Next: per-row owner lookups could be cached
  per session within a batch.

## Attempt 14: startup DB-open path (investigation)

- **Hypothesis**: on a realistic data dir, startup DB work (Connect:
  lock, open, pragmas, migration check; then ListSessions) does avoidable
  work: migrations re-checked via goose, redundant pragmas, or unneeded
  queries before first frame.
- **Method**: temporary `internal/db/connect_bench_test.go` populating a
  temp data dir with 200 sessions x 100 messages (20k), then looping
  `Connect(WithDataDirLock(true))` + `ListSessions` + `Release`, with a
  CPU profile focused on the loop. Also reviewed `buildWorkspace` and
  `app.New` for synchronous startup queries.
- **Findings** (GOMAXPROCS=4, count=10): 5.149 ms +- 4%/op, 339.9 KiB,
  1619 allocs. Breakdown: `PingContext` (driver open + DSN pragmas) ~85%
  of Connect, and nearly all of that is SQLite's one-time schema parse
  (`sqlite3InitOne`) triggered by the first pragma, which any first query
  would pay anyway; data-dir lock ~7%; `schemaCurrent` ~5%; goose is
  already skipped for an up-to-date DB (fast path exists). ListSessions
  over 200 rows is ~1-2 ms of page reads. The 40 ms `ws_db` seen in
  attempt 11 is migrations on a brand-new DB (first run only); on an
  existing DB the whole DB path is ~3-5 ms of a ~470 ms cold first frame
  (<1%). Swarm backfill, MCP init and update checks already run in
  goroutines off the critical path.
- **Change**: none kept (benchmark file removed per protocol).
- **Load avg**: 1.91 (start) / 2.06 (end).
- **Verdict**: REVERTED / no change. No single avoidable cost >3% of the
  DB path exists; the remaining cost is inherent schema load + file I/O.
  Startup time is better pursued in process init before `main` (~120 ms,
  paid twice on cold start) and the CreateWorkspace round trip.

## Attempt 15: skip the 25 ms readiness sleep on mesh start

- **Hypothesis**: reconnect (28 ms) and cold-join mesh time (84 ms) are
  dominated not by route backoff, gossip, or serial dials, but by
  `StartMesh` itself: nats-server's `ReadyForConnections(10s)` sleeps a
  fixed 25 ms whenever its first check fails, which it always does right
  after `go srv.Start()`. Timing showed StartMesh = 28.4 ms of the 28.6 ms
  reconnect; routes connect <0.2 ms after it returns.
- **Change**: `internal/cluster/mesh.go`: `waitMeshReady` polls
  `ReadyForConnections(1ms)` with a 200 us sleep up to the same 10 s
  deadline. Same readiness semantics and failure path.
- **Results** (GOMAXPROCS=4, count=10, benchstat):

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ClusterMeshReconnect time/op | 28.05 ms +-1% | 6.32 ms +-3% | -77.5% (p=0.000) |
| ClusterMeshReconnect p99 | 28.79 ms | 11.55 ms | -59.9% |
| ClusterColdJoin time/op | 194.7 ms +-2% | 123.6 ms +-3% | -36.6% (p=0.000) |
| ClusterColdJoin mesh-ms | 84 | 13 | -84.5% |
| B/op, allocs/op | | | -18% to -33% |

- **Load avg**: 1.83 (start) / 3.08 (end).
- **Verdict**: KEPT. `go test -race ./internal/cluster`, vet, lint clean.
  Survivor-side route redial (fixed 1 s NATS `routeConnectDelay`, not
  configurable) remains the next limit when the restarted peer cannot
  dial out; remaining cold-join cost is pairing (~110 ms).

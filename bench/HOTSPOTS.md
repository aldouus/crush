# Hotspots (baseline)

Measured on Apple M6, `GOMAXPROCS=4`, go1.27.3, HEAD `1e770dc7`. Raw data
and profiles live in `bench/out/baseline/` (`*.cpu.pprof`, `*.mem.pprof`,
`*.test`). Ranked by expected user-facing impact.

## 1. CLI cold start: `gh extension list` subprocess at package init

- Evidence: `crush --version` 206 ms, `--help` 214 ms, `dirs` 228 ms
  (hyperfine medians). `GODEBUG=inittrace=1` shows
  `internal/agent/tools` init at **~234 ms clock**, next largest is
  `chroma/v2/styles` at 14 ms.
- Cause: `ghStackAvailable` in `internal/agent/tools/tools.go:112` runs
  `exec.Command("gh", "extension", "list")` in a package-level var
  initializer, so every invocation (even `--version`) forks `gh`.
- Idea: make `ghAvailable`/`ghStackAvailable` lazy (`sync.OnceValue`),
  evaluated only when tool descriptions are rendered. Expect ~10x faster
  cold start. Secondary: chroma styles/lexers init (~26 ms, 72k allocs)
  could be deferred.

## 2. TUI frame: fresh 200x60 `ScreenBuffer` allocated per frame

- Evidence: `BenchmarkUIView` 4.1 ms/frame, 1.64 MiB/frame, 2.6k allocs.
  alloc_space: `uv.NewScreenBuffer` -> `uv.NewBuffer` **84.9%** of bytes.
  CPU: `Buffer.Render`/`renderLine` 28%, `UI.Draw` 32%, GC mark workers
  17%.
- Cause: `UI.View` (`internal/ui/model/draw.go:194`) allocates a new canvas
  every frame, then `canvas.Render()` + `ReplaceAll` + `Split` + per-line
  `TrimRight` + `Join` copies the whole screen string several times.
- Ideas: reuse the canvas across frames (clear instead of reallocate,
  realloc only on resize); trim trailing spaces and normalize newlines in
  one pass into a reused `strings.Builder`; `BenchmarkUIScrollFrame`
  (7.2 ms, 2.6 MiB) benefits from both.

## 3. Diff rendering: chroma re-highlighting and lipgloss per segment

- Evidence: `BenchmarkDiffRender` unified 201 ms / 21.9 MiB / 549k allocs,
  split 235 ms / 29 MiB / 768k allocs (400 line file). CPU:
  `lipgloss.Style.Render` **46.9%**, `DiffView.hightlightCode` 44.4%
  (chroma lexing `matchRules` 23%). alloc: `Style.Render` 55%,
  `strings.Builder.WriteString` 36% flat.
- Ideas: highlight each side once and slice per line instead of per
  hunk/line; cache line-number/gutter styles as prerendered strings;
  avoid `Style.Render` for plain padding (write spaces directly).

## 4. Server wire encoding of full messages

- Evidence: `BenchmarkWireFullMessage` 104 ms / 42.6 MiB / 41k allocs.
  alloc: `bytes.Clone` **74.9%** flat; `wrapEvent`/`envelope` 59%;
  `proto.Message.MarshalJSON` 38%. CPU: `jsontext.reformatObject` 26%.
- Cause: payload is marshalled, then cloned and re-encoded (reformatted)
  while wrapping into the envelope.
- Ideas: marshal envelope + payload in a single encoder pass (embed raw
  payload as `jsontext.Value` without reformat), drop the `bytes.Clone`,
  pool encode buffers.

## 5. Session load: JSON decode of message parts

- Evidence: `BenchmarkMessageList` 500 msgs 16.1 ms / 2.9 MiB,
  2000 msgs 64.5 ms / 12 MiB / 74k allocs (linear). CPU:
  `message.UnmarshalParts` **41%**, json/v2 decode 38%, SQLite
  `database/sql` 17%. alloc: `fromDBItem` 69%, `jsontext.Value.UnmarshalJSON`
  25% (raw part copies), `ListMessagesBySession` 25%.
- Ideas: decode parts with a single pass into typed structs (avoid
  intermediate `jsontext.Value` copies of `data`); avoid re-copying the
  `parts` column string to `[]byte`; paginate/lazy-load older messages in
  the TUI.

## 6. UI session load and markdown

- Evidence: `BenchmarkUISessionLoad` 500 msgs 21 ms / 145k allocs, 2000
  msgs 29 ms / 175k allocs. `BenchmarkMarkdownRender` 37 ms / 359k allocs
  for a ~4 KB reply (glamour + chroma on code blocks).
- Ideas: markdown is the dominant per-item cost when items become visible;
  cache rendered output per (content, width) across reloads and consider a
  lighter code-block highlighter path. Session load allocations grow with
  history size even though only the tail is visible: defer item
  construction for off-screen messages.

## Not hot

- `BenchmarkBrokerPublish`: 0.79 us, 0 allocs.
- `BenchmarkLoadFromConfigPaths`: 206 us, 396 allocs.
- `BenchmarkWireDeltas`: 2.7 ms, 715 KiB.

## Cluster (attempt 12 baseline)

Profiles in `bench/out/cluster-baseline/*.top.txt`.

1. **Replica full sync is by far the slowest path**: 1.33 s for 200
   sessions x 10 messages (2.2k rows, 842 KiB). `Apply` dominates (Serve is
   negligible): one unprepared `upsert` per row inside the tx plus a
   per-row `owner()` `SELECT` for every message, with sqlite prepare ~12%
   of CPU and >50% of samples in goroutine wakeups (`pthread_cond_signal`)
   from per-statement context handling. Ideas: cache owner lookups per
   session within a batch, prepare one statement per table/column set,
   batch multi-row upserts.
2. **Cold join**: 194 ms for 3 nodes, of which mesh routing is 84 ms (TLS
   route handshakes ~27% of CPU; NATS pools several route connections per
   peer, each with its own handshake). The other ~110 ms is pairing; real
   `Join` polls approval every **2 s** by default (`ops.go`
   `PollInterval`), so an interactive join waits up to 2 s after approval.
3. **Reconnect**: 28 ms when the restarted peer dials out. Survivors rely
   on NATS route reconnect (`ConnectRetries: -1`, default 1 s reconnect
   delay), so if the restarted peer cannot dial back, recovery is bounded
   by that interval instead.
4. **CallAPI** (0.44 ms) is ~3x a NATS round trip (0.14 ms): HTTP + JSON +
   mTLS per call on a keep-alive transport; `CallAPI` builds a new
   `http.Client` per call (cheap) and resolves the endpoint each time.
5. **Overview fetch**: 1.9 ms for 2 x 200 sessions, mostly JSON decode
   (472 KiB/op). The server's remote view also does a separate
   sync-position call before each overview fetch (serial, two round
   trips per node).
6. Not hot: NATS throughput 175 MiB/s; ServeDelta 0.48 ms.

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

## Repeated TUI attach: FS + DB discovery (attempt 26 baseline)

Measured with `bench/startup.py --mode {cold,warm-new,reuse,reuse-teardown}
--fixture {large,small}` (10 runs + 1 discarded warmup, medians). Large
fixture: committed git monorepo, 9,100 files incl. gitignored nested
`node_modules`, 20 project commands, 10 project skills, plus 8 extra
registered projects with 200-session DBs. All timings come from
`startuptrace.Span`/`Count` (every occurrence recorded; labels only).
`cli.` = TUI process, `srv.` = server process. Syscall tracing
(dtruss/ktrace) needs SIP changes, so counts are Go-level (walk entries,
calls).

**Harness caveat.** The previous `--warm` mode (now `warm-new`) reused the
server but created a *new non-git temp project* every run: every "warm"
number paid full workspace creation (DB create + 35 ms goose migrate,
first registry add) yet *skipped* all git-dependent work (checkpoints,
git status, worktree root), because the temp dirs are not repos. It
measured neither re-attaching to a loaded workspace nor relaunching in a
real repo. `reuse` (same path, workspace still loaded within the 2 min
reconnect grace) and `reuse-teardown` (same path, workspace torn down
between launches, server still running: what quitting and relaunching
the TUI does) are the repeat-attach cases.

| ready (ms) | cold | warm-new | reuse | reuse-teardown |
|---|---|---|---|---|
| large | 503 | 257 | 308 | 728 |
| small | 479 | 246 | 240 | 365 |

`main` (exec to `main()`) is ~118 ms in every mode and is not part of
this section.

### (a) Client, before/around connect (every launch)

| call chain | cost | freq |
|---|---|---|
| `cmd.connectToServer` -> `ResolveCwd` | 0.02 ms | 1 |
| `cmd.ensureServer` (socket stat + `restartIfStale` version RPC) | 1.5 ms | 1 |
| `ui.loadCustomCommands` -> `commands.LoadCustomCommands` (WalkDir of 3 command dirs) | 0.1 ms (3 ms w/ 20 cmds) | 1 |
| `ListSkills` RPC (server returns cached manager) | 1-6 ms | 1 |

No client-side project walk, git detection, or config discovery runs on
launch: the TUI gets config from the server. The `@` file completion walk
(`completions.loadFiles` -> `fsext.ListDirectory`) is lazy (first `@`).

### (b) Server, on every connect even when the workspace is loaded (`reuse`)

| call chain | large | small | freq/connect |
|---|---|---|---|
| `backend.CreateWorkspace` -> `resolveWorkspaceKey` -> `config.ProjectRoot` -> `worktreeRoot` | 0.3 ms (cache hit); 23 ms `git rev-parse` fork when the 5 s TTL expired | same | 1 |
| `adoptExistingWorkspace` -> `reloadWorkspaceConfig` -> `ConfigStaleness` (stats) | 0.04 ms | 0.04 ms | 1 |
| `InitAgent` RPC -> `App.InitCoderAgent` -> `agent.NewCoordinator` (full coordinator rebuild) | 0.8 ms sync | 0.8 ms | 1 |
| `agent/update` RPC -> coordinator rebuild | 5-9 ms | 9 ms | 1 |
| -> async `coordinator.buildAgent` readiness -> `prompt.Build` -> `promptData` | 264 ms each, 1,851 ms total | 132 ms each, 923 ms total | **7** |
| -> `getGitStatus` (3 `git` execs via shell: branch, `status --short`, `log -n 3`) | 254 ms each, 1,780 ms total | 127 ms each, 888 ms total | **7** |
| -> `skills.Discover` (fastwalk of skill dirs, Follow) + builtin parse | 9 ms each, 63 ms total, 147 entries walked each | 2 ms each | **7** |
| -> `processContextPath` (16 context-file stats) | 0.2 ms each | 0.1 ms | 7 |
| `ListWorkspaceOverviews` -> `registry.List` + `peekCache` over 8 DBs | 6.4 ms in-handler, 39 ms HTTP (CPU contention with the git forks above) | 4.6 / 5 ms | 1 |

### (c) Server, once per workspace build (fresh path, and *again* after every teardown)

| call chain | large repo (`reuse-teardown`) | non-git temp (`warm-new`) |
|---|---|---|
| `buildWorkspace` total | 298 ms | 58 ms |
| `config.Init` -> `Load` (`lookupConfigs` 0.3, `loadFromConfigPaths` 0.4, `configureProviders` 3) | 7 ms | 12 ms |
| `db.Connect` (open + ping + `schemaCurrent`; migrate only if new) | 3.9 ms | 38 ms (35 ms goose migrate) |
| `skills.DiscoverFromConfig` | 2 ms | 0.7 ms |
| `app.New` -> `checkpoint.NewService` -> `InitRepoAt` -> `gitignore.ReadPatterns` (go-git walk of the whole work tree reading every `.gitignore`) | **273 ms** | 0 (not a repo) |
| `app.New` -> `InitCoderAgent` (then the client calls `InitAgent` again: 2 builds) | 2.4 ms x2 | 1.9 ms x2 |
| `prompt.Build` (async) | 12 calls, 5,345 ms total | 12 calls, 27 ms |
| `wireSwarmBackend` | 5 ms | 3 ms (93 ms on a cold server) |
| `registry.Add` | 1 ms | 0.3 ms |

Once per server process only: `enableCluster` (no-op without cluster
config, not measurable here) and registry store construction.

A non-persistent auto-spawned server exits when its last workspace tears
down (2 min after the last TUI quits), so a later launch pays `cold`
(+~250 ms) on top of (c).

### Ranked caching candidates

1. **`prompt.Build` git status, 7-12x per connect** (1.8-5.3 s CPU
   per attach on the large repo, 0.9-2.4 s on an empty repo). Build the
   system prompt once per coordinator generation, or at least cache
   `getGitStatus` per working dir with a short TTL / invalidate on
   `.git/index` + `HEAD` mtime. Also avoid the double coordinator rebuild
   (`app.New` InitCoderAgent + client `InitAgent` + `agent/update`).
   Invalidation: git index/HEAD change, config change (provider/model,
   context paths, skills paths), date rollover.
2. **`checkpoint.InitRepoAt` gitignore walk, 273 ms on the critical path
   of every rebuild.** Cache the matcher in the backend keyed by project
   root so it survives workspace teardown, or load patterns lazily on the
   first snapshot. Invalidation: any `.gitignore`/`.git/info/exclude`
   change (mtime of the files found by the previous walk; new
   directories can add new `.gitignore`s, so lazily re-walk at snapshot
   time).
3. **Skills discovery 7-13x per connect** (fastwalk with Follow). Reuse
   the workspace `skills.Manager` result in `promptData` instead of
   rediscovering. Invalidation: existing skills watcher / config
   skills_paths change.
4. **Workspace teardown itself**: keeping a torn-down workspace's config,
   ignore matcher and DB handle warm for a while (or not tearing down
   the last workspace on a persistent server) turns `reuse-teardown`
   (728 ms) into `reuse` (308 ms).
5. `worktreeRoot` 5 s TTL: a 23 ms `git rev-parse` fork whenever a launch
   comes >5 s after the previous lookup. Could key on `.git` HEAD/
   commondir mtime instead of time. Invalidation: `git init`, worktree
   add/remove.

Not hot: config lookup/parse (<1 ms), DB open of an existing DB (4 ms),
registry list (0.4 ms), overview peeks of 8x200-session DBs (3 ms),
client-side command loading (0.1-3 ms).

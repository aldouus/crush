# Performance harness

Reproducible benchmarks for Crush's user-facing hot paths. Nothing here
needs network access, LLM providers, the daemon, or a cluster; the CLI runs
with `HOME`/`XDG_*`/`CRUSH_GLOBAL_*` pointed at a temp dir.

## Usage

```sh
bench/run.sh baseline          # full run (count=10, 1s benchtime, 30 hyperfine runs)
bench/run.sh attempt-01
bench/run.sh smoke -quick      # fast sanity check (count=1, 100ms)
bench/compare.sh baseline attempt-01
```

`run.sh` builds `crush` to a temp path, runs hyperfine
(`--warmup 3 --runs 30`) on `crush --version`, `crush --help` and
`crush dirs` (resolves config paths), then runs the Go benchmarks with
`GOMAXPROCS=4` and `-benchmem -count=10`. Raw output goes to
`bench/out/<label>/` (gitignored):

| File             | Contents                                       |
| ---------------- | ---------------------------------------------- |
| `env.txt`        | `uptime` load averages, go version, git HEAD   |
| `hyperfine.json` | hyperfine export                               |
| `bench.txt`      | raw `go test -bench` output for benchstat      |
| `*.pprof`        | CPU/alloc profiles (captured manually)         |

`compare.sh` runs benchstat (`~/go/bin/benchstat`) between two labels and
prints hyperfine median ± stddev deltas. Check `env.txt` load averages
before trusting small deltas; rerun if the machine was busy.

Environment overrides: `BENCH_COUNT`, `BENCH_TIME`, `HYPERFINE`,
`BENCHSTAT`, `GOFLAGS`, `GOCACHE`.

## Benchmarks

| Benchmark                      | File                                          | Hot path                                                      |
| ------------------------------ | --------------------------------------------- | ------------------------------------------------------------- |
| `BenchmarkUIView`              | `internal/ui/model/view_bench_test.go`        | Uncached 200x60 frame: Draw + `canvas.Render()` flatten/trim |
| `BenchmarkUIDraw`              | `internal/ui/model/view_bench_test.go`        | Draw only, reused screen buffer                               |
| `BenchmarkUIScrollFrame`       | `internal/ui/model/view_bench_test.go`        | Frame after a one-line scroll                                 |
| `BenchmarkUISessionLoad`       | `internal/ui/model/view_bench_test.go`        | Messages to chat items + first frame (500/2000 msgs)         |
| `BenchmarkMarkdownRender`      | `internal/ui/common/render_bench_test.go`     | glamour render of an assistant reply                          |
| `BenchmarkDiffRender`          | `internal/ui/common/render_bench_test.go`     | Highlighted unified/split diff of a 400 line file             |
| `BenchmarkMessageList`         | `internal/message/list_bench_test.go`         | SQLite load + decode of a session (500/2000 msgs)             |
| `BenchmarkLoadFromConfigPaths` | `internal/config/load_bench_test.go`          | Config load and merge                                         |
| `BenchmarkWireFullMessage`     | `internal/server/wire_bench_test.go`          | Server wire encoding of full messages                         |
| `BenchmarkWireDeltas`          | `internal/server/wire_bench_test.go`          | Server wire encoding of streaming deltas                      |
| `BenchmarkBrokerPublish`       | `internal/pubsub/broker_bench_test.go`        | pubsub fan-out to 4 subscribers                               |

Synthetic sessions come from `internal/benchdata`, which deterministically
generates user prompts, markdown replies with code blocks/tables, bash tool
calls and tool results.

## Profiles

```sh
GOMAXPROCS=4 go test -run '^$' -bench '^BenchmarkDiffRender$' -benchtime=3s \
  -cpuprofile bench/out/baseline/diff.cpu.pprof \
  -memprofile bench/out/baseline/diff.mem.pprof ./internal/ui/common
go tool pprof -top -cum bench/out/baseline/diff.cpu.pprof | head -25
```

See `HOTSPOTS.md` for the ranked optimization candidates.

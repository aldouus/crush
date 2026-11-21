# Hill-Climb Results

| # | Title | Target | Before | After | Delta | Verdict |
|---|-------|--------|--------|-------|-------|---------|
| 01 | Lazy gh/gh-stack probes | CLI cold start (`--version`) | 204.9 ms | 112.3 ms | -45% | KEPT |
| 02 | Lazy chroma lexer registry (investigation) | CLI cold start (`--version`) | 111.1 ms | n/a | n/a | REVERTED |

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

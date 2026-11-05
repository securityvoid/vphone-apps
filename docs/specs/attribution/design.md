# design.md — docs/specs/attribution/
> vphone-apps

## Architecture Overview
Attribution is three artifacts: `LICENSE` (vphone-apps's own MIT licence), `NOTICE` (license texts, generated then committed), and a Go `embed` of `NOTICE` served by `--licenses`. A test keeps `NOTICE` complete and ordered (the `vphone-apps` block precedes the direct upstreams).

**Stack**: Go `embed`; license collection by a script run by the developer. **Deployment**: ships inside the binary.

## System Diagram
```
go list -deps ─▶ scripts/gen-notice.sh ─▶ NOTICE ─┬─▶ internal/notice (go:embed) ─▶ --licenses
                                                   └─▶ test: every module present
```

## Data Models

### NOTICE layout
| Part | Content |
|---|---|
| 1. Header | assembly paragraph; sentence that vphone-apps itself is MIT; inspiration sentence (REQ-ATTR-004); fork sentence (PR #28) |
| 2. `vphone-apps` block | the `LICENSE` text |
| 3. Direct upstreams | ipatool, unfairplay (with its own `NOTICE` text, DarkPhoenix Apache-2.0, macOSFairFree MIT), vphone-cli: copyright line, repository URL, full license text |
| 4. Dependencies | one block per module linked into the binary (the modules of `go list -deps -json ./cmd/vphone-apps`, main module excluded) other than the above: module path, version, license name, license text taken from the module's license file in the module cache |

## API / Interface Design

| Surface | Behaviour | REQ |
|---|---|---|
| `vphone-apps --licenses` | writes the embedded `NOTICE` to stdout, exit 0 | REQ-ATTR-002 |
| `vphone-apps --help` | top-level usage only, last line `Built on ipatool, unfairplay and vphone-cli — see --licenses` | REQ-ATTR-002 |

## Reuse
| Concern | Existing symbol (path) | Used by tasks |
|---|---|---|
| License files | each upstream's `LICENSE`, `LICENSE-DARKPHOENIX.txt`, `LICENSE-MACOSFAIRFREE.txt`, `NOTICE` | TASK-ATTR-001 |
| Dispatcher | `cmd/vphone-apps/main.go` (pipeline TASK-PIPE-001) | TASK-ATTR-002 |

## File Structure
```
LICENSE
NOTICE
go.mod                       (replace directive removed by TASK-ATTR-007)
internal/repo/repo_test.go  (no vendored upstream source)
scripts/gen-notice.sh
scripts/licenses/vphone-cli.LICENSE   (committed copy; vphone-cli is not a Go dependency)
internal/store/store.go      (comment citing ipatool cmd/common.go)
internal/notice/             text.go (setup TASK-SETUP-003, 014), notice.go, notice_test.go, NOTICE (copy written by gen-notice.sh; `go:embed ../../NOTICE` is not allowed)
```

## Security Design
Not applicable: static text.

## Decisions (locked)

| Decision | Value | Rationale |
|---|---|---|
| NOTICE generation | `scripts/gen-notice.sh` (bash + `go list -deps -json ./cmd/vphone-apps`, which excludes test-only and unused graph modules), output committed. For each module other than the main module (`Main: true`; the `replace` target counts as the unfairplay entry) it reads, in the module directory (`Dir`), the first existing file matching `LICENSE*`, `LICENCE*` or `COPYING*` (case-insensitive); the license name is the first line of that file that matches a known title (`MIT License`, `Apache License`, `BSD`, `ISC`, `Mozilla Public License`) else the file's first line; a module with no such file makes the script exit 1 naming the module. The script uses `set -euo pipefail`, writes to a temp file moved into both locations only after full success, and sorts modules by path | reproducible, reviewable diff |
| Embed location | `internal/notice/NOTICE`, a copy written by the script (Go `embed` cannot reach parent directories) | satisfies `embed` rules; test asserts the copy equals the root `NOTICE` |
| vphone-apps licence | MIT, root `LICENSE`; `gen-notice.sh` states it in the header and emits a `vphone-apps` block with the `LICENSE` text before the direct upstreams (REQ-ATTR-003) | `--licenses` shows it |
| Inspiration credit | a header paragraph in NOTICE (REQ-ATTR-004) | credit without implying a dependency |
| Fork note | comment above `replace` in `go.mod`; one header sentence in NOTICE (REQ-ATTR-005) | explains the fork reference |
| Upstream facts | per the index "Upstream projects" row | one owner |
| Copied code | none vendored; `store.New` replicates ipatool's wiring and cites `cmd/common.go` in a comment | NFR-ATTR-001 |

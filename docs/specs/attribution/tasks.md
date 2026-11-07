# tasks.md — docs/specs/attribution/
> vphone-apps

## Legend
- [ ] Not started
- [~] In progress
- [x] Complete
- [!] Blocked — reason noted inline

---

*Depends on*: **DEP-ATTR-001** — pipeline TASK-PIPE-001 (module and dispatcher), TASK-PIPE-002 (`internal/store`) and setup TASK-SETUP-003 (`internal/notice` package) exist.

## Phase 1: NOTICE
*Goal*: a complete, generated, tested NOTICE.

- [x] **TASK-ATTR-001** [REQ-ATTR-001]: Write `scripts/gen-notice.sh` that emits `NOTICE` per the design layout (header, direct upstreams with the three unfairplay license files and its NOTICE text copied from the module cache, then every other module from `go list -deps -json ./cmd/vphone-apps` (entries with `Module` set, main module skipped) with its license file text) and copies it to `internal/notice/NOTICE`; the unfairplay `LICENSE`, `LICENSE-DARKPHOENIX.txt`, `LICENSE-MACOSFAIRFREE.txt` and `NOTICE` are read from the unfairplay module directory (module github.com/DrWhax/unfairplay, resolved by `go list -m -json`), and the vphone-cli license is read from the committed file `scripts/licenses/vphone-cli.LICENSE` (copied from the `LICENSE` file at the tip of github.com/Lakr233/vphone-cli `main`, commit hash noted in the file's first line), so the script needs no network; add `internal/notice/notice_test.go` asserting every module path from the linked-module list appears in `NOTICE` (the main module is skipped), the strings `Apache License`, `Copyright (c) 2026 Josh Daneshrad` and `Copyright (c) 2021 Majd Alfhaily` appear, and one distinctive line read from each of the unfairplay `LICENSE`, `LICENSE-DARKPHOENIX.txt`, `LICENSE-MACOSFAIRFREE.txt` (module cache) and `scripts/licenses/vphone-cli.LICENSE` appears in `NOTICE`, and the two copies are identical; the test runs the linked-module list with `GOFLAGS=-mod=readonly GOPROXY=off` and fails (never skips) on a command error or an empty list.
  - _Refs rationale_: NOTICE completeness test.
  - _Reuse_: upstream license files from the module cache.
  - _Output_: `scripts/gen-notice.sh`, `NOTICE`, `internal/notice/NOTICE`, `notice_test.go`.
  - _Verify_: `bash scripts/gen-notice.sh && go test ./internal/notice`; deleting one module block from `NOTICE` makes the test fail; two consecutive runs produce identical output; a run with `GOPROXY=off` and an emptied module cache exits non-zero and leaves both NOTICE files unchanged.

## Phase 2: Binary
*Goal*: the credit travels with the binary.

- [x] **TASK-ATTR-002** [REQ-ATTR-002]: Add `internal/notice/notice.go` with `//go:embed NOTICE` exposing `Text`, wire `--licenses` in `main.go` (print `Text`, exit 0) and append the `Built on ...` line as the last line of the top-level usage block only (not per-command blocks).
  - _Refs rationale_: `--licenses` and help line.
  - _Reuse_: dispatcher in `main.go`.
  - _Output_: `internal/notice/notice.go`, `main.go` changes.
  - _Verify_: `go test ./cmd/vphone-apps -run Licenses`: stdout of `--licenses` equals the repository `NOTICE`; the last line of top-level `--help` equals the required line and per-command blocks lack it; exit codes are 0 for `--licenses` and `--help` and 2 with a usage message for `bogus`.
- [x] **TASK-ATTR-003** [NFR-ATTR-001]: Add a code comment in `store.New` citing ipatool `cmd/common.go` as the source of the wiring, and a test in `internal/repo/repo_test.go` that fails if a `.go` file exists outside `cmd/` and `internal/` (no vendored upstream source).
  - _Refs rationale_: no uncredited copied code.
  - _Reuse_: none.
  - _Output_: comment in `internal/store/store.go`; `internal/repo/repo_test.go`.
  - _Verify_: `go test ./internal/repo -run NoVendored` passes; adding a `third_party/x.go` makes it fail.

## Phase 3: Licence and credit notes
*Goal*: the repository states its own licence, the inspiration credit.

- [x] **TASK-ATTR-004** [REQ-ATTR-003]: Add `LICENSE` (MIT, Copyright (c) 2026 Doug Logan); `gen-notice.sh` header sentence and `vphone-apps` block with the `LICENSE` text; regenerate both NOTICE copies.
  - _Reuse_: `scripts/gen-notice.sh` (TASK-ATTR-001), `scripts/licenses/vphone-cli.LICENSE` pattern.
  - _Output_: `LICENSE`, `scripts/gen-notice.sh`, `NOTICE`, `internal/notice/NOTICE`, `internal/notice/notice_test.go`.
  - _Verify_: `bash scripts/gen-notice.sh && go test ./internal/notice` (tests assert the `LICENSE` copyright line and the NOTICE sentence, and that the `vphone-apps` block precedes the direct upstreams).
- [x] **TASK-ATTR-005** [REQ-ATTR-004]: After TASK-ATTR-004, add the inspiration sentence (REQ-ATTR-004 wording) to the NOTICE header via `gen-notice.sh`.
  - _Reuse_: `scripts/gen-notice.sh` header heredoc (TASK-ATTR-004).
  - _Output_: `scripts/gen-notice.sh`, `NOTICE` copies, `notice_test.go`.
  - _Verify_: `go test ./internal/notice` asserts the REQ-ATTR-004 sentence is in `NOTICE`.
- [x] **TASK-ATTR-007** [REQ-ATTR-005]: `go.mod` requires github.com/DrWhax/unfairplay v0.2.1 with no `replace`; delete the whole fork sentence (the 'built from the fork github.com/securityvoid/unfairplay because …' paragraph) from the `gen-notice.sh` header; in `gen-notice.sh` the unfairplay heading becomes `--- unfairplay (github.com/DrWhax/unfairplay) ---` and the replace-target lookup/handling is removed; run `go mod edit -dropreplace github.com/DrWhax/unfairplay`, delete the `//` comment line above it, `go get github.com/DrWhax/unfairplay@v0.2.1`, `go mod tidy`; regenerate `NOTICE`.
  - _Refs rationale_: credit tracks the dependency.
  - _Reuse_: `scripts/gen-notice.sh`.
  - _Output_: `go.mod`, `scripts/gen-notice.sh`, `NOTICE` copies, `README.md` (unfairplay entry drops the PR/fork sentence), `internal/notice/notice_test.go` (delete the `pull/28` and go.mod-comment assertions; assert `go.mod` has no unfairplay `replace`).
  - _Verify_: `go list -m github.com/DrWhax/unfairplay` prints v0.2.1, `grep -ci replace go.mod` is 0, `grep -c pull/28 NOTICE internal/notice/NOTICE scripts/gen-notice.sh internal/notice/notice_test.go` is 0 for each file, `grep -c securityvoid/unfairplay NOTICE internal/notice/NOTICE README.md` is 0 for each, and `go build ./...` and `go test ./internal/notice` pass.

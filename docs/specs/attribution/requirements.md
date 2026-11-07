# requirements.md — docs/specs/attribution/
> vphone-apps

## Overview
`vphone-apps` is an assembly of other people's open-source work. This set makes the credit impossible to miss and legally complete: a NOTICE with every license text, and a `--licenses` command. An upstream author who reads this repository should feel credited and glad they open-sourced, not copied.

## Operator ask
- 2026-11-05: "In the requirements we must also give proper attribution and credit to all the tools we're using."
- 2026-11-05: "We want people to read our repo and not go 'Wow, this guy ripped off my work', but instead, 'I'm so glad I open sourced, he gives me credit, and assembled it in another cool open source way that others can use'."

## Actors
- **Upstream author**: wrote ipatool, unfairplay, or vphone-cli/vphoned.
- **Reader**: a visitor to the repository or a user of the binary.

## Dependencies
- **DEP-ATTR-001**: relies on pipeline REQ-PIPE-012 (dispatcher and exit codes).

## Functional Requirements


### NOTICE and binary
- **REQ-ATTR-001**: A `NOTICE` file shall list every upstream project and every module linked into the binary (the modules of `go list -deps ./cmd/vphone-apps`, other than the main module, which is covered by the `vphone-apps` block, REQ-ATTR-003) with its copyright holder and license, and shall include the full text of each license required for redistribution: ipatool MIT, unfairplay MIT, DarkPhoenix Apache-2.0, macOSFairFree MIT, vphone-cli MIT, and the licenses of the transitive modules.
  - _Acceptance_: a test fails when any module linked into the binary is missing from `NOTICE`, or when the three unfairplay license files are not reproduced in it.
- **REQ-ATTR-002**: The binary shall embed `NOTICE`, print it for `vphone-apps --licenses`, and end the top-level `--help` block (only that block, not per-command blocks) with the line `Built on ipatool, unfairplay and vphone-cli — see --licenses`.
  - _Acceptance_: `vphone-apps --licenses` output equals the repository `NOTICE` byte for byte; the last line of `vphone-apps --help` is the line above.

### Licence and credit notes
- **REQ-ATTR-003**: The repository shall carry a `LICENSE` file (MIT, `Copyright (c) 2026 Doug Logan`); the NOTICE header shall state that vphone-apps itself is MIT, and a `vphone-apps` block shall reproduce the `LICENSE` text before the direct upstreams, so `--licenses` shows it.
  - _Acceptance_: `LICENSE` exists with that copyright line; `NOTICE` contains the `vphone-apps itself is released under the MIT License` sentence, and a `vphone-apps` block with `Copyright (c) 2026 Doug Logan` that precedes the `Direct upstreams` block.
- **REQ-ATTR-004**: The NOTICE header shall contain the sentence `Inspired by the darkwirelabs.com article at https://www.darkwirelabs.com/articles/from-app-store-to-virtual-iphone-with-vphone/; it is not a dependency.`
  - _Acceptance_: `NOTICE` contains that exact sentence.
- **REQ-ATTR-005**: `go.mod` shall have no `replace` for github.com/DrWhax/unfairplay, and the NOTICE shall contain no `pull/28` reference.
  - _Acceptance_: `grep -ci replace go.mod` is 0; `grep -c pull/28 NOTICE internal/notice/NOTICE` is 0 for both.

## Non-Functional Requirements
- **NFR-ATTR-001**: No source file copied or adapted from an upstream project shall be present without its original copyright header and an attribution comment naming the source.
  - _Measurement_: `go test ./internal/repo` finds no `.go` file outside `cmd/` and `internal/`; the design records the one place where ipatool's wiring is replicated (`store.New`) with a comment citing `cmd/common.go` in ipatool.

## Out of Scope
- Legal review of the licenses beyond reproducing them.

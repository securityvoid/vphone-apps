# tasks.md — docs/specs/release/
> vphone-apps

## Legend
- [ ] Not started
- [~] In progress
- [x] Complete
- [!] Blocked — reason noted inline

---

*Depends on*: **DEP-REL-001** — `LICENSE` and `NOTICE` exist (attribution set).

## Phase 1: Release automation
*Goal*: a tag publishes the release and the tap cask.

- [x] **TASK-REL-001** [REQ-REL-001, REQ-REL-002, REQ-REL-003]: Write `.goreleaser.yaml` per the design table (`project_name: vphone-apps`, `release.replace_existing_artifacts: true`, single `darwin/arm64` build with cgo, tar.gz archive with `LICENSE`, `NOTICE`, `README.md`, `checksums.txt`, GitHub changelog, `homebrew_casks` entry for `securityvoid/homebrew-tap` with the quarantine-clearing `install_steps`).
  - _Refs rationale_: release assets, tap cask.
  - _Reuse_: `LICENSE`, `NOTICE`, `go.mod`.
  - _Output_: `.goreleaser.yaml`.
  - _Verify_: `.goreleaser.yaml` sets `release.replace_existing_artifacts: true` and `changelog.use: github`; `go run github.com/goreleaser/goreleaser/v2@v2.20.0 check` passes with no deprecation; `HOMEBREW_TAP_TOKEN=x go run github.com/goreleaser/goreleaser/v2@v2.20.0 release --snapshot --clean --skip=publish` writes `dist/vphone-apps_*_darwin_arm64.tar.gz` and `checksums.txt`, `tar -tzf` lists the binary, `LICENSE`, `NOTICE` and `README.md`, and `dist/homebrew/Casks/vphone-apps.rb` contains `depends_on :macos`, `depends_on arch: :arm64`, a sha256, `binary "vphone-apps"` and the `xattr` step.
- [x] **TASK-REL-002** [REQ-REL-001, REQ-REL-004]: Write `.github/workflows/release.yml` per the design table.
  - _Refs rationale_: tag trigger, least privilege.
  - _Reuse_: `go.mod` Go version.
  - _Output_: `.github/workflows/release.yml`.
  - _Verify_: the trigger is `push: tags: ['v*']` and `runs-on` is `macos-latest`; `goreleaser-action`'s `version:` is `v2.20.0` (no `latest`, no `~>`); the `go test ./...` step precedes the goreleaser step and neither step has `continue-on-error` or `if:`; the file parses as YAML; `grep -o 'secrets\.[A-Z_]*'` shows only `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN`; `permissions` is the single `contents: write`; `go test ./...` passes locally.

## Phase 2: Docs
*Goal*: users know how to install.

- [x] **TASK-REL-003** [REQ-REL-005]: Add an `Install` H2 immediately before `Build` in `README.md` with the `brew install securityvoid/tap/vphone-apps` command, the Releases archive alternative, and the macOS-on-Apple-Silicon-only statement.
  - _Refs rationale_: install instructions.
  - _Reuse_: `README.md`.
  - _Output_: `README.md`.
  - _Verify_: `grep -n '^## ' README.md` lists `Install` immediately before `Build`; `grep -c 'brew install securityvoid/tap/vphone-apps\|releases\|arm64' README.md` finds all three strings.

## Phase 3: One-time human setup
*Goal*: the first tag can publish. Only the maintainer can do these.

- [ ] **TASK-REL-004** [REQ-REL-003, REQ-REL-004, NFR-REL-001]: Maintainer creates the public repository `securityvoid/homebrew-tap` with an initial commit (a README) on its default branch, creates a fine-grained PAT with contents read/write on that repository only, and stores it as the repository secret `HOMEBREW_TAP_TOKEN` in `securityvoid/vphone-apps`; adds a tag ruleset on `v*` limiting creation to the maintainer; then pushes `v0.1.0`.
  - _Refs rationale_: GoReleaser cannot write to another repository with `GITHUB_TOKEN`.
  - _Reuse_: none.
  - _Output_: the tap repository, the secret, the first release.
  - _Verify_: before pushing the tag, `gh api repos/securityvoid/vphone-apps/rulesets` lists a tag ruleset targeting `refs/tags/v*` with `enforcement: active`, a `creation` rule and maintainer-only bypass; the PAT's repository access lists only `securityvoid/homebrew-tap`; the workflow run for `v0.1.0` is green, the Release shows both assets, and `brew install securityvoid/tap/vphone-apps` installs.

# requirements.md — docs/specs/release/
> vphone-apps

## Overview
Publishing `vphone-apps` as an open-source release: a `v*` git tag builds the binary on free GitHub Actions runners, publishes a GitHub Release, and updates a Homebrew tap so users can `brew install` it. The binary targets macOS on Apple Silicon only, because vphone-cli runs a virtual iPhone on an Apple Silicon Mac and the tool needs `sudo`, the macOS keychain and cgo.

## Operator ask
- 2026-11-06: "I want to create a release of this project in Github. How do I do this for an open source project like this? Does it have free deploys that will run?"
- 2026-11-06: "We want to support every platform that we can. I'd also like to support a tap. I *think* vphone-cli might be limited to a Mac and therefore it has to be mac only."
- 2026-11-06: "Approved. You should include everything that will work."

## Actors
- **Maintainer**: pushes a `v*` tag.
- **User**: installs from the tap or downloads the archive.

## Dependencies
- **DEP-REL-001**: relies on the index "Module path / binary" and attribution REQ-ATTR-001 (`NOTICE`) and REQ-ATTR-003 (`LICENSE`).

## Functional Requirements
- **REQ-REL-001**: Pushing a tag matching `v*` shall run a GitHub Actions workflow on a free macOS arm64 runner that runs `go test ./...`, then builds `vphone-apps` for `darwin/arm64` (cgo enabled) and publishes a GitHub Release for that tag whose assets are one `vphone-apps_<version>_darwin_arm64.tar.gz` and a `checksums.txt`.
  - _Acceptance_: `goreleaser check` passes; `HOMEBREW_TAP_TOKEN=x goreleaser release --snapshot --clean --skip=publish` run from a checkout directory of any name leaves `dist/*.tar.gz` holding exactly that archive and `dist/checksums.txt` as the release assets (the cask under `dist/homebrew/` and GoReleaser's metadata files are not assets); the workflow trigger is `push: tags: ['v*']`, the job runs on `macos-latest`, the `go test ./...` step precedes the goreleaser step, and neither step has `continue-on-error` or `if:`, so a failing test publishes nothing.
- **REQ-REL-002**: The archive shall contain the `vphone-apps` binary, `LICENSE`, `NOTICE` and `README.md`, and the release notes shall be the generated changelog.
  - _Acceptance_: `tar -tzf` of the snapshot archive lists those four files; `.goreleaser.yaml` sets `changelog.use: github`.
- **REQ-REL-003**: The same workflow shall push a Homebrew cask for the release to the repository `securityvoid/homebrew-tap` using the repository secret `HOMEBREW_TAP_TOKEN`, so `brew install securityvoid/tap/vphone-apps` installs it; the cask shall require macOS and `arm64` and shall remove the `com.apple.quarantine` attribute from the installed binary, because the binary is unsigned. A cask is used because GoReleaser deprecates formulas for pre-built binaries (`brews`). If the tap push fails (expired or revoked token, missing tap repository) the run fails visibly after the Release is published; the maintainer fixes the cause and re-runs the same tag, and the re-run replaces the existing assets and updates the cask, ending with one Release and one cask.
  - _Acceptance_: `goreleaser check` reports no deprecations; `.goreleaser.yaml` sets `release.replace_existing_artifacts: true`; the snapshot run writes `dist/homebrew/Casks/vphone-apps.rb` containing `depends_on :macos`, `depends_on arch: :arm64`, the archive URL, its sha256, `binary "vphone-apps"` and a step running `/usr/bin/xattr` with `com.apple.quarantine` on `{{staged_path}}/vphone-apps` (Homebrew's install-time token; Ruby interpolation is rejected in steps).
- **REQ-REL-004**: The workflow shall request only `contents: write` and shall not use any secret other than the built-in `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN`; the repository shall carry a tag ruleset on `v*` that restricts tag creation to the maintainer, so only the maintainer can trigger a job holding the token.
  - _Acceptance_: the workflow file has a single top-level `permissions: contents: write` and references no other `secrets.` name; `gh api repos/securityvoid/vphone-apps/rulesets` lists a tag ruleset targeting `refs/tags/v*` with `enforcement: active`, a `creation` rule and bypass actors limited to the maintainer (one-time setup, TASK-REL-004).
- **REQ-REL-005**: The README shall have an `Install` section giving the `brew install securityvoid/tap/vphone-apps` command and the GitHub Releases archive as the alternative, and shall state that only macOS on Apple Silicon (arm64) is supported.
  - _Acceptance_: the README contains `brew install securityvoid/tap/vphone-apps`, `releases`, `arm64`; the `Install` H2 sits immediately before `Build`.

## Non-Functional Requirements
- **NFR-REL-001**: A release shall need no paid service or Apple Developer account.
  - _Measurement_: the workflow uses only GitHub-hosted runners and the secrets in REQ-REL-004; binaries are neither signed nor notarized.

## Out of Scope
- Linux, Windows and `darwin/amd64` binaries: vphone-cli supports Apple Silicon only, so they could not run the tool.
- Code signing and notarization.
- Creating the tap repository, the token and the secret (one-time human tasks, TASK-REL-004).

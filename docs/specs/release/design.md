# design.md — docs/specs/release/
> vphone-apps

## Architecture Overview
Two committed files drive everything: `.goreleaser.yaml` (what to build and publish) and `.github/workflows/release.yml` (when and where). GoReleaser builds the binary, archives it, writes checksums and a GitHub Release, and writes the Homebrew cask into the tap repository.

**Stack**: GoReleaser v2, GitHub Actions. **Deployment**: none; the artifacts are the release.

## System Diagram
```
git tag v* ─▶ release.yml (macos-latest, arm64)
                 ├─ go test ./...
                 └─ goreleaser release ─▶ GitHub Release (tar.gz + checksums.txt)
                                       └▶ securityvoid/homebrew-tap Casks/vphone-apps.rb
```

## Data Models

### `.goreleaser.yaml`
| Key | Value |
|---|---|
| `version` | `2` |
| `project_name` | `vphone-apps` (pins the archive name regardless of checkout directory) |
| `release` | `replace_existing_artifacts: true` (a re-run of the same tag converges) |
| `builds[0]` | `main: ./cmd/vphone-apps`, `binary: vphone-apps`, `goos: [darwin]`, `goarch: [arm64]`, `env: [CGO_ENABLED=1]`, `flags: [-trimpath]`, `ldflags: [-s -w]` |
| `archives[0]` | `formats: [tar.gz]`, `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`, `files: [LICENSE, NOTICE, README.md]` |
| `checksum` | `name_template: checksums.txt` |
| `changelog` | `use: github` |
| `homebrew_casks[0]` | `name: vphone-apps`, `repository: {owner: securityvoid, name: homebrew-tap, token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"}`, `homepage: https://github.com/securityvoid/vphone-apps`, `description: Install App Store apps on a vphone virtual iPhone in one command`, `binaries: [vphone-apps]`, `hooks.post.install_steps` running `/usr/bin/xattr -dr com.apple.quarantine {{ .StagedPath }}/vphone-apps`; GoReleaser writes `depends_on :macos` and `depends_on arch: :arm64` itself from the build |

### `.github/workflows/release.yml`
| Part | Value |
|---|---|
| trigger | `push: tags: ['v*']` |
| `permissions` | `contents: write` |
| runner | `macos-latest` (arm64) |
| steps | `actions/checkout` (`fetch-depth: 0`), `actions/setup-go` (`go-version-file: go.mod`), `go test ./...`, `goreleaser/goreleaser-action` (`version: v2.20.0`, the same release TASK-REL-001's verify runs, never `latest` or a range; `args: release --clean`) with env `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN` |

## API / Interface Design

| Surface | Behaviour | REQ |
|---|---|---|
| tag `vX.Y.Z` | triggers the release | REQ-REL-001 |
| `brew install securityvoid/tap/vphone-apps` | installs the cask | REQ-REL-003, REQ-REL-005 |
| Releases page | `vphone-apps_X.Y.Z_darwin_arm64.tar.gz`, `checksums.txt` | REQ-REL-001, REQ-REL-002 |

## Reuse
| Concern | Existing symbol (path) | Used by tasks |
|---|---|---|
| License texts | `LICENSE`, `NOTICE` at the repository root | TASK-REL-001 |
| Module path / Go version | `go.mod` | TASK-REL-001, TASK-REL-002 |
| README structure | `README.md` | TASK-REL-003 |

## File Structure
```
.goreleaser.yaml
.github/workflows/release.yml
README.md                      (Install section)
```

## Security Design
Only `GITHUB_TOKEN` (scoped by `permissions: contents: write`) and `HOMEBREW_TAP_TOKEN` (a fine-grained PAT limited to `securityvoid/homebrew-tap`, contents write) are used. No credentials enter the binary. GoReleaser is pinned to an exact version because the job holds the token. A tag ruleset on `v*` limits who can push release tags to the maintainer.

## Decisions (locked)
| Decision | Choice | Rationale |
|---|---|---|
| Platforms | `darwin/arm64` only | vphone-cli runs on Apple Silicon Macs only; the tool needs sudo, keychain and cgo |
| Build host | `macos-latest` native, not cross-compiled | cgo needs the Apple toolchain; free for public repositories |
| Tool | GoReleaser | one file for archive, checksums, release and tap |
| Signing | none | notarization needs a paid Apple Developer ID; the cask clears the quarantine attribute so Homebrew installs avoid Gatekeeper prompts |
| Tap | `securityvoid/homebrew-tap`, a cask not a formula | `brew install securityvoid/tap/vphone-apps`; `brews` is deprecated in GoReleaser v2 and fails `goreleaser check` |

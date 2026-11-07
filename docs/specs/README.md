# vphone-apps — system spec index

`vphone-apps` is a single Go CLI (one component, one binary) that searches the App Store, downloads an app, removes its FairPlay encryption, and installs it on a running vphone VM. It is an assembly of existing open-source projects, credited as such.

| Leaf set | Scope |
|---|---|
| [pipeline/](pipeline/) | `login`, `search`, `install`: App Store to running VM |
| [setup/](setup/) | `setup` (one-time unfairplay enrollment, also run automatically after `login` using Organic Maps as the throwaway enrollment app) and `doctor` (preflight) |
| [attribution/](attribution/) | Credit to the upstream projects: LICENSE, NOTICE (inspiration credit), `--licenses` |
| [release/](release/) | Tag-driven GitHub Release and Homebrew tap (macOS arm64 only) |

## Shared definitions (owned here, cited by the leaves)

- **Module path / binary**: module `github.com/securityvoid/vphone-apps`, binary `vphone-apps`, Go 1.26.5 (the version ipatool's `go.mod` requires); ipatool is required at commit `d9c0247` (`go get github.com/majd/ipatool/v2@d9c0247`), the revision whose `pkg/appstore` API these specs were written against.
- **Upstream projects**: ipatool (`github.com/majd/ipatool/v2`), unfairplay (`github.com/DrWhax/unfairplay`, upstream release v0.2.1), vphone-cli and its guest daemon vphoned (`github.com/Lakr233/vphone-cli`, used through its documented `vphone.sock` protocol, no code imported).
- **unfairplay pin**: `go.mod` requires `github.com/DrWhax/unfairplay v0.2.1` (the PR #28 merge commit `c1753f4`); there is no `replace`.
- **Cache directory**: `~/Library/Caches/vphone-apps/`, mode `0700`; files in it `0600`.
- **Model directory**: `<UserConfigDir of the invoking user>/unfairplay/models` (macOS: `~/Library/Application Support/unfairplay/models`), the same directory the `unfairplay` CLI uses, so models are shared. The invoking user is the current user; only when `os.Geteuid()==0` and `SUDO_USER` is set is it `SUDO_USER`, resolved with `user.Lookup`, and an unresolvable name is an error with no fallback.
- **VM control socket**: `~/.vphone/machines/<name>/vphone.sock`. One connection carries one request: one JSON object plus `\n`, answered by one JSON line plus `\n`.
- **External programs**: the binary starts exactly two, `/usr/bin/sudo` (setup elevation, only as `<this binary> setup …`) and `/usr/bin/open` (opens the Mac App Store page); everything else is Go code or the `vphone.sock` protocol.
- **Operator**: the human running the tool. **Apple Account credentials** are entered only into `vphone-apps login` and stored only by ipatool's keychain code.

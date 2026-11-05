# requirements.md — docs/specs/pipeline/
> vphone-apps

## Overview
`vphone-apps login`, `search` and `install` take an App Store app from search to a running vphone VM using ipatool and unfairplay as Go modules and the VM's `vphone.sock`. The only external programs it starts are `/usr/bin/sudo` (setup elevation, only to re-run this binary as `setup`) and `/usr/bin/open` (opens one `macappstore://` page during auto-setup).

## Operator ask
- 2026-11-05: "`search APPNAME` and `install APPNAME` that fetch an App Store app and install it on a vphone VM." A separate tool, not a vphone-cli feature.
- 2026-11-05: "DEPEND ON IPATOOL! THAT IS THE WHOLE FREAKING POINT" — ipatool and unfairplay are imported as Go code, so the user installs no ipatool or unfairplay binary.
- 2026-11-05: Install through vphoned (`apps.install` over `vphone.sock`), not `ideviceinstaller`.
- 2026-11-05: Depend on the unfairplay fork until its PR merges, then switch.

## Actors
- **Operator**: runs the CLI on an Apple Silicon Mac.
- **App Store**: reached through ipatool's `pkg/appstore`.
- **VM**: a running vphone guest reached through `vphone.sock`.

## Functional Requirements

### Account and search
- **REQ-PIPE-001**: The CLI shall provide `login`, which prompts for Apple Account email, password (no echo) and, when Apple requires it, the 2FA code, and persists the session through ipatool's keychain, cookie jar and state directory so the ipatool CLI shares the session (one process reads that keychain item at most once and never writes any other keychain item); after a successful sign-in it runs the auto-setup of REQ-SETUP-011 and still exits 0, even when that auto-setup fails or is interrupted (it then prints the failure output of REQ-SETUP-007, e.g. `error: setup: <message>` or `error: setup: interrupted` and `hint: run vphone-apps setup`; scripts use `doctor`, which exits 0 only on `ready`, to detect incomplete setup).
  - _Acceptance_: after `login` succeeds, `AccountInfo()` returns the account; a wrong password prints Apple's error and exits 1; a missing 2FA code (`ErrAuthCodeRequired`) re-prompts once for the code, and a second `ErrAuthCodeRequired` exits 1 with `error: login: two-factor code rejected` after exactly one re-prompt; an interrupt at a prompt restores echo and exits 1; whatever auto-setup does, including an interrupt during it, a successful sign-in exits 0; stdin not a terminal exits 1 with `error: login: a terminal is required` before prompting and before any store call.
- **REQ-PIPE-002**: The CLI shall provide `search <term>` that prints a header row and, for each result, name, bundle id (the `ID` to pass to `install --id`), version and price in aligned columns, most relevant first, at most 25 rows (the limit passed to ipatool `Search`), in the output format of the design, followed by an install hint on stderr.
  - _Acceptance_: a term with results prints the header and one row per result and the stderr hint (design § Output formats) and exits 0; a term with none prints `error: search: no results for "<term>"` and exits 1; with no stored session it exits 1 naming `vphone-apps login`.

### Install
- **REQ-PIPE-003**: `install <term>` shall select the first search result; `install --id <bundleId>` shall select the app with exactly that bundle id; the chosen name, bundle id and version shall be printed before any download as `Installing: …`, followed by the current step with a progress bar (design § Output formats).
  - _Acceptance_: the `Installing:` line precedes the first step; plain mode prints one `[i/n] <step>` line per step in order (5 steps with `--launch`), a terminal renderer redraws a bar in place; result lines stay on stdout; `--id com.example.none` that matches nothing exits 1 with `no app with bundle id`; a term with no results exits 1.
- **REQ-PIPE-004**: `install` shall download the selected app and replicate its sinf into the IPA (ipatool `Download` then `ReplicateSinf`) into the cache directory; with `--purchase` it shall first call ipatool `Purchase`; the `ErrLicenseAlreadyExists` reply counts as success and the download proceeds, and any other `Purchase` error exits 1 as `error: download: <message>`.
  - _Acceptance_: a free app not yet owned fails with a message naming `--purchase` when the flag is absent and succeeds when present; an already-owned app with `--purchase` still downloads once; a downloaded IPA contains `SC_Info/*.sinf`.
- **REQ-PIPE-005**: `install` shall decrypt the IPA with unfairplay `DecryptIPA` using the model directory, stripping account data from the output unless `--keep-account-data` is given.
  - _Acceptance_: the output contains no `SC_Info` members and no Apple ID keys in `iTunesMetadata.plist` by default; with `--keep-account-data` they remain; a missing model exits 1 naming `vphone-apps setup`.
- **REQ-PIPE-006**: `install` shall require `--vm <name>` and shall treat the VM as running only when `~/.vphone/machines/<name>/vphone.sock` accepts a connection.
  - _Acceptance_: a missing machine directory exits 1 with `no such VM`; a socket that refuses connections exits 1 with `VM is not running`; both checks happen before any download; an empty name, `.`, a name containing `/`, `..` or NUL, or a name over 64 bytes exits 1 with `error: vm: invalid VM name`.
- **REQ-PIPE-007**: `install` shall push the decrypted IPA to the guest path `/private/var/tmp/vphone-apps-<random>.ipa` (`<random>` is 16 hex characters from `crypto/rand`; a rand failure exits 1 as `error: push: ...` before any connection) with the socket request `{"t":"push","local":<abs path>,"path":<guest path>}` and shall continue only on `{"ok":true}`.
  - _Acceptance_: an `{"ok":false,"error":E}` reply exits 1 printing `E`; a reply that is not valid JSON, a connection closed before any reply, or no reply within the push deadline of the design exits 1 as `error: push: ...` followed by `hint: staged file may remain at <guest path>`.
- **REQ-PIPE-008**: `install` shall then send `{"t":"rpc","method":"apps.install","params":{"path":<guest path>}}` on a new connection with the `apps.install` read deadline of the design (§ Timeouts), and on `{"ok":true,"result":R}` print `R.bundle_id` and, when `R.upgraded` is true, that the app was upgraded.
  - _Acceptance_: success prints the bundle id; `{"ok":false,...}` exits 1 printing `error` and `guest_error.message`; a deadline expiry exits 1 with `error: install: timed out after 200s; the install may still be running in the guest` followed by `hint: check the guest app list before re-running`; a malformed reply, a closed connection, or `{"ok":true}` without `result.bundle_id` exits 1 as `error: install: ...`. Once `apps.install` has been sent, the guest deletes the staged IPA whether the install succeeds or fails; a run stopped between the push and the install leaves the staged file in the guest.
- **REQ-PIPE-009**: When `apps.install` fails because the bundle id is installed by another installer, the CLI shall print the guest error unchanged followed by the hint to remove the app in the guest first.
  - _Acceptance_: an error containing `already installed and icli did not install it` is followed by a `hint:` line (format in the design).
- **REQ-PIPE-010**: With `--launch`, `install` shall call `apps.launch` with the installed `bundle_id` after a successful install and print the returned `pid`.
  - _Acceptance_: `--launch` after a successful install prints a pid; a launch error, a malformed reply, a closed connection, or `{"ok":true}` without `result.pid` exits 1 as `error: launch: ...` after `installed:` was printed, with no `launched:` line.

### Files
- **REQ-PIPE-011**: Every file `install` creates shall live in a per-run subdirectory (`0700`) of the cache directory, with the modes in the index; on exit — success, failure, SIGINT or SIGTERM — that subdirectory shall be deleted unless `--keep` is given.
  - _Acceptance_: after a normal run the cache directory holds no IPAs; `app.ipa` (encrypted, with the sinf) is deleted as soon as decrypt succeeds, with or without `--keep`; with `--keep` and a fully successful decrypt only `app.decrypted.ipa` remains, in a `kept-*` directory the 24 h sweep never touches, with mode `0600`; on any failure `app.ipa` is deleted even with `--keep` and nothing is kept; the cache directory is `0700` even when it already existed as `0755`, and is refused when it is a symlink; two concurrent runs never delete each other's files; SIGINT during download leaves no IPA behind and exits 1; a `Clean()` failure exits 1 as `error: cleanup: ...`; a run killed by SIGKILL leaves its subdirectory, which `cache.New` removes after 24 h or the operator deletes; a file is written as `<name>.part` and renamed on success; `--keep` keeps the directory (as `kept-*`) and prints `kept: <path>` only when the whole command exits 0, so a push, install or launch failure under `--keep` keeps nothing; a `Keep()` failure exits 1 as `error: cache: ...`.
- **REQ-PIPE-012**: The CLI shall exit 0 only when the requested command completed, 1 for every handled failure, and 2 for a usage error, except `login`, which exits 0 after a successful sign-in whatever auto-setup does (REQ-PIPE-001, REQ-SETUP-011); every failure prints the format of the design (`error: <step>: <message>`, optional `hint:` lines). No step is retried; recovery is re-running the command.
  - _Acceptance_: each failure case above prints one `error:` line per failing step on stderr (a `cleanup` failure adds its own `error: cleanup:` line); a fake store whose `Download` fails once is called exactly once.

## Non-Functional Requirements
- **NFR-PIPE-001**: The tool shall never write Apple Account credentials, tokens or the contents of the keychain to stdout, stderr, logs or any file of its own.
  - _Measurement_: a test runs `login` against a fake store with password `SENTINEL-PW` and greps all output and the cache directory for it: zero hits.
- **NFR-PIPE-002**: The binary shall build with `go build ./cmd/vphone-apps` (cgo on: ipatool's keychain library requires it) on macOS arm64 and shall start no program other than `/usr/bin/sudo` (run by `setup` to elevate, REQ-SETUP-002) and `/usr/bin/open` (auto-setup, REQ-SETUP-008), each only with the argv the design allows.
  - _Measurement_: build succeeds; `grep -rlE '"os/exec"' --include=*.go --exclude=*_test.go . ` lists only `./cmd/vphone-apps/proc.go`, and `grep -rhoE '"/usr/bin/[a-z]+"' --include=*.go --exclude=*_test.go . | sort -u` prints only `"/usr/bin/open"` and `"/usr/bin/sudo"` (the same two commands as TASK-PIPE-012; `_test.go` files are excluded).
- **NFR-PIPE-003**: Unit tests shall run without network, an Apple Account or a VM.
  - _Measurement_: `go test ./...` passes with networking disabled.

## Out of Scope
- Anything beyond search and install of one app per invocation: no batch installs, version pinning, or uninstall command.
- Checking whether an app will run once installed.

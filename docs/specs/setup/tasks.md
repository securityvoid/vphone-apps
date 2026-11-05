# tasks.md — docs/specs/setup/
> vphone-apps

## Legend
- [ ] Not started
- [~] In progress
- [x] Complete
- [!] Blocked — reason noted inline

Literals live in the setup design; "bytes per design § X" = assert exactly that text.

---

*Depends on*: **DEP-SETUP-002** — pipeline TASK-PIPE-001, 002, 003, 004, 006, 007, 008, 012 are done first.

## Phase 1: Setup
*Goal*: `setup` enrolls without the unfairplay binary.

- [x] **TASK-SETUP-001** [REQ-SETUP-002]: Implement `runElevated(ctx, args []string) error` per design § Setup rules and § Exit codes (injectable runner).
  - _Output_: `runElevated`, `errChildReported` in `cmd/vphone-apps/setup.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run Elevate` with a fake runner:
    - Args `[--app, a, --ipa, i]`: runner gets `/usr/bin/sudo`, `-p`, `Mac login password (for sudo): `, self, `setup`, `--app`, `a`, `--ipa`, `i`, `--elevated`.
    - Child exit cases: 0 gives nil, exit 0; 3 gives exit 1; both: `runElevated` prints nothing (the box comes from `setup`, REQ-SETUP-003); 1, 5, signalled (-1) give exit 1 with the `sudo failed` line and hint (bytes per § Failure output).
    - Manual form cases (injected start error, `os.Executable` failure): exit 1, `error: setup: cannot elevate: <err>` then `hint: re-run as root`, no `Run` call.
    - Root (euid 0) never calls the runner; failing `Run` exits 3 with `--elevated` (else 1), one `error: setup:` line.
- [x] **TASK-SETUP-003** [REQ-SETUP-003]: Add `internal/notice/text.go` with the two constants of design § Notice texts (`Prerequisites`, `EnrollHint`) (TASK-ATTR-002 adds `Text`) and `notice.Box` (design § Output lines, unit-tested with exact bytes incl. a word longer than 72).
  - _Output_: `internal/notice/text.go`.
  - _Verify_: `go test ./internal/notice -run Notice`: both constants (`Prerequisites`, `EnrollHint`) byte for byte per design; `SetupDone` is TASK-SETUP-014.
- [x] **TASK-SETUP-002** [REQ-SETUP-001, REQ-SETUP-003, REQ-SETUP-010, NFR-SETUP-002]: Implement `setup` per design § Setup rules: flags (`--interface` default `en0`, `--keep-work`, `--verbose`, internal `--remove-app`, `--elevated`), usage errors (exit 2), path checks before elevation (exit 1), neither `--app` nor `--ipa` calls `autoSetup` (TASK-SETUP-009), `runElevated`. `Prerequisites` (TASK-SETUP-003) once before elevation, only with `--verbose`, never under `--elevated`; the box on success from the invoking process only. Root process only: `setup.Run` with `Progress` to stderr only under `--verbose`, `--remove-app` removal (TASK-SETUP-012), model path on stdout last, only with `--verbose`. `setup --help` prints design § Usage text.
  - _Output_: `cmd/vphone-apps/setup.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run Setup` with an injected `Run`:
    - Exit 2, `error: usage:` message per design § Usage text, neither runner nor `Run` called, cases: only `--app`; only `--ipa`; `--interface` of 17 characters, `en0;id`, `../x` (16 characters accepted); `setup x`; `setup --app a --ipa b.ipa x`; `--remove-app` with another `--app` path, and without `--app`/`--ipa` (remover uncalled).
    - `Prerequisites` (only with `--verbose`) precedes the first progress line and the elevation, once; the `--elevated` run prints none; the non-elevated run ends with the box on success. `Options.ModelDirectory == models.Dir()`; `HardwareInterface == "en0"` by default.
    - Exit 1 printing the error, cases: `Run` error; unresolvable `SUDO_USER` (`error: setup:`); with runner never called: nonexistent `--app`, `--app` a regular file or directory `x`, `--ipa` a directory or `x.zip`.
    - `setup --help`: four usage lines on stdout, exit 0, no `--remove-app`/`--elevated`, no runner call.
    - SIGINT while `Run` blocks on `ctx.Done()`: exit 1, `error: setup: interrupted`, stdout per § Setup rules: stdout.

## Phase 1b: Auto-setup
*Goal*: `login` and flagless `setup` enroll with no manual app or IPA.

- [x] **TASK-SETUP-014** [REQ-SETUP-007, REQ-SETUP-010]: Add the five `Setup*` constants (`SetupIntro` verbose-only) of design § Output lines (`SetupIntro`, `SetupKept`, `SetupNew`, `SetupRemoving`, `SetupDone` = the lawful-use completion text) to `internal/notice/text.go`.
  - _Output_: `internal/notice/text.go`.
  - _Verify_: `go test ./internal/notice -run Notice`: each constant byte for byte per design.
- [x] **TASK-SETUP-010** [REQ-SETUP-008]: In `autosetup.go` implement `ensureApp(ctx, deps, display) (listing, *setupError)`, `readPlistFile(path) (map[string]any, error)` and `verifyOrganicMaps(path) error` per design § Setup rules and § Auto-setup step 2 (`open` via the runner under `openTimeout`).
  - _Output_: those functions and the design § Auto-setup constants.
  - _Verify_: `go test ./cmd/vphone-apps -run EnsureApp` with fakes (error text = `line`):
    - Existing path: nil, no program. Absent path appearing on poll 3: `open` once with the exact URL for the fake `trackId`, 2 s waits, nil.
    - cases (existing or new): symlink, plain file, bundle id `com.other`: `is not Organic Maps` row with hint, nothing later runs.
    - Plist parsing on poll 5: nil after 5 polls, 4 waits; ready at poll 300: nil after 299 waits; never ready or ready at 301: 10m timeout row with click hint after 300 polls, 300 waits.
    - Real `iTunesMetadata.plist` as FIFO, symlink to FIFO or `/dev/zero`, 1 MiB + 1 byte: never ready (exactly 1 MiB: ready); timeout after 300 polls, or `interrupted` on cancel, within 2 s. `ctx` cancelled while waiting: `interrupted`.
    - `open` cases: start error `cannot open the App Store: <err>` and exit 1 with nil error `… exit 1` (one call, zero polls) with the install hint; real runner on a sleeping helper at 50 ms `openTimeout` `… timed out after 50ms` with the click hint, within 2 s, zero polls, helper gone.
    - Lookup cases (other bundle id, price 4.99, `trackId` 0): `unexpected App Store listing for app.organicmaps` with hint, `open`/`Purchase`/`Download` never run; `ErrSessionExpired`: `session expired` with hint; blocking fake at 50 ms `lookupTimeout`: `timed out after 50ms`.
    - `verifyOrganicMaps`, `Info.plist` present as FIFO, symlink to FIFO or `/dev/zero`, 10 MiB, 1 MiB + 1 byte, garbage, or without the key, each next to a valid `Wrapper/iTunesMetadata.plist`: error within 2 s; exactly 1 MiB (valid, padded): passes; no `Info.plist` + valid wrapper plist: passes.
- [x] **TASK-SETUP-011** [REQ-SETUP-009]: In `autosetup.go` implement `readExternalVersion(appPath) (version string, ok bool)` and `fetchIPA(ctx, deps, listing, display) (cache, ipaPath, *setupError)` per design § Auto-setup step 3 (per-run cache, `store.Download(...)` as pipeline TASK-PIPE-004 defines it, no `store` change; deadlines are the store's `PurchaseTimeout`/`DownloadTimeout`; `howett.net/plist`).
  - _Output_: `readExternalVersion`, `fetchIPA` in `cmd/vphone-apps/autosetup.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run 'ExternalVersion|FetchIPA'`:
    - `softwareVersionExternalIdentifier` 123456 (integer, string): `"123456"`, ok.
    - exactly 1 MiB (padded, valid key): `"123456"`, ok.
    - cases giving `""`: 10 MiB and 1 MiB + 1 byte files, `1; x`, `-1`, 21-digit string, missing file, garbage, no key.
    - FIFO, symlink to FIFO or `/dev/zero`: within 2 s, `ok=false`, `""`.
    - Store records `purchase=true` and version; blocking Download and Purchase at 50 ms deadlines each give `timed out after 50ms`; `ErrSessionExpired` gives `session expired` with hint.
    - `cache.New()` failure (symlinked cache dir): `error: cache: <path> is a symlink or not owned by you`, store never called.
- [x] **TASK-SETUP-012** [REQ-SETUP-010]: Implement `--remove-app` in `setup.go` per design § Setup rules (root removal) with injectable `removeApp(path)`.
  - _Output_: removal in `cmd/vphone-apps/setup.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run RemoveApp` with fake remover and injected `Run`:
    - Remover called once after success and after a `Run` error (exit 1, its `error: setup:` line); not called without the flag.
    - Failing remover: `error: cleanup:` line (no hint), stdout per § Setup rules: stdout, exit 1 (3 under `--elevated`) even after successful `Run`; model path only when both succeed.
    - Real `removeApp` on a temp path leaves it untouched with `error: cleanup: refusing to remove` (`cannot remove` if missing), cases: symlink to a directory (target intact), bundle id `com.other`, plain file, missing path, `Info.plist` as symlink to FIFO or `/dev/zero`, FIFO, 10 MiB, 1 MiB + 1 byte, garbage; each next to a valid wrapper plist, within 2 s. Bundle id `app.organicmaps` (also exactly 1 MiB, or no `Info.plist` + valid wrapper plist) is removed.
    - SIGINT during a blocking `Run` and during a slow remover: app removed, `error: setup: interrupted`, stdout per § Setup rules: stdout, exit 3 under `--elevated`.
- [x] **TASK-SETUP-009** [REQ-SETUP-007]: Implement `autoSetup(ctx, deps, iface, keepWork, verbose bool) *setupError` per design § Auto-setup (wires `newStepDisplay` with n = 3 or 4, closed before the child starts; intro and `Prerequisites` lines only when `verbose`; a child-reported failure sets `childFailed` for `notice.EnrollHint` (design § Failure output step 4); uses TASK-SETUP-010, 011, 001, 014); wire `setup` with neither flag to it after the session check.
  - _Output_: `autoSetup` in `cmd/vphone-apps/autosetup.go`.
  - _Verify_: TASK-SETUP-015 (`-run AutoSetup`).
- [x] **TASK-SETUP-013** [REQ-SETUP-011, REQ-PIPE-001]: In `login.go`, after a successful login call `models.Dir()` then `models.Exists(dir)` (an error from either is a setup failure); true returns; else run `autoSetup` under `signal.NotifyContext(SIGINT, SIGTERM)` and print per § Failure output with the `login`-only hints; return 0 in every case after sign-in. `login --help` prints design § Usage text (`Login` not called); `login --bogus` and `login x` exit 2 (§ Usage text).
  - _Output_: hook in `cmd/vphone-apps/login.go`.
  - _Verify_: TASK-SETUP-015 (`-run LoginSetup`).
- [x] **TASK-SETUP-015** [REQ-SETUP-007..011, NFR-PIPE-001]: Add `autosetup_test.go` and `login_test.go` with fakes (runner, clock, `pathExists`, store, remover; pipeline TASK-PIPE-002/012).
  - _Output_: `cmd/vphone-apps/autosetup_test.go`, `cmd/vphone-apps/login_test.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run 'AutoSetup|LoginSetup'` passes ("likewise" = same under `setup` and `login`; texts per design § Failure output):
    - Kept app: sudo args lack `--remove-app`, `Setup*` order per REQ-SETUP-007, default stderr exactly the three steps plus the box and stdout empty (installed by us: the four steps, the child's `SetupRemoving` line, then the box), with `--verbose` stdout `<model path>\n` from a fake child; runner gets exactly `/usr/bin/sudo, -p, <sudoPrompt>, self, setup, --app, /Applications/Organic Maps.app, --ipa, <ipa>, [--remove-app,] --interface, en0, --elevated` for the default interface (`--remove-app` only if installed by us, `--keep-work` and `--verbose` only when given); a fake root with failing `Run` still removes an installed-by-us app.
    - Steps (fake store, runner and terminal buffer): kept app prints exactly `[1/3] Checking Organic Maps`, `[2/3] Downloading installer`, `[3/3] Enrolling with Unfairplay` plain; installed by us `[1/4] Installing Organic Maps` … `[4/4]`; with a terminal stderr and a fake `Download` that sets the bar to 40 of 100 and blocks until the hook ran, step 2 draws a `40%` frame (dropping `WithProgress` fails it); the display is closed (`\n`) before the runner is called, before `error:` lines and before the box; `login` shows the same steps.
    - Progress lines: a root `setup --elevated` with a fake `Run` calling `Progress("profile=x phase=y")` prints it only with `--verbose` (likewise manual form); `--verbose` is passed to the child by `setup --verbose` and never by `login`; the child's `error:` lines show either way.
    - Injected euid 0, under `setup` and `login`: stderr is exactly `error: setup: running as root; run without sudo\n` (`login` adds `hint: run vphone-apps setup`), exit 1 (0 under `login`), runner and store never called.
    - Existing symlink or other-bundle-id app: `is not Organic Maps`, no download, no sudo, no delete hint (newly appeared: with it); unexpected listing and `ErrSessionExpired` from `Lookup`, `Purchase`, `Download` print their rows, without `login`'s `run vphone-apps setup` hint.
    - Installed by us, likewise: wait timeout ends stderr with timeout row, click hint, delete hint (no download, no sudo, no IPA in cache); cases open start error, exit 1 with nil error (`exit 1`, one runner call, zero polls): line plus install hint and delete hint; hung `open` at 50 ms: line plus click and delete hints; cases `Lookup` failure, download failure, 50 ms `lookupTimeout`/`PurchaseTimeout`/`DownloadTimeout` (`timed out after 50ms`, IPA-free cache): `error: setup:` line plus delete hint. With an existing app: no delete hint.
    - `cache.New()` failure (symlinked dir): `error: cache:` row and hint, no download, no sudo, no `Clean`, exit 1 (0 under `login`).
    - Sudo outcomes, likewise: `cannot elevate` line, no `re-run as root` (`login` adds `hint: run vphone-apps setup`); child exit 3: no `error:` line, exit 1; exit 1, 5, signalled: the `sudo failed` line once with sudo hint; delete hint last when installed by us.
    - IPA deleted on success and failure; cache empty after a failed `Download` and after an interrupt during `Download`; `Clean()` failure after a failed child adds `error: cleanup:`.
    - Likewise, for SIGINT during the wait and `ctx` cancelled in blocking fake `Lookup`, `Purchase`, `Download` (`interrupted`), and store error `boom\n` from each of the three (`boom?`): `error: setup:` line, no own hint, IPA-free cache.
    - Child exit 0 then failing `Clean()`, likewise: no box, stderr ends `error: cleanup: <message>\nhint: delete <dir> manually\n` (nothing else), stdout per § Setup rules: stdout, exit 1 (0 under `login`).
    - `login`, existing model: no output, program or store call; after successful auto-setup: stdout empty, steps then the exact box bytes, child output between the Enrolling step and the box.
    - `login`, failing or interrupted setup (real SIGINT during the wait): exit 0, stdout per § Setup rules: stdout, failure output, `hint: run vphone-apps setup`, then delete hint if installed by us; wait timeout or open failure: its line, its own hint (click or install), delete hint, no `run vphone-apps setup`.
    - Child exit 3, under `setup` (exit 1) and `login` (exit 0): no model saved: stderr ends with exactly one `hint: ` + `notice.EnrollHint` line; model present: stdout per § Setup rules: stdout, `hint: enrollment finished; delete /Applications/Organic Maps.app manually` last, no `EnrollHint`.
    - Default `setup`, `setup --verbose` and `login`: the box last on stderr on success only (never on failure); `SetupIntro`, `SetupKept`/`SetupNew`, `Prerequisites` and the model path only with `setup --verbose`. Manual form `--elevated --verbose`: stderr holds only progress lines, no notices, no box; the non-elevated manual form prints the box once.
    - `models.Dir()`/`models.Exists` errors: same form; a `models.Exists` error in the step-4 re-check prints `hint: run vphone-apps setup` (no model saved); failed login runs no auto-setup; `login x` exits 2, `Login`/store never called. `login --help`: three usage lines, exit 0, `Login`/store never called. Sentinel password absent.

## Phase 2: Doctor
*Goal*: `doctor` reports readiness with exact fixes.

- [x] **TASK-SETUP-004** [REQ-SETUP-004, REQ-SETUP-005, REQ-SETUP-006]: Implement `doctor` per design § doctor output with `store.Account`, `models.Exists` (`(bool, error)`), `vm.DialWithTimeout`, `(*Machine).RPC(ctx, …)`.
  - _Output_: `cmd/vphone-apps/doctor.go`.
  - _Verify_: `go test ./cmd/vphone-apps -run Doctor` with fakes (fixes per design):
    - All pass: `ready`, exit 0.
    - No session: `FAIL session: run vphone-apps login`, later checks still run, exit 1; generic `Account()` error and mode-0000 model directory: `FAIL session`, `FAIL model`.
    - Empty model dir: the `vphone-apps setup` fix; `SUDO_USER=nosuchuser` as root: `FAIL model: cannot resolve SUDO_USER nosuchuser`.
    - `methods.list` without `apps.install`: `FAIL apps.install: update vphone-cli`; cases `{"ok":false,"error":"x"}`, `garbage`, EOF, 2 MiB line, no reply (50 ms deadline): `FAIL apps.install`, no `ready`.
    - `vm` cases, exit 1, each then `SKIP apps.install: needs vm`: `--vm ""`, `--vm ../x` (`FAIL vm: invalid VM name`); blocking dial at 50 ms (`FAIL vm: timed out connecting`); missing machine directory (`FAIL vm: create it with vphone-cli vm create`).
    - Without `--vm`: final lines `skipped: VM checks (no --vm)` then `ready` (exit 0 if checks 1 and 2 pass); a failing check: no `ready`, skipped line last.
    - `doctor --help` stdout is exactly the design's two-line block, exit 0; `doctor --bogus` and `doctor x` exit 2, `doctor x` stderr being `error: usage: unexpected argument x\n` plus that block.
    - Model and cache directories unchanged.

## Phase 3: Real-world check
*Goal*: setup works on the operator's Mac.

- [ ] **TASK-SETUP-005** [REQ-SETUP-001, REQ-SETUP-002, REQ-SETUP-007..011]: With no model, run `vphone-apps login` (click Get/Install when asked), then `doctor`, then `login` again.
  - _Reuse_: none: runs the existing `login` and `doctor` commands.
  - _Output_: PR note.
  - _Verify_: first `login` exits 0 printing the steps, the box (installed by us: `Removing Organic Maps…` before it) (no intro lines or model path; those are `setup --verbose` only); `doctor` shows `PASS model`; `stat -f '%Su %Lp'` of model file and directory shows the invoking user, `600` and `700`; `test ! -e '/Applications/Organic Maps.app'` succeeds when the run installed it; `ls ~/Library/Caches/vphone-apps` holds no IPA; second `login` prints nothing extra.

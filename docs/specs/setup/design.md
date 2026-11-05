# design.md — docs/specs/setup/
> vphone-apps

## Architecture Overview
Files: `cmd/vphone-apps/setup.go`, `cmd/vphone-apps/autosetup.go`, `cmd/vphone-apps/login.go`, `cmd/vphone-apps/doctor.go`, `cmd/vphone-apps/autosetup_test.go`, `cmd/vphone-apps/login_test.go`, `internal/notice/text.go`.

## API / Interface Design

| Command | Flags | REQ |
|---|---|---|
| `setup` | `--app` and `--ipa` (both or neither; neither = auto-setup), `--interface` (default `en0` = `HardwareInterface`), `--keep-work`, `--verbose`, internal `--remove-app`, `--elevated` (not in usage text) | REQ-SETUP-001..003, 007..010 |
| `doctor` | `--vm <name>` (optional) | REQ-SETUP-004..006 |

`setup.Options`/`Run` are those of `github.com/DrWhax/unfairplay/pkg/setup` (fork): `Options{AppPath, IPAPath, ModelDirectory, HardwareInterface string; KeepWork bool; Progress func(string)}`; `Run(ctx, Options) (Result, error)`; `Result{Summary map[string]any; ModelPath string}`.

### Setup rules
- Usage errors (exit 2, before elevation): the § Usage text table. A `models.Dir()` failure is `error: setup: <message>`, exit 1. Manual form, before elevating: `--app` a directory ending `.app`, `--ipa` a regular file ending `.ipa`.
- `Run` gets a `signal.NotifyContext(SIGINT, SIGTERM)` context held until exit; interrupt = `error: setup: interrupted`.
- **stdout** (`setup`): `Result.ModelPath` + `\n` from the root process only with `--verbose`, and only after `Run` and (with `--remove-app`) the removal succeeded, no signal; also the only output when the child exited 0 and just the IPA deletion failed; empty otherwise (so always empty without `--verbose`). `login` never prints it (it passes no `--verbose`).
- **Progress** (`--verbose`): the root process passes `Options.Progress` lines (unfairplay's `profile=… phase=…`) to stderr only with `--verbose`; otherwise they are dropped. `error:` lines are never filtered. Auto-setup appends `--verbose` to the child's args when given; `login` has no such flag.
- **Runner** (`proc.go`): sudo is started as `sudo -p <sudoPrompt> …` with `sudoPrompt` = `Mac login password (for sudo): `; it inherits stdio, gets SIGINT/SIGTERM forwarded, no context kill; `open` is killed when `ctx` is done (runner returns its error).
- `runElevated(ctx, args) error`: `args` has no leading `setup`; runs `/usr/bin/sudo` with `["-p", sudoPrompt, self, "setup", args…, "--elevated"]` when `os.Geteuid() != 0`. Returns nil for child exit 0, `errChildReported` for 3, else the message per § Exit codes (`interrupted` when `ctx` is done).
- Auto-setup (flagless `setup`, `login`) refuses when `os.Geteuid() == 0`: `error: setup: running as root; run without sudo`, exit 1 (`login` still 0), runner never called.
- `verifyOrganicMaps(path) error` (`autosetup.go`): `Lstat` must show a real directory (no symlink) whose bundle id (`CFBundleIdentifier` of `Contents/Info.plist`; `softwareVersionBundleId` of `Wrapper/iTunesMetadata.plist` only when `Info.plist` does not exist, ENOENT;
  any other `Info.plist` read, parse or missing-key error is an error, never overridden by the second plist) equals `app.organicmaps`. Runs on the existing or newly appeared app (before download or sudo) and in `removeApp`.
  Plists are read only via `readPlistFile(path) (map[string]any, error)` : `Lstat` first; only a regular file of at most 1 MiB is opened (symlink, FIFO, device refused unopened; read capped with `io.LimitReader`); anything else is an error giving the reason.
- Root removal (`--remove-app`): after `Run` returns (a signal never aborts it) the root process prints `notice.SetupRemoving`, then `removeApp(path)`: `verifyOrganicMaps` failing touches nothing and records `error: cleanup: refusing to remove <path>: <reason>`;
  else `os.RemoveAll`, a failure recorded as `error: cleanup: cannot remove /Applications/Organic Maps.app: <err>` (no hint). On failure: `error: setup: <message>` for a `Run` error (`interrupted` when a signal arrived, even if `Run` succeeded), then the `error: cleanup:` line, exit 1 (3 under `--elevated`) even when `Run` succeeded.

Model-directory modes (`0700`/`0600`), ownership and contents are written by `setup.Run`; tests with a stubbed `Run` can only assert that the command itself adds nothing to the directory.

### Exit codes
| Process | Success | Handled failure | Usage error |
|---|---|---|---|
| `setup`, `doctor` | 0 | 1 | 2 (`doctor`: `error: usage:`) |
| root `setup … --elevated` | 0 | 3, after printing its own `error:` line | 2 |
| `login` | 0 | 0 once signed in, whatever auto-setup does; 1 for sign-in failure | 2 |

Child exit → parent: 0 → 0; 3 → 1 with no further `error:` line; other `n` → `sudo failed (exit <n>)`; signalled → `sudo failed (killed by signal)`; start or `os.Executable` failure → `cannot elevate: <err>`.

### Failure output
`autoSetup` prints nothing; its caller (`setup` or `login`) prints `error: setup: <message>` (unless quoted in full), then the own hint, exit 1.

| Cause | Message | Own hint |
|---|---|---|
| no session (checked before `autoSetup`) | `not logged in` | `hint: run vphone-apps login` |
| `ErrSessionExpired` from `Lookup`, `Purchase` or `Download` | `session expired` | `hint: run vphone-apps login` |
| existing or newly appeared app fails `verifyOrganicMaps` | `/Applications/Organic Maps.app is not Organic Maps` | `hint: delete or move /Applications/Organic Maps.app if it is a leftover or another app, then re-run vphone-apps setup` |
| other store error from `Lookup`, `Purchase` or `Download` | `<store message>` (sanitized as in the pipeline design) | none |
| listing not `app.organicmaps`, price 0, `trackId` > 0 | `unexpected App Store listing for app.organicmaps` | `hint: the App Store listing changed; run vphone-apps setup with --app and --ipa` |
| `open` start error / non-zero exit with nil error | `cannot open the App Store: <err>` / `cannot open the App Store: exit <n>` | `hint: open the Mac App Store, install Organic Maps, then re-run vphone-apps setup` |
| `open` deadline | `cannot open the App Store: timed out after 30s` | click hint: `hint: click Get/Install in the App Store window, then re-run vphone-apps setup` |
| 300 polls without a ready app | `timed out waiting for Organic Maps after 10m` | click hint |
| Lookup/Purchase deadline; Download deadline | `timed out after 120s`; `timed out after 30m` (injected deadline `<d>`: `timed out after <d>`) | none |
| `cache.New()` failure (no download, sudo or `Clean`) | `error: cache: <message>` (full line) | the cache error's own hint, if any |
| child exit 3 (enrollment failed; the child printed its own error) | none | none (step 4 below) |
| `sudo failed (exit <n>)` / `sudo failed (killed by signal)` | as named | `hint: sudo needs an administrator account and its password; fix that, then re-run vphone-apps setup` |
| `cannot elevate: <err>` | as named | manual form only: `hint: re-run as root` |
| IPA directory deletion (`Clean()`) fails | `error: cleanup: <message>` | `hint: delete <dir> manually` |
| interrupt at any auto-setup step (`Lookup`, wait, `Purchase`, `Download`, `sudo`) or in the manual form; a cancelled `ctx` error from the store maps here | `interrupted` | none |
| auto-setup run as root (euid 0; checked first, nothing else runs) | `running as root; run without sudo` | none (`login` adds its usual hint) |
| manual form | `<path> not found`; `<path> is not a valid app/ipa`; `<message>` for a `Run` error | none |

Print order (both callers; wait timeout installed by us: timeout row, click hint, delete hint):
1. `line`, unless the child already printed its own.
2. The own hint.
3. The `cleanup` lines.
4. When the child reported failure (`childFailed`), or for `login` with no own hint: re-check `models.Exists(dir)` (an error there counting as no model saved); a model present means the child enrolled but could not remove the app: `hint: enrollment finished; delete /Applications/Organic Maps.app manually` (5 is skipped); else `hint: ` + `notice.EnrollHint` (once) after a child-reported failure, or `hint: run vphone-apps setup` for `login` otherwise.
5. Last, when `installedByUs`: `hint: if /Applications/Organic Maps.app is present, delete it manually (a re-run keeps an installed Organic Maps)`.

When `enrolled` only 3 is printed, no completion box. A `models.Dir()` or `models.Exists` error in `login` is `error: setup: <message>`.

### Timeouts
| Step | Default | Field |
|---|---|---|
| `Lookup` | 120 s | `lookupTimeout` |
| `Purchase` | 120 s | store `PurchaseTimeout` |
| `Download` | 30 min | store `DownloadTimeout` |
| `/usr/bin/open` | 30 s | `openTimeout` |
| wait for the app | `appMaxPolls` 300 × `appPollInterval` 2 s = 10 min | constants |

`<d>` in every `timed out … after <d>` is `dur.Format` of the deadline (pipeline design § Timeouts). `setup.Run` is unbounded.

### Usage text
A usage error prints `error: usage: <message>`, then the command's usage block to stderr, exit 2, before any elevation, store or program call. `setup`, `login` and `doctor` reject any positional argument; the root `--elevated` run validates identically, and the argv auto-setup builds holds only constants and the validated `--interface`.

| Case | `<message>` |
|---|---|
| `--interface` not `^[a-z][a-z0-9]{0,15}$` | `--interface must match [a-z][a-z0-9]{0,15}` |
| exactly one of `--app`/`--ipa` | `--app and --ipa must be given together` |
| `--remove-app` without `--app /Applications/Organic Maps.app` and `--ipa` | `--remove-app needs --app "/Applications/Organic Maps.app" and --ipa` |
| any positional argument | `unexpected argument <first one>` |
| bad flag (`setup`, `login`, `doctor`) | `flag provided but not defined: -<name>` (stdlib text) |
| flag without its value (`--interface`, `doctor --vm`) | `flag needs an argument: -<name>` (stdlib text) |

`doctor` usage block (also `doctor --help` on stdout, exit 0):
```
usage: vphone-apps doctor [--vm <name>]
Exit codes: 0 ready, 1 not ready, 2 usage error.
```

`setup --help` (stdout, exit 0, runner never called):
```
usage: vphone-apps setup [--app <path.app> --ipa <path.ipa>] [--interface <name>] [--keep-work] [--verbose]
With no --app and --ipa, setup installs Organic Maps if needed, downloads its IPA and enrolls automatically.
--app and --ipa must be given together or not at all.
Exit codes: 0 success, 1 failure, 2 usage error.
```
`login --help` (stdout, exit 0, `Login` never called):
```
usage: vphone-apps login
After sign-in, login sets up Unfairplay automatically when no model exists; if that fails or is interrupted login still exits 0 - run vphone-apps doctor, which exits 0 only when ready.
Exit codes: 0 signed in, 1 sign-in failure, 2 usage error.
```

### Auto-setup
Constants: `organicMapsBundleID = "app.organicmaps"`, `organicMapsPath = "/Applications/Organic Maps.app"`, `appPollInterval = 2 * time.Second`, `appMaxPolls = 300`.

`autoSetup(ctx, deps, iface, keepWork, verbose bool) *setupError`; injectable `deps`: store, runner, `pathExists`, clock (`After(d) <-chan time.Time`), `readExternalVersion(appPath) (version string, ok bool)` (via `readPlistFile`; `ok` = parsed; `version` = `""` if the key is missing or invalid), `lookupTimeout`, `openTimeout`, cache, output.
Returns nil or `*setupError{line, hint string; cleanup []string; deleteHint, enrolled, childFailed bool}`: `line` the first line (empty when the child printed its own), `hint` the own hint, `cleanup` the `error: cleanup:` line and hint, `deleteHint` = `installedByUs`, `enrolled` = child exited 0, `childFailed` = child exited 3 (`errChildReported`; `line` empty). Steps:
1. With `verbose` first print `SetupIntro`, then `SetupKept` if `pathExists(organicMapsPath)`, else `SetupNew`, then `Prerequisites` (§ Notice texts); `installedByUs = !exists`, set once. Then open the step display (pipeline design § Step display: stderr, `a.stderrTTY`) with n = 3 (kept) or 4 (installed by us), steps named per § Steps; `done()` runs before the completion box, before any `error:`/`hint:` line and before the child starts, on every exit path.
2. `ensureApp(ctx, deps, display) (listing, *setupError)` (`listing` = the Lookup result; `display` = the step display of step 1):
   - Exists: step `Checking Organic Maps`: `verifyOrganicMaps(organicMapsPath)`, then `lookupListing` (the track ID is needed by `fetchIPA`; same `lookupTimeout`, checks and error rows as below, so a Lookup failure fails a run that keeps the app).
   - Else: step `Installing Organic Maps`: `lookupListing`: `Lookup(ctx, organicMapsBundleID)` must give `app.organicmaps`, price 0, `trackId` > 0, else nothing else runs; `/usr/bin/open macappstore://apps.apple.com/app/id<trackId>` under `openTimeout`; an open failure ends the run. The Waiting step (§ Steps); poll up to `appMaxPolls` times:
     ready when `pathExists` and `readExternalVersion` `ok` → `verifyOrganicMaps`, stop; else wait `appPollInterval` or `ctx.Done()` (`interrupted`). Ready on poll 300 succeeds (299 waits); else the timeout row after 300 polls, 300 waits.
3. `fetchIPA(ctx, deps, listing, display) (cache, ipaPath, *setupError)` (`cache` non-nil once `cache.New()` succeeded): step `Downloading installer`; `cache.New()`; `store.Download(store.WithProgress(ctx, display.progress()), app, cache.Path("app.ipa"), true, extID)`; `extID` = `version` of `readExternalVersion` (ipatool `ExternalVersionID`; key `softwareVersionExternalIdentifier` parsed with `howett.net/plist`, an integer formatted in decimal; must match `^[0-9]{1,20}$`, so `-1`, `1; x`, an oversized file and an unreadable plist give `""` = latest). `Purchase` with `ErrLicenseAlreadyExists` counts as success (pipeline TASK-PIPE-004).
4. Step `Enrolling with Unfairplay`, then `display.done()` (the sudo prompt and child lines follow it); `runElevated(ctx, ["--app", organicMapsPath, "--ipa", <cache app.ipa>, ("--remove-app" if installedByUs), "--interface", iface, ("--keep-work"), ("--verbose")])`; `errChildReported` gives an empty `line`; any other error a `line` and own hint.
5. Once `cache.New()` succeeded, `cache.Clean()` runs on every exit path (after the child, or after a failed or interrupted download); a failure fills `cleanup` and is a failure (after child exit 0 it is the whole failure: `enrolled`, empty `line`/`hint`, no `deleteHint`). The completion box (§ Output lines) only when the child exited 0 and `Clean()` ok.

The model-exists skip is `login` only.

### Steps
Shown as `[i/n] <name>`; n = 3 when the app is kept, 4 when installed by us (Checking is replaced by Installing and Waiting):

| Step | When | Name |
|---|---|---|
| Checking | app kept | `Checking Organic Maps` |
| Installing | installed by us | `Installing Organic Maps` |
| Waiting | installed by us | `Waiting for the App Store: click Get/Install (up to 10 min)` |
| Downloading | always | `Downloading installer` (byte bar) |
| Enrolling | always | `Enrolling with Unfairplay` |

Plain mode prints one `[i/n] <name>` line per step. Kept example, plain: `[1/3] Checking Organic Maps`, `[2/3] Downloading installer`, `[3/3] Enrolling with Unfairplay`.

### Output lines (stderr; constants in `internal/notice`)
Success order (stderr; intro, `SetupKept`/`SetupNew` and `Prerequisites` only with `--verbose`): intro, `SetupKept`/`SetupNew`, `Prerequisites`, steps, then the root child's `SetupRemoving` (only `--remove-app`), the model path on stdout (only with `--verbose`), the completion box.

**Completion box**: on success only, after the step display is closed, the invoking (non-`--elevated`) process writes `notice.Box(notice.SetupDone)` to stderr (auto-setup and `login` after the `Clean()` check; manual form after the child exited 0, or after `setupRoot` for a root run without `--elevated`). A failed run prints errors and hints, never the box. Plain ASCII, identical on a terminal and not. `notice.Box(text)` is a pure function: greedy word-wrap at single spaces to `BoxWidth = 72` columns (a word longer than 72 is split at 72), each line `"| "` + line padded with spaces to 72 + `" |"`, between `"+" + 74 "-" + "+"` rules, every line ending `\n`. This is the only home of the frame. For `SetupDone` the output is exactly:

```
+--------------------------------------------------------------------------+
| Setup is complete. Please remember to only use vphone-apps for legal and |
| lawful purposes allowed by the laws of your locality. Consult your       |
| attorney if you have any questions on what this does or does not         |
| include.                                                                 |
+--------------------------------------------------------------------------+
```

| Constant | Text |
|---|---|
| `notice.SetupIntro` | `Setting up Unfairplay: it needs one App Store app from your account; Organic Maps (open source) is used.` |
| `notice.SetupKept` | `Organic Maps is already installed on this Mac and will be kept.` |
| `notice.SetupNew` | `Organic Maps is not installed on this Mac; it will be installed, then removed after enrollment.` |
| `notice.SetupRemoving` | `Removing Organic Maps…` (root process, only with `--remove-app`) |
| `notice.SetupDone` | `Setup is complete. Please remember to only use vphone-apps for legal and lawful purposes allowed by the laws of your locality. Consult your attorney if you have any questions on what this does or does not include.` (shown in the completion box, only when the child exited 0 and the IPA deletion succeeded) |

### Notice texts
`Prerequisites` is printed once, only with `--verbose`, by the unprivileged invoking process before the sudo prompt (auto-setup: right after the `SetupKept`/`SetupNew` line; manual form: after the path checks, before elevation; a root run without `--elevated` likewise), never by the `--elevated` child.
- `notice.Prerequisites` = `Before setup: sign in to the Mac App Store with the same Apple Account you used for vphone-apps login.`
- `notice.EnrollHint` = `sign in to the Mac App Store with the same Apple Account you used for vphone-apps login, then re-run vphone-apps setup` (failure hint, not printed on success)

### doctor output
One line per check in order: `PASS <name>`, `FAIL <name>: <fix>` or `SKIP <name>: needs vm`.

| Check | Fix |
|---|---|
| `session` | `run vphone-apps login` (`ErrNotLoggedIn`); any other `Account()` error: its text |
| `model` | `run vphone-apps setup`; a `models.Dir()` or `models.Exists` error: `FAIL model: <message>` |
| `vm` | `create it with vphone-cli vm create` (`ErrNoSuchVM`), `start the VM, then re-run` (`ErrNotRunning`), `invalid VM name` (bad or blank `--vm`), `timed out connecting` (dial deadline), else the dial error |
| `apps.install` | `update vphone-cli`; `methods.list failed: <reason>` (`{"ok":false}`, malformed JSON, EOF, line over 1 MiB); `methods.list timed out after 5s` |

Check 4 passes on an `apps.install` entry in `result.methods` (`{"methods":[{name,params,force}...]}`); `SKIP apps.install: needs vm` when check 3 fails. Without `--vm`: `skipped: VM checks (no --vm)` after the two checks, then `ready` only if none failed (on a failure the skipped line is last); with `--vm`, `ready` only when all four passed. Exit 0 only when `ready` was printed. `session` uses `store.Account()` only (no network); `doctor` is read-only.

## Security Design
Only `/usr/bin/sudo` and `/usr/bin/open` (one `macappstore://` URL, numeric `trackId`) are started, no shell (NFR-PIPE-002). No credentials read.

# design.md — docs/specs/pipeline/
> vphone-apps

## Architecture Overview
A subcommand dispatcher in `cmd/vphone-apps` calls three small internal packages — `store` (ipatool wiring), `vm` (`vphone.sock` client) and `cache` — plus `models` (model directory). Decryption calls unfairplay `fairplay.DecryptIPA` directly from the install command; it needs no wrapper.

**Stack**: Go 1.26.5, stdlib `flag`, `golang.org/x/term` (no-echo prompts), `github.com/majd/ipatool/v2/pkg/...`, `github.com/DrWhax/unfairplay/pkg/fairplay` (fork, see index).
**Deployment**: local CLI on the operator's Mac, installed with `go install ./cmd/vphone-apps` or `go build`. No service.

## System Diagram
```
search/install ─▶ store (ipatool appstore) ─▶ cache/app.ipa
                                              │ fairplay.DecryptIPA
                                              ▼
                                  cache/app.decrypted.ipa
                                              │ vm.Push  (vphone.sock "push")
                                              ▼
                       guest /private/var/tmp/vphone-apps-<rand>.ipa
                                              │ vm.RPC "apps.install" (+ "apps.launch")
                                              ▼
                                           running VM
```

## Data Models

### store.Session (no persisted data of our own)
| Field | Type | Constraints | Notes |
|---|---|---|---|
| AppStore | `appstore.AppStore` | built by `store.New()` | ipatool constructor `NewAppStore(Args)` |
| Account | `appstore.Account` | from `AccountInfo()` | `store.Account()` returns `ErrNotLoggedIn` when absent |

### vm request/response (JSON lines, per the index)
| Name | Fields |
|---|---|
| push request | `t:"push"`, `local` (abs host path), `path` (abs guest path) |
| rpc request | `t:"rpc"`, `method`, `params` (object) |
| reply | `ok` bool; on success `result` object; on failure `error` string and optional `guest_error{code,message}` |

## API / Interface Design

CLI surface (flags via stdlib `flag`, one `FlagSet` per subcommand):

| Command | Flags | REQ |
|---|---|---|
| `login` | none (runs auto-setup afterwards and exits 0 regardless, REQ-SETUP-011) | REQ-PIPE-001 |
| `search <term…>` | none | REQ-PIPE-002 |
| `install <term…>` | `--id <bundleId>`, `--vm <name>` (required), `--purchase`, `--keep-account-data`, `--keep`, `--launch` | REQ-PIPE-003..012 |

**CLI contract.** Positional words are joined with single spaces into the term. Flags may appear before or after the term: each subcommand parses, takes the first positional, then re-parses the remainder (stdlib `flag` stops at the first positional). `--help` or `-h` on any command prints its usage to stdout and exits 0. A repeated flag takes its last value. A usage error prints `error: usage: <message>` and the usage text to stderr and exits 2; `<message>` is exactly one of the rows below (checked in this order after parsing). An explicit empty `--vm` is the `invalid VM name` error, not a usage error.

| Case (`install`, `search`) | `<message>` |
|---|---|
| bad flag | `flag provided but not defined: -<name>` (stdlib text) |
| flag without its value | `flag needs an argument: -<name>` (stdlib text) |
| `--id` given with an empty value | `--id needs a value` |
| `--id` together with a term | `--id and a search term are mutually exclusive` |
| no term, or a term empty after joining and trimming (`--id` absent) | `missing search term` |
| `--vm` absent (`install`) | `--vm is required` |

Top-level (`--help`, `-h` on stdout, exit 0; also follows `error: usage: <message>` on stderr, exit 2, for no command (`missing command`) or an unknown one (`unknown command "<name>"`, `<name>` sanitized as in § Output formats)). It is the only block ending with the `Built on` line (REQ-ATTR-002); per-command blocks below do not:
```
usage: vphone-apps <command> [flags]

commands:
  login                      sign in to the App Store (session shared with ipatool)
  search <term>              search the App Store
  install <term> --vm <name> download, decrypt and install an app on a VM
  setup                      one-time unfairplay enrollment
  doctor                     preflight checks
  --licenses                 show third-party licenses

Built on ipatool, unfairplay and vphone-cli — see --licenses
```

`search --help` (stdout, exit 0, store never called; the same block follows `error: usage: <message>` on stderr):
```
usage: vphone-apps search <term...>
Prints NAME, ID (the bundle id for install --id), VERSION and PRICE.
Exit codes: 0 success, 1 failure, 2 usage error.
```
`install --help` (stdout, exit 0, nothing started; same use on stderr):
```
usage: vphone-apps install <term...> --vm <name> [flags]
       vphone-apps install --id <bundleId> --vm <name> [flags]

flags:
  --id <bundleId>      install the app with exactly this bundle id
  --vm <name>          target VM (required)
  --purchase           acquire a license first if the app is not owned
  --keep-account-data  keep Apple account data in the decrypted IPA
  --keep               keep the decrypted IPA and print its path
  --launch             launch the app after installing
Exit codes: 0 success, 1 failure, 2 usage error.
```

**Output formats** (stable; tested byte for byte):

| Output | Stream | Format |
|---|---|---|
| login prompts | stderr | `Apple Account email: `, `Apple Account password: `, `Apple 2FA code: `; a successful `login` prints nothing of its own on stdout; when no model exists it then runs auto-setup, whose stderr lines (steps (setup design § Steps), `SetupRemoving` (when installed by us), the completion box; the sudo prompt is sudo's own, `sudoPrompt` of the setup design) and stdout (always empty: `login` never prints the model path; setup design § Setup rules) are those of the setup design § Output lines and § Failure output; with a model, nothing more |
| search table | stdout | header `NAME`, `ID` (the bundle id for `install --id`), `VERSION`, `PRICE`, then one row per result; columns left-aligned with `text/tabwriter` (minwidth 0, tabwidth 8, padding 2, padchar space, no trailing padding after the last column); price is `Free` when ipatool's numeric `Price` is 0, else `%.2f`, e.g. `0.99`, `4.50` |
| search hint | stderr | after the table: `Install one with: vphone-apps install --id <ID> --vm <name>` (literal `<ID>` and `<name>`); not printed on failure |
| selection | stderr | `Installing: <name> (<bundleId>) <version>`, before any download |
| install steps | stderr | steps `Downloading`, `Decrypting`, `Copying to VM`, `Installing` and, with `--launch`, `Launching`, shown as `[i/n] <step>` (n = 4, or 5 with `--launch`), rendered per § Step display |
| install success | stdout | `installed: <bundleId>` and, when upgraded, `upgraded: yes` |
| launch | stdout | `launched: <bundleId> pid <pid>` |
| failure | stderr | one line `error: <step>: <message>` per failing step (a `cleanup` failure adds its own `error: cleanup:` line), each followed by zero or more `hint: <text>` lines |
| kept file | stdout | with `--keep` only, printed last: `kept: <absolute path of the decrypted ipa>` |
| doctor | stdout | one line per check: `PASS <name>` or `FAIL <name>: <fix>`; a check that cannot run because an earlier one failed prints `SKIP <name>: needs vm`; format owned by the setup design § doctor output |

`<step>` is one of `usage`, `login`, `search`, `select`, `download`, `decrypt`, `vm`, `push`, `install`, `launch`, `setup`, `doctor`, `licenses`, `cache`, `cleanup`. Every string printed from the App Store or the guest (names, versions, prices, `error`, `guest_error.message`) has every byte below 0x20 (tab and newline included) and 0x7f replaced by `?` before the format adds its own separators. A launch failure prints `error: launch: ...` after `installed:` was already printed, and exits 1. Literal texts:

| Case | stderr |
|---|---|
| guest error | `error: install: <error>: <guest_error.message>` |
| guest error without `guest_error` | `error: install: <error>` |
| already installed elsewhere (REQ-PIPE-009) | the line above, then `hint: remove the app in the guest, then re-run` |
| `apps.install` expiry | `error: install: timed out after <d>; the install may still be running in the guest`, then `hint: check the guest app list before re-running` |
| VM not running | `error: vm: VM is not running`, then `hint: start the VM, then re-run` |
| no such VM | `error: vm: no such VM`, then `hint: create it with vphone-cli vm create` |
| missing model | `error: decrypt: no unfairplay model`, then `hint: run vphone-apps setup` |
| not logged in | `error: <step>: not logged in`, then `hint: run vphone-apps login` |
| reply over the bound | `error: <step>: reply too large` |
| expired session | `error: <step>: session expired`, then `hint: run vphone-apps login` |
| other store error (network, rate limit) | `error: <step>: <message>`; never retried |
| cache directory symlink or foreign owner | `error: cache: <path> is a symlink or not owned by you`, then `hint: remove it and re-run` |
| `SUDO_USER` not resolvable | `error: <step>: cannot resolve SUDO_USER <name>` (`doctor` prints `FAIL model: cannot resolve SUDO_USER <name>`) |
| push failure | the `error: push: …` line, then `hint: staged file may remain at <guest path>` |
| interrupt in `login` or `search` | `error: login: interrupted` / `error: search: interrupted` |
| interrupt in `select`, `download` or `decrypt` | `error: <step>: interrupted` |
| interrupt from the start of the push until `apps.install` is sent | `error: push: interrupted`, then `hint: staged file may remain at <guest path>` |
| interrupt after `apps.install` was sent | `error: install: interrupted; the install may still be running in the guest`, then `hint: check the guest app list before re-running` |
| interrupt during `apps.launch` | `error: launch: interrupted` (after `installed:`) |
| unowned app without `--purchase` | `error: download: app not owned`, then `hint: re-run with --purchase` |
| `--id` matches nothing | `error: select: no app with bundle id <id>` |
| `install` term with no results | `error: select: no results for "<term>"` |
| cleanup failure | `error: cleanup: <message>`, then `hint: delete <dir> manually`; exit 1 even when the install succeeded (also the removal of Organic Maps by `setup --remove-app`: `error: cleanup: cannot remove <path>: <err>` or `error: cleanup: refusing to remove <path>: <reason>`, with no hint of its own; the auto-setup caller prints the delete hint, setup design § Failure output) |

### Step display (`progress.go`, the only renderer)
- Mode: terminal when `term.IsTerminal(stderr)`, `TERM` is not `dumb` and the width is at least 37 cells (the bar line: `[`, 30 cells, `] `, `100%`); otherwise plain, one `[i/n] <step>\n` per step, no `\r` or ANSI.
- Terminal frame, two lines, redrawn at each step start and each progress call: `<up>\r\x1b[K[i/n] <step>\n\r\x1b[K[<f "=">< 30-f " ">] <label>`. `<up>` is `\x1b[1A` on every draw after the first, empty on the first; the cursor stays at the end of the bar line. `f` = floor(fraction x 30), computed with integers like the label. Step 1 of 4 first draws `\r\x1b[K[1/4] Downloading\n\r\x1b[K[` + 30 spaces + `] 1/4`.
- Download step: with total > 0, with `c` = min(max(cur,0),total) in bytes (integer arithmetic, no floating point): label = `c*100/total` + `%` and `f` = `c*30/total` (100% means all bytes arrived; the next step's frame follows when `Download` returns). Total <= 0 (unknown) and every other step: fraction = (i-1)/n, label `i/n`.
- `finish()` redraws the open frame with a full bar (`f` = 30, label `100%`), then does `done()`; it is called when the last step succeeds (`installed:` / `launched:` result lines) and when auto-setup hands off to the elevated child, so no run ends on a part-filled bar. `done()` (failure and every other close) writes one `\n` if a frame is open, then progress calls draw nothing until the next `step` (which draws a fresh frame without `<up>`). The hook given to `store.WithProgress` belongs to its step; calls after the step changed are ignored.
- API (`progress.go`): `newStepDisplay(w io.Writer, tty bool, n int) *stepDisplay`; `step(name string)` starts the next step; `progress() func(cur, max int64)` returns the current step's callback (pairs are `(cur, max)`, the argument of `store.WithProgress`); `finish()` and `done()` (both idempotent, no-ops in plain mode).
- Auto-setup (setup design § Auto-setup) uses this renderer with n = 3 or 4 and closes it before the elevated child starts.
- `install` closes the renderer, with the poller already joined (`Download` returned or was abandoned), before any stdout result, `error:` or `hint:` line on every exit path, including SIGINT and an abandoned `Download`: bar frames, `\n`, `error: …`, no frame bytes after.

### Timeouts
`<d>` in an expiry line is `dur.Format(deadline)` (`internal/dur`), used by every caller: whole minutes `<n>m` when the deadline is a multiple of 10 minutes (`30m`, `10m`), else whole seconds `<n>s` (`120s`, `30s`, `200s`), else `Duration.String()` (`50ms`); each passed as a context deadline or read deadline; tests inject small values.

| Step | Value | On expiry |
|---|---|---|
| `login`, `search` (App Store calls) | 120 s | `error: <step>: timed out after 120s` |
| `--id` lookup and `install` term search | 120 s | `error: select: timed out after 120s` |
| `purchase` | 120 s | `error: download: timed out after 120s` |
| `download` | 30 min | `error: download: timed out after 30m` |
| auto-setup `Lookup`, `Purchase` | 120 s | `error: setup: timed out after 120s` |
| auto-setup `Download` | 30 min | `error: setup: timed out after 30m` |
| auto-setup `/usr/bin/open` | 30 s | `error: setup: cannot open the App Store: timed out after 30s` |
| auto-setup wait for Organic Maps | 10 min, polled every 2 s | `error: setup: timed out waiting for Organic Maps after 10m` |
| `vm` dial | 5 s | `error: vm: timed out connecting` |
| `push` reply | 30 min | `error: push: timed out after 30m` |
| `apps.install` reply | 200 s | REQ-PIPE-008 |
| `apps.launch` reply | 30 s | `error: launch: timed out after 30s` |
| `doctor` dial and `methods.list` | 5 s each | the check fails (REQ-SETUP-004) |

Only `setup.Run`, `decrypt` and the elevated sudo child (password prompt included) run unbounded; Ctrl-C ends them. The network steps of auto-setup use the auto-setup rows above. `store.Download` applies the 120 s deadline to `Purchase` and the 30 min deadline to the rest (the exported `Store` fields `PurchaseTimeout` and `DownloadTimeout`, zero meaning the default; tests and auto-setup set them); an expiry returns an error whose text is `timed out after <d>`, and each caller prints it as `error: <step>: timed out after <d>` with its own step (`download` or `setup`). `vm` reads accept at most 1 MiB per reply line; a longer line fails the step with `reply too large`.

Go interfaces (internal):
- `store.New(dir string) (*Store, error)` (`dir` is the ipatool state directory; the empty string means resolve it by the rules below, tests pass a temporary directory) builds the ipatool client with the same keychain service (`ipatool-auth.service`), cookie file (`<state>/cookies`) and state directory rules as ipatool's `cmd/common.go` (state dir: `$XDG_STATE_HOME/ipatool`, else `$XDG_DATA_HOME/ipatool`, else `~/.ipatool`). Every network method takes `ctx` first: `Login(ctx, …)`, `Search(ctx, …)`, `Lookup(ctx, …)`, `Download(ctx, app, outPath, purchase, externalVersionID)`; ipatool's calls other than `Download` take no context (`Download` also receives `ctx` through `DownloadInput.Context`), so each runs in a goroutine and the method returns `ctx.Err()` as soon as `ctx` is done, abandoning the call's result (the 120 s and 30 min deadlines and the signal cancellation act this way). `Account()` is local (keychain only) and takes none. The keyring handed to ipatool is wrapped (`store.build`): `Get` is cached per process (the first `Get` of a key reads the system keychain, later ones return the cached item; `Set` writes through and refreshes the cache, `Remove` drops it, a miss is never cached), and ipatool's `kbsync-v1` item (a performance cache that ipatool rebuilds on a miss) is held in memory only and never touches the system keychain, its `Get` returning `keyring.ErrKeyNotFound` until set. Reason: macOS shows a keychain prompt for each read or write of an item and a rewrite resets "Always Allow"; so a run reads the `account` item at most once. Every network method (not `Account()`) maps ipatool's `ErrPasswordTokenExpired` to `ErrSessionExpired`, so the expired-session row applies to `login`, `search`, `select` and `download`. `Account` returns `ErrNotLoggedIn` when absent and never `ErrSessionExpired` (REQ-SETUP-006). `WithProgress(ctx context.Context, fn func(cur, max int64)) context.Context` attaches a hook that `Download` calls about every 200 ms with the bytes received and the total of ipatool's `DownloadInput.Progress` bar (built with output discarded, so it draws nothing), only once the total is known (> 1; 1 is the bar's placeholder); `fn` must be goroutine-safe. The poll goroutine is owned by `store`, started before ipatool's `Download` and joined before `Download` returns, so `fn` is never called after it. Methods: `Login`, `Search`, `Lookup`, `Download` (`outPath` is always `cache.Path("app.ipa")`; `externalVersionID` is passed as `DownloadInput.ExternalVersionID`, `""` = latest, `install` passes `""`) (Download + ReplicateSinf, REQ-PIPE-004), `Account`.
- `vm.DialWithTimeout(name, d) (*Machine, error)` rejects as `invalid VM name` an empty name, `.`, a name containing `/` or `..`, a NUL, or more than 64 bytes, returns `ErrNoSuchVM` for ENOENT on the machine directory and `ErrNotRunning` only for ECONNREFUSED; every other dial error (EACCES, ENOTSOCK, …) fails closed as `error: vm: <message>` (REQ-PIPE-006); `vm.NewGuestPath(r io.Reader) (string, error)` (16 hex characters from `r`, `crypto/rand` in production; `install` calls it before `DialWithTimeout` and a failure is `error: push: ...` with no connection made), `(*Machine).Push(ctx, local, guest)`, `(*Machine).RPC(ctx, method, params, deadline)` (`Machine` carries an injectable push deadline, default 30 min) each open a new connection (REQ-PIPE-007/008/010).
- `cache.New() (*Cache, error)` `Lstat`s the cache directory, rejects a symlink or a directory not owned by the user, creates it `0700` or `chmod`s an existing one to `0700`, then makes a per-run subdirectory with `os.MkdirTemp(dir, "run-")` (`0700`); `(*Cache).Path(name)` returns a `0600` file path inside it and errors on a name containing a path separator or `..`; Download and Decrypt write `<name>.part` and rename on success; `New` also removes sibling directories named `run-*` (directories only) that it owns and whose mtime is older than 24 h, ignoring every error from that sweep (including ENOENT from a concurrent sweep) so it never fails or blocks the current run (a run killed by SIGKILL or power loss leaves its directory until then; recovery is deleting it); `(*Cache).Keep() (string, error)` is called only when every step of the command succeeded, renames the run directory to `kept-*` (never swept) and returns `<kept dir>/app.decrypted.ipa`; a `Keep()` failure prints `error: cache: cannot keep <path>: <err>`, exits 1, prints no `kept:` line and the directory is cleaned; `(*Cache).Clean()` removes only that subdirectory (REQ-PIPE-011). `install` runs under `signal.NotifyContext(SIGINT, SIGTERM)` and passes that context to `Push` and `RPC` (`fairplay.DecryptOptions` has no context: `DecryptIPA` runs in a goroutine and on cancel is abandoned, not waited for) (a cancelled socket call closes its connection); the Decrypt step writes `app.decrypted.ipa.part` and renames on success, waits for the active step to return, except `DecryptIPA` and a store call, which are abandoned on cancel (a store call on cancel may keep running in the background; `Clean()` retries `RemoveAll` once and the process exits right after, and a timed-out `Login` or `Purchase` may still complete at Apple), then calls `Clean()` and exits 1. A `Clean()` failure prints the `cleanup` error and exits 1.
- `models.Dir() (string, error)` and `models.Exists(dir string) (bool, error)` (true when the directory holds a regular non-empty `*.bin`; the error is set only for a read failure other than not-exist).

## Reuse
| Concern | Existing symbol (path) | Used by tasks |
|---|---|---|
| Search, login (2FA), purchase, download, sinf | `appstore.AppStore` (`github.com/majd/ipatool/v2/pkg/appstore`) | TASK-PIPE-002, 003, 004, 005 |
| Keychain, cookie jar, machine, OS wrappers | `keychain.New`, `http.CookieJar`, `machine.New`, `operatingsystem.New` (ipatool `pkg/`) | TASK-PIPE-002 |
| FairPlay decrypt, account-data stripping | `fairplay.DecryptIPA` (`github.com/DrWhax/unfairplay/pkg/fairplay`) | TASK-PIPE-006 |
| Install, launch inside the guest | vphoned `apps.install`, `apps.launch` over `vphone.sock` | TASK-PIPE-008, 009, 010 |

## File Structure
```
vphone-apps/
  go.mod, go.sum
  cmd/vphone-apps/        main.go, proc.go, login.go, search.go, install.go, progress.go (step display, the only renderer) (+ _test.go), sentinel_test.go, execallow_test.go; setup.go, autosetup.go, doctor.go (setup set)
  internal/store/         store.go (New, Login, Account, Search, Lookup, Download, WithProgress), store_test.go
  internal/models/        models.go (Dir, Exists), models_test.go
  internal/vm/            vm.go, vm_test.go
  internal/cache/         cache.go, cache_test.go
  internal/dur/           dur.go (Format), dur_test.go
```

## Security Design
- Credentials exist only in `login` memory and ipatool's keychain (NFR-PIPE-001). The login password is read without echo.
- Cache directory `0700`, files `0600`, deleted on exit (REQ-PIPE-011); decrypted IPAs carry the account's data unless stripped, so stripping is the default (REQ-PIPE-005).
- Downloaded content is trusted as ipatool delivers it (TLS to the App Store) and no extra hash check is made; `install` only reads the IPA as bytes and the app executes only inside the guest, never on the host.
- `login` restores the terminal mode on any exit from a prompt and writes `\n` to stderr after each no-echo read and after an interrupted prompt (the user's Enter or Ctrl-C is not echoed); an interrupt at a prompt exits 1 with no credential output.
- `vm` connects only to a unix socket under `~/.vphone/machines/<name>/`; `<name>` is rejected as `invalid VM name` by the `vm.DialWithTimeout` rule (empty, `.`, `/`, `..`, NUL, over 64 bytes).

## Decisions (locked)

| Decision | Value | Rationale |
|---|---|---|
| External programs | exactly `/usr/bin/sudo` and `/usr/bin/open`, started only through `cmd/vphone-apps/proc.go`, which also validates argv per path: `sudo` only `["-p", sudoPrompt, os.Executable(), "setup", …]` (`sudoPrompt`: setup design § Setup rules); `open` exactly one argument matching `^macappstore://apps\.apple\.com/app/id[0-9]+$`; otherwise an error and nothing started | the operator never required a no-program rule; an allowlist keeps the surface small and testable |
| Flag parsing | stdlib `flag` with the re-parse rule above | five small commands; no dependency needed |
| Login | `vphone-apps login` using ipatool `Login`, session shared with the ipatool CLI | ipatool is a library here, so the tool needs its own login entry point |
| Strip account data | default on, `--keep-account-data` to retain | the IPA is pushed to a VM and deleted; the account's identity is not needed to run it |
| Install route | `apps.install` over `vphone.sock` | the operator chose vphoned; no extra tools or tokens |
| Guest staging path | `/private/var/tmp/vphone-apps-<random>.ipa` | documented example path; vphoned deletes it after install |
| RPC deadline | 200 s | vphone's host-side whole-RPC limit is 180 s; a larger client value lets the host's error arrive first |
| Search limit | 25 | enough to choose from; passed to ipatool `Search` |
| Retries | none | recovery is re-running; avoids hidden repeat purchases or downloads |
| Staged guest file | removed by vphoned once `apps.install` was sent, on success and failure; left behind if the run stops between push and install | vphoned's `apps.install` deletes the path in a deferred cleanup |
| Work file cleanup | per-run subdirectory; `install` deletes `app.ipa` right after a successful decrypt; the directory is always removed (including SIGINT/SIGTERM) unless `--keep` succeeded, when it becomes `kept-*` holding only `app.decrypted.ipa` | simplest rule; isolates concurrent runs; re-running re-downloads |
| Unfairplay source | fork via `replace`, pinned commit | operator decision; switch after PR #28 merges |

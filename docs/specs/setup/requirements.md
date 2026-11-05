# requirements.md — docs/specs/setup/
> vphone-apps

## Overview
`vphone-apps setup` performs unfairplay's one-time enrollment (embedding unfairplay's `pkg/setup`), `vphone-apps login` runs it automatically after a successful sign-in when no model exists, and `vphone-apps doctor` reports whether the machine is ready to run `install`.
Exact output text, hints, exit codes and timeouts are in the setup design (§ Failure output, § Exit codes, § Timeouts, § Output lines, § Usage text, § doctor output).

## Operator ask
- 2026-11-05: "Yes setup is in scope." Setup is exposed so users need no separate `unfairplay` binary.
- 2026-11-05: Preflight `doctor`: logged in, model exists, VM running.
- 2026-11-05: After `login` the tool sets Unfairplay up by itself: it uses Organic Maps (open source) as the one enrollment app, installs it from the Mac App Store when absent, and removes it afterwards when it installed it; the operator is never asked to install an app or download an IPA by hand.

## Actors
- **Operator**: has the Mac App Store signed in with the Apple Account used for `login` and clicks Get/Install in the App Store window when asked (auto-setup only); for the manual `--app`/`--ipa` form also an installed iPhone/iPad app from it and the matching encrypted IPA.

## Dependencies
- **DEP-SETUP-002**: pipeline REQ-PIPE-001 (session via `store`), REQ-PIPE-006 (VM socket check), REQ-PIPE-005 (model directory), REQ-PIPE-008 (RPC framing), REQ-PIPE-004 (IPA download), REQ-PIPE-011 (per-run cache directory).

## Functional Requirements

### Setup
- **REQ-SETUP-001**: `setup [--app <path.app> --ipa <path.ipa>] [--interface <name>] [--keep-work] [--verbose]` with both `--app` and `--ipa` (manual form) shall call unfairplay `setup.Run` with `ModelDirectory` set to the model directory from the index, printing unfairplay's progress lines to stderr only with `--verbose` and leaving the app and IPA untouched;
  with neither flag it runs REQ-SETUP-007. `--interface` defaults to `en0`.
  - _Acceptance_:
    - Success exits 0 and prints the saved model path on stdout only with `--verbose` (design § Setup rules: stdout).
    - Each usage error of design § Usage text (bad `--interface`, exactly one of `--app`/`--ipa`, any positional argument) exits 2 with its message before elevation.
    - A missing path, or a path that is not a valid app/ipa, exits 1 before any sudo prompt; a `Run` error exits 1; an interrupt exits 1 (lines per design § Failure output).
    - `setup --help` prints the design § Usage text, exit 0.
- **REQ-SETUP-002**: When not root, `setup` shall run itself as a child through `/usr/bin/sudo` with the same arguments plus internal `--elevated`, wait, and distinguish "the child printed its error" from "sudo failed before the child ran" by the child's exit code (design § Exit codes); it shall keep the model directory that of the invoking user.
  - _Acceptance_:
    - The process runs `/usr/bin/sudo -p <sudoPrompt> <self> setup <args> --elevated` (`sudoPrompt`: design § Setup rules) with stdin, stdout and stderr inherited and `setup` once in the argv; `Prerequisites` (with `--verbose`) and the box appear once, printed by the invoking process, never by the child.
    - The model lands in the invoking user's directory, owned by that user.
    - With a fake runner: child exit 0 gives exit 0 and `runElevated` prints nothing (the box comes from `setup`, REQ-SETUP-003); exit 3 gives exit 1 and `runElevated` prints nothing; exit 1, exit 5 and a signalled child give exit 1 with the `sudo failed` line and sudo hint; a start failure gives `cannot elevate` (manual form adds `hint: re-run as root`) and `Run` is never called.
    - A root `setup --elevated` whose `Run` fails prints its `error: setup:` line and exits 3 (1 without `--elevated`).
- **REQ-SETUP-003**: `setup` shall, only with `--verbose`, print `notice.Prerequisites` (design § Notice texts) once before the sudo prompt (the `--elevated` child never), and on success end with the completion box of design § Output lines (`notice.SetupDone`: the lawful-use reminder).
  - _Acceptance_: `Prerequisites` appears once on stderr before the sudo prompt only with `--verbose`; on success (manual form and auto-setup) the last stderr output is the box, exactly once, from the invoking process; the `--elevated` run prints neither; a failed run prints no box.

### Auto-setup
- **REQ-SETUP-007**: `setup` with neither `--app` nor `--ipa` shall, in the unprivileged process and in the order of design § Auto-setup: with `--verbose` print the intro lines, then `Prerequisites` (REQ-SETUP-003); obtain the app (REQ-SETUP-008); download the IPA (REQ-SETUP-009);
  run itself through sudo with `--app "/Applications/Organic Maps.app" --ipa <ipa> [--remove-app] --interface <name> [--keep-work] [--verbose]` (`--interface` always passed, `--remove-app` only when the app was absent at the start, `installedByUs`); delete the IPA directory (`cache.Clean`) on every exit path once `cache.New()` succeeded (design § Auto-setup step 5);
  show the completion box (`notice.SetupDone`) only when the child exited 0 AND the deletion succeeded. The target app is fixed: Organic Maps, bundle id `app.organicmaps`, path `/Applications/Organic Maps.app`.
  - _Acceptance_:
    - Default output: stderr is the steps then the box (installed by us: the child's `SetupRemoving` line before the box), stdout is empty; the intro lines, `Prerequisites` and the model path (stdout) appear only with `setup --verbose` (`login` never).
    - Existing app: with `--verbose` `SetupIntro`, `SetupKept`, `Prerequisites`; always the steps `[1/3] Checking Organic Maps`, `[2/3] Downloading installer`, `[3/3] Enrolling with Unfairplay`, the box in that order on stderr (sudo prompt and child output only after the Enrolling step), no `--remove-app`, app never touched.
    - Absent app: with `--verbose` `SetupIntro`, `SetupNew`, `Prerequisites`; always the steps `[1/4] Installing Organic Maps`, `[2/4]` the Waiting step (design § Steps), then the same with n = 4, with `--remove-app`.
    - Terminal stderr: the Downloading step draws a byte `%` bar (design § Steps); the display is closed (one `\n`) before the child starts, any `error:`/`hint:` line and the box; plain mode prints one `[i/n]` line per step.
    - `unfairplay` progress lines (`profile=… phase=…`) are absent by default and shown with `--verbose` (passed to the child); the child's `error:` lines always show.
    - stdout per design § Setup rules: stdout, including a failed removal after a successful `Run` and an interrupt.
    - No IPA remains in the cache directory on success, failure or interrupt.
    - Any failure exits 1 with the output of design § Failure output; a child-reported enrollment failure with no saved model prints `hint: ` + `notice.EnrollHint` (before the delete hint when installed by us).
    - Running as root (euid 0) prints `error: setup: running as root; run without sudo` and exits 1 (`login` still 0); the runner and store are never called.
    - A child that exited 0 followed by a failing deletion prints only the `error: cleanup:` line and its hint, no box, keeps stdout per design § Setup rules: stdout, exits 1.
    - `setup` without a session exits 1 (`not logged in` row), checked before auto-setup starts.
- **REQ-SETUP-008**: Auto-setup shall keep `/Applications/Organic Maps.app` when it exists before the run (still doing the lookup below for the track ID; a lookup failure fails the run); otherwise it shall look up bundle id `app.organicmaps` (ipatool `Lookup`), require bundle id `app.organicmaps`, price 0 and `trackId` > 0, open `macappstore://apps.apple.com/app/id<trackId>` with `/usr/bin/open`, show the Waiting step,
  and poll every 2 s for at most 300 polls (10 min) until the app is ready (path exists and `<app>/Wrapper/iTunesMetadata.plist` parses via the shared plist reader of design § Setup rules).
  Before using the app, pre-existing or newly appeared, it shall run `verifyOrganicMaps(path)` (design § Setup rules) and fail before any download or sudo on a mismatch.
  - _Acceptance_:
    - Fake `open`/clock: `open` called once with the exact URL, 2 s poll interval; ready on poll 300 succeeds, ready only on poll 301 fails with the 10m timeout after exactly 300 polls and 300 waits; an interrupt while waiting exits 1.
    - Each `open` failure (start error, non-zero exit with nil error, 30 s deadline) fails before any poll, with its hint of design § Failure output.
    - A plist that is a FIFO or a symlink (to a FIFO or `/dev/zero`) is never opened or ready; the wait ends in timeout or interrupt, never hangs.
    - An app path that is a symlink, a plain file or has another bundle id fails with `is not Organic Maps`, no download, no sudo; a lookup with another bundle id, price 4.99 or `trackId` 0 fails with `unexpected App Store listing`, `open`/`Purchase`/`Download` never run.
    - Each error carries its own hint (design § Failure output) under `setup` and `login`, suppressing `login`'s `hint: run vphone-apps setup`.
- **REQ-SETUP-009**: Auto-setup shall download the Organic Maps IPA with the stored session into the per-run cache directory (`cache.New()`; ipatool `Purchase` with `ErrLicenseAlreadyExists` counting as success, then `Download`, then `ReplicateSinf`, i.e.
  `store.Download` of TASK-PIPE-004 with `purchase=true`), requesting ipatool's `ExternalVersionID` equal to `softwareVersionExternalIdentifier` of `<app>/Wrapper/iTunesMetadata.plist` (shared plist reader) when it holds an integer or a string matching `^[0-9]{1,20}$`, else the latest version; and delete it afterwards (REQ-SETUP-007).
  - _Acceptance_:
    - Plist value 123456 (integer or string) → the fake store receives external version id `123456`; a missing, unreadable, symlinked, FIFO, over-1-MiB, key-less, negative or non-numeric value → empty string.
    - Store errors, `ErrSessionExpired` and the deadline expiries give their rows of design § Failure output and leave no IPA in the cache; a failing `cache.New()` gives the `error: cache:` row, store never called.
- **REQ-SETUP-010**: `setup` shall accept the internal flags `--remove-app` (set only by auto-setup) and `--elevated` (set only by the elevation of REQ-SETUP-002), neither in the usage text; `--remove-app` is valid only with `--app "/Applications/Organic Maps.app"` and `--ipa`;
  after `setup.Run` returns, success or failure and whether or not a signal arrived, the root process shall remove that app bundle per design § Setup rules (root removal), only after `verifyOrganicMaps` succeeds.
  - _Acceptance_:
    - A fake remover is called once with `/Applications/Organic Maps.app` after a successful and after a failing `Run`; never without the flag.
    - `--remove-app` with another `--app` path, or without `--app`/`--ipa`, exits 2 with usage, `Run` and remover uncalled.
    - A removal failure prints its `error: cleanup:` line, prints no model path (stdout rule) and exits 1 even when `Run` succeeded; the real remover refuses (`refusing to remove`, target intact) a symlink, another bundle id, a plain file and unsafe plists, and removes a real `app.organicmaps` directory (cases: TASK-SETUP-012).
    - SIGINT while `Run` blocks or during a slow remover still ends with the app removed, `error: setup: interrupted`, no model path; exit codes are 3 instead of 1 under `--elevated`.
- **REQ-SETUP-011**: After a successful sign-in, `login` shall run auto-setup (REQ-SETUP-007) when `dir, err := models.Dir()` and `models.Exists(dir)` report no model, print nothing extra when a model exists, and exit 0 whatever auto-setup does, including an interrupt (operator-approved: the sign-in succeeded);
  scripts detect incomplete setup with `doctor`, which exits 0 only on `ready`. `login`'s stdout follows design § Setup rules: stdout; its stderr is the prompts, then (only when auto-setup ran) the lines of REQ-SETUP-007.
  - _Acceptance_:
    - With an existing model `login` prints nothing and starts no program; with none, auto-setup runs once and stdout is empty (`login` never prints the path).
    - When auto-setup fails, is interrupted or times out, `login` prints the failure output of design § Failure output (including the `login`-only hints), prints no model path, exits 0; `doctor` then prints `FAIL model` (`PASS model` when the model was saved).
    - An error from `models.Dir()` or `models.Exists(dir)` is a setup failure of the same form; a failed login runs no auto-setup.
    - `login --help` prints the design § Usage text block on stdout, exit 0, without calling `Login`; `login x` exits 2 (design § Usage text).

### Doctor
- **REQ-SETUP-004**: `doctor [--vm <name>]` shall check, in order, (1) an ipatool session exists, (2) the model directory holds a model, and with `--vm` also (3) the VM socket accepts a connection and (4)
  the reply to `{"t":"rpc","method":"methods.list","params":{}}` has an entry with `name` equal to `apps.install` in `result.methods`, printing the lines of design § doctor output; the dial and `methods.list` calls have the 5 s deadlines of the pipeline design (§ Timeouts); when check 3 fails, check 4 prints `SKIP`.
  - _Acceptance_: each failing check prints `FAIL <check>: <fix>` (fix per design § doctor output); all pass prints `ready`, exit 0; a timeout fails the check; any failure exits 1 after printing every check; check 2 reads the model directory only; `doctor x` exits 2 (design § Usage text).
- **REQ-SETUP-005**: Without `--vm`, `doctor` shall skip checks 3 and 4 and say so.
  - _Acceptance_: the output contains `skipped: VM checks (no --vm)` after the two checks, then `ready` only when no check failed; on a failure the skipped line is last.
- **REQ-SETUP-006**: `doctor` shall make no network request and change nothing.
  - _Acceptance_: with networking disabled and no session, `doctor` completes and fails only check 1; the model and cache directories are unchanged.

## Non-Functional Requirements
- **NFR-SETUP-002**: `setup` shall not write the Apple Account credentials anywhere; the only artifact it keeps is the model file written by unfairplay.
  - _Measurement_: after a run, the model directory contains only files written by `setup.Run` (mode `0600`, directory `0700`).

## Out of Scope
- Driving the Mac App Store sign-in or clicking Get/Install for the operator.
- Removing a pre-existing Organic Maps; removing it when installation by this run failed before elevation (the hint tells the operator).

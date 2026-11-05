package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/dur"
	"github.com/securityvoid/vphone-apps/internal/models"
	"github.com/securityvoid/vphone-apps/internal/notice"
	"github.com/securityvoid/vphone-apps/internal/store"
	"howett.net/plist"
)

const (
	organicMapsBundleID = "app.organicmaps"
	organicMapsPath     = "/Applications/Organic Maps.app"
	appPollInterval     = 2 * time.Second
	appMaxPolls         = 300
	maxPlistSize        = 1 << 20
)

const (
	notOrganicMapsHint = "delete or move " + organicMapsPath + " if it is a leftover or another app, then re-run vphone-apps setup"
	listingHint        = "the App Store listing changed; run vphone-apps setup with --app and --ipa"
	openHint           = "open the Mac App Store, install Organic Maps, then re-run vphone-apps setup"
	clickHint          = "click Get/Install in the App Store window, then re-run vphone-apps setup"
)

// Step names of the setup design § Steps.
const (
	stepChecking    = "Checking Organic Maps"
	stepInstalling  = "Installing Organic Maps"
	stepWaiting     = "Waiting for the App Store: click Get/Install (up to 10 min)"
	stepDownloading = "Downloading installer"
	stepEnrolling   = "Enrolling with Unfairplay"
)

var versionRE = regexp.MustCompile(`^[0-9]{1,20}$`)

// setupError is the failure output of auto-setup; every string is a complete
// line. line is empty when the child printed its own error.
type setupError struct {
	line, hint  string
	cleanup     []string
	deleteHint  bool // Organic Maps was installed by this run
	enrolled    bool // the child exited 0
	childFailed bool // the child exited 3 after printing its own error
}

func newSetupError(hint, format string, args ...any) *setupError {
	e := &setupError{line: "error: setup: " + clean(fmt.Sprintf(format, args...))}
	if hint != "" {
		e.hint = "hint: " + hint
	}
	return e
}

// fromFailure converts a classified failure (one hint at most is used).
func fromFailure(f *failure) *setupError {
	e := &setupError{line: "error: " + f.step + ": " + clean(f.msg)}
	if len(f.hints) > 0 {
		e.hint = "hint: " + clean(f.hints[0])
	}
	return e
}

// reportSetup prints e in the order of the setup design § Failure output;
// login adds its own hints.
func (a *app) reportSetup(e *setupError, login bool) {
	lines := []string{e.line, e.hint}
	lines = append(lines, e.cleanup...)
	deleteHint := e.deleteHint
	if !e.enrolled {
		if e.childFailed || (login && e.hint == "") {
			saved := false
			if dir, err := a.modelDir(); err == nil {
				saved, _ = models.Exists(dir)
			}
			if saved { // the child enrolled but could not remove the app
				lines = append(lines, "hint: enrollment finished; delete "+organicMapsPath+" manually")
				deleteHint = false
			} else if e.childFailed {
				lines = append(lines, "hint: "+notice.EnrollHint)
			} else {
				lines = append(lines, "hint: run vphone-apps setup")
			}
		}
		if deleteHint {
			lines = append(lines, "hint: if "+organicMapsPath+" is present, delete it manually (a re-run keeps an installed Organic Maps)")
		}
	}
	for _, l := range lines {
		if l != "" {
			fmt.Fprintln(a.stderr, l)
		}
	}
}

// readPlistFile parses a plist that is a regular file of at most 1 MiB; a
// symlink, FIFO or device is refused without being opened.
func readPlistFile(path string) (map[string]any, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > maxPlistSize {
		return nil, fmt.Errorf("%s is larger than 1 MiB", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPlistSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPlistSize {
		return nil, fmt.Errorf("%s is larger than 1 MiB", path)
	}
	var m map[string]any
	if _, err := plist.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return m, nil
}

// verifyOrganicMaps checks that path is a real directory whose bundle id is
// app.organicmaps. The wrapper plist is consulted only when Info.plist does
// not exist; any other Info.plist problem is an error.
func verifyOrganicMaps(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("not a real directory")
	}
	file, key := filepath.Join(path, "Contents", "Info.plist"), "CFBundleIdentifier"
	m, err := readPlistFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		file, key = filepath.Join(path, "Wrapper", "iTunesMetadata.plist"), "softwareVersionBundleId"
		m, err = readPlistFile(file)
	}
	if err != nil {
		return err
	}
	if id, _ := m[key].(string); id != organicMapsBundleID {
		return fmt.Errorf("%s: %s is not %s", file, key, organicMapsBundleID)
	}
	return nil
}

// readExternalVersion returns the App Store version id recorded in the app's
// iTunesMetadata.plist: ok is true when the plist parsed, version is "" unless
// the id is 1 to 20 digits.
func readExternalVersion(appPath string) (version string, ok bool) {
	m, err := readPlistFile(filepath.Join(appPath, "Wrapper", "iTunesMetadata.plist"))
	if err != nil {
		return "", false
	}
	switch v := m["softwareVersionExternalIdentifier"].(type) {
	case uint64:
		version = strconv.FormatUint(v, 10)
	case int64:
		version = strconv.FormatInt(v, 10)
	case string:
		version = v
	}
	if !versionRE.MatchString(version) {
		version = ""
	}
	return version, true
}

// lookupListing finds the Organic Maps listing and checks it is the free app
// we expect.
func (a *app) lookupListing(ctx context.Context, st Store) (appstore.App, *setupError) {
	sctx, cancel := context.WithTimeout(ctx, a.to.AutoLookup)
	defer cancel()
	listing, err := st.Lookup(sctx, organicMapsBundleID)
	if err != nil {
		return listing, fromFailure(classify(ctx, sctx, a.to.AutoLookup, "setup", err))
	}
	if listing.BundleID != organicMapsBundleID || listing.Price != 0 || listing.ID <= 0 {
		return listing, newSetupError(listingHint, "unexpected App Store listing for %s", organicMapsBundleID)
	}
	return listing, nil
}

func notOrganicMaps() *setupError {
	return newSetupError(notOrganicMapsHint, "%s is not Organic Maps", organicMapsPath)
}

// ensureApp makes sure a verified Organic Maps is installed and returns its
// App Store listing.
func (a *app) ensureApp(ctx context.Context, st Store, pr *stepDisplay) (appstore.App, *setupError) {
	if a.pathExists(organicMapsPath) {
		pr.step(stepChecking)
		if a.verifyApp(organicMapsPath) != nil {
			return appstore.App{}, notOrganicMaps()
		}
		return a.lookupListing(ctx, st)
	}
	pr.step(stepInstalling)
	listing, e := a.lookupListing(ctx, st)
	if e != nil {
		return listing, e
	}
	octx, cancel := context.WithTimeout(ctx, a.to.AutoOpen)
	code, err := a.proc.Run(octx, openPath, []string{fmt.Sprintf("macappstore://apps.apple.com/app/id%d", listing.ID)}, false)
	cancel()
	switch {
	case ctx.Err() != nil:
		return listing, newSetupError("", "interrupted")
	case errors.Is(err, context.DeadlineExceeded):
		return listing, newSetupError(clickHint, "cannot open the App Store: timed out after %s", dur.Format(a.to.AutoOpen))
	case err != nil:
		return listing, newSetupError(openHint, "cannot open the App Store: %v", err)
	case code != 0:
		return listing, newSetupError(openHint, "cannot open the App Store: exit %d", code)
	}
	pr.step(stepWaiting)
	for range appMaxPolls {
		ready := a.pathExists(organicMapsPath)
		if ready {
			_, ready = a.externalVersion(organicMapsPath)
		}
		if ready {
			if a.verifyApp(organicMapsPath) != nil {
				return listing, notOrganicMaps()
			}
			return listing, nil
		}
		select {
		case <-ctx.Done():
			return listing, newSetupError("", "interrupted")
		case <-a.after(appPollInterval):
		}
	}
	return listing, newSetupError(clickHint, "timed out waiting for Organic Maps after %s", dur.Format(appMaxPolls*appPollInterval))
}

// fetchIPA downloads the Organic Maps IPA into a fresh per-run cache and
// returns the cache (non-nil once cache.New succeeded) and the IPA path.
func (a *app) fetchIPA(ctx context.Context, st Store, listing appstore.App, pr *stepDisplay) (*cache.Cache, string, *setupError) {
	pr.step(stepDownloading)
	c, err := a.newCache()
	if err != nil {
		f := fail("cache", "%s", err.Error())
		var ue *cache.UnsafeDirError
		if errors.As(err, &ue) {
			f.hint("remove it and re-run")
		}
		return nil, "", fromFailure(f)
	}
	ipa, err := c.Path("app.ipa")
	if err != nil {
		return c, "", newSetupError("", "%s", err.Error())
	}
	version, _ := a.externalVersion(organicMapsPath)
	if err := st.Download(store.WithProgress(ctx, pr.progress()), listing, ipa, true, version); err != nil {
		return c, "", fromFailure(classify(ctx, ctx, 0, "setup", err))
	}
	return c, ipa, nil
}

// enroll runs setup as root through sudo on the downloaded IPA.
func (a *app) enroll(ctx context.Context, ipa string, installedByUs bool, iface string, keepWork, verbose bool, pr *stepDisplay) *setupError {
	pr.step(stepEnrolling)
	pr.finish() // the sudo prompt and the child's lines follow; the bar ends full
	args := []string{"--app", organicMapsPath, "--ipa", ipa}
	if installedByUs {
		args = append(args, "--remove-app")
	}
	args = append(args, "--interface", iface)
	if keepWork {
		args = append(args, "--keep-work")
	}
	if verbose {
		args = append(args, "--verbose")
	}
	err := a.runElevated(ctx, args)
	var ee *elevError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee):
		return newSetupError(ee.hint, "%s", ee.msg)
	}
	return &setupError{childFailed: true} // errChildReported
}

// autoSetup installs (if needed) and verifies Organic Maps, downloads its
// IPA, enrolls through sudo and deletes the IPA on every exit path.
func (a *app) autoSetup(ctx context.Context, st Store, iface string, keepWork, verbose bool) *setupError {
	if a.geteuid() == 0 {
		return newSetupError("", "running as root; run without sudo")
	}
	installedByUs := !a.pathExists(organicMapsPath)
	if verbose {
		fmt.Fprintln(a.stderr, notice.SetupIntro)
		if installedByUs {
			fmt.Fprintln(a.stderr, notice.SetupNew)
		} else {
			fmt.Fprintln(a.stderr, notice.SetupKept)
		}
		fmt.Fprintln(a.stderr, notice.Prerequisites)
	}
	n := 3
	if installedByUs {
		n = 4
	}
	pr := newStepDisplay(a.stderr, a.stderrTTY, n)
	defer pr.done()
	listing, e := a.ensureApp(ctx, st, pr)
	if e != nil {
		e.deleteHint = installedByUs
		return e
	}
	c, ipa, e := a.fetchIPA(ctx, st, listing, pr)
	enrolled := false
	if e == nil {
		e = a.enroll(ctx, ipa, installedByUs, iface, keepWork, verbose, pr)
		enrolled = e == nil
	}
	if c != nil {
		if err := c.Clean(); err != nil {
			if e == nil {
				e = &setupError{}
			}
			e.cleanup = []string{"error: cleanup: " + clean(err.Error()), "hint: delete " + clean(c.Dir()) + " manually"}
		}
	}
	pr.done()
	switch {
	case e == nil:
		a.complete()
	case enrolled:
		e.enrolled = true
	default:
		e.deleteHint = installedByUs
	}
	return e
}

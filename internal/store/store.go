// Package store wires ipatool's appstore client the same way the ipatool CLI
// does (keychain service, cookie file, state directory) so the session is
// shared, and adds context handling around its context-free calls.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/byteness/keyring"
	cookiejar "github.com/juju/persistent-cookiejar"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/majd/ipatool/v2/pkg/keychain"
	"github.com/majd/ipatool/v2/pkg/util/machine"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
	"github.com/schollz/progressbar/v3"
	"github.com/securityvoid/vphone-apps/internal/dur"
)

const (
	keychainService = "ipatool-auth.service"
	cookieFile      = "cookies"
)

var (
	ErrNotLoggedIn    = errors.New("not logged in")
	ErrSessionExpired = errors.New("session expired")
	// ErrNotOwned is returned by Download when the account has no license.
	ErrNotOwned = errors.New("app not owned")
	// ErrAppNotFound is ipatool's, re-exported for callers.
	ErrAppNotFound = appstore.ErrAppNotFound
	// ErrAuthCodeRequired is ipatool's, returned unchanged by Login.
	ErrAuthCodeRequired = appstore.ErrAuthCodeRequired
)

// TimeoutError reports an expired Download deadline (purchase or download).
type TimeoutError struct{ D time.Duration }

func (e *TimeoutError) Error() string { return "timed out after " + dur.Format(e.D) }

// Store is the App Store client. Zero PurchaseTimeout and DownloadTimeout
// mean the defaults (120 s, 30 min); tests and auto-setup set them.
type Store struct {
	as              appstore.AppStore
	PurchaseTimeout time.Duration
	DownloadTimeout time.Duration
}

// NewWithAppStore wraps an existing client (tests inject fakes here).
func NewWithAppStore(as appstore.AppStore) *Store { return &Store{as: as} }

// New builds the client. dir is ipatool's state directory; "" resolves it.
//
// The keyring and cookie-jar wiring below replicates how ipatool (Majd
// Alfhaily, MIT, https://github.com/majd/ipatool) builds its AppStore in
// cmd/common.go, so that sessions are shared with the ipatool CLI.
func New(dir string) (*Store, error) { return open(dir, true) }

// NewExisting is New for read-only callers: it never creates anything and
// returns ErrNotLoggedIn when the state directory does not exist.
func NewExisting() (*Store, error) { return open("", false) }

func open(dir string, create bool) (*Store, error) {
	dir, err := resolveDir(dir, create)
	if err != nil {
		return nil, err
	}
	ring, err := keyring.Open(keyring.Config{
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,
			keyring.SecretServiceBackend,
			keyring.FileBackend,
		},
		ServiceName:              keychainService,
		KeychainTrustApplication: true,
		FileDir:                  dir,
		FilePasswordFunc: func(string) (string, error) {
			return "", errors.New("keychain passphrase prompts are not supported; use the system keychain")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open keyring: %w", err)
	}
	return build(dir, ring)
}

func build(dir string, ring keychain.Keyring) (*Store, error) {
	jar, err := cookiejar.New(&cookiejar.Options{Filename: filepath.Join(dir, cookieFile)})
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	opsys := operatingsystem.New()
	s := NewWithAppStore(appstore.NewAppStore(appstore.Args{
		CookieJar:       jar,
		OperatingSystem: opsys,
		Keychain:        keychain.New(keychain.Args{Keyring: newQuietRing(ring), Label: keychainService}),
		Machine:         machine.New(machine.Args{OS: opsys}),
	}))
	return s, nil
}

// resolveDir applies ipatool's state directory rules: $XDG_STATE_HOME/ipatool,
// else $XDG_DATA_HOME/ipatool, else ~/.ipatool; an existing ~/.ipatool wins
// (ipatool keeps it as the legacy location).
func resolveDir(dir string, create bool) (string, error) {
	if dir != "" {
		if !create {
			return dir, nil
		}
		return dir, os.MkdirAll(dir, 0o700)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	legacy := filepath.Join(home, ".ipatool")
	chosen := legacy
	for _, v := range []string{"XDG_STATE_HOME", "XDG_DATA_HOME"} {
		if base := os.Getenv(v); filepath.IsAbs(base) {
			chosen = filepath.Join(base, "ipatool")
			break
		}
	}
	if info, err := os.Stat(legacy); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("legacy state path is not a directory: %s", legacy)
		}
		return legacy, nil
	}
	if !create {
		if _, err := os.Stat(chosen); errors.Is(err, os.ErrNotExist) {
			return "", ErrNotLoggedIn
		}
		return chosen, nil
	}
	if err := os.MkdirAll(chosen, 0o700); err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}
	return chosen, nil
}

func mapErr(err error) error {
	if errors.Is(err, appstore.ErrPasswordTokenExpired) {
		return ErrSessionExpired
	}
	return err
}

// call runs f in a goroutine and abandons it when ctx is done.
func call[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := f()
		ch <- res{v, err}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.v, mapErr(r.err)
	}
}

// Login authenticates; ErrAuthCodeRequired is returned unchanged.
func (s *Store) Login(ctx context.Context, email, password, code string) error {
	_, err := call(ctx, func() (struct{}, error) {
		_, err := s.as.Login(appstore.LoginInput{Email: email, Password: password, AuthCode: code})
		return struct{}{}, err
	})
	return err
}

// Account returns the stored account (local, keychain only); it never
// reports ErrSessionExpired.
func (s *Store) Account() (appstore.Account, error) {
	out, err := s.as.AccountInfo()
	if err != nil {
		if errors.Is(err, keyring.ErrKeyNotFound) {
			return appstore.Account{}, ErrNotLoggedIn
		}
		return appstore.Account{}, err
	}
	return out.Account, nil
}

// Search returns at most limit results, most relevant first.
func (s *Store) Search(ctx context.Context, term string, limit int) ([]appstore.App, error) {
	acct, err := s.Account()
	if err != nil {
		return nil, err
	}
	out, err := call(ctx, func() (appstore.SearchOutput, error) {
		return s.as.Search(appstore.SearchInput{Account: acct, Term: term, Limit: int64(limit)})
	})
	if err != nil {
		return nil, err
	}
	if len(out.Results) > limit {
		out.Results = out.Results[:limit]
	}
	return out.Results, nil
}

// Lookup finds the app with exactly this bundle id (ErrAppNotFound if none).
func (s *Store) Lookup(ctx context.Context, bundleID string) (appstore.App, error) {
	acct, err := s.Account()
	if err != nil {
		return appstore.App{}, err
	}
	out, err := call(ctx, func() (appstore.LookupOutput, error) {
		return s.as.Lookup(appstore.LookupInput{Account: acct, BundleID: bundleID})
	})
	return out.App, err
}

// orDefault returns d, or def when d is zero.
func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// Download fetches app into outPath (written as outPath+".part", sinf
// replicated on the .part file, renamed only on success). With purchase it
// first acquires a license; ErrLicenseAlreadyExists counts as success.
// externalVersionID selects a version ("" = latest). The purchase runs under
// PurchaseTimeout, the rest under DownloadTimeout; an expiry is a *TimeoutError.
func (s *Store) Download(ctx context.Context, app appstore.App, outPath string, purchase bool, externalVersionID string) error {
	acct, err := s.Account()
	if err != nil {
		return err
	}
	if purchase {
		ttl := orDefault(s.PurchaseTimeout, 120*time.Second)
		pctx, cancel := context.WithTimeout(ctx, ttl)
		_, err := call(pctx, func() (struct{}, error) {
			return struct{}{}, s.as.Purchase(appstore.PurchaseInput{Account: acct, App: app})
		})
		cancel()
		if err != nil && !errors.Is(err, appstore.ErrLicenseAlreadyExists) {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return &TimeoutError{D: ttl}
			}
			return err
		}
	}
	ttl := orDefault(s.DownloadTimeout, 30*time.Minute)
	dctx, cancel := context.WithTimeout(ctx, ttl)
	defer cancel()
	err = s.fetch(dctx, acct, app, outPath, externalVersionID)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{D: ttl}
	}
	return err
}

type progressKey struct{}

// WithProgress makes Download report bytes received so far and the total
// (0 until known) about every 200 ms to fn, which must be goroutine-safe.
func WithProgress(ctx context.Context, fn func(cur, max int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// pollProgress polls ipatool's progress bar (it fills it while downloading)
// for the WithProgress hook of ctx; the returned func stops the polling and
// waits for the goroutine, so the hook is never called after it returns.
func pollProgress(ctx context.Context, bar *progressbar.ProgressBar) (stop func()) {
	fn, _ := ctx.Value(progressKey{}).(func(cur, max int64))
	if fn == nil {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				st := bar.State()
				if st.Max > 1 { // 1 is the placeholder until ipatool sets the length
					fn(st.CurrentNum, st.Max)
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

func (s *Store) fetch(ctx context.Context, acct appstore.Account, app appstore.App, outPath, externalVersionID string) error {
	part := outPath + ".part"
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(part)
		}
	}()
	bar := progressbar.NewOptions64(1, progressbar.OptionSetWriter(io.Discard))
	stop := pollProgress(ctx, bar)
	defer stop()
	out, err := call(ctx, func() (appstore.DownloadOutput, error) {
		return s.as.Download(appstore.DownloadInput{Context: ctx, Account: acct, App: app, OutputPath: part, ExternalVersionID: externalVersionID, Progress: bar})
	})
	if err != nil {
		if errors.Is(err, appstore.ErrLicenseRequired) {
			return ErrNotOwned
		}
		return err
	}
	_, err = call(ctx, func() (struct{}, error) {
		return struct{}{}, s.as.ReplicateSinf(appstore.ReplicateSinfInput{Sinfs: out.Sinfs, PackagePath: part})
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(part, 0o600); err != nil {
		return err
	}
	if err := os.Rename(part, outPath); err != nil {
		return err
	}
	ok = true
	return nil
}

// kbsyncKey is ipatool's unexported kbsyncCacheKey (pkg/appstore
// appstore_kbsync_cache.go, "kbsync-v1"); it must be matched by string. Bump
// it together with the ipatool version if ipatool re-keys the cache.
const kbsyncKey = "kbsync-v1"

// quietRing limits system-keychain access, because macOS prompts for every
// read or write of an item: Get is cached per process, Set writes through,
// and the kbsync performance cache lives in memory only.
type quietRing struct {
	real keychain.Keyring
	mu   sync.Mutex
	got  map[string]keyring.Item
	mem  map[string]keyring.Item // kbsync: never reaches the real keyring
}

func newQuietRing(real keychain.Keyring) *quietRing {
	return &quietRing{real: real, got: map[string]keyring.Item{}, mem: map[string]keyring.Item{}}
}

func (q *quietRing) Get(key string) (keyring.Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if key == kbsyncKey {
		if it, ok := q.mem[key]; ok {
			return it, nil
		}
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	if it, ok := q.got[key]; ok {
		return it, nil
	}
	it, err := q.real.Get(key)
	if err == nil {
		q.got[key] = it
	}
	return it, err
}

func (q *quietRing) Set(item keyring.Item) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item.Key == kbsyncKey {
		q.mem[item.Key] = item
		return nil
	}
	delete(q.got, item.Key)
	if err := q.real.Set(item); err != nil {
		return err
	}
	q.got[item.Key] = item
	return nil
}

func (q *quietRing) Remove(key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if key == kbsyncKey {
		if _, ok := q.mem[key]; !ok {
			return keyring.ErrKeyNotFound
		}
		delete(q.mem, key)
		return nil
	}
	delete(q.got, key)
	return q.real.Remove(key)
}

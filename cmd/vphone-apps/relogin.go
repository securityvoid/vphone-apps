package main

import (
	"context"
	"errors"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/store"
)

// reloginStore wraps a Store: when Search, Lookup or Download answer
// ErrSessionExpired and a saved-credentials item exists, it signs in again
// with it and repeats the call once (REQ-PIPE-014).
type reloginStore struct {
	Store
	a      *app
	parent context.Context // command context: the re-login and the retry derive from it
	d      time.Duration   // step deadline of Search and Lookup (Download has its own)
}

func (a *app) withRelogin(parent context.Context, st Store, d time.Duration) *reloginStore {
	return &reloginStore{Store: st, a: a, parent: parent, d: d}
}

// again reports whether the call that returned err should be repeated. It
// returns the error to give back when not: the original, or the interrupt.
func (r *reloginStore) again(err error) (bool, error) {
	if !errors.Is(err, store.ErrSessionExpired) {
		return false, err
	}
	email, password, lerr := r.Store.LoadCredentials()
	if lerr != nil {
		return false, err
	}
	if serr := r.a.signIn(r.parent, r.Store, email, password); serr != nil {
		if r.parent.Err() != nil {
			return false, r.parent.Err()
		}
		return false, err
	}
	return true, nil
}

// repeated marks the error of a repeated call: the outer step deadline says
// nothing about it, so classify reports it as is.
type repeated struct{ error }

func (e repeated) Unwrap() error { return e.error }

// retry repeats a Search or Lookup under its own fresh step deadline. Its
// own expiry is a *store.TimeoutError; any other error is marked repeated.
func (r *reloginStore) retry(f func(context.Context) error) error {
	c, cancel := context.WithTimeout(r.parent, r.d)
	defer cancel()
	err := f(c)
	switch {
	case err == nil:
		return nil
	case r.parent.Err() == nil && errors.Is(c.Err(), context.DeadlineExceeded):
		return &store.TimeoutError{D: r.d}
	}
	return repeated{err}
}

func (r *reloginStore) Search(ctx context.Context, term string, limit int) ([]appstore.App, error) {
	res, err := r.Store.Search(ctx, term, limit)
	if ok, err := r.again(err); !ok {
		return res, err
	}
	var res2 []appstore.App
	err = r.retry(func(c context.Context) (e error) { res2, e = r.Store.Search(c, term, limit); return })
	return res2, err
}

func (r *reloginStore) Lookup(ctx context.Context, bundleID string) (appstore.App, error) {
	app, err := r.Store.Lookup(ctx, bundleID)
	if ok, err := r.again(err); !ok {
		return app, err
	}
	var app2 appstore.App
	err = r.retry(func(c context.Context) (e error) { app2, e = r.Store.Lookup(c, bundleID); return })
	return app2, err
}

// Download repeats with the caller's ctx (it carries the progress hook; the
// store applies its own deadlines).
func (r *reloginStore) Download(ctx context.Context, app appstore.App, outPath string, purchase bool, externalVersionID string) error {
	err := r.Store.Download(ctx, app, outPath, purchase, externalVersionID)
	if ok, err := r.again(err); !ok {
		return err
	}
	return r.Store.Download(ctx, app, outPath, purchase, externalVersionID)
}

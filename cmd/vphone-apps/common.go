package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/dur"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

// Store is the App Store client used by the commands (*store.Store).
type Store interface {
	Login(ctx context.Context, email, password, code string) error
	Account() (appstore.Account, error)
	Search(ctx context.Context, term string, limit int) ([]appstore.App, error)
	Lookup(ctx context.Context, bundleID string) (appstore.App, error)
	Download(ctx context.Context, app appstore.App, outPath string, purchase bool, externalVersionID string) error
}

// failure is one `error: <step>: <msg>` line plus optional hint lines.
type failure struct {
	step, msg string
	hints     []string
	waited    bool // interrupted or timed out while the VM was working
}

func fail(step, format string, args ...any) *failure {
	return &failure{step: step, msg: fmt.Sprintf(format, args...)}
}

func (f *failure) hint(h string) *failure {
	f.hints = append(f.hints, h)
	return f
}

// report prints the failure to stderr and returns exit code 1.
func (a *app) report(f *failure) int {
	fmt.Fprintf(a.stderr, "error: %s: %s\n", f.step, clean(f.msg))
	for _, h := range f.hints {
		fmt.Fprintf(a.stderr, "hint: %s\n", clean(h))
	}
	return 1
}

const loginHint = "run vphone-apps login"

// classify turns an error from a bounded step into a failure. parent is the
// command context (cancelled by a signal), sctx the step context with
// deadline d.
func classify(parent, sctx context.Context, d time.Duration, step string, err error) *failure {
	var te *store.TimeoutError
	switch {
	case errors.Is(err, store.ErrNotLoggedIn):
		return fail(step, "not logged in").hint(loginHint)
	case errors.Is(err, store.ErrSessionExpired):
		return fail(step, "session expired").hint(loginHint)
	case errors.Is(err, store.ErrNotOwned):
		return fail(step, "app not owned").hint("re-run with --purchase")
	case errors.As(err, &te):
		return fail(step, "timed out after %s", dur.Format(te.D))
	case parent.Err() != nil:
		return fail(step, "interrupted")
	case errors.Is(sctx.Err(), context.DeadlineExceeded):
		return fail(step, "timed out after %s", dur.Format(d))
	}
	return fail(step, "%s", err.Error())
}

// bounded runs f under a deadline d and classifies its error.
func bounded(parent context.Context, d time.Duration, step string, f func(context.Context) error) *failure {
	sctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	if err := f(sctx); err != nil {
		return classify(parent, sctx, d, step, err)
	}
	return nil
}

// vmFailure classifies an error from a VM call (push or RPC) in one place.
func vmFailure(ctx context.Context, step string, err error) *failure {
	var te *vm.TimeoutError
	var re *vm.RemoteError
	switch {
	case ctx.Err() != nil:
		f := fail(step, "interrupted")
		f.waited = true
		return f
	case errors.As(err, &te):
		f := fail(step, "timed out after %s", dur.Format(te.D))
		f.waited = true
		return f
	case errors.As(err, &re):
		return fail(step, "%s", remoteMessage(re))
	}
	return fail(step, "%s", err.Error())
}

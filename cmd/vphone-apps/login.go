package main

import (
	"context"
	"errors"
	"flag"
	"os/signal"
	"syscall"

	"github.com/securityvoid/vphone-apps/internal/models"
	"github.com/securityvoid/vphone-apps/internal/store"
)

const loginUsage = `usage: vphone-apps login
After sign-in, login sets up Unfairplay automatically when no model exists; if that fails or is interrupted login still exits 0 - run vphone-apps doctor, which exits 0 only when ready.
Exit codes: 0 signed in, 1 sign-in failure, 2 usage error.
`

func (a *app) login(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	words, code, done := a.parse(fs, loginUsage, args)
	if done {
		return code
	}
	if len(words) > 0 {
		return a.usageError(loginUsage, "unexpected argument "+clean(words[0]))
	}
	if !a.prompt.IsTerminal() {
		return a.report(fail("login", "a terminal is required"))
	}
	st, err := a.newStore()
	if err != nil {
		return a.report(fail("login", "%s", err.Error()))
	}
	// On an interrupt the prompt reads return ctx.Err(); report it uniformly.
	failed := func(err error) int {
		if ctx.Err() != nil {
			return a.report(fail("login", "interrupted"))
		}
		return a.report(fail("login", "%s", err.Error()))
	}
	email, err := a.prompt.Line(ctx, "Apple Account email: ")
	if err != nil {
		return failed(err)
	}
	password, err := a.prompt.Secret(ctx, "Apple Account password: ")
	if err != nil {
		return failed(err)
	}
	attempt := func(code string) (needCode bool, f *failure) {
		f = bounded(ctx, a.to.Login, "login", func(c context.Context) error {
			err := st.Login(c, email, password, code)
			needCode = errors.Is(err, store.ErrAuthCodeRequired)
			if needCode {
				return nil
			}
			return err
		})
		return needCode, f
	}
	needCode, f := attempt("")
	if f == nil && needCode {
		var code2 string
		if code2, err = a.prompt.Secret(ctx, "Apple 2FA code: "); err != nil {
			return failed(err)
		}
		if needCode, f = attempt(code2); f == nil && needCode {
			f = fail("login", "two-factor code rejected")
		}
	}
	if f != nil {
		return a.report(f)
	}
	a.loginSetup(ctx, st)
	return 0
}

// loginSetup runs auto-setup when no model exists. The sign-in succeeded, so
// whatever happens here, login still exits 0.
func (a *app) loginSetup(ctx context.Context, st Store) {
	dir, err := a.modelDir()
	exists := false
	if err == nil {
		exists, err = models.Exists(dir)
	}
	var e *setupError
	switch {
	case err != nil:
		e = newSetupError("", "%s", err.Error())
	case exists:
		return
	default:
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		e = a.autoSetup(ctx, st, "en0", false, false)
	}
	if e != nil {
		a.reportSetup(e, true)
	}
}

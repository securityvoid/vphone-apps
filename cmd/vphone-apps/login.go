package main

import (
	"context"
	"errors"
	"flag"
	"os/signal"
	"strings"
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
	// A saved item signs in without prompting for email and password.
	email, password, err := st.LoadCredentials()
	hadItem := err == nil || errors.Is(err, store.ErrBadCredentials)
	switch {
	case err == nil:
		err = a.signIn(ctx, st, email, password)
		if err == nil {
			a.loginSetup(ctx, st)
			return 0
		}
		f := classify(ctx, ctx, 0, "login", err)
		var te *store.TimeoutError
		if ctx.Err() != nil || errors.As(err, &te) || errors.Is(err, errCodeRejected) {
			return a.report(f)
		}
		a.report(f) // a message; falls back to the typed prompts
	case errors.Is(err, store.ErrNoCredentials):
	case errors.Is(err, store.ErrBadCredentials):
		a.report(fail("login", "%s", err.Error())) // a message; falls back
	default:
		return a.report(fail("login", "%s", err.Error()))
	}
	// On an interrupt the prompt reads return ctx.Err(); classify reports it.
	if email, err = a.prompt.Line(ctx, "Apple Account email: "); err != nil {
		return a.report(classify(ctx, ctx, 0, "login", err))
	}
	if password, err = a.prompt.Secret(ctx, "Apple Account password: "); err != nil {
		return a.report(classify(ctx, ctx, 0, "login", err))
	}
	if err = a.signIn(ctx, st, email, password); err != nil {
		return a.report(classify(ctx, ctx, 0, "login", err))
	}
	if f := a.saveAnswer(ctx, st, email, password, hadItem); f != nil {
		return a.report(f)
	}
	a.loginSetup(ctx, st)
	return 0
}

// saveAnswer asks the save question after a typed sign-in and saves (yes) or
// deletes the item (anything else, when one exists).
func (a *app) saveAnswer(ctx context.Context, st Store, email, password string, hadItem bool) *failure {
	ans, err := a.prompt.Line(ctx, "Save credentials to Keychain for reuse? [y/N] ")
	if ctx.Err() != nil {
		return fail("login", "interrupted")
	}
	if err != nil { // EOF counts as no
		ans = ""
	}
	switch ans = strings.ToLower(strings.TrimSpace(ans)); {
	case ans == "y" || ans == "yes":
		if err := st.SaveCredentials(email, password); err != nil {
			return fail("login", "cannot save credentials: %s", err.Error())
		}
	case hadItem:
		if err := st.ForgetCredentials(); err != nil {
			return fail("login", "cannot delete saved credentials: %s", err.Error())
		}
	}
	return nil
}

var (
	errCodeRejected = errors.New("two-factor code rejected")
	errNoTerminal   = errors.New("a terminal is required for the two-factor code")
)

// signIn logs in under the login deadline and, when Apple requires it,
// prompts once for the 2FA code (outside the deadline). It is the only place
// that handles ErrAuthCodeRequired. An expired deadline is a
// *store.TimeoutError; a prompt interrupt returns the prompt's error.
func (a *app) signIn(ctx context.Context, st Store, email, password string) error {
	needCode, err := a.loginOnce(ctx, st, email, password, "")
	if err != nil || !needCode {
		return err
	}
	if !a.prompt.IsTerminal() {
		return errNoTerminal
	}
	code, err := a.prompt.Secret(ctx, "Apple 2FA code: ")
	if err != nil {
		return err
	}
	if needCode, err = a.loginOnce(ctx, st, email, password, code); err == nil && needCode {
		err = errCodeRejected
	}
	return err
}

func (a *app) loginOnce(ctx context.Context, st Store, email, password, code string) (needCode bool, err error) {
	sctx, cancel := context.WithTimeout(ctx, a.to.Login)
	defer cancel()
	err = st.Login(sctx, email, password, code)
	switch {
	case errors.Is(err, store.ErrAuthCodeRequired):
		return true, nil
	case err != nil && ctx.Err() == nil && errors.Is(sctx.Err(), context.DeadlineExceeded):
		return false, &store.TimeoutError{D: a.to.Login}
	}
	return false, err
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/securityvoid/vphone-apps/internal/models"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

const doctorUsage = "usage: vphone-apps doctor [--vm <name>]\nExit codes: 0 ready, 1 not ready, 2 usage error.\n"

type checkResult struct {
	name string
	ok   bool
	fix  string
}

func (a *app) printCheck(c checkResult) {
	if c.ok {
		fmt.Fprintf(a.stdout, "PASS %s\n", c.name)
		return
	}
	fmt.Fprintf(a.stdout, "FAIL %s: %s\n", c.name, clean(c.fix))
}

func (a *app) doctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	vmName := fs.String("vm", "", "")
	words, code, done := a.parse(fs, doctorUsage, args)
	if done {
		return code
	}
	if len(words) > 0 {
		return a.usageError(doctorUsage, "unexpected argument "+clean(words[0]))
	}
	withVM := false
	fs.Visit(func(f *flag.Flag) { withVM = withVM || f.Name == "vm" })

	failed := false
	emit := func(c checkResult) { a.printCheck(c); failed = failed || !c.ok }

	emit(a.checkSession())
	emit(a.checkModel())
	if !withVM {
		fmt.Fprintln(a.stdout, "skipped: VM checks (no --vm)")
	} else {
		vmRes, m := a.checkVMSocket(*vmName)
		emit(vmRes)
		if m == nil {
			fmt.Fprintln(a.stdout, "SKIP apps.install: needs vm")
		} else {
			emit(a.checkMethods(ctx, m))
		}
	}
	if failed {
		return 1
	}
	fmt.Fprintln(a.stdout, "ready")
	return 0
}

func (a *app) checkSession() checkResult {
	c := checkResult{name: "session"}
	st, err := a.openStore()
	if err == nil {
		_, err = st.Account()
	}
	switch {
	case err == nil:
		c.ok = true
	case errors.Is(err, store.ErrNotLoggedIn):
		c.fix = loginHint
	default:
		c.fix = err.Error()
	}
	return c
}

func (a *app) checkModel() checkResult {
	c := checkResult{name: "model"}
	dir, err := a.modelDir()
	if err != nil {
		c.fix = err.Error()
		return c
	}
	ok, err := models.Exists(dir)
	switch {
	case err != nil:
		c.fix = err.Error()
	case !ok:
		c.fix = "run vphone-apps setup"
	default:
		c.ok = true
	}
	return c
}

// checkVMSocket dials with the doctor deadline; the machine is non-nil only
// on success.
func (a *app) checkVMSocket(name string) (checkResult, *vm.Machine) {
	c := checkResult{name: "vm"}
	m, f := a.checkVM(name, a.to.DoctorDial)
	if f == nil {
		c.ok = true
		return c, m
	}
	c.fix = f.msg
	if len(f.hints) > 0 {
		c.fix = f.hints[0]
	}
	return c, nil
}

func (a *app) checkMethods(ctx context.Context, m *vm.Machine) checkResult {
	c := checkResult{name: "apps.install"}
	raw, err := m.RPC(ctx, "methods.list", map[string]any{}, a.to.DoctorRPC)
	if err != nil {
		f := vmFailure(ctx, "methods.list", err)
		if f.waited {
			c.fix = "methods.list " + f.msg
		} else {
			c.fix = "methods.list failed: " + f.msg
		}
		return c
	}
	var res struct {
		Methods []struct {
			Name string `json:"name"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		c.fix = "methods.list failed: malformed result"
		return c
	}
	for _, mm := range res.Methods {
		if mm.Name == "apps.install" {
			c.ok = true
			return c
		}
	}
	c.fix = "update vphone-cli"
	return c
}

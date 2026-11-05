package main

import (
	"strings"
	"testing"
)

const builtOn = "Built on ipatool, unfairplay and vphone-cli — see --licenses\n"

func TestUsageUnknownCommand(t *testing.T) {
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"bogus"}, `unknown command "bogus"`},
		{[]string{"bo\x1bgus"}, `unknown command "bo?gus"`},
		{nil, "missing command"},
	} {
		e := newEnv(t, happy)
		wantExit(t, e.run(c.args...), 2, e)
		wantErr(t, e, "error: usage: "+c.msg+"\n"+usageText)
		if e.out.Len() != 0 || e.as.callList() != "" {
			t.Fatal(e.out, e.as.callList())
		}
	}
}

// Only the top-level block ends with the Built on line.
func TestUsageBuiltOnTopLevelOnly(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("--help"), 0, e)
	if e.out.String() != usageText || !strings.HasSuffix(usageText, "\n\n"+builtOn) || e.errb.Len() != 0 {
		t.Fatalf("%q", e.out)
	}
	for _, b := range []string{searchBlock, installBlock, loginUsage, setupUsage, doctorBlock} {
		if strings.Contains(b, "Built on") {
			t.Fatalf("per-command block has Built on: %q", b)
		}
	}
}

// Bad flag and flag-without-value: exact message, the command's block, exit 2.
func TestUsageFlagErrorsSetupLoginDoctor(t *testing.T) {
	for _, c := range []struct {
		args  []string
		msg   string
		block string
	}{
		{[]string{"setup", "--bogus"}, "flag provided but not defined: -bogus", setupUsage},
		{[]string{"setup", "--interface"}, "flag needs an argument: -interface", setupUsage},
		{[]string{"login", "--bogus"}, "flag provided but not defined: -bogus", loginUsage},
		{[]string{"doctor", "--bogus"}, "flag provided but not defined: -bogus", doctorBlock},
		{[]string{"doctor", "--vm"}, "flag needs an argument: -vm", doctorBlock},
	} {
		e := newEnv(t, happy)
		wantExit(t, e.run(c.args...), 2, e)
		wantErr(t, e, "error: usage: "+c.msg+"\n"+c.block)
		if e.out.Len() != 0 || e.as.callList() != "" || len(e.steps) != 0 {
			t.Errorf("%v: something started", c.args)
		}
	}
}

func TestUsageHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"search", "--help"}, {"install", "-h"}, {"login", "--help"}} {
		e := newEnv(t, happy)
		wantExit(t, e.run(args...), 0, e)
		if !strings.Contains(e.out.String(), "usage:") || e.errb.Len() != 0 {
			t.Fatalf("%v: out=%q err=%q", args, e.out, e.errb)
		}
	}
}

// Every usage-error row of the design: exit 2, exact stderr bytes, nothing
// started.
func TestUsageInstallErrors(t *testing.T) {
	for _, c := range []struct {
		args  []string
		msg   string
		block string
	}{
		{[]string{"install", "--vm", "v1"}, "missing search term", installBlock},
		{[]string{"install", "--id", "x", "foo", "--vm", "v1"}, "--id and a search term are mutually exclusive", installBlock},
		{[]string{"install", "foo"}, "--vm is required", installBlock},
		{[]string{"install", "  ", "--vm", "v1"}, "missing search term", installBlock},
		{[]string{"install", "--id", "", "--vm", "v"}, "--id needs a value", installBlock},
		{[]string{"install", "--bogus", "foo", "--vm", "v"}, "flag provided but not defined: -bogus", installBlock},
		{[]string{"install", "foo", "--vm"}, "flag needs an argument: -vm", installBlock},
		// precedence: the table order of the design decides which message wins
		{[]string{"install"}, "missing search term", installBlock}, // before --vm is required
		{[]string{"install", "--id", "x", "foo"}, "--id and a search term are mutually exclusive", installBlock},
		{[]string{"install", "--id", "", "foo", "--vm", "v"}, "--id needs a value", installBlock},
		{[]string{"install", "--bogus"}, "flag provided but not defined: -bogus", installBlock},
		{[]string{"install", "--id", "", "foo", "--vm"}, "flag needs an argument: -vm", installBlock},
		{[]string{"search", "--bogus"}, "flag provided but not defined: -bogus", searchBlock}, // before missing term
		{[]string{"search", "--vm", "v"}, "flag provided but not defined: -vm", searchBlock},
		{[]string{"search"}, "missing search term", searchBlock},
		{[]string{"search", "   "}, "missing search term", searchBlock},
		{[]string{"search", "--bogus", "x"}, "flag provided but not defined: -bogus", searchBlock},
		{[]string{"login", "extra"}, "unexpected argument extra", loginUsage},
	} {
		e := newEnv(t, happy)
		wantExit(t, e.run(c.args...), 2, e)
		wantErr(t, e, "error: usage: "+c.msg+"\n"+c.block)
		if e.as.callList() != "" || len(e.steps) != 0 || len(e.cacheEntries(t)) != 0 {
			t.Errorf("%v: something started: %q %v", c.args, e.as.callList(), e.steps)
		}
	}
}

func TestParseFlagAfterTerm(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 0, e)
	e = newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "Bar", "--vm", "v1", "--vm", "v1"), 0, e) // repeated flag: last wins
	if !strings.Contains(e.errb.String(), "Installing: Foo") {
		t.Fatal(e.out)
	}
}

const searchBlock = `usage: vphone-apps search <term...>
Prints NAME, ID (the bundle id for install --id), VERSION and PRICE.
Exit codes: 0 success, 1 failure, 2 usage error.
`

const installBlock = `usage: vphone-apps install <term...> --vm <name> [flags]
       vphone-apps install --id <bundleId> --vm <name> [flags]

flags:
  --id <bundleId>      install the app with exactly this bundle id
  --vm <name>          target VM (required)
  --purchase           acquire a license first if the app is not owned
  --keep-account-data  keep Apple account data in the decrypted IPA
  --keep               keep the decrypted IPA and print its path
  --launch             launch the app after installing
Exit codes: 0 success, 1 failure, 2 usage error.
`

func TestUsageBlocks(t *testing.T) {
	for cmd, block := range map[string]string{"search": searchBlock, "install": installBlock} {
		e := newEnv(t, happy)
		wantExit(t, e.run(cmd, "--help"), 0, e)
		if e.out.String() != block || e.errb.Len() != 0 {
			t.Errorf("%s --help: out=%q err=%q", cmd, e.out, e.errb)
		}
		if e.as.callList() != "" || len(e.steps) != 0 || len(e.cacheEntries(t)) != 0 {
			t.Errorf("%s --help touched store/VM/cache: %q %v", cmd, e.as.callList(), e.steps)
		}
	}
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "--vm", "v1"), 2, e)
	wantErr(t, e, "error: usage: missing search term\n"+installBlock)
	e = newEnv(t, happy)
	wantExit(t, e.run("install", "foo"), 2, e)
	wantErr(t, e, "error: usage: --vm is required\n"+installBlock)
	e = newEnv(t, happy)
	wantExit(t, e.run("search"), 2, e)
	wantErr(t, e, "error: usage: missing search term\n"+searchBlock)
}

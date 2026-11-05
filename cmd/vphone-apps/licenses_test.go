package main

import (
	"os"
	"strings"
	"testing"
)

func TestLicenses(t *testing.T) {
	want, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, happy)
	wantExit(t, e.run("--licenses"), 0, e)
	if e.out.String() != string(want) {
		t.Fatal("--licenses output differs from NOTICE")
	}
	e = newEnv(t, happy)
	wantExit(t, e.run("--help"), 0, e)
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	if lines[len(lines)-1] != "Built on ipatool, unfairplay and vphone-cli — see --licenses" {
		t.Fatal(lines[len(lines)-1])
	}
	for _, c := range []string{"login", "search", "install", "setup", "doctor"} {
		e = newEnv(t, happy)
		wantExit(t, e.run(c, "--bogus"), 2, e)
		if strings.Contains(e.errb.String()+e.out.String(), "Built on ipatool") {
			t.Errorf("%s usage contains the Built on line", c)
		}
	}
	e = newEnv(t, happy)
	wantExit(t, e.run("bogus"), 2, e)
	if !strings.HasPrefix(e.errb.String(), "error: usage:") {
		t.Fatal(e.errb)
	}
}

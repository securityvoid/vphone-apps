package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

func TestSearchResults(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchRes = []appstore.App{
		{Name: "A", BundleID: "a.a", Version: "1", Price: 0},
		{Name: "B", BundleID: "b.b", Version: "2", Price: 0.99},
		{Name: "C", BundleID: "c.c", Version: "3", Price: 4.5},
	}
	wantExit(t, e.run("search", "some", "term"), 0, e)
	want := "NAME  ID   VERSION  PRICE\nA     a.a  1        Free\nB     b.b  2        0.99\nC     c.c  3        4.50\n"
	if e.out.String() != want {
		t.Fatalf("got %q", e.out)
	}
	if e.errb.String() != "Install one with: vphone-apps install --id <ID> --vm <name>\n" {
		t.Fatalf("hint %q", e.errb)
	}
}

func TestSearchLimit25(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchRes = make([]appstore.App, 26)
	for i := range e.as.searchRes {
		e.as.searchRes[i] = appstore.App{Name: fmt.Sprint("n", i), BundleID: "b", Version: "1"}
	}
	wantExit(t, e.run("search", "x"), 0, e)
	if e.as.searchLim != 25 {
		t.Fatal("limit", e.as.searchLim)
	}
	if n := strings.Count(e.out.String(), "\n"); n != 26 {
		t.Fatalf("%d rows", n)
	}
}

func TestSearchNoResults(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchRes = nil
	wantExit(t, e.run("search", "x"), 1, e)
	wantErr(t, e, "error: search: no results for \"x\"\n")
}

func TestSearchSanitizes(t *testing.T) {
	e := newEnv(t, happy)
	e.as.searchRes = []appstore.App{{Name: "Ev\til\nName\x1b[2J", BundleID: "b\x7f", Version: "1\r"}}
	wantExit(t, e.run("search", "x"), 0, e)
	out := e.out.String()
	if strings.Count(out, "\n") != 2 || strings.ContainsAny(out, "\t\r\x1b\x7f") {
		t.Fatalf("%q", out)
	}
}

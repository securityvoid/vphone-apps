package main

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

const searchUsage = "usage: vphone-apps search <term...>\nPrints NAME, ID (the bundle id for install --id), VERSION and PRICE.\nExit codes: 0 success, 1 failure, 2 usage error.\n"

const searchLimit = 25

func (a *app) search(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	words, code, done := a.parse(fs, searchUsage, args)
	if done {
		return code
	}
	term := joinTerm(words)
	if term == "" {
		return a.usageError(searchUsage, "missing search term")
	}
	st, err := a.newStore()
	if err != nil {
		return a.report(fail("search", "%s", err.Error()))
	}
	var results []appstore.App
	if f := bounded(ctx, a.to.Search, "search", func(c context.Context) (err error) {
		results, err = st.Search(c, term, searchLimit)
		return err
	}); f != nil {
		return a.report(f)
	}
	if len(results) == 0 {
		return a.report(fail("search", `no results for "%s"`, clean(term)))
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tVERSION\tPRICE")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", clean(r.Name), clean(r.BundleID), clean(r.Version), price(r.Price))
	}
	tw.Flush()
	fmt.Fprintln(a.stderr, "Install one with: vphone-apps install --id <ID> --vm <name>")
	return 0
}

func price(p float64) string {
	if p == 0 {
		return "Free"
	}
	return fmt.Sprintf("%.2f", p)
}

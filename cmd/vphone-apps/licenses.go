package main

import (
	"context"
	"fmt"

	"github.com/securityvoid/vphone-apps/internal/notice"
)

// licenses prints the embedded NOTICE.
func (a *app) licenses(ctx context.Context, args []string) int {
	fmt.Fprint(a.stdout, notice.Text)
	return 0
}

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// prompter reads interactive input. Prompts go to stderr.
type prompter interface {
	IsTerminal() bool
	Line(ctx context.Context, prompt string) (string, error)
	Secret(ctx context.Context, prompt string) (string, error)
}

// ttyPrompter prompts on a terminal. Every read runs in a goroutine so an
// interrupt (which the signal context swallows) can abandon it, and the
// terminal mode is restored on every exit from a secret prompt.
type ttyPrompter struct {
	in  *os.File
	out io.Writer
}

func (p *ttyPrompter) IsTerminal() bool { return term.IsTerminal(int(p.in.Fd())) }

type readResult struct {
	s   string
	err error
}

func (p *ttyPrompter) Line(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	ch := make(chan readResult, 1)
	go func() {
		s, err := bufio.NewReader(p.in).ReadString('\n')
		ch <- readResult{strings.TrimRight(s, "\r\n"), err}
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return "", ctx.Err()
	case r := <-ch:
		return r.s, r.err
	}
}

func (p *ttyPrompter) Secret(ctx context.Context, prompt string) (string, error) {
	fd := int(p.in.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, state)
	fmt.Fprint(p.out, prompt)
	ch := make(chan readResult, 1)
	go func() {
		b, err := term.ReadPassword(fd)
		ch <- readResult{string(b), err}
	}()
	select {
	case <-ctx.Done():
		term.Restore(fd, state)
		fmt.Fprintln(p.out)
		return "", ctx.Err()
	case r := <-ch:
		fmt.Fprintln(p.out)
		return r.s, r.err
	}
}

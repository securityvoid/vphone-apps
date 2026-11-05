package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
)

// The only programs the binary starts (NFR-PIPE-002).
const (
	sudoPath = "/usr/bin/sudo"
	openPath = "/usr/bin/open"
	// sudoPrompt is sudo's password prompt (passed with -p).
	sudoPrompt = "Mac login password (for sudo): "
)

var openURLRE = regexp.MustCompile(`^macappstore://apps\.apple\.com/app/id[0-9]+$`)

// runner starts an external program and returns its exit code (-1 when
// signalled). inherit connects the child to this process's stdio.
type runner interface {
	Run(ctx context.Context, path string, args []string, inherit bool) (exitCode int, err error)
}

// procRunner is the real runner. It starts only sudoPath and openPath, only
// with the argv the design allows; sudoBin and openBin are what actually
// runs (tests point them at a helper).
type procRunner struct{ sudoBin, openBin string }

func newProcRunner() *procRunner { return &procRunner{sudoBin: sudoPath, openBin: openPath} }

func (r *procRunner) Run(ctx context.Context, path string, args []string, inherit bool) (int, error) {
	var bin string
	switch path {
	case sudoPath:
		self, err := os.Executable()
		if err != nil {
			return -1, err
		}
		if len(args) < 4 || args[0] != "-p" || args[1] != sudoPrompt || args[2] != self || args[3] != "setup" {
			return -1, fmt.Errorf("refusing to run %s with these arguments", path)
		}
		bin = r.sudoBin
	case openPath:
		if len(args) != 1 || !openURLRE.MatchString(args[0]) {
			return -1, fmt.Errorf("refusing to run %s with these arguments", path)
		}
		bin = r.openBin
	default:
		return -1, fmt.Errorf("refusing to run %s", path)
	}

	var cmd *exec.Cmd
	if path == openPath { // open dies with ctx; sudo never does
		cmd = exec.CommandContext(ctx, bin, args...)
	} else {
		cmd = exec.Command(bin, args...)
	}
	if inherit {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	if inherit {
		sigs := make(chan os.Signal, 2)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		done := make(chan struct{})
		defer close(done)
		defer signal.Stop(sigs)
		go func() {
			for {
				select {
				case s := <-sigs:
					_ = cmd.Process.Signal(s)
				case <-done:
					return
				}
			}
		}()
	}
	err := cmd.Wait()
	if path == openPath && ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return -1, err
}

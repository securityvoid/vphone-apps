// Command vphone-apps searches the App Store, downloads and decrypts an app
// and installs it on a running vphone VM.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"github.com/DrWhax/unfairplay/pkg/setup"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/models"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
	"golang.org/x/term"
)

const usageText = `usage: vphone-apps <command> [flags]

commands:
  login                      sign in to the App Store (session shared with ipatool)
  search <term>              search the App Store
  install <term> --vm <name> download, decrypt and install an app on a VM
  setup                      one-time unfairplay enrollment
  doctor                     preflight checks
  --licenses                 show third-party licenses

Built on ipatool, unfairplay and vphone-cli — see --licenses
`

// command runs one subcommand and returns the process exit code.
type command func(a *app, ctx context.Context, args []string) int

// commands is the dispatch table; add a subcommand by adding an entry.
var commands = map[string]command{
	"login":      (*app).login,
	"search":     (*app).search,
	"install":    (*app).install,
	"setup":      (*app).setup,
	"doctor":     (*app).doctor,
	"--licenses": (*app).licenses,
}

type timeouts struct {
	Login, Search, Select, Push, Install, Launch time.Duration
	DoctorDial, DoctorRPC                        time.Duration
	AutoLookup, AutoOpen                         time.Duration
}

func defaultTimeouts() timeouts {
	return timeouts{
		Login: 120 * time.Second, Search: 120 * time.Second, Select: 120 * time.Second,
		Push: 30 * time.Minute, Install: 200 * time.Second, Launch: 30 * time.Second,
		DoctorDial: 5 * time.Second, DoctorRPC: 5 * time.Second,
		AutoLookup: 120 * time.Second, AutoOpen: 30 * time.Second,
	}
}

// app holds the process-level dependencies; tests replace them with fakes.
type app struct {
	stdout, stderr  io.Writer
	stderrTTY       bool // barMode: install redraws its progress bar
	prompt          prompter
	newStore        func() (Store, error)
	openStore       func() (Store, error) // read-only; creates nothing (doctor)
	newCache        func() (*cache.Cache, error)
	keep            func(*cache.Cache) (string, error)
	modelDir        func() (string, error)
	decrypt         func(fairplay.DecryptOptions) (fairplay.DecryptResult, error)
	dialVM          func(name string, timeout time.Duration) (*vm.Machine, error)
	geteuid         func() int
	executable      func() (string, error)
	proc            runner
	runSetup        func(context.Context, setup.Options) (setup.Result, error)
	removeApp       func(path string) error
	stat            func(path string) (os.FileInfo, error)
	pathExists      func(path string) bool
	verifyApp       func(path string) error
	externalVersion func(appPath string) (version string, ok bool)
	after           func(time.Duration) <-chan time.Time
	guestPath       func() (string, error)
	to              timeouts
}

func newApp() *app {
	a := &app{
		stdout:          os.Stdout,
		stderr:          os.Stderr,
		stderrTTY:       stderrBar(),
		prompt:          &ttyPrompter{in: os.Stdin, out: os.Stderr},
		newCache:        cache.New,
		keep:            (*cache.Cache).Keep,
		modelDir:        models.Dir,
		decrypt:         fairplay.DecryptIPA,
		dialVM:          vm.DialWithTimeout,
		geteuid:         os.Geteuid,
		executable:      os.Executable,
		proc:            newProcRunner(),
		runSetup:        setup.Run,
		removeApp:       removeApp,
		stat:            os.Stat,
		pathExists:      func(p string) bool { _, err := os.Lstat(p); return err == nil },
		verifyApp:       verifyOrganicMaps,
		externalVersion: readExternalVersion,
		after:           time.After,
		guestPath:       func() (string, error) { return vm.NewGuestPath(vm.RandReader) },
		to:              defaultTimeouts(),
	}
	a.newStore = func() (Store, error) {
		return store.New("")
	}
	a.openStore = func() (Store, error) {
		s, err := store.NewExisting()
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	return a
}

// stderrBar reports whether stderr gets the in-place progress bar.
func stderrBar() bool {
	fd := int(os.Stderr.Fd())
	if !term.IsTerminal(fd) {
		return false
	}
	w, _, err := term.GetSize(fd)
	return err == nil && barMode(true, os.Getenv("TERM"), w)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(newApp(), ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(a *app, ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.usageError(usageText, "missing command")
	}
	switch args[0] {
	case "--help", "-h":
		fmt.Fprint(a.stdout, usageText)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return a.usageError(usageText, fmt.Sprintf("unknown command %q", clean(args[0])))
	}
	return cmd(a, ctx, args[1:])
}

// usageError prints `error: usage: <msg>` and the usage text, exit code 2.
func (a *app) usageError(usage, msg string) int {
	fmt.Fprintf(a.stderr, "error: usage: %s\n%s", msg, usage)
	return 2
}

// parseFlags implements the CLI contract: flags may come before or after the
// positional words, so it parses, takes the first positional, and re-parses
// the rest. It returns the positional words.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return words, nil
		}
		if i := len(args) - fs.NArg() - 1; i >= 0 && args[i] == "--" {
			return append(words, fs.Args()...), nil
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// parse runs parseFlags and handles --help (exit 0) and bad flags (exit 2).
// done is true when the caller must return code.
func (a *app) parse(fs *flag.FlagSet, usage string, args []string) (words []string, code int, done bool) {
	words, err := parseFlags(fs, args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(a.stdout, usage)
		return nil, 0, true
	case err != nil:
		return nil, a.usageError(usage, clean(err.Error())), true
	}
	return words, 0, false
}

// joinTerm joins words with single spaces and trims; empty is a usage error.
func joinTerm(words []string) string { return strings.TrimSpace(strings.Join(words, " ")) }

// clean replaces every byte below 0x20 and 0x7f with '?'.
func clean(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 || c == 0x7f {
			b[i] = '?'
		}
	}
	return string(b)
}

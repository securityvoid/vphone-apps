package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/DrWhax/unfairplay/pkg/fairplay"
	"github.com/DrWhax/unfairplay/pkg/setup"
	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"github.com/securityvoid/vphone-apps/internal/cache"
	"github.com/securityvoid/vphone-apps/internal/store"
	"github.com/securityvoid/vphone-apps/internal/vm"
)

// fakeAS is a fake ipatool AppStore; unset methods panic (nil interface).
type fakeAS struct {
	appstore.AppStore
	mu    sync.Mutex
	calls []string

	loginFn     func(in appstore.LoginInput) error
	acctErr     error
	searchRes   []appstore.App
	searchErr   error
	searchLim   int64
	lookupApp   appstore.App
	lookupErr   error
	purchase    func() error
	downloadFn  func(in appstore.DownloadInput) error
	sinfErr     error
	lookupBlock chan struct{} // Lookup blocks until closed when set
	extID       string
	acctCalls   int // AccountInfo calls
}

func (f *fakeAS) rec(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeAS) callList() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func (f *fakeAS) Login(in appstore.LoginInput) (appstore.LoginOutput, error) {
	f.rec("login")
	if f.loginFn != nil {
		return appstore.LoginOutput{}, f.loginFn(in)
	}
	return appstore.LoginOutput{}, nil
}
func (f *fakeAS) AccountInfo() (appstore.AccountInfoOutput, error) {
	f.mu.Lock()
	f.acctCalls++
	f.mu.Unlock()
	return appstore.AccountInfoOutput{Account: appstore.Account{Email: "me@example.com"}}, f.acctErr
}
func (f *fakeAS) Search(in appstore.SearchInput) (appstore.SearchOutput, error) {
	f.rec("search")
	f.searchLim = in.Limit
	return appstore.SearchOutput{Results: f.searchRes}, f.searchErr
}
func (f *fakeAS) Lookup(in appstore.LookupInput) (appstore.LookupOutput, error) {
	f.rec("lookup")
	if f.lookupBlock != nil {
		<-f.lookupBlock
	}
	return appstore.LookupOutput{App: f.lookupApp}, f.lookupErr
}
func (f *fakeAS) Purchase(appstore.PurchaseInput) error {
	f.rec("purchase")
	if f.purchase != nil {
		return f.purchase()
	}
	return nil
}
func (f *fakeAS) Download(in appstore.DownloadInput) (appstore.DownloadOutput, error) {
	f.rec("download")
	f.extID = in.ExternalVersionID
	if f.downloadFn != nil {
		if err := f.downloadFn(in); err != nil {
			return appstore.DownloadOutput{}, err
		}
	} else if err := os.WriteFile(in.OutputPath, []byte("encrypted"), 0o644); err != nil {
		return appstore.DownloadOutput{}, err
	}
	return appstore.DownloadOutput{DestinationPath: in.OutputPath}, nil
}
func (f *fakeAS) ReplicateSinf(appstore.ReplicateSinfInput) error {
	f.rec("sinf")
	return f.sinfErr
}

// fakePrompt scripts prompt answers.
type fakePrompt struct {
	tty     bool
	lines   []string
	secrets []string
	asked   []string
	err     error
}

func (p *fakePrompt) IsTerminal() bool { return p.tty }
func (p *fakePrompt) Line(_ context.Context, prompt string) (string, error) {
	p.asked = append(p.asked, prompt)
	if p.err != nil {
		return "", p.err
	}
	s := p.lines[0]
	p.lines = p.lines[1:]
	return s, nil
}
func (p *fakePrompt) Secret(_ context.Context, prompt string) (string, error) {
	p.asked = append(p.asked, prompt)
	if p.err != nil {
		return "", p.err
	}
	s := p.secrets[0]
	p.secrets = p.secrets[1:]
	return s, nil
}

// fakeServer is a vphone.sock fake. handle receives each request line and
// returns the reply line ("" sends nothing and holds the connection open
// until the test ends).
type fakeServer struct {
	root string
	mu   sync.Mutex
	reqs []string
	done chan struct{}
}

func newServer(t *testing.T, handle func(req string) string) *fakeServer {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "vpa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	os.MkdirAll(filepath.Join(root, "v1"), 0o700)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "v1", "vphone.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{root: root, done: make(chan struct{})}
	t.Cleanup(func() { close(s.done); ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return // the dial probe
				}
				s.mu.Lock()
				s.reqs = append(s.reqs, line)
				s.mu.Unlock()
				if out := handle(line); out == closeConn {
					return
				} else if out != "" {
					c.Write([]byte(out))
					return
				}
				<-s.done
			}()
		}
	}()
	return s
}

func (s *fakeServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

const closeConn = "\x00close"

// happy answers push and apps.install/launch successfully.
func happy(req string) string {
	switch {
	case strings.Contains(req, `"t":"push"`):
		return `{"ok":true}` + "\n"
	case strings.Contains(req, `apps.install`):
		return `{"ok":true,"result":{"bundle_id":"com.example.app","upgraded":false}}` + "\n"
	case strings.Contains(req, `apps.launch`):
		return `{"ok":true,"result":{"pid":4242}}` + "\n"
	}
	return `{"ok":false,"error":"unexpected"}` + "\n"
}

type testEnv struct {
	a      *app
	out    *bytes.Buffer
	errb   *bytes.Buffer
	as     *fakeAS
	srv    *fakeServer
	cache  string // cache base dir
	models string
	prompt *fakePrompt
	steps  []string
	// store deadlines (zero = default)
	purchaseTO, downloadTO time.Duration
	run_                   *fakeRunner
	exists                 map[string]bool // pathExists answers; absent = false
	removed                []string
	// decryptFn can be replaced per test.
	decryptFn func(fairplay.DecryptOptions) (fairplay.DecryptResult, error)
}

var appFoo = appstore.App{ID: 1, Name: "Foo", BundleID: "com.example.app", Version: "1.2.3"}

func newEnv(t *testing.T, handle func(string) string) *testEnv {
	t.Helper()
	e := &testEnv{out: &bytes.Buffer{}, errb: &bytes.Buffer{}}
	e.run_ = &fakeRunner{}
	e.exists = map[string]bool{}
	e.as = &fakeAS{searchRes: []appstore.App{appFoo}, lookupApp: appFoo}
	e.srv = newServer(t, handle)
	e.cache = filepath.Join(t.TempDir(), "cache")
	e.models = t.TempDir()
	os.WriteFile(filepath.Join(e.models, "m.bin"), []byte("model"), 0o600)
	e.prompt = &fakePrompt{tty: true, lines: []string{"me@example.com"}, secrets: []string{"SENTINEL-PW"}}
	e.decryptFn = func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
		if err := os.WriteFile(o.OutputPath, []byte("decrypted"), 0o644); err != nil {
			return fairplay.DecryptResult{}, err
		}
		return fairplay.DecryptResult{}, os.WriteFile(o.ReportPath, []byte("{}"), 0o600)
	}
	e.a = &app{
		stdout: e.out, stderr: e.errb, prompt: e.prompt,
		newStore: func() (Store, error) {
			s := store.NewWithAppStore(e.as)
			s.PurchaseTimeout, s.DownloadTimeout = e.purchaseTO, e.downloadTO
			return s, nil
		},
		openStore: func() (Store, error) { return store.NewWithAppStore(e.as), nil },
		newCache:  func() (*cache.Cache, error) { return cache.NewIn(e.cache) },
		keep:      (*cache.Cache).Keep,
		modelDir:  func() (string, error) { return e.models, nil },
		decrypt: func(o fairplay.DecryptOptions) (fairplay.DecryptResult, error) {
			e.steps = append(e.steps, "decrypt")
			return e.decryptFn(o)
		},
		dialVM: func(name string, d time.Duration) (*vm.Machine, error) {
			e.steps = append(e.steps, "vm")
			return vm.DialInWithTimeout(e.srv.root, name, d)
		},
		geteuid:         func() int { return 0 },
		executable:      func() (string, error) { return "/bin/vphone-apps", nil },
		proc:            e.run_,
		removeApp:       func(p string) error { e.removed = append(e.removed, p); return nil },
		stat:            os.Stat,
		pathExists:      func(p string) bool { return e.exists[p] },
		verifyApp:       func(string) error { return nil },
		externalVersion: func(string) (string, bool) { return "123456", true },
		after:           func(time.Duration) <-chan time.Time { c := make(chan time.Time, 1); c <- time.Time{}; return c },
		runSetup: func(context.Context, setup.Options) (setup.Result, error) {
			return setup.Result{}, errors.New("runSetup not injected")
		},
		guestPath: func() (string, error) { return "/private/var/tmp/vphone-apps-0123456789abcdef.ipa", nil },
		to:        defaultTimeouts(),
	}
	return e
}

func (e *testEnv) run(args ...string) int { return e.runCtx(context.Background(), args...) }
func (e *testEnv) runCtx(ctx context.Context, args ...string) int {
	return run(e.a, ctx, args)
}

// cacheEntries lists what is left in the cache base directory.
func (e *testEnv) cacheEntries(t *testing.T) []string {
	t.Helper()
	es, err := os.ReadDir(e.cache)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, x := range es {
		names = append(names, x.Name())
	}
	return names
}

func (e *testEnv) errLines() []string {
	var l []string
	for _, ln := range strings.Split(e.errb.String(), "\n") {
		if strings.HasPrefix(ln, "error:") {
			l = append(l, ln)
		}
	}
	return l
}

func wantExit(t *testing.T, got, want int, e *testEnv) {
	t.Helper()
	if got != want {
		t.Fatalf("exit %d want %d\nstdout:\n%s\nstderr:\n%s", got, want, e.out, e.errb)
	}
}

// wantErr compares the raw stderr.
func wantErr(t *testing.T, e *testEnv, stderr string) {
	t.Helper()
	if got := e.errb.String(); got != stderr {
		t.Fatalf("stderr\n got: %q\nwant: %q", got, stderr)
	}
}

// installing is the selection line of appFoo.
const installing = "Installing: Foo (com.example.app) 1.2.3\n"

// wantStepErr compares stderr after a failure in step number n (1-based) of
// a plain-mode install: the selection line, the step lines up to step n, then
// the error lines.
func wantStepErr(t *testing.T, e *testEnv, n int, stderr string) {
	t.Helper()
	names := []string{"Downloading", "Decrypting", "Copying to VM", "Installing", "Launching"}
	total := 4
	if strings.Contains(e.errb.String(), "/5]") {
		total = 5
	}
	want := installing
	for i := 0; i < n; i++ {
		want += fmt.Sprintf("[%d/%d] %s\n", i+1, total, names[i])
	}
	wantErr(t, e, want+stderr)
}

func storeOf(as appstore.AppStore) Store { return store.NewWithAppStore(as) }

// fakeRunner records external program calls; fn scripts the answer.
type fakeRunner struct {
	mu    sync.Mutex
	calls []runCall
	fn    func(ctx context.Context, c runCall) (int, error)
}

type runCall struct {
	path    string
	args    []string
	inherit bool
}

func (c runCall) String() string { return c.path + " " + strings.Join(c.args, " ") }

func (r *fakeRunner) Run(ctx context.Context, path string, args []string, inherit bool) (int, error) {
	c := runCall{path, append([]string(nil), args...), inherit}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	if r.fn != nil {
		return r.fn(ctx, c)
	}
	return 0, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

var keyringNotFound = keyring.ErrKeyNotFound

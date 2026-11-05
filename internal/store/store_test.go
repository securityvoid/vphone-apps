package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
)

type fakeAS struct {
	appstore.AppStore
	calls []string

	loginErr, acctErr, searchErr, lookupErr, purchaseErr, downloadErr, sinfErr error
	searchRes                                                                  []appstore.App
	searchLimit                                                                int64
	blockDownload                                                              bool
	extID                                                                      string
	onDownload                                                                 func(appstore.DownloadInput)
}

func (f *fakeAS) Login(appstore.LoginInput) (appstore.LoginOutput, error) {
	f.calls = append(f.calls, "login")
	return appstore.LoginOutput{}, f.loginErr
}
func (f *fakeAS) AccountInfo() (appstore.AccountInfoOutput, error) {
	return appstore.AccountInfoOutput{Account: appstore.Account{Email: "e"}}, f.acctErr
}
func (f *fakeAS) Search(in appstore.SearchInput) (appstore.SearchOutput, error) {
	f.searchLimit = in.Limit
	return appstore.SearchOutput{Results: f.searchRes}, f.searchErr
}
func (f *fakeAS) Lookup(appstore.LookupInput) (appstore.LookupOutput, error) {
	return appstore.LookupOutput{}, f.lookupErr
}
func (f *fakeAS) Purchase(appstore.PurchaseInput) error {
	f.calls = append(f.calls, "purchase")
	return f.purchaseErr
}
func (f *fakeAS) Download(in appstore.DownloadInput) (appstore.DownloadOutput, error) {
	f.calls = append(f.calls, "download")
	f.extID = in.ExternalVersionID
	if f.onDownload != nil {
		f.onDownload(in)
	}
	if f.blockDownload {
		<-in.Context.Done()
		return appstore.DownloadOutput{}, in.Context.Err()
	}
	if f.downloadErr != nil {
		return appstore.DownloadOutput{}, f.downloadErr
	}
	return appstore.DownloadOutput{DestinationPath: in.OutputPath}, os.WriteFile(in.OutputPath, []byte("ipa"), 0o644)
}
func (f *fakeAS) ReplicateSinf(in appstore.ReplicateSinfInput) error {
	f.calls = append(f.calls, "sinf:"+filepath.Base(in.PackagePath))
	return f.sinfErr
}

func TestLoginAuthCodeUnchanged(t *testing.T) {
	s := NewWithAppStore(&fakeAS{loginErr: appstore.ErrAuthCodeRequired})
	if err := s.Login(context.Background(), "a", "b", ""); err != ErrAuthCodeRequired {
		t.Fatal(err)
	}
}

func TestAccountNotLoggedIn(t *testing.T) {
	s := NewWithAppStore(&fakeAS{acctErr: fmt.Errorf("failed to get account: %w", keyring.ErrKeyNotFound)})
	if _, err := s.Account(); err != ErrNotLoggedIn {
		t.Fatal(err)
	}
}

func TestSessionExpiredMapping(t *testing.T) {
	exp := appstore.ErrPasswordTokenExpired
	ctx := context.Background()
	app := appstore.App{}
	cases := map[string]func() error{
		"login": func() error { return NewWithAppStore(&fakeAS{loginErr: exp}).Login(ctx, "a", "b", "") },
		"search": func() error {
			_, e := NewWithAppStore(&fakeAS{searchErr: exp}).Search(ctx, "x", 25)
			return e
		},
		"lookup": func() error { _, e := NewWithAppStore(&fakeAS{lookupErr: exp}).Lookup(ctx, "x"); return e },
		"purchase": func() error {
			return NewWithAppStore(&fakeAS{purchaseErr: exp}).Download(ctx, app, filepath.Join(t.TempDir(), "a"), true, "")
		},
		"download": func() error {
			return NewWithAppStore(&fakeAS{downloadErr: exp}).Download(ctx, app, filepath.Join(t.TempDir(), "a"), false, "")
		},
	}
	for n, f := range cases {
		if err := f(); !errors.Is(err, ErrSessionExpired) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestAccountNeverExpired(t *testing.T) {
	_, err := NewWithAppStore(&fakeAS{acctErr: appstore.ErrPasswordTokenExpired}).Account()
	if err == nil || errors.Is(err, ErrSessionExpired) {
		t.Fatal(err)
	}
}

func TestSearchLimit(t *testing.T) {
	f := &fakeAS{searchRes: make([]appstore.App, 30)}
	got, err := NewWithAppStore(f).Search(context.Background(), "x", 25)
	if err != nil || len(got) != 25 || f.searchLimit != 25 {
		t.Fatal(len(got), f.searchLimit, err)
	}
}

func TestSearchAbandonsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	block := &blockSearch{release: make(chan struct{})}
	defer close(block.release)
	start := time.Now()
	_, err := NewWithAppStore(block).Search(ctx, "x", 5)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal(err)
	}
}

type blockSearch struct {
	appstore.AppStore
	release chan struct{}
}

func (b *blockSearch) AccountInfo() (appstore.AccountInfoOutput, error) {
	return appstore.AccountInfoOutput{}, nil
}
func (b *blockSearch) Search(appstore.SearchInput) (appstore.SearchOutput, error) {
	<-b.release
	return appstore.SearchOutput{}, nil
}

func TestDownloadNotOwned(t *testing.T) {
	out := filepath.Join(t.TempDir(), "app.ipa")
	err := NewWithAppStore(&fakeAS{downloadErr: appstore.ErrLicenseRequired}).Download(context.Background(), appstore.App{}, out, false, "")
	if err != ErrNotOwned {
		t.Fatal(err)
	}
}

func TestDownloadPurchaseOrderAndAlreadyOwned(t *testing.T) {
	out := filepath.Join(t.TempDir(), "app.ipa")
	f := &fakeAS{purchaseErr: appstore.ErrLicenseAlreadyExists}
	if err := NewWithAppStore(f).Download(context.Background(), appstore.App{}, out, true, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"purchase", "download", "sinf:app.ipa.part"}
	if fmt.Sprint(f.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %v", f.calls)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(err, info)
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part remains")
	}
}

func TestDownloadPurchaseOtherError(t *testing.T) {
	f := &fakeAS{purchaseErr: errors.New("boom")}
	err := NewWithAppStore(f).Download(context.Background(), appstore.App{}, filepath.Join(t.TempDir(), "a"), true, "")
	if err == nil || err.Error() != "boom" {
		t.Fatal(err)
	}
	if fmt.Sprint(f.calls) != "[purchase]" {
		t.Fatal(f.calls)
	}
}

func TestDownloadNoPurchaseWhenNotRequested(t *testing.T) {
	f := &fakeAS{}
	NewWithAppStore(f).Download(context.Background(), appstore.App{}, filepath.Join(t.TempDir(), "a"), false, "")
	if fmt.Sprint(f.calls) != "[download sinf:a.part]" {
		t.Fatal(f.calls)
	}
}

func TestDownloadSinfErrorLeavesNoIPA(t *testing.T) {
	out := filepath.Join(t.TempDir(), "app.ipa")
	err := NewWithAppStore(&fakeAS{sinfErr: errors.New("sinf")}).Download(context.Background(), appstore.App{}, out, false, "")
	if err == nil {
		t.Fatal("want error")
	}
	for _, p := range []string{out, out + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s remains", p)
		}
	}
}

func TestDownloadBlockingReturnsOnDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := NewWithAppStore(&fakeAS{blockDownload: true}).Download(ctx, appstore.App{}, filepath.Join(t.TempDir(), "a"), false, "")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal(err)
	}
}

func TestPurchaseTimeout(t *testing.T) {
	b := &blockPurchase{release: make(chan struct{})}
	defer close(b.release)
	s := NewWithAppStore(b)
	s.PurchaseTimeout = 50 * time.Millisecond
	err := s.Download(context.Background(), appstore.App{}, filepath.Join(t.TempDir(), "a"), true, "")
	var te *TimeoutError
	if !errors.As(err, &te) || err.Error() != "timed out after 50ms" {
		t.Fatal(err)
	}
}

func TestDownloadTimeout(t *testing.T) {
	s := NewWithAppStore(&fakeAS{blockDownload: true})
	s.DownloadTimeout = 50 * time.Millisecond
	start := time.Now()
	err := s.Download(context.Background(), appstore.App{}, filepath.Join(t.TempDir(), "a"), false, "")
	if err == nil || err.Error() != "timed out after 50ms" || time.Since(start) > 2*time.Second {
		t.Fatal(err)
	}
}

func TestDownloadExternalVersionID(t *testing.T) {
	for _, id := range []string{"", "123456"} {
		f := &fakeAS{}
		if err := NewWithAppStore(f).Download(context.Background(), appstore.App{}, filepath.Join(t.TempDir(), "a"), false, id); err != nil {
			t.Fatal(err)
		}
		if f.extID != id {
			t.Errorf("got %q want %q", f.extID, id)
		}
	}
}

type blockPurchase struct {
	appstore.AppStore
	release chan struct{}
}

func (b *blockPurchase) AccountInfo() (appstore.AccountInfoOutput, error) {
	return appstore.AccountInfoOutput{}, nil
}
func (b *blockPurchase) Purchase(appstore.PurchaseInput) error { <-b.release; return nil }

// The WithProgress hook sees ipatool's progress bar (bytes, total) polled
// while Download runs; Download is held until the hook has seen the bar.
func TestDownloadProgressHook(t *testing.T) {
	seen := make(chan [2]int64, 1)
	f := &fakeAS{onDownload: func(in appstore.DownloadInput) {
		in.Progress.ChangeMax64(100)
		in.Progress.Set64(40)
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
		}
	}}
	got := make(chan [2]int64, 1)
	ctx := WithProgress(context.Background(), func(c, m int64) {
		select {
		case got <- [2]int64{c, m}:
			seen <- [2]int64{c, m}
		default:
		}
	})
	if err := NewWithAppStore(f).Download(ctx, appstore.App{}, filepath.Join(t.TempDir(), "a"), false, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-got:
		if v != [2]int64{40, 100} {
			t.Fatalf("hook saw %v", v)
		}
	default:
		t.Fatal("hook never called")
	}
}

// The poller is joined when Download returns: a hook still running (it takes
// 100 ms) has finished by then, and the hook was called at least once.
func TestDownloadProgressJoined(t *testing.T) {
	called := make(chan struct{})
	var once sync.Once
	f := &fakeAS{onDownload: func(in appstore.DownloadInput) {
		in.Progress.ChangeMax64(100)
		in.Progress.Set64(10)
		select {
		case <-called:
		case <-time.After(5 * time.Second):
		}
	}}
	var running, calls atomic.Int32
	ctx := WithProgress(context.Background(), func(c, m int64) {
		running.Add(1)
		calls.Add(1)
		once.Do(func() { close(called) })
		time.Sleep(100 * time.Millisecond)
		running.Add(-1)
	})
	if err := NewWithAppStore(f).Download(ctx, appstore.App{}, filepath.Join(t.TempDir(), "a"), false, ""); err != nil {
		t.Fatal(err)
	}
	if running.Load() != 0 {
		t.Fatal("hook still running after Download returned")
	}
	if calls.Load() == 0 {
		t.Fatal("hook never called")
	}
}

// Without a WithProgress hook no poller runs and Download completes normally.
func TestDownloadNoHook(t *testing.T) {
	f := &fakeAS{onDownload: func(in appstore.DownloadInput) {
		in.Progress.ChangeMax64(100)
		in.Progress.Set64(100)
	}}
	out := filepath.Join(t.TempDir(), "a")
	if err := NewWithAppStore(f).Download(context.Background(), appstore.App{}, out, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("no output: %v", err)
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part left: %v", err)
	}
}

type countRing struct {
	items map[string]keyring.Item
	ops   []string
}

func (c *countRing) Get(k string) (keyring.Item, error) {
	c.ops = append(c.ops, "get:"+k)
	if it, ok := c.items[k]; ok {
		return it, nil
	}
	return keyring.Item{}, keyring.ErrKeyNotFound
}
func (c *countRing) Set(it keyring.Item) error {
	c.ops = append(c.ops, "set:"+it.Key)
	c.items[it.Key] = it
	return nil
}
func (c *countRing) Remove(k string) error {
	c.ops = append(c.ops, "remove:"+k)
	delete(c.items, k)
	return nil
}

func (c *countRing) count(op string) int {
	n := 0
	for _, o := range c.ops {
		if o == op {
			n++
		}
	}
	return n
}

func TestQuietRingReadsAccountOnce(t *testing.T) {
	c := &countRing{items: map[string]keyring.Item{"account": {Key: "account", Data: []byte("x")}}}
	q := newQuietRing(c)
	// Lookup + Download equivalent: AccountInfo reads, kbsync miss/set/get/remove.
	for i := 0; i < 3; i++ {
		if it, err := q.Get("account"); err != nil || string(it.Data) != "x" {
			t.Fatalf("get: %v", err)
		}
	}
	if _, err := q.Get(kbsyncKey); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("kbsync miss: %v", err)
	}
	if err := q.Set(keyring.Item{Key: kbsyncKey, Data: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if it, err := q.Get(kbsyncKey); err != nil || string(it.Data) != "k" {
		t.Fatalf("kbsync get: %v", err)
	}
	if err := q.Remove(kbsyncKey); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Get(kbsyncKey); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("kbsync after remove: %v", err)
	}
	if n := c.count("get:account"); n != 1 {
		t.Fatalf("account reads = %d, want 1", n)
	}
	for _, o := range c.ops {
		if o != "get:account" {
			t.Fatalf("unexpected real keyring op %q", o)
		}
	}
}

func TestQuietRingSetRemoveAndNotFound(t *testing.T) {
	c := &countRing{items: map[string]keyring.Item{}}
	q := newQuietRing(c)
	if _, err := q.Get("account"); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
	if err := q.Set(keyring.Item{Key: "account", Data: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if c.count("set:account") != 1 {
		t.Fatal("Set must write through")
	}
	if it, _ := q.Get("account"); string(it.Data) != "a" {
		t.Fatal("cache not refreshed by Set")
	}
	if c.count("get:account") != 1 { // only the initial miss
		t.Fatalf("ops %v", c.ops)
	}
	if err := q.Remove("account"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Get("account"); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("after Remove: %v", err)
	}
	if c.count("remove:account") != 1 || c.count("get:account") != 2 {
		t.Fatalf("Remove must invalidate: %v", c.ops)
	}
}

// Through the real construction path (build), repeated account reads hit the
// real keyring once and kbsync never reaches it.
func TestBuildWiresQuietRing(t *testing.T) {
	acct, err := json.Marshal(appstore.Account{Email: "a@b.c", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	c := &countRing{items: map[string]keyring.Item{"account": {Key: "account", Data: acct}}}
	s, err := build(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		a, err := s.Account()
		if err != nil || a.Email != "a@b.c" {
			t.Fatalf("account: %+v %v", a, err)
		}
	}
	if n := c.count("get:account"); n != 1 {
		t.Fatalf("get:account = %d, want 1 (ops %v)", n, c.ops)
	}
	for _, o := range c.ops {
		if o != "get:account" {
			t.Fatalf("unexpected real keyring op %q", o)
		}
	}
}

func TestQuietRingMissNotCached(t *testing.T) {
	c := &countRing{items: map[string]keyring.Item{}}
	q := newQuietRing(c)
	if _, err := q.Get("account"); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("miss: %v", err)
	}
	c.items["account"] = keyring.Item{Key: "account", Data: []byte("late")}
	if it, err := q.Get("account"); err != nil || string(it.Data) != "late" {
		t.Fatalf("after external insert: %v %q", err, it.Data)
	}
}

type failSetRing struct{ countRing }

func (f *failSetRing) Set(it keyring.Item) error {
	f.ops = append(f.ops, "set:"+it.Key)
	return errors.New("denied")
}

func TestQuietRingFailedSetNotCached(t *testing.T) {
	c := &failSetRing{countRing{items: map[string]keyring.Item{"account": {Key: "account", Data: []byte("old")}}}}
	q := newQuietRing(c)
	if _, err := q.Get("account"); err != nil {
		t.Fatal(err)
	}
	if err := q.Set(keyring.Item{Key: "account", Data: []byte("new")}); err == nil {
		t.Fatal("want Set error")
	}
	it, err := q.Get("account")
	if err != nil || string(it.Data) != "old" {
		t.Fatalf("got %q %v, want real ring value", it.Data, err)
	}
	if n := c.count("get:account"); n != 2 {
		t.Fatalf("get:account = %d, want 2 (stale cache entry?)", n)
	}
}

func TestQuietRingRemoveAbsentKbsync(t *testing.T) {
	c := &countRing{items: map[string]keyring.Item{}}
	if err := newQuietRing(c).Remove(kbsyncKey); !errors.Is(err, keyring.ErrKeyNotFound) {
		t.Fatalf("got %v, want ErrKeyNotFound", err)
	}
	if len(c.ops) != 0 {
		t.Fatalf("real ring touched: %v", c.ops)
	}
}

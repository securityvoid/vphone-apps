package store

import (
	"errors"
	"os"
	"testing"

	"github.com/byteness/keyring"
)

// memRing is an in-memory keyring.
type memRing struct {
	items             map[string]keyring.Item
	getErr, removeErr error
	sets              int
}

func newMemRing() *memRing { return &memRing{items: map[string]keyring.Item{}} }

func (r *memRing) Get(k string) (keyring.Item, error) {
	if r.getErr != nil {
		return keyring.Item{}, r.getErr
	}
	it, ok := r.items[k]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return it, nil
}
func (r *memRing) GetMetadata(string) (keyring.Metadata, error) { return keyring.Metadata{}, nil }
func (r *memRing) Set(it keyring.Item) error                    { r.sets++; r.items[it.Key] = it; return nil }
func (r *memRing) Remove(k string) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	if _, ok := r.items[k]; !ok {
		return keyring.ErrKeyNotFound
	}
	delete(r.items, k)
	return nil
}
func (r *memRing) Keys() ([]string, error) { return nil, nil }

func TestCredsRoundTrip(t *testing.T) {
	ring := newMemRing()
	s := NewWithCreds(&fakeAS{}, ring)
	if _, _, err := s.LoadCredentials(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("empty: %v", err)
	}
	if err := s.SaveCredentials("me@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	if got := string(ring.items["apple-account"].Data); got != `{"email":"me@example.com","password":"pw"}` {
		t.Fatalf("item %s", got)
	}
	e, p, err := s.LoadCredentials()
	if err != nil || e != "me@example.com" || p != "pw" {
		t.Fatalf("%q %q %v", e, p, err)
	}
	if err := s.ForgetCredentials(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoadCredentials(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("after forget: %v", err)
	}
	if err := s.ForgetCredentials(); err != nil {
		t.Fatalf("forget absent: %v", err)
	}
}

func TestCredsBadItems(t *testing.T) {
	for _, data := range []string{"not json", `{"email":"","password":"x"}`, `{"email":"a","password":""}`, `{}`, ``} {
		ring := newMemRing()
		ring.items["apple-account"] = keyring.Item{Key: "apple-account", Data: []byte(data)}
		_, _, err := NewWithCreds(&fakeAS{}, ring).LoadCredentials()
		if !errors.Is(err, ErrBadCredentials) {
			t.Errorf("%q: %v", data, err)
		}
	}
}

func TestCredsReadErrorIsNeitherNoneNorBad(t *testing.T) {
	ring := newMemRing()
	ring.getErr = errors.New("keychain locked")
	_, _, err := NewWithCreds(&fakeAS{}, ring).LoadCredentials()
	if err == nil || errors.Is(err, ErrNoCredentials) || errors.Is(err, ErrBadCredentials) {
		t.Fatalf("%v", err)
	}
}

func TestCredsRemoveError(t *testing.T) {
	ring := newMemRing()
	ring.removeErr = errors.New("denied")
	if err := NewWithCreds(&fakeAS{}, ring).ForgetCredentials(); err == nil {
		t.Fatal("want error")
	}
}

// openCreds asks for the Keychain backend only; with it unavailable the
// methods fail and no file is written.
func TestCredsOpenKeychainOnlyAndUnavailable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var cfg keyring.Config
	s := NewWithAppStore(&fakeAS{})
	err := s.openCreds(func(c keyring.Config) (keyring.Keyring, error) {
		cfg = c
		return nil, errors.New("keychain unavailable")
	})
	if err == nil {
		t.Fatal("want error")
	}
	if len(cfg.AllowedBackends) != 1 || cfg.AllowedBackends[0] != keyring.KeychainBackend || cfg.ServiceName != "vphone-apps" || !cfg.KeychainTrustApplication {
		t.Fatalf("%+v", cfg)
	}
	if err := s.SaveCredentials("a", "b"); err == nil {
		t.Fatal("save must fail")
	}
	if _, _, err := s.LoadCredentials(); err == nil || errors.Is(err, ErrNoCredentials) {
		t.Fatalf("load: %v", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("files written: %v", es)
	}
}

func TestCredsOpenSetsRing(t *testing.T) {
	ring := newMemRing()
	s := NewWithAppStore(&fakeAS{})
	if err := s.openCreds(func(keyring.Config) (keyring.Keyring, error) { return ring, nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCredentials("a", "b"); err != nil || ring.sets != 1 {
		t.Fatal(err, ring.sets)
	}
}

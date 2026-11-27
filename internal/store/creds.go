package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteness/keyring"
	"github.com/majd/ipatool/v2/pkg/appstore"
)

const (
	credsService = "vphone-apps"
	credsKey     = "apple-account"
)

var (
	// ErrNoCredentials means no credentials item is saved.
	ErrNoCredentials = errors.New("no saved credentials")
	// ErrBadCredentials means the saved item is malformed or has an empty
	// email or password.
	ErrBadCredentials = errors.New("saved credentials are malformed or incomplete")

	errNoKeychain = errors.New("the macOS Keychain is not available")
)

type savedCreds struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// NewWithCreds is NewWithAppStore with a credentials keyring (tests inject
// fakes here).
func NewWithCreds(as appstore.AppStore, ring keyring.Keyring) *Store {
	s := NewWithAppStore(as)
	s.creds = ring
	return s
}

// openCreds opens the saved-credentials keyring on the Keychain backend only
// (no SecretService or file fallback) and sets s.creds.
func (s *Store) openCreds(open func(keyring.Config) (keyring.Keyring, error)) error {
	ring, err := open(keyring.Config{
		AllowedBackends:          []keyring.BackendType{keyring.KeychainBackend},
		ServiceName:              credsService,
		KeychainTrustApplication: true,
	})
	if err != nil {
		return fmt.Errorf("open keychain: %w", err)
	}
	s.creds = ring
	return nil
}

// SaveCredentials stores the Apple Account email and password in the Keychain.
func (s *Store) SaveCredentials(email, password string) error {
	if s.creds == nil {
		return errNoKeychain
	}
	data, err := json.Marshal(savedCreds{email, password})
	if err != nil {
		return err
	}
	return s.creds.Set(keyring.Item{Key: credsKey, Data: data, Label: credsService})
}

// LoadCredentials returns the saved email and password: ErrNoCredentials when
// absent, ErrBadCredentials for a malformed item or an empty field.
func (s *Store) LoadCredentials() (email, password string, err error) {
	if s.creds == nil {
		return "", "", errNoKeychain
	}
	it, err := s.creds.Get(credsKey)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return "", "", ErrNoCredentials
	}
	if err != nil {
		return "", "", err
	}
	var c savedCreds
	if json.Unmarshal(it.Data, &c) != nil || c.Email == "" || c.Password == "" {
		return "", "", ErrBadCredentials
	}
	return c.Email, c.Password, nil
}

// ForgetCredentials deletes the saved item; an absent item is not an error.
func (s *Store) ForgetCredentials() error {
	if s.creds == nil {
		return errNoKeychain
	}
	if err := s.creds.Remove(credsKey); err != nil && !errors.Is(err, keyring.ErrKeyNotFound) {
		return err
	}
	return nil
}

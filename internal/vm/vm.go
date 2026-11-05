// Package vm is a client for the vphone VM control socket
// (~/.vphone/machines/<name>/vphone.sock): one request per connection, one
// JSON line each way.
package vm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/securityvoid/vphone-apps/internal/dur"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	maxName         = 64
	maxReply        = 1 << 20
	defaultPushWait = 30 * time.Minute
	defaultDialWait = 5 * time.Second
)

var (
	ErrInvalidName   = errors.New("invalid VM name")
	ErrNoSuchVM      = errors.New("no such VM")
	ErrNotRunning    = errors.New("VM is not running")
	ErrDialTimeout   = errors.New("timed out connecting")
	ErrReplyTooLarge = errors.New("reply too large")
)

// TimeoutError reports that no reply arrived within D.
type TimeoutError struct{ D time.Duration }

func (e *TimeoutError) Error() string { return "timed out after " + dur.Format(e.D) }

// GuestError is the optional guest_error object of a failed reply.
type GuestError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

// RemoteError is an {"ok":false,...} reply.
type RemoteError struct {
	Msg   string
	Guest *GuestError
}

func (e *RemoteError) Error() string { return e.Msg }

type reply struct {
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error"`
	GuestError *GuestError     `json:"guest_error"`
}

// Machine is a running VM reachable through its control socket.
type Machine struct {
	sock string
	// PushDeadline bounds the push reply (default 30 min); tests inject less.
	PushDeadline time.Duration
	// DialTimeout bounds each connect (default 5 s).
	DialTimeout time.Duration
}

// DialWithTimeout validates name under ~/.vphone/machines and checks that the
// VM's socket accepts a connection (d <= 0 means the default timeout).
func DialWithTimeout(name string, d time.Duration) (*Machine, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".vphone", "machines")
	return DialInWithTimeout(root, name, d)
}

// DialInWithTimeout is DialWithTimeout with an explicit machines directory.
func DialInWithTimeout(root, name string, d time.Duration) (*Machine, error) {
	if d <= 0 {
		d = defaultDialWait
	}
	if name == "" || name == "." || len(name) > maxName ||
		strings.ContainsAny(name, "/\x00") || strings.Contains(name, "..") {
		return nil, ErrInvalidName
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoSuchVM
		}
		return nil, err
	}
	m := &Machine{sock: filepath.Join(dir, "vphone.sock"), PushDeadline: defaultPushWait, DialTimeout: d}
	conn, err := m.connect(context.Background())
	if err != nil {
		return nil, err
	}
	conn.Close()
	return m, nil
}

func (m *Machine) connect(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: m.DialTimeout}
	conn, err := d.DialContext(ctx, "unix", m.sock)
	if err != nil {
		var ne net.Error
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, syscall.ECONNREFUSED):
			return nil, ErrNotRunning
		case errors.As(err, &ne) && ne.Timeout():
			return nil, ErrDialTimeout
		}
		return nil, err
	}
	return conn, nil
}

// NewGuestPath returns /private/var/tmp/vphone-apps-<16 hex>.ipa.
func NewGuestPath(r io.Reader) (string, error) {
	b := make([]byte, 8)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return "/private/var/tmp/vphone-apps-" + hex.EncodeToString(b) + ".ipa", nil
}

// RandReader is crypto/rand, for NewGuestPath.
var RandReader io.Reader = rand.Reader

func marshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil // Encode appends the newline
}

// exchange opens a new connection, sends one request line and reads one
// reply line, all within deadline. A cancelled ctx closes the connection.
func (m *Machine) exchange(ctx context.Context, req any, deadline time.Duration) (*reply, error) {
	line, err := marshalLine(req)
	if err != nil {
		return nil, err
	}
	conn, err := m.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(deadline))

	fail := func(err error) (*reply, error) {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, os.ErrDeadlineExceeded):
			return nil, &TimeoutError{D: deadline}
		}
		return nil, err
	}
	if _, err := conn.Write(line); err != nil {
		return fail(err)
	}
	data, err := bufio.NewReader(io.LimitReader(conn, maxReply+2)).ReadBytes('\n')
	if len(bytes.TrimSuffix(data, []byte("\n"))) > maxReply {
		return nil, ErrReplyTooLarge
	}
	if err != nil && !(errors.Is(err, io.EOF) && len(data) > 0) {
		if errors.Is(err, io.EOF) {
			return fail(errors.New("connection closed before a reply"))
		}
		return fail(err)
	}
	var r reply
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("malformed reply: %w", err)
	}
	if !r.OK {
		msg := r.Error
		if msg == "" {
			msg = "request failed"
		}
		return nil, &RemoteError{Msg: msg, Guest: r.GuestError}
	}
	return &r, nil
}

type pushReq struct {
	T     string `json:"t"`
	Local string `json:"local"`
	Path  string `json:"path"`
}

// Push asks vphoned to copy the host file local to the guest path.
func (m *Machine) Push(ctx context.Context, local, guest string) error {
	abs, err := filepath.Abs(local)
	if err != nil {
		return err
	}
	_, err = m.exchange(ctx, pushReq{T: "push", Local: abs, Path: guest}, m.PushDeadline)
	return err
}

type rpcReq struct {
	T      string `json:"t"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// RPC calls method on the guest daemon and returns the raw result object.
func (m *Machine) RPC(ctx context.Context, method string, params any, deadline time.Duration) (json.RawMessage, error) {
	r, err := m.exchange(ctx, rpcReq{T: "rpc", Method: method, Params: params}, deadline)
	if err != nil {
		return nil, err
	}
	return r.Result, nil
}

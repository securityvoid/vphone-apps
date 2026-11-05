package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

func TestStepDisplayPlain(t *testing.T) {
	var b bytes.Buffer
	p := newStepDisplay(&b, false, 2)
	p.step("Downloading")
	p.progress()(5, 10) // ignored without a terminal
	p.step("Decrypting")
	p.done()
	if b.String() != "[1/2] Downloading\n[2/2] Decrypting\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestStepDisplayTTY(t *testing.T) {
	var b bytes.Buffer
	p := newStepDisplay(&b, true, 4)
	p.step("Downloading")
	hook := p.progress()
	hook(5, 10)
	p.step("Decrypting")
	hook(9, 10) // stale: the step changed
	p.done()
	bar := func(fill int, label string) string {
		return "[" + strings.Repeat("=", fill) + strings.Repeat(" ", barWidth-fill) + "] " + label
	}
	want := "\r\x1b[K[1/4] Downloading\n\r\x1b[K" + bar(0, "1/4") +
		"\x1b[1A\r\x1b[K[1/4] Downloading\n\r\x1b[K" + bar(15, "50%") +
		"\x1b[1A\r\x1b[K[2/4] Decrypting\n\r\x1b[K" + bar(7, "2/4") + "\n"
	if b.String() != want {
		t.Fatalf("got  %q\nwant %q", b.String(), want)
	}
}

// With a terminal, install drives the bar from the download hook and ends
// the in-place display with a newline before the result lines.
func TestInstallTTYProgress(t *testing.T) {
	e := newEnv(t, happy)
	e.a.stderrTTY = true
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 0, e)
	got := e.errb.String()
	if !strings.HasPrefix(got, "Installing: Foo (com.example.app) 1.2.3\n\r\x1b[K[1/4] Downloading\n") ||
		!strings.Contains(got, "[4/4] Installing\n") || !strings.HasSuffix(got, "\n") || e.out.String() != "installed: com.example.app\n" {
		t.Fatalf("%q %q", got, e.out)
	}
}

func TestInstallLaunchSteps(t *testing.T) {
	e := newEnv(t, happy)
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--launch"), 0, e)
	if !strings.HasSuffix(e.errb.String(), "[4/5] Installing\n[5/5] Launching\n") {
		t.Fatalf("%q", e.errb)
	}
}

// frame is one terminal draw (up = not the first).
func frame(up bool, i, n int, name string, fill int, label string) string {
	u := ""
	if up {
		u = "\x1b[1A"
	}
	return fmt.Sprintf("%s\r\x1b[K[%d/%d] %s\n\r\x1b[K[%s%s] %s", u, i, n, name,
		strings.Repeat("=", fill), strings.Repeat(" ", barWidth-fill), label)
}

// Unknown, negative and oversized totals: unknown shows the i/n fill, the
// fraction is clamped to 0..1.
func TestStepDisplayTotals(t *testing.T) {
	for _, c := range []struct {
		cur, max int64
		fill     int
		label    string
	}{{0, 0, 0, "1/4"}, {5, -1, 0, "1/4"}, {150, 100, 30, "100%"}, {-5, 100, 0, "0%"}, {29, 100, 8, "29%"}} {
		var b bytes.Buffer
		p := newStepDisplay(&b, true, 4)
		p.step("Downloading")
		p.progress()(c.cur, c.max)
		want := frame(false, 1, 4, "Downloading", 0, "1/4") + frame(true, 1, 4, "Downloading", c.fill, c.label)
		if b.String() != want {
			t.Errorf("(%d,%d): got %q want %q", c.cur, c.max, b.String(), want)
		}
	}
}

// After done() progress draws nothing; the next step starts a new frame.
func TestStepDisplayClosed(t *testing.T) {
	var b bytes.Buffer
	p := newStepDisplay(&b, true, 4)
	p.step("Downloading")
	hook := p.progress()
	p.done()
	n := b.Len()
	hook(5, 10)
	p.done()
	if b.Len() != n || !strings.HasSuffix(b.String(), "\n") {
		t.Fatalf("%q", b.String())
	}
	p.step("Decrypting") // a new step draws a fresh frame, no cursor-up
	if want := frame(false, 2, 4, "Decrypting", 7, "2/4"); !strings.HasSuffix(b.String(), want) {
		t.Fatalf("%q", b.String())
	}
}

func TestBarMode(t *testing.T) {
	for _, c := range []struct {
		tty   bool
		term  string
		width int
		want  bool
	}{{true, "xterm", 80, true}, {true, "xterm", 37, true}, {true, "xterm", 36, false}, {true, "dumb", 80, false}, {false, "xterm", 80, false}} {
		if got := barMode(c.tty, c.term, c.width); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

// The download hook reaches the bar: a % frame is drawn in step 1 (fails
// when install stops passing store.WithProgress).
func TestInstallTTYDownloadBar(t *testing.T) {
	e := newEnv(t, happy)
	e.a.stderrTTY = true
	seen := make(chan struct{})
	e.a.stderr = &notifyWriter{w: e.errb, sub: "40%", ch: seen}
	e.as.downloadFn = func(in appstore.DownloadInput) error {
		in.Progress.ChangeMax64(100)
		in.Progress.Set64(40)
		select { // the poller drew the frame
		case <-seen:
		case <-time.After(5 * time.Second):
			return errors.New("no progress frame drawn")
		}
		return os.WriteFile(in.OutputPath, []byte("encrypted"), 0o644)
	}
	wantExit(t, e.run("install", "Foo", "--vm", "v1"), 0, e)
	if want := frame(true, 1, 4, "Downloading", 12, "40%"); !strings.Contains(e.errb.String(), want) {
		t.Fatalf("no %q in %q", want, e.errb)
	}
}

// With a terminal the bar is closed before every stdout result line: stdout
// and stderr share one buffer here, as on a shared terminal.
func TestInstallTTYClosesBarBeforeStdout(t *testing.T) {
	e := newEnv(t, func(r string) string {
		if strings.Contains(r, "apps.install") {
			return `{"ok":true,"result":{"bundle_id":"com.example.app","upgraded":true}}` + "\n"
		}
		return happy(r)
	})
	e.a.stderrTTY = true
	var b bytes.Buffer
	e.a.stdout, e.a.stderr = &b, &b
	wantExit(t, e.run("install", "Foo", "--vm", "v1", "--launch", "--keep"), 0, e)
	got := b.String()
	for _, want := range []string{
		frame(true, 4, 5, "Installing", 30, "100%") + "\ninstalled: com.example.app\nupgraded: yes\n\r\x1b[K[5/5] Launching\n",
		frame(true, 5, 5, "Launching", 30, "100%") + "\nlaunched: com.example.app pid 4242\nkept: ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in %q", want, got)
		}
	}
	// after "launched:" only the kept line: no bar bytes, ends in one newline
	tail := got[strings.Index(got, "launched: "):]
	if strings.Contains(tail, "\x1b") || strings.Contains(tail, "\r") || strings.Count(tail, "\n") != 2 || !strings.HasSuffix(tail, "\n") {
		t.Fatalf("%q", tail)
	}
}

// With a terminal, a failed or interrupted download closes the renderer
// (bar frames, "\n") before the error line, and nothing follows it.
func TestInstallTTYFailureClosesBar(t *testing.T) {
	first := installing + frame(false, 1, 4, "Downloading", 0, "1/4")
	t.Run("failure", func(t *testing.T) {
		e := newEnv(t, happy)
		e.a.stderrTTY = true
		e.as.downloadFn = func(appstore.DownloadInput) error { return errors.New("network down") }
		wantExit(t, e.run("install", "Foo", "--vm", "v1"), 1, e)
		wantErr(t, e, first+"\nerror: download: network down\n")
	})
	t.Run("interrupt", func(t *testing.T) {
		e := newEnv(t, happy)
		e.a.stderrTTY = true
		release := make(chan struct{})
		defer close(release)
		e.as.downloadFn = func(appstore.DownloadInput) error { <-release; return nil }
		ctx, cancel := context.WithCancel(context.Background())
		cancelSoon(cancel)
		within2s(t, func() { wantExit(t, e.runCtx(ctx, "install", "Foo", "--vm", "v1"), 1, e) })
		wantErr(t, e, first+"\nerror: download: interrupted\n")
	})
}

// notifyWriter closes ch once sub was written.
type notifyWriter struct {
	w    io.Writer
	sub  string
	ch   chan struct{}
	once sync.Once
	buf  []byte
}

func (n *notifyWriter) Write(p []byte) (int, error) {
	n.buf = append(n.buf, p...)
	if strings.Contains(string(n.buf), n.sub) {
		n.once.Do(func() { close(n.ch) })
	}
	return n.w.Write(p)
}

// finish ends the open frame on a full bar; after done() or in plain mode it
// adds nothing.
func TestStepDisplayFinish(t *testing.T) {
	var b bytes.Buffer
	p := newStepDisplay(&b, true, 3)
	p.step("Enrolling")
	p.finish()
	p.finish()
	want := frame(false, 1, 3, "Enrolling", 0, "1/3") + frame(true, 1, 3, "Enrolling", 30, "100%") + "\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	p = newStepDisplay(&b, true, 3)
	p.step("Enrolling")
	p.done()
	p.finish()
	if want := frame(false, 1, 3, "Enrolling", 0, "1/3") + "\n"; b.String() != want {
		t.Fatalf("finish after done: %q", b.String())
	}
	b.Reset()
	p = newStepDisplay(&b, false, 3)
	p.step("Enrolling")
	p.finish()
	if b.String() != "[1/3] Enrolling\n" {
		t.Fatalf("%q", b.String())
	}
}

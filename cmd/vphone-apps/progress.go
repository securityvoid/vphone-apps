package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// stepDisplay shows the install steps on stderr. On a terminal it redraws a
// status line and a bar beneath it in place ("\r", ANSI erase and cursor-up);
// otherwise it prints one plain "[i/n] <step>" line per step.
type stepDisplay struct {
	mu       sync.Mutex
	w        io.Writer
	tty      bool
	n, i     int
	name     string
	cur, max int64 // bytes of the current step; max 0 = not measurable
	drawn    bool
	ended    bool // done() was called since the last step
}

const (
	barWidth = 30
	// minBarCols is the bar line: "[", barWidth cells, "] ", "100%".
	minBarCols = barWidth + 2 + 1 + 4
)

// barMode reports whether stderr gets the in-place bar: a terminal that is
// not TERM=dumb and is wide enough for the bar line.
func barMode(isTerminal bool, termEnv string, width int) bool {
	return isTerminal && termEnv != "dumb" && width >= minBarCols
}

func newStepDisplay(w io.Writer, tty bool, steps int) *stepDisplay {
	return &stepDisplay{w: w, tty: tty, n: steps}
}

// step starts the next step.
func (p *stepDisplay) step(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.i, p.name, p.cur, p.max, p.ended = p.i+1, name, 0, 0, false
	p.draw()
}

// progress returns the progress callback of the current step: calls after the
// step changed or after done() are ignored. Unmeasurable (max <= 0) shows the
// step fill.
func (p *stepDisplay) progress() func(cur, max int64) {
	p.mu.Lock()
	i := p.i
	p.mu.Unlock()
	return func(cur, max int64) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.tty && !p.ended && p.i == i {
			p.cur, p.max = cur, max
			p.draw()
		}
	}
}

// done ends the frame in place (one "\n" if drawn); call before printing
// anything else. It is idempotent; progress calls draw nothing until the next
// step starts, which draws a fresh frame.
func (p *stepDisplay) done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = true
	if p.drawn {
		fmt.Fprint(p.w, "\n")
		p.drawn = false
	}
}

// finish fills the open bar to 100% and ends the frame like done(): the
// current step completed (or, for the elevated child, was handed off).
func (p *stepDisplay) finish() {
	p.mu.Lock()
	if p.tty && p.drawn && !p.ended {
		p.cur, p.max = 1, 1
		p.draw()
	}
	p.mu.Unlock()
	p.done()
}

func (p *stepDisplay) draw() {
	if !p.tty {
		fmt.Fprintf(p.w, "[%d/%d] %s\n", p.i, p.n, p.name)
		return
	}
	// Integer arithmetic on clamped bytes: no float rounding (29/100 is 29%).
	fill, label := (p.i-1)*barWidth/p.n, fmt.Sprintf("%d/%d", p.i, p.n)
	if p.max > 0 {
		c := min(max(p.cur, 0), p.max)
		fill, label = int(c*barWidth/p.max), fmt.Sprintf("%d%%", c*100/p.max)
	}
	up := ""
	if p.drawn {
		up = "\x1b[1A"
	}
	fmt.Fprintf(p.w, "%s\r\x1b[K[%d/%d] %s\n\r\x1b[K[%s%s] %s", up, p.i, p.n, p.name,
		strings.Repeat("=", fill), strings.Repeat(" ", barWidth-fill), label)
	p.drawn = true
}

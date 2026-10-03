package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

const lintOutputLimit = 1 << 20

type diagnostic struct {
	Path         string
	Line, Column int
	Message      string
}
type lintReport struct {
	Items  []diagnostic
	Output string
	Err    error
}
type lintJob struct {
	cancel   context.CancelFunc
	done     chan lintReport
	path     string
	revision uint64
}
type lintState struct {
	revision uint64
	path     string
	report   lintReport
}
type lintWake struct{}

// exec serializes writes when stdout and stderr share the same writer.
type limitedOutput struct {
	strings.Builder
	truncated bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := lintOutputLimit - w.Len()
	if n > left {
		w.truncated = true
		p = p[:left]
	}
	_, _ = w.Builder.Write(p)
	return n, nil
}

var diagnosticPattern = regexp.MustCompile(`^(.+?):([0-9]+):(?:([0-9]+):)?\s*(.*)$`)
var ansiPattern = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")

func parseDiagnostics(output, dir string) []diagnostic {
	var items []diagnostic
	for _, line := range strings.Split(output, "\n") {
		m := diagnosticPattern.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		row, err := strconv.Atoi(m[2])
		if err != nil || row < 1 {
			continue
		}
		col := 1
		if m[3] != "" {
			col, err = strconv.Atoi(m[3])
			if err != nil || col < 1 {
				continue
			}
		}
		path := m[1]
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if p, err := canonical(path); err == nil {
			path = p
		}
		items = append(items, diagnostic{path, row, col, m[4]})
		if len(items) == 1000 {
			break
		}
	}
	return items
}
func runLint(ctx context.Context, r LintRule, path string) lintReport {
	expand := func(s string) string {
		return strings.NewReplacer("{file}", path, "{dir}", filepath.Dir(path)).Replace(s)
	}
	args := make([]string, len(r.Command))
	for i, a := range r.Command {
		args[i] = expand(a)
	}
	dir := filepath.Dir(path)
	if r.Directory != "" {
		dir = expand(r.Directory)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(path), dir)
		}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = 200 * time.Millisecond
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	text := ansiPattern.ReplaceAllString(output.String(), "")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	if output.truncated {
		text += "\n[output truncated at 1 MiB]"
	}
	return lintReport{parseDiagnostics(text, dir), text, err}
}
func (e *Editor) stopLint(b *Buffer) {
	if job := e.lintJobs[b]; job != nil {
		job.cancel()
		delete(e.lintJobs, b)
	}
}
func (e *Editor) shutdownLint() {
	for b := range e.lintJobs {
		e.stopLint(b)
	}
	e.lintWorkers.Wait()
}
func (e *Editor) startLint(b *Buffer, manual bool) {
	if !manual && !e.config.Lint.OnSave {
		return
	}
	if b.Path == "" || b.dirty() {
		if manual {
			e.message = "Save this buffer before linting"
		}
		return
	}
	r, ok := e.config.Lint.rule(b.Path)
	if !ok {
		if manual {
			e.message = "No lint rule configured for this file"
		}
		return
	}
	e.stopLint(b)
	if e.lintJobs == nil {
		e.lintJobs = map[*Buffer]*lintJob{}
		e.lintResults = map[*Buffer]lintState{}
	}
	seconds := e.config.Lint.TimeoutSeconds
	if seconds == 0 {
		seconds = 10
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	job := &lintJob{cancel, make(chan lintReport, 1), b.Path, b.revision}
	e.lintJobs[b] = job
	delete(e.lintResults, b)
	screen := e.screen
	e.lintWorkers.Add(1)
	go func() {
		defer e.lintWorkers.Done()
		report := runLint(ctx, r, job.path)
		job.done <- report
		if screen != nil {
			_ = screen.PostEvent(tcell.NewEventInterrupt(lintWake{}))
		}
	}()
	e.message = "Lint running: " + filepath.Base(b.Path)
}
func (e *Editor) pollLint() {
	for b, job := range e.lintJobs {
		if b.revision != job.revision || b.Path != job.path {
			e.stopLint(b)
			delete(e.lintResults, b)
			continue
		}
		select {
		case report := <-job.done:
			e.stopLint(b)
			e.lintResults[b] = lintState{job.revision, job.path, report}
			if len(e.buffers) > 0 && b == e.current() {
				e.message = fmt.Sprintf("Lint: %d diagnostics (F11)", len(report.Items))
				if report.Err != nil {
					e.message += " | " + report.Err.Error()
				}
			}
		default:
		}
	}
}
func (e *Editor) currentLint() (lintReport, bool) {
	s, ok := e.lintResults[e.current()]
	return s.report, ok && s.revision == e.current().revision && s.path == e.current().Path
}
func (e *Editor) lintKeys(ev *tcell.EventKey) {
	report, _ := e.currentLint()
	n := len(report.Items)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC, tcell.KeyF11:
		e.lintView = false
	case tcell.KeyF10:
		e.startLint(e.current(), true)
		e.lintIndex = 0
	case tcell.KeyUp:
		e.lintIndex = max(0, e.lintIndex-1)
	case tcell.KeyDown:
		e.lintIndex = min(max(0, n-1), e.lintIndex+1)
	case tcell.KeyEnter:
		if n == 0 {
			return
		}
		d := report.Items[min(e.lintIndex, n-1)]
		if err := e.open(d.Path); err != nil {
			e.fail(err)
			return
		}
		e.current().goTo(d.Line, d.Column)
		e.clearSelection()
		e.lintView = false
		e.message = d.Message
	}
}
func (e *Editor) drawLint(w, h int) {
	e.screen.Clear()
	e.screen.HideCursor()
	e.bar(0, w, barStyle)
	e.text(0, 0, w, " Lint: "+e.current().name(), barStyle)
	e.text(0, 1, w, "Up/Down: select | Enter: jump | F10: run | Esc: close", messageStyle)
	r, ok := e.currentLint()
	if e.lintJobs[e.current()] != nil {
		e.text(0, 2, w, "Running...", normalStyle)
	} else if !ok {
		e.text(0, 2, w, "No current results. Save and press F10.", normalStyle)
	} else if len(r.Items) == 0 {
		text := "No diagnostics"
		if r.Err != nil {
			text = "Lint failed: " + r.Err.Error()
		}
		e.text(0, 2, w, text, messageStyle)
		for i, line := range strings.Split(r.Output, "\n") {
			if i >= h-5 {
				break
			}
			e.text(0, i+3, w, line, normalStyle)
		}
	} else {
		e.lintIndex = min(e.lintIndex, len(r.Items)-1)
		top := max(0, e.lintIndex-max(1, h-4)+1)
		for i := top; i < len(r.Items) && i-top < h-4; i++ {
			d := r.Items[i]
			style := normalStyle
			if i == e.lintIndex {
				style = activeStyle
				e.bar(i-top+2, w, style)
			}
			e.text(0, i-top+2, w, fmt.Sprintf("%s:%d:%d %s", d.Path, d.Line, d.Column, d.Message), style)
		}
	}
	e.bar(h-1, w, barStyle)
	e.text(0, h-1, w, e.message, barStyle)
}

// Worker lifetime is independent of a buffer being closed.
type lintWorkers struct{ sync.WaitGroup }

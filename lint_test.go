package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestLintHelper(t *testing.T) {
	for i, a := range os.Args {
		if a != "--atto-lint-helper" {
			continue
		}
		switch os.Args[i+1] {
		case "diagnostic":
			fmt.Printf("%s:2:3: example warning\n", filepath.Base(os.Args[i+2]))
			os.Exit(1)
		case "slow":
			time.Sleep(5 * time.Second)
			os.Exit(0)
		case "output":
			fmt.Print(strings.Repeat("x", lintOutputLimit+100))
			os.Exit(0)
		}
	}
}
func helperLint(t *testing.T, mode string) LintRule {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return LintRule{Pattern: "*.go", Command: []string{exe, "-test.run=^TestLintHelper$", "--", "--atto-lint-helper", mode, "{file}"}}
}
func TestLintConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, bad := range []string{`{"lint":{"on_svae":true}}`, `{"lint":{"timeout_seconds":-1}}`, `{"lint":{"rules":[{"pattern":"[","command":["a"]}]}}`, `{"lint":{"rules":[{"pattern":"*","command":[]}]}}`, `{} {}`} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := loadConfig(path); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	os.WriteFile(path, []byte(`{"lint":{"on_save":true,"rules":[{"pattern":"*.go","command":["tool","{file}"]}]}}`), 0600)
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Lint.OnSave || c.Lint.TimeoutSeconds != 10 {
		t.Fatal(c)
	}
	if _, ok := c.Lint.rule("/tmp/file.go"); !ok {
		t.Fatal("rule missing")
	}
	if _, ok := c.Lint.rule("/tmp/file.txt"); ok {
		t.Fatal("wrong match")
	}
	if _, err := loadConfig(path + ".missing"); err == nil {
		t.Fatal("missing explicit config accepted")
	}
}
func TestLintExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "name with spaces.go")
	r := runLint(context.Background(), helperLint(t, "diagnostic"), path)
	if r.Err == nil || len(r.Items) != 1 || r.Items[0].Path != path || r.Items[0].Line != 2 || r.Items[0].Column != 3 {
		t.Fatalf("%+v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	r = runLint(ctx, helperLint(t, "slow"), path)
	if r.Err != context.DeadlineExceeded || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %+v", r)
	}
	r = runLint(context.Background(), helperLint(t, "output"), path)
	if len(r.Output) > lintOutputLimit+100 || !strings.Contains(r.Output, "truncated") {
		t.Fatal("unbounded output")
	}
	r = runLint(context.Background(), LintRule{Command: []string{"/nonexistent/atto-linter"}}, path)
	if r.Err == nil {
		t.Fatal("missing command should fail")
	}
}
func TestDiagnosticParsing(t *testing.T) {
	dir := t.TempDir()
	d := parseDiagnostics("file.go:4: message\nfile.go:5:6: error\nfile.go:0: no\nfile.go:2:0: no\nother output", dir)
	if len(d) != 2 || d[0].Column != 1 || d[1].Column != 6 {
		t.Fatalf("%+v", d)
	}
}
func TestSaveLintAndStaleResults(t *testing.T) {
	e := newEditor(nil)
	defer e.shutdownLint()
	path := filepath.Join(t.TempDir(), "test.go")
	if err := e.open(path); err != nil {
		t.Fatal(err)
	}
	b := e.current()
	b.insert("first\nsecond\n")
	e.config.Lint = LintConfig{OnSave: true, Rules: []LintRule{helperLint(t, "diagnostic")}}
	e.saveBuffer(b, false, nil)
	if e.lintJobs[b] == nil {
		t.Fatal("save did not start lint")
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.lintJobs[b] != nil && time.Now().Before(deadline) {
		e.pollLint()
		time.Sleep(time.Millisecond)
	}
	r, ok := e.currentLint()
	if !ok || len(r.Items) != 1 {
		t.Fatalf("no result: %+v", r)
	}
	e.lintView = true
	e.lintKeys(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	row, col := b.rowCol()
	if row != 1 || col != 2 || e.lintView {
		t.Fatalf("jump %d:%d", row, col)
	}
	b.insert("edit")
	if _, ok := e.currentLint(); ok {
		t.Fatal("stale result visible")
	}
	e.startLint(b, true)
	if e.lintJobs[b] != nil {
		t.Fatal("lint ran with unsaved changes")
	}
	e.config.Lint.OnSave = false
	e.saveBuffer(b, false, nil)
	if e.lintJobs[b] != nil {
		t.Fatal("disabled on_save ran")
	}
	e.config.Lint.Rules = []LintRule{helperLint(t, "slow")}
	e.startLint(b, true)
	if e.lintJobs[b] == nil {
		t.Fatal("manual lint did not run")
	}
	b.insert("again")
	e.pollLint()
	if e.lintJobs[b] != nil {
		t.Fatal("stale job not cancelled")
	}
}

func TestLintMultipleBuffersAndClose(t *testing.T) {
	e := testEditor(t)
	t.Cleanup(e.shutdownLint)
	dir := t.TempDir()
	e.config.Lint = LintConfig{OnSave: true, Rules: []LintRule{helperLint(t, "diagnostic")}}
	var buffers []*Buffer
	for _, name := range []string{"one.go", "two.go"} {
		if err := e.open(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		b := e.current()
		b.insert("first\nsecond\n")
		buffers = append(buffers, b)
		e.saveBuffer(b, false, nil)
	}
	deadline := time.Now().Add(4 * time.Second)
	for len(e.lintJobs) > 0 && time.Now().Before(deadline) {
		e.pollLint()
		time.Sleep(time.Millisecond)
	}
	for _, b := range buffers {
		if len(e.lintResults[b].report.Items) != 1 {
			t.Fatal("buffer result missing")
		}
		if e.lintResults[b].report.Items[0].Path != b.Path {
			t.Fatal("mixed buffer results")
		}
	}
	key(e, tcell.KeyF11)
	e.draw()
	key(e, tcell.KeyEscape)
	if e.lintView {
		t.Fatal("lint view did not close")
	}
	e.config.Lint.Rules = []LintRule{helperLint(t, "slow")}
	b := e.current()
	key(e, tcell.KeyF10)
	if e.lintJobs[b] == nil {
		t.Fatal("F10 did not start")
	}
	key(e, tcell.KeyCtrlX)
	if e.lintJobs[b] != nil {
		t.Fatal("closed buffer still running")
	}
	if _, ok := e.lintResults[b]; ok {
		t.Fatal("closed buffer retained")
	}
}

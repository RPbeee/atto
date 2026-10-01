package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func awaitProject(t *testing.T, e *Editor) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.projectJob != nil && time.Now().Before(deadline) {
		e.pollProject()
		time.Sleep(time.Millisecond)
	}
	if e.projectJob != nil {
		t.Fatal("project search did not finish")
	}
}
func TestProjectRecursiveSearchUnicodeAndSkips(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "src", "one.txt"), []byte("日本 target target\nTARGET\n"))
	writeFixture(t, filepath.Join(root, "two.txt"), []byte("\xef\xbb\xbfline\r\ntarget\r\n"))
	writeFixture(t, filepath.Join(root, "binary"), []byte("target\x00"))
	writeFixture(t, filepath.Join(root, "mixed"), []byte("target\r\nline\n"))
	for _, name := range []string{".git", "node_modules", "vendor", "build", "dist"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(root, name, "ignored"), []byte("target"))
	}
	if err := os.Symlink(filepath.Join(root, "two.txt"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	report := projectScan(context.Background(), root, "target", nil)
	if report.Err != nil || len(report.Matches) != 3 || report.Files != 2 || report.Skipped != 3 {
		t.Fatal(report)
	}
	first := report.Matches[0]
	if first.Line != 1 || first.Column != 4 || !strings.Contains(first.Path, "one.txt") {
		t.Fatal(first)
	}
	if report.Matches[1].Column != 11 || report.Matches[2].Line != 2 || report.Matches[2].Column != 1 {
		t.Fatal(report.Matches)
	}
}
func TestProjectIncludesUnsavedNamedBuffers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	writeFixture(t, path, []byte("disk"))
	missing := filepath.Join(root, "new")
	overrides := map[string]string{path: "edited target", missing: "target", filepath.Join(root, "node_modules", "missing"): "target"}
	report := projectScan(context.Background(), root, "target", overrides)
	if len(report.Matches) != 2 || report.Matches[0].Path != path || report.Matches[1].Path != missing {
		t.Fatal(report)
	}
	if fileText(t, path) != "disk" {
		t.Fatal("search wrote unsaved file")
	}
}
func TestProjectCancellationLimitsAndBadRoot(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "many"), []byte(strings.Repeat("target\n", projectMatchLimit+1)))
	report := projectScan(context.Background(), root, "target", nil)
	if len(report.Matches) != projectMatchLimit || report.Limit == "" || report.Err != nil {
		t.Fatal(report.Limit, report.Err, len(report.Matches))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report = projectScan(ctx, root, "target", nil)
	if !errors.Is(report.Err, context.Canceled) {
		t.Fatal(report)
	}
	if report = projectScan(context.Background(), root, "", nil); report.Err == nil {
		t.Fatal("empty query")
	}
	if report = projectScan(context.Background(), filepath.Join(root, "missing"), "target", nil); report.Err == nil {
		t.Fatal("bad root")
	}
	if report = projectScan(context.Background(), filepath.Join(root, "many"), "target", nil); report.Err == nil {
		t.Fatal("file root")
	}
}
func TestProjectUIJumpAndKeepUnsavedBuffer(t *testing.T) {
	e := testEditor(t)
	root := t.TempDir()
	path := filepath.Join(root, "text")
	writeFixture(t, path, []byte("line\n日本 target\n"))
	if err := e.open(path); err != nil {
		t.Fatal(err)
	}
	b := e.current()
	b.insert("new\n")
	key(e, tcell.KeyF3)
	e.projectRoot = root
	key(e, tcell.KeyCtrlP)
	key(e, tcell.KeyEnter)
	answer(e, "target")
	awaitProject(t, e)
	e.draw()
	if !e.projectView || len(e.projectResults.Matches) != 1 || e.projectResults.Matches[0].Line != 3 {
		t.Fatal(e.projectResults)
	}
	key(e, tcell.KeyEnter)
	row, col := b.rowCol()
	if e.projectView || e.current() != b || row != 2 || col != 3 || !b.dirty() {
		t.Fatal("jump replaced unsaved buffer", row, col)
	}
	if fileText(t, path) != "line\n日本 target\n" {
		t.Fatal("project search changed disk")
	}
	alt(e, 'p')
	if !e.projectView {
		t.Fatal("reopen results")
	}
	key(e, tcell.KeyEscape)
}
func TestProjectStaleResultAndCancelReplacesJob(t *testing.T) {
	e := testEditor(t)
	root := t.TempDir()
	path := filepath.Join(root, "text")
	writeFixture(t, path, []byte("target"))
	e.startProject(root, "target")
	awaitProject(t, e)
	writeFixture(t, path, []byte("changed"))
	key(e, tcell.KeyEnter)
	if !e.projectView || !strings.Contains(e.message, "Result changed") {
		t.Fatal("stale result accepted", e.message)
	}
	key(e, tcell.KeyEscape)
	e.startProject(root, "old")
	key(e, tcell.KeyEscape)
	if e.projectJob != nil || e.projectView {
		t.Fatal("cancel did not stop search")
	}
	e.startProject(root, "changed")
	awaitProject(t, e)
	if len(e.projectResults.Matches) != 1 || e.projectQuery != "changed" {
		t.Fatal("old job overwrote new results")
	}
}

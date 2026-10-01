package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

const projectMatchLimit = 1000
const projectFileLimit = 10000
const projectByteLimit = 64 << 20

type projectMatch struct {
	Path         string
	Line, Column int
	Preview      string
}
type projectReport struct {
	Matches        []projectMatch
	Files, Skipped int
	Limit          string
	Err            error
}
type projectWake struct{}
type projectWorkers struct{ sync.WaitGroup }
type projectJob struct {
	cancel context.CancelFunc
	done   chan projectReport
}

var projectSkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".cache": true}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func excludedProjectPath(root, path string) bool {
	rel, _ := filepath.Rel(root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		if projectSkipDirs[part] {
			return true
		}
	}
	return false
}
func projectScan(ctx context.Context, root, query string, overrides map[string]string) projectReport {
	var report projectReport
	if query == "" {
		report.Err = errors.New("empty project query")
		return report
	}
	info, err := os.Stat(root)
	if err != nil {
		report.Err = err
		return report
	}
	if !info.IsDir() {
		report.Err = errors.New("project root must be a directory")
		return report
	}
	visitedCount := 0
	visited := map[string]bool{}
	bytesRead := 0
	stop := errors.New("search limit reached")
	scan := func(path, text string) error {
		report.Files++
		for row, line := range strings.Split(text, "\n") {
			if err := ctx.Err(); err != nil {
				return err
			}
			for from := 0; from <= len(line); {
				i := strings.Index(line[from:], query)
				if i < 0 {
					break
				}
				i += from
				if len(report.Matches) >= projectMatchLimit {
					report.Limit = "1000 match limit"
					return stop
				}
				column := utf8.RuneCountInString(line[:i])
				runes := []rune(line)
				start := max(0, column-60)
				end := min(len(runes), start+240)
				preview := string(runes[start:end])
				if start > 0 {
					preview = "…" + preview
				}
				if end < len(runes) {
					preview += "…"
				}
				report.Matches = append(report.Matches, projectMatch{path, row + 1, column + 1, preview})
				from = i + len(query)
			}
		}
		return nil
	}
	account := func(size int) error {
		if visitedCount >= projectFileLimit {
			report.Limit = "10000 file limit"
			return stop
		}
		visitedCount++
		if bytesRead+size > projectByteLimit {
			report.Limit = "64 MiB scan limit"
			return stop
		}
		bytesRead += size
		return nil
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil {
			report.Skipped++
			return nil
		}
		if entry.IsDir() {
			if path != root && projectSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Do not follow symlinks or read devices/FIFOs while scanning a project.
		if !entry.Type().IsRegular() {
			report.Skipped++
			return nil
		}
		if visitedCount >= projectFileLimit {
			report.Limit = "10000 file limit"
			return stop
		}
		visited[path] = true
		if text, ok := overrides[path]; ok {
			if err := account(len(text)); err != nil {
				return err
			}
			if len(text) > maxFileBytes {
				report.Skipped++
				return nil
			}
			return scan(path, text)
		}
		data, _, _, err := readDisk(path)
		if err != nil {
			visitedCount++
			report.Skipped++
			return nil
		}
		if err := account(len(data)); err != nil {
			return err
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			report.Skipped++
			return nil
		}
		text := strings.TrimPrefix(string(data), "\ufeff")
		if strings.Contains(text, "\r\n") && strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
			report.Skipped++
			return nil
		}
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if strings.Contains(text, "\r") {
			report.Skipped++
			return nil
		}
		return scan(path, text)
	})
	if err == nil {
		// Named, not-yet-saved buffers may not have appeared in WalkDir.
		paths := make([]string, 0, len(overrides))
		for path := range overrides {
			if !visited[path] && withinRoot(root, path) && !excludedProjectPath(root, path) {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		for _, path := range paths {
			if err = ctx.Err(); err != nil {
				break
			}
			if err = account(len(overrides[path])); err != nil {
				break
			}
			if len(overrides[path]) > maxFileBytes {
				report.Skipped++
				continue
			}
			if err = scan(path, overrides[path]); err != nil {
				break
			}
		}
	}
	if err != nil && !errors.Is(err, stop) {
		report.Err = err
	}
	return report
}
func (e *Editor) stopProject() {
	if e.projectJob != nil {
		e.projectJob.cancel()
		e.projectJob = nil
	}
}
func (e *Editor) shutdownProject() { e.stopProject(); e.projectWorkers.Wait() }
func (e *Editor) askProject() {
	e.stopProject()
	e.projectView = false
	root := e.projectRoot
	if root == "" {
		root, _ = os.Getwd()
	}
	e.ask("Project directory:", root, func(path string) {
		abs, err := filepath.Abs(path)
		if err == nil {
			abs, err = filepath.EvalSymlinks(abs)
		}
		if err != nil {
			e.fail(err)
			return
		}
		info, err := os.Stat(abs)
		if err != nil {
			e.fail(err)
			return
		}
		if !info.IsDir() {
			e.fail(errors.New("project root must be a directory"))
			return
		}
		e.ask("Project search:", e.projectQuery, func(query string) {
			if query == "" {
				e.message = "Empty search cancelled"
				return
			}
			e.startProject(abs, query)
		})
	})
}
func (e *Editor) startProject(root, query string) {
	e.stopProject()
	e.projectRoot = root
	e.projectQuery = query
	e.projectResults = projectReport{}
	e.projectIndex = 0
	e.projectView = true
	overrides := map[string]string{}
	for _, b := range e.buffers {
		if b.Path != "" && withinRoot(root, b.Path) {
			overrides[b.Path] = string(b.Text)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &projectJob{cancel, make(chan projectReport, 1)}
	e.projectJob = job
	screen := e.screen
	e.projectWorkers.Add(1)
	go func() {
		defer e.projectWorkers.Done()
		report := projectScan(ctx, root, query, overrides)
		select {
		case <-ctx.Done():
			return
		case job.done <- report:
		}
		if screen != nil {
			screen.PostEvent(tcell.NewEventInterrupt(projectWake{}))
		}
	}()
	e.message = "Searching project; Esc cancels"
}
func (e *Editor) pollProject() {
	if e.projectJob == nil {
		return
	}
	select {
	case report := <-e.projectJob.done:
		e.projectJob.cancel()
		e.projectJob = nil
		e.projectResults = report
		e.message = fmt.Sprintf("Project: %d matches, %d files, %d skipped", len(report.Matches), report.Files, report.Skipped)
		if report.Limit != "" {
			e.message += " | " + report.Limit
		}
		if report.Err != nil {
			e.fail(report.Err)
		}
	default:
	}
}
func (e *Editor) projectKeys(ev *tcell.EventKey) {
	e.pollProject()
	n := len(e.projectResults.Matches)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		e.stopProject()
		e.projectView = false
	case tcell.KeyCtrlP:
		e.askProject()
	case tcell.KeyUp:
		e.projectIndex = max(0, e.projectIndex-1)
	case tcell.KeyDown:
		e.projectIndex = min(max(0, n-1), e.projectIndex+1)
	case tcell.KeyPgUp:
		_, h := e.screen.Size()
		e.projectIndex = max(0, e.projectIndex-max(1, h-4))
	case tcell.KeyPgDn:
		_, h := e.screen.Size()
		e.projectIndex = min(max(0, n-1), e.projectIndex+max(1, h-4))
	case tcell.KeyHome:
		e.projectIndex = 0
	case tcell.KeyEnd:
		e.projectIndex = max(0, n-1)
	case tcell.KeyEnter:
		if n > 0 && e.projectJob == nil {
			e.openProjectMatch(e.projectResults.Matches[e.projectIndex])
		}
	}
}
func (e *Editor) openProjectMatch(match projectMatch) {
	if err := e.open(match.Path); err != nil {
		e.fail(err)
		return
	}
	b := e.current()
	lines := b.lines()
	if match.Line > len(lines) {
		e.message = "Result changed; rerun project search"
		return
	}
	line := lines[match.Line-1]
	runes := []rune(line)
	offset := match.Column - 1
	if offset < 0 || offset > len(runes) || !strings.HasPrefix(string(runes[offset:]), e.projectQuery) {
		e.message = "Result changed; rerun project search"
		return
	}
	b.goTo(match.Line, match.Column)
	e.clearSelection()
	e.projectView = false
	e.message = "Project match: " + e.projectQuery
}
func (e *Editor) drawProject(w, h int) {
	e.screen.Clear()
	e.screen.HideCursor()
	e.bar(0, w, barStyle)
	e.text(0, 0, w, " Project: "+e.projectQuery+" | "+e.projectRoot, barStyle)
	e.text(0, 1, w, "Up/Down/PgUp/PgDn, Enter opens, Esc closes/cancels, Ctrl-P searches", messageStyle)
	if e.projectJob != nil {
		e.text(0, 3, w, "Searching... Esc cancels", normalStyle)
	} else if len(e.projectResults.Matches) == 0 {
		e.text(0, 3, w, "No matches", normalStyle)
	}
	height := max(1, h-4)
	top := max(0, e.projectIndex-height+1)
	for i := top; i < len(e.projectResults.Matches) && i-top < height; i++ {
		match := e.projectResults.Matches[i]
		rel, _ := filepath.Rel(e.projectRoot, match.Path)
		style := normalStyle
		y := i - top + 2
		if i == e.projectIndex {
			style = activeStyle
			e.bar(y, w, style)
		}
		e.text(0, y, w, fmt.Sprintf("%s:%d:%d  %s", rel, match.Line, match.Column, match.Preview), style)
	}
	e.bar(h-1, w, barStyle)
	e.text(0, h-1, w, e.message, barStyle)
}

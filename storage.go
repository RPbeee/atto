package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 8 << 20

var errConflict = errors.New("file changed on disk; reopen it before saving")

type diskState struct {
	exists bool
	hash   [32]byte
}

func readDisk(path string) ([]byte, diskState, os.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, diskState{}, 0600, nil
	}
	if err != nil {
		return nil, diskState{}, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, diskState{}, 0, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, diskState{}, 0600, nil
	}
	if err != nil {
		return nil, diskState{}, 0, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, diskState{}, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, diskState{}, 0, fmt.Errorf("not a regular file: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, diskState{}, 0, err
	}
	if len(data) > maxFileBytes {
		return nil, diskState{}, 0, fmt.Errorf("file exceeds %d MiB limit", maxFileBytes>>20)
	}
	return data, diskState{true, sha256.Sum256(data)}, info.Mode().Perm(), nil
}

// Resolve aliases, including a symlinked parent for a file that doesn't exist yet.
func canonical(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty file name")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if info, e := os.Lstat(abs); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("dangling symlink")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}
func loadBuffer(path string) (*Buffer, error) {
	path, err := canonical(path)
	if err != nil {
		return nil, err
	}
	data, state, _, err := readDisk(path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("only UTF-8 text without NUL is supported")
	}
	b := newBuffer()
	b.Path = path
	b.disk = state
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		b.BOM = true
		data = data[3:]
	}
	text := string(data)
	if strings.Contains(text, "\r\n") && !strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		b.EOL = "\r\n"
	}
	// Reject mixed newlines rather than silently rewriting an unrelated part of a file.
	if strings.Contains(text, "\r") && (b.EOL != "\r\n" || strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\r")) {
		return nil, errors.New("mixed or CR-only line endings are unsupported")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	b.Text = []rune(text)
	b.saved = text
	return b, nil
}
func (b *Buffer) encoded() []byte {
	s := string(b.Text)
	if b.EOL == "\r\n" {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if b.BOM {
		s = "\ufeff" + s
	}
	return []byte(s)
}

// expected was captured when opening or when asking to overwrite a Save As target.
// A temp file in the same directory prevents partial writes to the destination.
func (b *Buffer) save(path string, expected diskState) error {
	path, err := canonical(path)
	if err != nil {
		return err
	}
	_, current, mode, err := readDisk(path)
	if err != nil {
		return err
	}
	if current != expected {
		return errConflict
	}
	if current.exists && mode&0222 == 0 {
		return errors.New("file is read-only")
	}
	data := b.encoded()
	if len(data) > maxFileBytes {
		return errors.New("edited file exceeds 8 MiB limit")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".atto-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Check again after writing the temporary file to narrow the race window.
	_, latest, _, err := readDisk(path)
	if err != nil {
		return err
	}
	if latest != expected {
		return errConflict
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	b.Path = path
	b.saved = string(b.Text)
	b.disk = diskState{true, sha256.Sum256(data)}
	return nil
}

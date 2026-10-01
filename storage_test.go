package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func canonicalTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
}
func TestRoundTripFormats(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("日本語\nlast"), []byte("a\r\nb\r\n"), []byte("\xef\xbb\xbf日本\r\n")} {
		t.Run(string(data), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.txt")
			writeFixture(t, path, data)
			b, err := loadBuffer(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = b.save(path, b.disk); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("%q vs %q: %v", got, data, err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0640 {
				t.Fatal(info.Mode())
			}
		})
	}
}
func TestNewFileAndSaveAs(t *testing.T) {
	dir := canonicalTestDir(t)
	path := filepath.Join(dir, "new.txt")
	b, err := loadBuffer(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.disk.exists {
		t.Fatal("new file exists")
	}
	b.insert("new\n")
	if err = b.save(path, b.disk); err != nil || b.dirty() {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.txt")
	if err = b.save(other, diskState{}); err != nil || b.Path != other {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(path)
	if string(old) != "new\n" {
		t.Fatal("save as changed original")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if len(entry.Name()) >= 6 && entry.Name()[:6] == ".atto-" {
			t.Fatal("temp file leaked")
		}
	}
}
func TestConflictDoesNotOverwrite(t *testing.T) {
	for _, kind := range []string{"modified", "deleted", "created"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "text")
			if kind != "created" {
				writeFixture(t, path, []byte("original"))
			}
			b, err := loadBuffer(path)
			if err != nil {
				t.Fatal(err)
			}
			b.insert("edit")
			if kind == "deleted" {
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(t, path, []byte("external"))
			}
			if err = b.save(path, b.disk); !errors.Is(err, errConflict) {
				t.Fatal(err)
			}
			if !b.dirty() {
				t.Fatal("failed save marked clean")
			}
			data, err := os.ReadFile(path)
			if kind == "deleted" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			} else if string(data) != "external" {
				t.Fatal("external file overwritten")
			}
		})
	}
}
func TestRejectUnsupportedFiles(t *testing.T) {
	for _, data := range [][]byte{{0xff}, {'a', 0, 'b'}, []byte("a\r\nb\n"), []byte("a\rb"), bytes.Repeat([]byte("a"), maxFileBytes+1)} {
		path := filepath.Join(t.TempDir(), "bad")
		writeFixture(t, path, data)
		if _, err := loadBuffer(path); err == nil {
			t.Fatal("accepted unsupported content")
		}
	}
	if _, err := loadBuffer(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := loadBuffer(filepath.Join(t.TempDir(), "missing", "text")); err == nil {
		t.Fatal("missing parent accepted")
	}
}
func TestReadOnlyAndSymlink(t *testing.T) {
	dir := canonicalTestDir(t)
	path := filepath.Join(dir, "text")
	writeFixture(t, path, []byte("hello"))
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(path, link); err != nil {
		t.Skip(err)
	}
	b, err := loadBuffer(link)
	if err != nil || b.Path != path {
		t.Fatal(err, b)
	}
	b.insert("!")
	if err = b.save(link, b.disk); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(link)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
	if err = os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	b.insert("?")
	if err = b.save(path, b.disk); err == nil {
		t.Fatal("read-only save succeeded")
	}
}

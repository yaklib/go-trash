package trash

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// setupHome points XDG_DATA_HOME at a temp dir and returns the home trash.
func setupHome(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	return filepath.Join(data, "Trash")
}

// readInfo parses $trash/info/<name>.trashinfo and returns the decoded Path
// and the DeletionDate.
func readInfo(t *testing.T, trash, name string) (path string, date time.Time) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(trash, "info", name+".trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	if lines[0] != "[Trash Info]" {
		t.Fatalf("first line = %q, want [Trash Info]", lines[0])
	}
	var rawPath, rawDate string
	for _, l := range lines[1:] {
		if v, ok := strings.CutPrefix(l, "Path="); ok && rawPath == "" {
			rawPath = v
		}
		if v, ok := strings.CutPrefix(l, "DeletionDate="); ok && rawDate == "" {
			rawDate = v
		}
	}
	if strings.ContainsAny(rawPath, " \n%") && !strings.Contains(rawPath, "%") {
		t.Errorf("Path %q is not escaped", rawPath)
	}
	path, err = url.PathUnescape(rawPath)
	if err != nil {
		t.Fatalf("Path %q: %v", rawPath, err)
	}
	date, err = time.ParseInLocation("2006-01-02T15:04:05", rawDate, time.Local)
	if err != nil {
		t.Fatalf("DeletionDate %q: %v", rawDate, err)
	}
	return path, date
}

func TestTrashWeirdNames(t *testing.T) {
	names := []string{
		"plain.txt",
		"with space.txt",
		"  leading and trailing  ",
		"ünïcödé 🗑 名前.txt",
		"new\nline.txt",
		"tab\tand\rcr",
		"100%.txt",
		"semi;colon&amp=plus+$,@:.txt",
		"back\\slash",
		"quote\"'`",
		"-dash",
		"trailing.",
	}
	for _, name := range names {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			trash := setupHome(t)
			dir := t.TempDir()
			src := filepath.Join(dir, name)
			if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := time.Now().Truncate(time.Second)

			if err := Trash(src); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Errorf("source still exists: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(trash, "files", name))
			if err != nil || string(got) != "data" {
				t.Errorf("trashed file: %q, %v", got, err)
			}
			path, date := readInfo(t, trash, name)
			if path != src {
				t.Errorf("Path = %q, want %q", path, src)
			}
			if date.Before(before) || date.After(time.Now()) {
				t.Errorf("DeletionDate %v not within test run", date)
			}
		})
	}
}

func TestTrashSameNameTwice(t *testing.T) {
	trash := setupHome(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "dup\nname.txt")
	for i := 1; i <= 3; i++ {
		if err := os.WriteFile(src, []byte(strconv.Itoa(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Trash(src); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(filepath.Join(trash, "files"))
	infos, _ := os.ReadDir(filepath.Join(trash, "info"))
	if len(files) != 3 || len(infos) != 3 {
		t.Fatalf("got %d files, %d infos, want 3 each", len(files), len(infos))
	}
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(trash, "files", f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		seen[string(b)] = true
		if path, _ := readInfo(t, trash, f.Name()); path != src {
			t.Errorf("%q: Path = %q, want %q", f.Name(), path, src)
		}
	}
	if len(seen) != 3 {
		t.Errorf("contents %v, want 1 2 3", seen)
	}
}

func TestTrashDirectory(t *testing.T) {
	trash := setupHome(t)
	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(filepath.Join(dir, "sub\ndir"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(dir, "sub\ndir", "f.txt")
	if err := os.WriteFile(inner, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Trash(dir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(trash, "files", "dir with space", "sub\ndir", "f.txt"))
	if err != nil || string(got) != "x" {
		t.Errorf("nested file: %q, %v", got, err)
	}
	if path, _ := readInfo(t, trash, "dir with space"); path != dir {
		t.Errorf("Path = %q, want %q", path, dir)
	}
	if _, err := os.Lstat(filepath.Join(trash, "info", "sub\ndir.trashinfo")); err == nil {
		t.Error("info created for nested directory")
	}
}

func TestTrashRelativePath(t *testing.T) {
	trash := setupHome(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("rel file.txt", nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Trash("rel file.txt"); err != nil {
		t.Fatal(err)
	}

	path, _ := readInfo(t, trash, "rel file.txt")
	want, _ := filepath.Abs("rel file.txt")
	if path != want {
		t.Errorf("Path = %q, want absolute %q", path, want)
	}
}

func TestTrashSymlink(t *testing.T) {
	trash := setupHome(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}

	if err := Trash(link); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Errorf("target removed: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(trash, "files", "link"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("trashed entry is not a symlink: %v %v", fi, err)
	}
	if path, _ := readInfo(t, trash, "link"); path != link {
		t.Errorf("Path = %q, want %q", path, link)
	}
}

func TestTrashMissing(t *testing.T) {
	trash := setupHome(t)
	if err := Trash(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error")
	}
	if infos, _ := os.ReadDir(filepath.Join(trash, "info")); len(infos) != 0 {
		t.Errorf("stray info files: %v", infos)
	}
}

func TestHomeTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, xdg := range []string{"", "relative/path"} {
		t.Setenv("XDG_DATA_HOME", xdg)
		got, err := homeTrash()
		want := filepath.Join(home, ".local", "share", "Trash")
		if err != nil || got != want {
			t.Errorf("XDG_DATA_HOME=%q: %q, %v; want %q", xdg, got, err, want)
		}
	}
	abs := t.TempDir()
	t.Setenv("XDG_DATA_HOME", abs)
	if got, _ := homeTrash(); got != filepath.Join(abs, "Trash") {
		t.Errorf("got %q", got)
	}
}

func TestTopDirTrash(t *testing.T) {
	const uid = 1234
	tests := []struct {
		name  string
		setup func(top string)
		want  string
	}{
		{"no .Trash", func(string) {}, ".Trash-1234"},
		{"sticky .Trash", func(top string) {
			mkdirSticky(t, filepath.Join(top, ".Trash"))
		}, ".Trash/1234"},
		{"non-sticky .Trash", func(top string) {
			os.Mkdir(filepath.Join(top, ".Trash"), 0o777)
		}, ".Trash-1234"},
		{".Trash is a symlink", func(top string) {
			mkdirSticky(t, filepath.Join(top, "real"))
			os.Symlink(filepath.Join(top, "real"), filepath.Join(top, ".Trash"))
		}, ".Trash-1234"},
		{".Trash is a file", func(top string) {
			os.WriteFile(filepath.Join(top, ".Trash"), nil, 0o644)
		}, ".Trash-1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := t.TempDir()
			tt.setup(top)
			got, err := topDirTrash(top, uid)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(top, tt.want); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			for _, sub := range []string{"files", "info"} {
				if fi, err := os.Stat(filepath.Join(got, sub)); err != nil || !fi.IsDir() {
					t.Errorf("%s: %v", sub, err)
				}
			}
		})
	}
}

func mkdirSticky(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(dir); fi.Mode()&os.ModeSticky == 0 {
		t.Skip("filesystem does not support the sticky bit")
	}
}

func TestTopDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(dir)
	top := topDir(dir, device(fi))
	// top must be an ancestor of dir, and its parent (if any) must be on
	// another device.
	if rel, err := filepath.Rel(top, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("topDir %q is not an ancestor of %q", top, dir)
	}
	if parent := filepath.Dir(top); parent != top {
		pfi, _ := os.Lstat(parent)
		if device(pfi) == device(fi) {
			t.Errorf("parent %q of topDir %q is on the same device", parent, top)
		}
	}
}

func TestEscape(t *testing.T) {
	tests := map[string]string{
		"/home/u/plain.txt":   "/home/u/plain.txt",
		"a b":                 "a%20b",
		"a\nb":                "a%0Ab",
		"100%":                "100%25",
		"ü":                   "%C3%BC",
		"🗑":                   "%F0%9F%97%91",
		"-_.!~*'()":           "-_.!~*'()",
		";&=+$,@:?#[]\\\"`<>": "%3B%26%3D%2B%24%2C%40%3A%3F%23%5B%5D%5C%22%60%3C%3E",
		"\xff\x00":            "%FF%00",
	}
	for in, want := range tests {
		if got := escape(in); got != want {
			t.Errorf("escape(%q) = %q, want %q", in, got, want)
		}
		if back, err := url.PathUnescape(escape(in)); err != nil || back != in {
			t.Errorf("round trip %q -> %q, %v", in, back, err)
		}
	}
}

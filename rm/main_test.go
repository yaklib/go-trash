package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture creates a temp tree and points XDG_DATA_HOME at another temp dir.
// It returns the tree root and the trash's files directory.
func fixture(t *testing.T) (root, files string) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	root = t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "ne", "sub"), 0o755))
	must(os.Mkdir(filepath.Join(root, "e"), 0o755))
	must(os.WriteFile(filepath.Join(root, "ne", "f"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(root, "empty"), nil, 0o644))
	must(os.WriteFile(filepath.Join(root, "w"), nil, 0o444))
	must(os.Symlink("file", filepath.Join(root, "link")))
	return root, filepath.Join(data, "Trash", "files")
}

func rm(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestFiles(t *testing.T) {
	root, files := fixture(t)
	code, out, errOut := rm(t, "", filepath.Join(root, "file"), filepath.Join(root, "link"))
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if exists(filepath.Join(root, "file")) || !exists(filepath.Join(files, "file")) {
		t.Error("file not trashed")
	}
	if fi, err := os.Lstat(filepath.Join(files, "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink not trashed as symlink")
	}
	if !exists(filepath.Join(root, "ne", "f")) {
		t.Error("symlink target touched")
	}
}

func TestDirectoriesWithoutRecursive(t *testing.T) {
	root, files := fixture(t)
	code, _, errOut := rm(t, "", filepath.Join(root, "ne"), filepath.Join(root, "e")+"/")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if !exists(filepath.Join(files, "ne", "sub")) || !exists(filepath.Join(files, "ne", "f")) {
		t.Error("directory not trashed whole")
	}
	if !exists(filepath.Join(files, "e")) {
		t.Error("trailing-slash directory not trashed")
	}
	root, _ = fixture(t)
	if code, _, errOut := rm(t, "", "-r", filepath.Join(root, "ne")); code != 0 || errOut != "" {
		t.Errorf("-r: code=%d err=%q", code, errOut)
	}
}

func TestMissing(t *testing.T) {
	root, _ := fixture(t)
	nope := filepath.Join(root, "nope")
	code, _, errOut := rm(t, "", nope, filepath.Join(root, "file"))
	if code != 1 || errOut != "rm: cannot remove '"+nope+"': No such file or directory\n" {
		t.Errorf("code=%d err=%q", code, errOut)
	}
	if exists(filepath.Join(root, "file")) {
		t.Error("later operand not processed after an error")
	}
	if code, _, errOut := rm(t, "", "-f", nope); code != 0 || errOut != "" {
		t.Errorf("-f: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "--interactive=never", nope); code != 1 || errOut == "" {
		t.Errorf("--interactive=never must not ignore missing files: code=%d err=%q", code, errOut)
	}
}

func TestMissingOperand(t *testing.T) {
	fixture(t)
	code, _, errOut := rm(t, "")
	if code != 1 || errOut != "rm: missing operand\nTry 'rm --help' for more information.\n" {
		t.Errorf("code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "-f"); code != 0 || errOut != "" {
		t.Errorf("-f: code=%d err=%q", code, errOut)
	}
}

func TestDotAndRoot(t *testing.T) {
	root, _ := fixture(t)
	t.Chdir(root)
	for _, arg := range []string{".", "..", "ne/..", "./ne/."} {
		code, _, errOut := rm(t, "", "-r", arg)
		want := "rm: refusing to remove '.' or '..' directory: skipping '" + arg + "'\n"
		if code != 1 || errOut != want {
			t.Errorf("%s: code=%d err=%q", arg, code, errOut)
		}
	}
	if !exists("ne/f") {
		t.Fatal("fixture damaged")
	}
	for _, args := range [][]string{{"-r", "/"}, {"-rf", "/"}, {"/"}} {
		code, _, errOut := rm(t, "", args...)
		want := "rm: it is dangerous to operate recursively on '/'\nrm: use --no-preserve-root to override this failsafe\n"
		if code != 1 || errOut != want {
			t.Errorf("%v: code=%d err=%q", args, code, errOut)
		}
	}
}

func TestInteractive(t *testing.T) {
	tests := []struct {
		name   string
		stdin  string
		args   []string
		prompt string
		gone   bool
	}{
		{"regular no", "n\n", []string{"-i", "file"}, "rm: remove regular file 'file'? ", false},
		{"regular yes", "y\n", []string{"-i", "file"}, "rm: remove regular file 'file'? ", true},
		{"regular YES", "YES\n", []string{"-i", "file"}, "rm: remove regular file 'file'? ", true},
		{"eof", "", []string{"-i", "file"}, "rm: remove regular file 'file'? ", false},
		{"empty", "n\n", []string{"-i", "empty"}, "rm: remove regular empty file 'empty'? ", false},
		{"protected", "n\n", []string{"-i", "w"}, "rm: remove write-protected regular empty file 'w'? ", false},
		{"symlink", "n\n", []string{"-i", "link"}, "rm: remove symbolic link 'link'? ", false},
		{"dir", "n\n", []string{"-i", "ne"}, "rm: remove directory 'ne'? ", false},
		{"dir -r", "n\n", []string{"-ri", "ne"}, "rm: remove directory 'ne'? ", false},
		{"long", "n\n", []string{"--interactive", "file"}, "rm: remove regular file 'file'? ", false},
		{"long always", "n\n", []string{"--interactive=always", "file"}, "rm: remove regular file 'file'? ", false},
		{"-f then -i", "n\n", []string{"-f", "-i", "file"}, "rm: remove regular file 'file'? ", false},
		{"-i then -f", "n\n", []string{"-i", "-f", "file"}, "", true},
		{"-I -i", "n\n", []string{"-I", "-i", "file"}, "rm: remove regular file 'file'? ", false},
		{"never", "n\n", []string{"--interactive=never", "file"}, "", true},
		{"not a tty: no protected prompt", "n\n", []string{"w"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := fixture(t)
			t.Chdir(root)
			code, out, errOut := rm(t, tt.stdin, tt.args...)
			if code != 0 || out != "" || errOut != tt.prompt {
				t.Errorf("code=%d out=%q err=%q, want prompt %q", code, out, errOut, tt.prompt)
			}
			if gone := !exists(tt.args[len(tt.args)-1]); gone != tt.gone {
				t.Errorf("gone=%v, want %v", gone, tt.gone)
			}
		})
	}
}

func TestPromptOnce(t *testing.T) {
	tests := []struct {
		name   string
		stdin  string
		args   []string
		prompt string
		gone   bool
	}{
		{"three files", "n\n", []string{"-I", "file", "empty", "link"}, "", true},
		{"four files no", "n\n", []string{"-I", "file", "empty", "link", "w"}, "rm: remove 4 arguments? ", false},
		{"four files yes", "y\n", []string{"-I", "file", "empty", "link", "w"}, "rm: remove 4 arguments? ", true},
		{"recursive one", "n\n", []string{"-I", "-r", "ne"}, "rm: remove 1 argument recursively? ", false},
		{"recursive two", "n\n", []string{"-rI", "e", "ne"}, "rm: remove 2 arguments recursively? ", false},
		{"once long", "n\n", []string{"--interactive=once", "-r", "ne"}, "rm: remove 1 argument recursively? ", false},
		{"-I then -f", "n\n", []string{"-I", "-f", "file", "empty", "link", "w"}, "", true},
		{"dir without -r", "n\n", []string{"-I", "ne"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := fixture(t)
			t.Chdir(root)
			code, _, errOut := rm(t, tt.stdin, tt.args...)
			if code != 0 || errOut != tt.prompt {
				t.Errorf("code=%d err=%q, want prompt %q", code, errOut, tt.prompt)
			}
			if gone := !exists(tt.args[len(tt.args)-1]); gone != tt.gone {
				t.Errorf("gone=%v, want %v", gone, tt.gone)
			}
		})
	}
}

func TestVerbose(t *testing.T) {
	root, _ := fixture(t)
	t.Chdir(root)
	code, out, errOut := rm(t, "", "-v", "file", "ne", "link")
	want := "removed 'file'\nremoved directory 'ne'\nremoved 'link'\n"
	if code != 0 || out != want || errOut != "" {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, errOut = rm(t, "", "--verbose", "nope", "empty")
	if code != 1 || out != "removed 'empty'\n" || !strings.Contains(errOut, "'nope'") {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestOptionParsing(t *testing.T) {
	root, files := fixture(t)
	t.Chdir(root)
	if err := os.WriteFile("-f", nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := rm(t, "", "-rfv", "--one-file-system", "--preserve-root", "--preserve-root=all", "--no-preserve-root", "--recursive", "--force", "ne"); code != 0 || errOut != "" {
		t.Errorf("combined: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "file", "-v"); code != 0 || errOut != "" || exists("file") {
		t.Errorf("option after operand: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "--", "-f"); code != 0 || errOut != "" || !exists(filepath.Join(files, "-f")) {
		t.Errorf("-- -f: code=%d err=%q", code, errOut)
	}

	errs := []struct {
		args []string
		want string
	}{
		{[]string{"-x", "empty"}, "rm: invalid option -- 'x'\n"},
		{[]string{"-rx", "empty"}, "rm: invalid option -- 'x'\n"},
		{[]string{"--foo", "empty"}, "rm: unrecognized option '--foo'\n"},
		{[]string{"--force=1", "empty"}, "rm: option '--force' doesn't allow an argument\n"},
		{[]string{"--dir", "empty"}, "rm: unrecognized option '--dir'\n"},
		{[]string{"-D", "empty"}, "rm: invalid option -- 'D'\n"},
		{[]string{"--preserve-root=bad", "empty"}, "rm: unrecognized --preserve-root argument: 'bad'\n"},
		{[]string{"--interactive=bad", "empty"}, "rm: invalid argument ‘bad’ for ‘--interactive’\n" +
			"Valid arguments are:\n  - ‘never’, ‘no’, ‘none’\n  - ‘once’\n  - ‘always’, ‘yes’\n"},
	}
	for _, tt := range errs {
		code, _, errOut := rm(t, "", tt.args...)
		if code != 1 || errOut != tt.want+"Try 'rm --help' for more information.\n" {
			t.Errorf("%v: code=%d err=%q", tt.args, code, errOut)
		}
	}
	if !exists("empty") {
		t.Error("operand trashed despite option error")
	}
}

func TestHelpVersion(t *testing.T) {
	fixture(t)
	code, out, errOut := rm(t, "", "--help", "file")
	if code != 0 || !strings.HasPrefix(out, "Usage: rm [OPTION]... [FILE]...\n") || !strings.Contains(out, "rm -- -foo") || errOut != "" {
		t.Errorf("--help: code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, _ = rm(t, "", "--version")
	if code != 0 || !strings.HasPrefix(out, "rm (go-trash) ") {
		t.Errorf("--version: code=%d out=%q", code, out)
	}
}

func TestWeirdNames(t *testing.T) {
	root, files := fixture(t)
	t.Chdir(root)
	names := []string{"with space", "ünï 🗑", "new\nline", "100%", "-", "--x"}
	for _, n := range names {
		if err := os.WriteFile(n, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"-v", "--"}, names...)
	code, out, errOut := rm(t, "", args...)
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	for _, n := range names {
		if exists(n) || !exists(filepath.Join(files, n)) {
			t.Errorf("%q not trashed", n)
		}
		if !strings.Contains(out, "removed '"+n+"'\n") {
			t.Errorf("%q missing from verbose output %q", n, out)
		}
	}
}

func TestDelete(t *testing.T) {
	root, files := fixture(t)
	t.Chdir(root)
	if code, out, errOut := rm(t, "", "-dv", "file", "ne", "link"); code != 0 || errOut != "" ||
		out != "removed 'file'\nremoved directory 'ne'\nremoved 'link'\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	for _, n := range []string{"file", "ne", "link"} {
		if exists(n) {
			t.Errorf("%s still exists", n)
		}
		if exists(filepath.Join(files, n)) {
			t.Errorf("%s was trashed, not deleted", n)
		}
	}
	if code, _, errOut := rm(t, "n\n", "--delete", "-i", "empty"); code != 0 || errOut != "rm: remove regular empty file 'empty'? " || !exists("empty") {
		t.Errorf("-i: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "-d", "nope"); code != 1 || !strings.Contains(errOut, "No such file or directory") {
		t.Errorf("missing: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := rm(t, "", "-d", "-r", "/"); code != 1 || !strings.Contains(errOut, "dangerous") {
		t.Errorf("root: code=%d err=%q", code, errOut)
	}
}

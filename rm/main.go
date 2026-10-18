// Command rm is a drop-in replacement for GNU rm(1) that moves files to the
// trash (freedesktop.org Trash specification) instead of unlinking them.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	trash "codeberg.org/yaklib/go-trash"
)

// prog is the name used in diagnostics, like GNU rm uses argv[0].
var prog = "rm"

// Interactive modes, mirroring coreutils' RMI_NEVER/RMI_SOMETIMES/RMI_ALWAYS.
const (
	sometimes = iota // prompt only for write-protected files on a terminal
	never
	always
)

type options struct {
	interactive    int
	ignoreMissing  bool // -f
	promptOnce     bool // -I
	recursive      bool // explicit -r; only affects the -I prompt
	verbose        bool
	noPreserveRoot bool
	delete         bool // -d: unlink instead of trashing
}

var (
	errHelp    = errors.New("help")
	errVersion = errors.New("version")
)

func main() {
	prog = filepath.Base(os.Args[0])
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, files, err := parseArgs(args)
	switch {
	case err == errHelp:
		fmt.Fprintf(stdout, usage, prog, prog, prog)
		return 0
	case err == errVersion:
		fmt.Fprintf(stdout, "%s (go-trash) %s\n", prog, version())
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\nTry '%s --help' for more information.\n", prog, err, prog)
		return 1
	}
	if len(files) == 0 {
		if o.ignoreMissing {
			return 0
		}
		fmt.Fprintf(stderr, "%s: missing operand\nTry '%s --help' for more information.\n", prog, prog)
		return 1
	}

	in := bufio.NewReader(stdin)
	if o.promptOnce && (len(files) > 3 || o.recursive) {
		plural, rec := "", ""
		if len(files) > 1 {
			plural = "s"
		}
		if o.recursive {
			rec = " recursively"
		}
		fmt.Fprintf(stderr, "%s: remove %d argument%s%s? ", prog, len(files), plural, rec)
		if !yes(in) {
			return 0
		}
	}

	status := 0
	tty := isTerminal(stdin)
	for _, f := range files {
		if !remove(o, f, in, tty, stdout, stderr) {
			status = 1
		}
	}
	return status
}

// remove trashes one operand, reporting diagnostics like GNU rm. It returns
// false if a diagnostic was issued (a declined prompt is not an error).
func remove(o *options, path string, in *bufio.Reader, tty bool, stdout, stderr io.Writer) bool {
	fail := func(msg string) bool {
		fmt.Fprintf(stderr, "%s: cannot remove '%s': %s\n", prog, path, msg)
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if o.ignoreMissing && errors.Is(err, fs.ErrNotExist) {
			return true
		}
		return fail(errStr(err))
	}
	if fi.IsDir() {
		// Directories are always trashed whole, with or without -r.
		if base := filepath.Base(path); base == "." || base == ".." {
			fmt.Fprintf(stderr, "%s: refusing to remove '.' or '..' directory: skipping '%s'\n", prog, path)
			return false
		}
		if abs, err := filepath.Abs(path); err == nil && filepath.Dir(abs) == abs && !o.noPreserveRoot {
			fmt.Fprintf(stderr, "%s: it is dangerous to operate recursively on '%s'\n", prog, path)
			fmt.Fprintf(stderr, "%s: use --no-preserve-root to override this failsafe\n", prog)
			return false
		}
	}
	if !confirm(o, fi, path, in, tty, stderr) {
		return true
	}
	if o.delete {
		err = os.RemoveAll(path)
	} else {
		err = trash.Trash(path)
	}
	if err != nil {
		return fail(errStr(err))
	}
	if o.verbose {
		if fi.IsDir() {
			fmt.Fprintf(stdout, "removed directory '%s'\n", path)
		} else {
			fmt.Fprintf(stdout, "removed '%s'\n", path)
		}
	}
	return true
}

// confirm implements rm's prompting rules: always with -i; for
// write-protected non-symlinks when stdin is a terminal; never with -f.
func confirm(o *options, fi os.FileInfo, path string, in *bufio.Reader, tty bool, stderr io.Writer) bool {
	if o.interactive == never {
		return true
	}
	protected := false
	if (o.interactive == always || tty) && fi.Mode()&os.ModeSymlink == 0 {
		protected = fi.Mode().Perm()&0o222 == 0
	}
	if !protected && o.interactive != always {
		return true
	}
	prefix := ""
	if protected {
		prefix = "write-protected "
	}
	fmt.Fprintf(stderr, "%s: remove %s%s '%s'? ", prog, prefix, fileType(fi), path)
	return yes(in)
}

func yes(in *bufio.Reader) bool {
	line, _ := in.ReadString('\n')
	line = strings.TrimLeft(line, " \t")
	return strings.HasPrefix(line, "y") || strings.HasPrefix(line, "Y")
}

func fileType(fi os.FileInfo) string {
	switch m := fi.Mode(); {
	case m.IsDir():
		return "directory"
	case m&os.ModeSymlink != 0:
		return "symbolic link"
	case m&os.ModeNamedPipe != 0:
		return "fifo"
	case m&os.ModeSocket != 0:
		return "socket"
	case m&os.ModeCharDevice != 0:
		return "character special file"
	case m&os.ModeDevice != 0:
		return "block special file"
	case fi.Size() == 0:
		return "regular empty file"
	default:
		return "regular file"
	}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// errStr renders err the way strerror(3) would ("No such file or directory").
func errStr(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		s := errno.Error()
		return strings.ToUpper(s[:1]) + s[1:]
	}
	return err.Error()
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "unknown"
}

// parseArgs parses GNU-style options: bundled short options (-rf), long
// options with optional =value, "--" to end options, and options permuted
// with operands.
func parseArgs(args []string) (*options, []string, error) {
	o := &options{}
	var files []string
	for i, a := range args {
		switch {
		case a == "--":
			return o, append(files, args[i+1:]...), nil
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			if err := o.long(name, val, hasVal); err != nil {
				return nil, nil, err
			}
		case len(a) > 1 && a[0] == '-':
			for _, c := range a[1:] {
				if err := o.short(c); err != nil {
					return nil, nil, err
				}
			}
		default:
			files = append(files, a)
		}
	}
	return o, files, nil
}

func (o *options) short(c rune) error {
	switch c {
	case 'f':
		o.interactive, o.ignoreMissing, o.promptOnce = never, true, false
	case 'i':
		o.interactive, o.ignoreMissing, o.promptOnce = always, false, false
	case 'I':
		o.interactive, o.ignoreMissing, o.promptOnce = sometimes, false, true
	case 'r', 'R':
		o.recursive = true
	case 'd':
		o.delete = true
	case 'v':
		o.verbose = true
	default:
		return fmt.Errorf("invalid option -- '%c'", c)
	}
	return nil
}

func (o *options) long(name, val string, hasVal bool) error {
	switch name {
	case "interactive":
		switch val {
		case "never", "no", "none":
			o.interactive, o.ignoreMissing, o.promptOnce = never, false, false
		case "once":
			o.interactive, o.ignoreMissing, o.promptOnce = sometimes, false, true
		case "always", "yes", "":
			o.interactive, o.ignoreMissing, o.promptOnce = always, false, false
		default:
			return fmt.Errorf("invalid argument ‘%s’ for ‘--interactive’\n"+
				"Valid arguments are:\n  - ‘never’, ‘no’, ‘none’\n  - ‘once’\n  - ‘always’, ‘yes’", val)
		}
		return nil
	case "preserve-root":
		if hasVal && val != "all" {
			return fmt.Errorf("unrecognized --preserve-root argument: '%s'", val)
		}
		o.noPreserveRoot = false
		return nil
	}
	if hasVal {
		return fmt.Errorf("option '--%s' doesn't allow an argument", name)
	}
	switch name {
	case "force":
		return o.short('f')
	case "recursive":
		return o.short('r')
	case "verbose":
		return o.short('v')
	case "delete":
		return o.short('d')
	case "one-file-system":
		// A directory is trashed as a whole; see README.
	case "no-preserve-root":
		o.noPreserveRoot = true
	case "help":
		return errHelp
	case "version":
		return errVersion
	default:
		return fmt.Errorf("unrecognized option '--%s'", name)
	}
	return nil
}

const usage = `Usage: %s [OPTION]... [FILE]...
Move the FILE(s) to the trash instead of unlinking them
(freedesktop.org Trash specification).

  -f, --force           ignore nonexistent files and arguments, never prompt
  -i                    prompt before every removal
  -I                    prompt once before removing more than three files,
                          or when removing recursively; less intrusive than -i,
                          while still giving protection against most mistakes
      --interactive[=WHEN]  prompt according to WHEN: never, once (-I), or
                          always (-i); without WHEN, prompt always
      --one-file-system  accepted for compatibility only: a directory is
                          trashed as a whole, so one containing a mount point
                          is reported as an error rather than partially removed
      --no-preserve-root  do not treat '/' specially
      --preserve-root[=all]  do not remove '/' (default); 'all' is accepted
                          for compatibility (a mount point can never be trashed)
  -r, -R, --recursive   accepted for compatibility: directories are always
                          trashed together with their contents, even without -r
  -d, --delete          permanently delete (unlink) instead of trashing, like
                          GNU rm -r; directories are removed with their contents
  -v, --verbose         explain what is being done
      --help            display this help and exit
      --version         output version information and exit

Unlike GNU rm, directories are removed (trashed whole) without -r, because
trashing a directory is a single atomic move, never a partial deletion.

Any attempt to remove a file whose last file name component is '.' or '..'
is rejected with a diagnostic.

To remove a file whose name starts with a '-', for example '-foo',
use one of these commands:
  %s -- -foo

  %s ./-foo

Without -d, files are never erased: they are moved to $XDG_DATA_HOME/Trash or to the
.Trash directory of the file's own filesystem, and can be restored with any
freedesktop.org-compatible trash tool (gio trash, trash-cli, file managers).
`

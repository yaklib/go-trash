# go-trash

A small, dependency-free Go library and an `rm`-compatible command that move
files into the trash as described by the
[FreeDesktop.org Trash specification, version 1.0](https://specifications.freedesktop.org/trash-spec/latest/).

Files trashed by this package are visible to, and restorable with, any other
compliant implementation (GNOME `gio trash`, KDE, `trash-cli`, file managers).

## Library

```go
import trash "codeberg.org/yaklib/go-trash"

err := trash.Trash("some/file or dir")
```

`Trash` moves the file or directory at the given path into a trash directory
and writes the matching `.trashinfo` file. It is the only exported API.

What it does, in spec terms:

* **Home trash.** A file on the same filesystem as `$XDG_DATA_HOME`
  (default `~/.local/share`) goes to `$XDG_DATA_HOME/Trash`, created on
  demand. `Path=` is stored absolute.
* **Top-directory trash.** A file on another filesystem goes to the trash of
  that filesystem's mount point `$topdir`: `$topdir/.Trash/$uid` if
  `$topdir/.Trash` exists, is not a symlink, has the sticky bit and the `$uid`
  subdirectory can be created; otherwise `$topdir/.Trash-$uid`. `Path=` is
  stored relative to `$topdir`, as the spec requires.
* **Atomic, collision-free naming.** The `info/NAME.trashinfo` file is created
  first with `O_EXCL`; on collision `NAME.2`, `NAME.3`, … are tried. Only then
  is the file renamed into `files/NAME`. Trashing the same name repeatedly
  never overwrites an earlier entry.
* **Names are preserved byte-for-byte.** Unicode, spaces, `%`, control
  characters and even newlines in file names are fine: the entry in `files/`
  keeps the original name, and `Path=` is percent-encoded per RFC 2396 (every
  byte except unreserved characters and `/`), e.g.
  `Path=/home/u/w%C3%AFth%0Anewline%20%25.txt`.
* **Symbolic links are trashed as links**; the target is never followed.
* `DeletionDate` is local time in `YYYY-MM-DDThh:mm:ss` form.
* Pure Go, no cgo. Builds on every Go platform; on non-Unix systems only the
  home trash is used (see limitations).

## `rm` command

`rm` is a drop-in replacement for GNU `rm(1)` that trashes instead of
unlinking. It accepts the same options (`-f -i -I --interactive[=WHEN]
--one-file-system --no-preserve-root --preserve-root[=all] -r -R --recursive
-v --verbose --help --version`), except that `-d` is repurposed: `-d, --delete`
permanently deletes (unlinks, recursively for directories) instead of
trashing, and GNU's `--dir` is not accepted. It supports bundled short options (`-rf`),
options after operands, `--` to end options, and prints the same diagnostics
and exit statuses as GNU rm.

```sh
go build -o ~/bin/rm ./rm       # put it before coreutils in $PATH, or:
alias rm=~/bin/rm
```

Deliberate differences from GNU rm:

| GNU rm | this rm |
|---|---|
| Refuses directories without `-r` (`Is a directory`); `-d` removes empty ones. | **Directories are always trashed whole**, with or without `-r`. Trashing a directory is one atomic `rename`, so there is no partial-deletion risk that `-r` guards against. `-r`/`-R` are accepted and ignored, except that an explicit `-r` still triggers `-I`'s "remove N arguments recursively?" prompt. |
| `-r -i` prompts for every entry while descending (`descend into directory …?`, then each file). | `-i` asks once per operand: `remove directory 'd'?`. The directory is moved as a unit; there is nothing to descend into. |
| `--one-file-system` skips sub-trees on other filesystems. | Accepted, no effect. A directory containing a mount point cannot be moved at all; `rename(2)` fails with `EBUSY` and the operand is reported as an error, nothing is removed. |
| `--preserve-root=all` rejects operands that are mount points. | Accepted, no effect: a mount point can never be renamed, so the protection is inherent. |
| `-d, --dir` removes empty directories. | **`-d, --delete` means something else here**: delete for real (`os.RemoveAll`, recursive) instead of trashing. All prompts and safety checks still apply; `--one-file-system` is ignored here too. `--dir` is rejected as unknown. |
| `rm -v` prints `removed 'x'`. | Same wording, to stay a drop-in; the file is in the trash. |
| Long options may be abbreviated (`--rec`). Names are shell-quoted in messages (`'a'$'\n''b'`). | Full option names only. Names are printed verbatim inside `'…'`. |

## Limitations and spec corners

Things the spec leaves open, or that this implementation chooses not to do.
They are documented here so you are not surprised.

### From the specification itself

* **No cross-filesystem copying.** The spec says an implementation "MUST
  either trash the file into the user's home trash or refuse" when neither
  `$topdir/.Trash/$uid` nor `$topdir/.Trash-$uid` can be used. This library
  refuses (returns an error, the file is untouched) rather than copying data
  across filesystems. Consequences: a file on a read-only, full, or
  non-writable mount (a USB stick where you cannot create `.Trash-$uid`, an
  NFS export with root-squash, …) cannot be trashed. Use coreutils `rm` for
  those.
* **Network/foreign filesystems without POSIX uids** (CIFS, FTP) are
  explicitly out of the spec's scope. Here they are treated like any other
  mount: a `.Trash-$uid` is attempted, and if that fails the call errors.
* **Trash is per-user by numeric uid.** Files trashed as one uid are
  invisible to another; `sudo rm` trashes into root's `~/.local/share/Trash`
  or `$topdir/.Trash-0`.
* **`DeletionDate` has no time zone.** The spec mandates local time without
  an offset, so the stamp is ambiguous across DST changes and machines in
  different zones. (Some implementations, including the `rmx` tool this
  replaces, write UTC; readers cannot tell the difference.)
* **`Path=` for top-directory trashes is relative to the mount point.** If
  the device is later mounted elsewhere, the recorded path still resolves
  relative to the new mount point; that is by design, but it means the path
  in `.trashinfo` is not the absolute path you deleted.
* **The name in `files/` is not authoritative.** The spec forbids recovering
  the original name from it; always use the `.trashinfo`. This library keeps
  the original base name (plus `.N` on collision) purely as a convenience.
* **Percent-encoding scope.** The spec references RFC 2396 but only requires
  escaping what is necessary. This implementation escapes every byte except
  `A–Z a–z 0–9 - _ . ! ~ * ' ( ) /` so that readers which decode
  conservatively still get the right bytes; readers MUST accept that per the
  spec.
* **The `.Trash` checks are silent.** The spec says a `$topdir/.Trash` that
  fails the sticky-bit/symlink checks "SHOULD be reported to the
  administrator". This library just falls back to `.Trash-$uid` quietly.
* **`directorysizes` cache is not maintained.** The spec (1.0) says
  implementations SHOULD update `$trash/directorysizes` when trashing a
  directory. This library does not; readers treat a missing entry as "not
  cached" and compute the size themselves, so nothing breaks, only their
  first size calculation is slower.
* **Permission check before trashing.** The spec suggests checking that the
  user may delete the file before starting. This library simply attempts the
  `rename`; a permission problem surfaces as an error and nothing is
  half-done.
* **"Trashing the trash"** is not prevented. Trashing a file that already
  lives under a `$trash/files` directory creates a nested trash entry.
  (The spec's footnote asks implementations to avoid this; it is left to the
  caller.)

### Implementation limits

* **Name length.** The info file is `NAME.trashinfo`, so a base name longer
  than `NAME_MAX − 10` bytes (245 on most Linux filesystems) fails with
  `File name too long` rather than being truncated. Renaming the file first
  is the workaround.
* **Windows / non-Unix.** Filesystem identity cannot be determined portably,
  so everything is assumed to be on the home-trash filesystem. A rename across
  volumes fails with the OS error. There is no integration with the Windows
  Recycle Bin or macOS `~/.Trash`; this is the freedesktop layout only.
* **Mount points and directories containing mount points** cannot be trashed
  (`rename` returns `EBUSY`/`EXDEV`).
* **Bind mounts and overlayfs** can confuse the "same device" test used to
  pick home vs. top-directory trash; the worst case is an `EXDEV` error from
  `rename`, never data loss.
* **Listing, restoring and emptying the trash are not implemented.** Use
  `gio trash --list` / `gio trash --restore`, `trash-list`/`trash-restore`
  from trash-cli, or a file manager.

## Testing

```sh
go test ./...
```

The tests cover unicode, whitespace, newline, `%` and shell-metacharacter
names, repeated trashing of the same name, directories, symlinks, relative
paths, the four `.Trash` selection rules, and the full `rm` option surface
(prompts, exit codes and diagnostics were checked against GNU coreutils 9.11).
The cross-filesystem path is unit-tested for directory selection only; there
is no portable way to get a second mount inside `go test`.

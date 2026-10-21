package helmjournal

import "golang.org/x/sys/unix"

// No copy/delete fallback: unsupported filesystems fail closed.
func (r *privateRoot) renameNoReplace(old, new string) error {
	if r.check() != nil {
		return ErrUnavailable
	}
	if err := unix.Renameat2(int(r.dir.Fd()), old, int(r.dir.Fd()), new, unix.RENAME_NOREPLACE); err != nil {
		return ErrUnavailable
	}
	return nil
}

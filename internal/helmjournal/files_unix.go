//go:build linux || darwin

package helmjournal

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openPrivateRoot(path string) (*privateRoot, error) {
	return openPrivateRootMode(path, true)
}

func openExistingPrivateRoot(path string) (*privateRoot, error) {
	return openPrivateRootMode(path, false)
}

func openPrivateRootMode(path string, create bool) (*privateRoot, error) {
	created := false
	if create {
		if err := os.Mkdir(path, 0700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, ErrUnavailable
			}
		} else {
			created = true
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if !create && errors.Is(err, os.ErrNotExist) {
			return nil, errStoreNotInitialized
		}
		return nil, ErrUnavailable
	}
	dir := os.NewFile(uintptr(fd), path)
	if validatePrivateFile(dir, true) != nil {
		dir.Close()
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		dir.Close()
		return nil, ErrUnavailable
	}
	r := &privateRoot{path: path, dir: dir, root: root}
	if r.check() != nil {
		r.close()
		return nil, ErrUnavailable
	}
	if created {
		parent, err := os.Open(filepath.Dir(path))
		if err != nil {
			r.close()
			return nil, ErrUnavailable
		}
		syncErr, closeErr := parent.Sync(), parent.Close()
		if syncErr != nil || closeErr != nil {
			r.close()
			return nil, ErrUnavailable
		}
	}
	return r, nil
}

func validatePrivateFile(f *os.File, directory bool) error {
	if f == nil {
		return ErrUnavailable
	}
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || st.Uid != uint32(os.Geteuid()) {
		return ErrUnavailable
	}
	mode := st.Mode
	if directory {
		if mode&unix.S_IFMT != unix.S_IFDIR || mode&07777 != 0700 {
			return ErrUnavailable
		}
	} else if mode&unix.S_IFMT != unix.S_IFREG || mode&07777 != 0600 || st.Nlink != 1 {
		return ErrUnavailable
	}
	return nil
}

func (r *privateRoot) openFile(name string, exclusive, writable bool) (*os.File, error) {
	if r.check() != nil {
		return nil, ErrUnavailable
	}
	flags := os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if writable {
		flags = os.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK
	}
	if exclusive {
		flags |= os.O_CREATE | os.O_EXCL
	} else if writable && name == lockName {
		flags |= os.O_CREATE
	}
	f, err := r.root.OpenFile(name, flags, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	if r.checkFile(name, f) != nil {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

func (r *privateRoot) sync() error {
	if r.dir.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}

func acquireLock(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrBusy
		}
		return ErrUnavailable
	}
	return nil
}

func releaseLock(f *os.File) error {
	if unix.Flock(int(f.Fd()), unix.LOCK_UN) != nil {
		return ErrUnavailable
	}
	return nil
}

//go:build !linux && !darwin && !windows

package helmjournal

import "os"

func openPrivateRoot(string) (*privateRoot, error)                 { return nil, ErrUnavailable }
func validatePrivateFile(*os.File, bool) error                     { return ErrUnavailable }
func (*privateRoot) openFile(string, bool, bool) (*os.File, error) { return nil, ErrUnavailable }
func (*privateRoot) sync() error                                   { return ErrUnavailable }
func acquireLock(*os.File) error                                   { return ErrUnavailable }
func releaseLock(*os.File) error                                   { return ErrUnavailable }

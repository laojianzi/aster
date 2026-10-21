//go:build !linux && !darwin && !windows

package helmjournal

func (*privateRoot) renameNoReplace(string, string) error { return ErrUnavailable }

//go:build !linux && !darwin && !windows

package credentialvault

func native(request) ([]byte, error) { return nil, ErrUnavailable }

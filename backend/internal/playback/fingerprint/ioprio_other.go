//go:build !linux

package fingerprint

func setIdleIOPriority(int) {}

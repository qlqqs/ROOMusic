//go:build !linux

package analysis

func setIdleIOPriority(int) {}

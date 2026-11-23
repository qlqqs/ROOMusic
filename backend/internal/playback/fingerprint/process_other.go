//go:build !unix

package fingerprint

import "os/exec"

const platformSupported = false

func configureProcess(*exec.Cmd) {}
func setLowPriority(int)         {}

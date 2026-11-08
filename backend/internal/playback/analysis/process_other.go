//go:build !unix

package analysis

import "os/exec"

const platformSupported = false

func configureProcess(*exec.Cmd) {}
func setLowPriority(int)         {}

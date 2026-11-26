//go:build !linux

package library

import (
	"context"
	"errors"
)

var errNetworkMount = errors.New("网络共享目录")

type watchEvent struct {
	dirs        []string
	removedTree string
	move        uint32
	overflow    bool
}

type watchSystem struct{}

func defaultWatchSystem() watchSystem { return watchSystem{} }

func (watchSystem) watch(context.Context, registeredRoot, func(), func(watchEvent), []string) error {
	return errors.ErrUnsupported
}

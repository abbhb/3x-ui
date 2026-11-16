//go:build !linux && !darwin && !freebsd

package network

import (
	"errors"
	"net"
)

func ListenPrivateUnix(string) (net.Listener, error) {
	return nil, errors.New("Unix listeners are unsupported on this operating system")
}

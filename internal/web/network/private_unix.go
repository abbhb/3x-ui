//go:build linux || darwin || freebsd

package network

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type privateUnixListener struct {
	net.Listener
	lock *os.File
	once sync.Once
	err  error
}

func (l *privateUnixListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close(); _ = l.lock.Close() })
	return l.err
}

// A private parent and an advisory lock protect both the socket and stale-file
// recovery. Only the reverse proxy's deliberately shared group may access it.
func ListenPrivateUnix(path string) (net.Listener, error) {
	if !filepath.IsAbs(path) || len(path) > 100 {
		return nil, errors.New("Unix socket needs a short absolute path")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&7 != 0 {
		return nil, errors.New("Unix socket parent must be a private existing directory")
	}
	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	owned := false
	defer func() {
		if !owned {
			_ = lock.Close()
		}
	}()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.New("Unix socket is already owned by a running service")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing to replace a non-socket path")
		}
		conn, dialErr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(context.Background(), "unix", path)
		if dialErr == nil {
			_ = conn.Close()
			return nil, errors.New("Unix socket already has a listener")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, dialErr
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = listener.Close()
		return nil, err
	}
	owned = true
	return &privateUnixListener{Listener: listener, lock: lock}, nil
}

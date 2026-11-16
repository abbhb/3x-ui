//go:build linux || darwin || freebsd

package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateUnixListenerOwnershipAndCleanup(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "xui-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "panel.sock")
	listener, err := ListenPrivateUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ListenPrivateUnix(path); err == nil {
		t.Fatal("second process stole the Unix endpoint")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o660 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned socket remained after shutdown")
	}
	listener, err = ListenPrivateUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := os.WriteFile(path, []byte("synthetic protected file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenPrivateUnix(path); err == nil {
		t.Fatal("replaced a regular file")
	}
}

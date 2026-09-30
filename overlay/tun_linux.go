//go:build linux

package overlay

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type tunDevice struct {
	file *os.File
	name string
}

func openTUN(name string) (Device, error) {
	if name == "" {
		name = "enserie0"
	}
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(int(f.Fd()), unix.TUNSETIFF, ifr); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("TUNSETIFF: %w", err)
	}
	got := ifr.Name()
	return &tunDevice{file: f, name: got}, nil
}

func (t *tunDevice) Name() string { return t.name }

func (t *tunDevice) ReadPacket(b []byte) (int, error) {
	return t.file.Read(b)
}

func (t *tunDevice) WritePacket(b []byte) (int, error) {
	return t.file.Write(b)
}

func (t *tunDevice) Close() error { return t.file.Close() }

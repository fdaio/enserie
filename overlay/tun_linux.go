//go:build linux

package overlay

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type tunDevice struct {
	file *os.File
	fd   int
	name string
}

const tunSlots = 16

func openTUN(name string) (Device, error) {
	if name != "" {
		return openTUNNamed(name)
	}
	var last error
	for i := 0; i < tunSlots; i++ {
		d, err := openTUNNamed(fmt.Sprintf("enserie%d", i))
		if err == nil {
			return d, nil
		}
		last = err
		if !isTUNBusy(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no free TUN (enserie0-enserie%d busy): %w", tunSlots-1, last)
}

func isTUNBusy(err error) bool {
	return errors.Is(err, unix.EBUSY) || errors.Is(err, unix.EEXIST)
}

func openTUNNamed(name string) (Device, error) {
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
	fd := int(f.Fd())
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("TUNSETIFF %s: %w", name, err)
	}
	// Orb virtio TUN does not wake the Go epoll poller. A blocking
	// unix.Read sees packets that file.Read never returns.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &tunDevice{file: f, fd: fd, name: ifr.Name()}, nil
}

func (t *tunDevice) Name() string { return t.name }

func (t *tunDevice) ReadPacket(b []byte) (int, error) {
	return unix.Read(t.fd, b)
}

func (t *tunDevice) WritePacket(b []byte) (int, error) {
	return unix.Write(t.fd, b)
}

func (t *tunDevice) Close() error { return t.file.Close() }

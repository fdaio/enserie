//go:build darwin

package overlay

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	utunControlName = "com.apple.net.utun_control"
	utunOptIfname   = 2
)

type tunDevice struct {
	file *os.File
	name string
}

func openTUN(_ string) (Device, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, unix.AF_SYS_CONTROL)
	if err != nil {
		return nil, err
	}
	var info unix.CtlInfo
	copy(info.Name[:], utunControlName)
	if err := unix.IoctlCtlInfo(fd, &info); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// Unit 0 lets the kernel pick the next utun index.
	sa := &unix.SockaddrCtl{ID: info.Id, Unit: 0}
	if err := unix.Connect(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	name, err := unix.GetsockoptString(fd, unix.AF_SYS_CONTROL, utunOptIfname)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("utun: NewFile failed")
	}
	return &tunDevice{file: file, name: name}, nil
}

func (t *tunDevice) Name() string { return t.name }

func (t *tunDevice) ReadPacket(b []byte) (int, error) {
	// utun prefixes a 4-byte address family.
	buf := make([]byte, len(b)+4)
	n, err := t.file.Read(buf)
	if err != nil {
		return 0, err
	}
	if n < 4 {
		return 0, fmt.Errorf("utun short read")
	}
	return copy(b, buf[4:n]), nil
}

func (t *tunDevice) WritePacket(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	family := uint32(unix.AF_INET)
	if b[0]>>4 == 6 {
		family = uint32(unix.AF_INET6)
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf[:4], family)
	copy(buf[4:], b)
	n, err := t.file.Write(buf)
	if err != nil {
		return 0, err
	}
	if n < 4 {
		return 0, fmt.Errorf("utun short write")
	}
	return n - 4, nil
}

func (t *tunDevice) Close() error { return t.file.Close() }

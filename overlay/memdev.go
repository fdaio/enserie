package overlay

import (
	"fmt"
	"sync"
	"time"
)

// memDevice is a userspace TUN for tests. injectLocal mimics a kernel write
// into the overlay. waitRemote returns packets the overlay wrote out.
type memDevice struct {
	fromKernel chan []byte
	toKernel   chan []byte
	closed     chan struct{}
	once       sync.Once
}

func newMemDevice() *memDevice {
	return &memDevice{
		fromKernel: make(chan []byte, 8),
		toKernel:   make(chan []byte, 8),
		closed:     make(chan struct{}),
	}
}

func (d *memDevice) Name() string { return "mem0" }

func (d *memDevice) ReadPacket(b []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, fmt.Errorf("device closed")
	case pkt := <-d.fromKernel:
		return copy(b, pkt), nil
	}
}

func (d *memDevice) WritePacket(b []byte) (int, error) {
	pkt := append([]byte(nil), b...)
	select {
	case <-d.closed:
		return 0, fmt.Errorf("device closed")
	case d.toKernel <- pkt:
		return len(b), nil
	default:
		select {
		case <-d.closed:
			return 0, fmt.Errorf("device closed")
		case d.toKernel <- pkt:
			return len(b), nil
		case <-time.After(2 * time.Second):
			return 0, fmt.Errorf("device write timeout")
		}
	}
}

func (d *memDevice) Close() error {
	d.once.Do(func() { close(d.closed) })
	return nil
}

func (d *memDevice) injectLocal(pkt []byte) error {
	select {
	case <-d.closed:
		return fmt.Errorf("device closed")
	case d.fromKernel <- append([]byte(nil), pkt...):
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("inject timeout")
	}
}

func (d *memDevice) waitRemote(timeout time.Duration) ([]byte, error) {
	select {
	case <-d.closed:
		return nil, fmt.Errorf("device closed")
	case pkt := <-d.toKernel:
		return pkt, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for packet")
	}
}

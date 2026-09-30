//go:build !linux && !darwin

package overlay

import "fmt"

func openTUN(string) (Device, error) {
	return nil, fmt.Errorf("TUN is not supported on this platform")
}

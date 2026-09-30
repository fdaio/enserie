package overlay

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
)

const overlayMTU = 1280

func configureTUN(name string, local, peer net.IP, mask net.IPMask) error {
	ones, _ := mask.Size()
	switch runtime.GOOS {
	case "darwin":
		netmask := net.IP(mask).String()
		cmd := exec.Command("ifconfig", name, "inet", local.String(), peer.String(), "netmask", netmask, "mtu", strconv.Itoa(overlayMTU), "up")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ifconfig %s: %w (%s)", name, err, out)
		}
		return nil
	case "linux":
		cidr := fmt.Sprintf("%s/%d", local.String(), ones)
		if out, err := exec.Command("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(overlayMTU)).CombinedOutput(); err != nil {
			return fmt.Errorf("ip link mtu: %w (%s)", err, out)
		}
		if out, err := exec.Command("ip", "addr", "add", cidr, "dev", name).CombinedOutput(); err != nil {
			return fmt.Errorf("ip addr add: %w (%s)", err, out)
		}
		if out, err := exec.Command("ip", "link", "set", "dev", name, "up").CombinedOutput(); err != nil {
			return fmt.Errorf("ip link up: %w (%s)", err, out)
		}
		return nil
	default:
		return fmt.Errorf("address setup is not supported on %s", runtime.GOOS)
	}
}

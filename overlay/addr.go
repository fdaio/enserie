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
	switch runtime.GOOS {
	case "darwin":
		netmask := net.IP(mask).String()
		cmd := exec.Command("ifconfig", name, "inet", local.String(), peer.String(), "netmask", netmask, "mtu", strconv.Itoa(overlayMTU), "up")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ifconfig %s: %w (%s)", name, err, out)
		}
		return nil
	case "linux":
		for _, args := range linuxTUNCommands(name, local, peer, overlayMTU) {
			if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
				return fmt.Errorf("ip %s: %w (%s)", args[0], err, out)
			}
		}
		return nil
	default:
		return fmt.Errorf("address setup is not supported on %s", runtime.GOOS)
	}
}

func linuxTUNCommands(name string, local, peer net.IP, mtu int) [][]string {
	ms := strconv.Itoa(mtu)
	return [][]string{
		{"link", "set", "dev", name, "mtu", ms},
		{"addr", "add", local.String() + "/32", "peer", peer.String() + "/32", "dev", name},
		{"link", "set", "dev", name, "up"},
	}
}

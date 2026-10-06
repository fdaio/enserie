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
		configureLinuxTUNFirewall(name)
		_ = exec.Command("sysctl", "-w", "net.ipv4.conf."+name+".rp_filter=0").Run()
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
		// Some kernels keep the peer address but omit the on-link /32.
		{"route", "replace", peer.String() + "/32", "dev", name, "src", local.String()},
	}
}

// linuxTUNFirewallCommands returns the iptables commands that open a device to
// overlay traffic. direction is the flag that names the device, which differs
// between the input and the output chain.
func linuxTUNFirewallCommands(chain, flag, name string) [][]string {
	return [][]string{
		{"-C", chain, flag, name, "-j", "ACCEPT"},
		{"-I", chain, flag, name, "-j", "ACCEPT"},
		{"-D", chain, flag, name, "-j", "ACCEPT"},
	}
}

// linuxTUNFirewallRules returns one command set per chain, each holding the
// check, the insert and the matching delete.
func linuxTUNFirewallRules(name string) [][][]string {
	return [][][]string{
		linuxTUNFirewallCommands("INPUT", "-i", name),
		linuxTUNFirewallCommands("OUTPUT", "-o", name),
	}
}

func configureLinuxTUNFirewall(name string) {
	for _, rules := range linuxTUNFirewallRules(name) {
		if exec.Command("iptables", rules[0]...).Run() == nil {
			continue
		}
		_ = exec.Command("iptables", rules[1]...).Run()
	}
}

// removeLinuxTUNFirewall drops the rules configureLinuxTUNFirewall added.
// Closing the TUN removes the device, which also removes its routes, but the
// rules naming it stay in the host chains and then point at a device that no
// longer exists.
func removeLinuxTUNFirewall(name string) {
	if runtime.GOOS != "linux" {
		return
	}
	for _, rules := range linuxTUNFirewallRules(name) {
		_ = exec.Command("iptables", rules[2]...).Run()
	}
}

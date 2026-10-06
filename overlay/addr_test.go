package overlay

import (
	"net"
	"testing"
)

func TestLinuxTUNCommandsArePointToPoint(t *testing.T) {
	local := net.ParseIP("198.18.0.1").To4()
	peer := net.ParseIP("198.18.0.2").To4()
	cmds := linuxTUNCommands("enserie0", local, peer, overlayMTU)
	if len(cmds) != 4 {
		t.Fatalf("got %d commands", len(cmds))
	}
	if !containsAll(cmds[1], "198.18.0.1/32", "peer", "198.18.0.2/32", "enserie0") {
		t.Fatalf("addr command %v", cmds[1])
	}
	if !containsAll(cmds[3], "route", "replace", "198.18.0.2/32", "enserie0", "198.18.0.1") {
		t.Fatalf("route command %v", cmds[3])
	}
}

func containsAll(args []string, want ...string) bool {
	have := map[string]bool{}
	for _, a := range args {
		have[a] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

// Each firewall rule set has to carry the delete that undoes its insert, or the
// rule outlives the interface it names.
func TestLinuxTUNFirewallRulesIncludeTheirDelete(t *testing.T) {
	rules := linuxTUNFirewallRules("enserie0")
	if len(rules) != 2 {
		t.Fatalf("got %d chains", len(rules))
	}
	for _, set := range rules {
		if len(set) != 3 {
			t.Fatalf("got %d commands in a chain", len(set))
		}
		check, insert, remove := set[0], set[1], set[2]
		if check[0] != "-C" || insert[0] != "-I" || remove[0] != "-D" {
			t.Fatalf("modes %v %v %v", check[0], insert[0], remove[0])
		}
		// The delete matches the insert in every argument but the mode, which
		// is what makes iptables remove that exact rule.
		if len(insert) != len(remove) {
			t.Fatalf("insert %v, delete %v", insert, remove)
		}
		for i := 1; i < len(insert); i++ {
			if insert[i] != remove[i] {
				t.Fatalf("insert %v, delete %v differ at %d", insert, remove, i)
			}
		}
		if insert[len(insert)-1] != "ACCEPT" {
			t.Fatalf("insert %v", insert)
		}
	}
}

func TestLinuxTUNFirewallRulesPickTheChainFlag(t *testing.T) {
	rules := linuxTUNFirewallRules("enserie0")
	if !containsAll(rules[0][1], "INPUT", "-i", "enserie0") {
		t.Errorf("input chain %v", rules[0][1])
	}
	if !containsAll(rules[1][1], "OUTPUT", "-o", "enserie0") {
		t.Errorf("output chain %v", rules[1][1])
	}
}

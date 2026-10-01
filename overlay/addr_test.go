package overlay

import (
	"net"
	"testing"
)

func TestLinuxTUNCommandsArePointToPoint(t *testing.T) {
	local := net.ParseIP("198.18.0.1").To4()
	peer := net.ParseIP("198.18.0.2").To4()
	cmds := linuxTUNCommands("enserie0", local, peer, overlayMTU)
	if len(cmds) != 3 {
		t.Fatalf("got %d commands", len(cmds))
	}
	if !containsAll(cmds[1], "198.18.0.1/32", "peer", "198.18.0.2/32", "enserie0") {
		t.Fatalf("addr command %v", cmds[1])
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

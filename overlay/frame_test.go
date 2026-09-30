package overlay

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestIPv4Dest(t *testing.T) {
	src := netIP(t, "10.7.0.1")
	dst := netIP(t, "10.7.0.2")
	pkt := ipv4Packet(src, dst, []byte("hi"))
	got, err := ipv4Dest(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(dst) {
		t.Fatalf("dest %s", got)
	}
}

func TestParseCIDRRejectsTinyPrefix(t *testing.T) {
	if _, _, err := parseIPv4("10.7.0.1/32"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteFrameSingleWrite(t *testing.T) {
	var w writeCounter
	if err := writeFrame(&w, typePacket, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if w.n != 1 {
		t.Fatalf("writes %d, want 1", w.n)
	}
}

type writeCounter struct {
	n int
}

func (w *writeCounter) Write(p []byte) (int, error) {
	w.n++
	return len(p), nil
}

func TestFrameHelloRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeHello(&buf, "alpha", "s3cret"); err != nil {
		t.Fatal(err)
	}
	id, secret, err := readHello(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if id != "alpha" || secret != "s3cret" {
		t.Fatalf("got id=%q secret=%q", id, secret)
	}
}

func netIP(t *testing.T, s string) []byte {
	t.Helper()
	ip, err := parseIPv4Host(s)
	if err != nil {
		t.Fatal(err)
	}
	return ip
}

func ipv4Packet(src, dst []byte, payload []byte) []byte {
	total := 20 + len(payload)
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64
	pkt[9] = 253
	copy(pkt[12:16], src)
	copy(pkt[16:20], dst)
	copy(pkt[20:], payload)
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(pkt[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	binary.BigEndian.PutUint16(pkt[10:12], ^uint16(sum))
	return pkt
}

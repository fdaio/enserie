package overlay

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

const (
	typeHello  byte = 1
	typePacket byte = 2
	maxFrame        = 64 << 10
)

type hello struct {
	ID string `json:"id"`
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload)+1 > maxFrame {
		return fmt.Errorf("overlay frame too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)+1))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{typ}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrame {
		return 0, nil, fmt.Errorf("overlay frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func writeHello(w io.Writer, id string) error {
	b, err := json.Marshal(hello{ID: id})
	if err != nil {
		return err
	}
	return writeFrame(w, typeHello, b)
}

func readHello(r io.Reader) (string, error) {
	typ, payload, err := readFrame(r)
	if err != nil {
		return "", err
	}
	if typ != typeHello {
		return "", fmt.Errorf("expected hello, got type %d", typ)
	}
	var h hello
	if err := json.Unmarshal(payload, &h); err != nil {
		return "", err
	}
	if h.ID == "" {
		return "", fmt.Errorf("hello missing id")
	}
	return h.ID, nil
}

func ipv4Dest(pkt []byte) (net.IP, error) {
	if len(pkt) < 20 {
		return nil, fmt.Errorf("ip packet too short")
	}
	if pkt[0]>>4 != 4 {
		return nil, fmt.Errorf("not ipv4")
	}
	return net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19]), nil
}

func ipv4Src(pkt []byte) (net.IP, error) {
	if len(pkt) < 20 {
		return nil, fmt.Errorf("ip packet too short")
	}
	if pkt[0]>>4 != 4 {
		return nil, fmt.Errorf("not ipv4")
	}
	return net.IPv4(pkt[12], pkt[13], pkt[14], pkt[15]), nil
}

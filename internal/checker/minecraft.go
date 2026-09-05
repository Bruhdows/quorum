package checker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Minecraft Server List Ping, the handshake kind, so 1.7 and up. A TCP
// check only proves something accepts connections on the port. The status
// exchange proves a real server sits behind it and is healthy enough to
// answer.
//
// Handshake packet first (protocol version, address, port, next state 1
// for status), then an empty status request, then the status response with
// its JSON payload. Latency covers the whole exchange, like every other
// check type.

const (
	mcTimeout     = 5 * time.Second
	mcMaxPacket   = 1 << 20 // Status JSON runs a few KB. This only bounds garbage.
	mcProtocolVer = 767     // Announced only. The server answers regardless.
	mcNextStatus  = 1
)

// minecraftStatus is the subset of the status response we validate.
type minecraftStatus struct {
	Version struct {
		Name string `json:"name"`
	} `json:"version"`
}

func checkMinecraft(ctx context.Context, target string) Result {
	start := time.Now()
	fail := func(err error) Result {
		return Result{Success: false, LatencyMS: int(time.Since(start).Milliseconds()), Error: err.Error()}
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return fail(fmt.Errorf("invalid target %q: want host:port", target))
	}
	var port int
	if port, err = strconv.Atoi(portStr); err != nil || port < 1 || port > 65535 {
		return fail(fmt.Errorf("invalid target %q: want host:port", target))
	}

	d := net.Dialer{Timeout: mcTimeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	// One deadline covers the whole exchange. The context still cancels
	// the dial.
	if err := conn.SetDeadline(time.Now().Add(mcTimeout)); err != nil {
		return fail(err)
	}
	r := bufio.NewReader(conn)

	var hs []byte
	hs = appendVarInt(hs, mcProtocolVer)
	hs = appendMCString(hs, host)
	hs = binary.BigEndian.AppendUint16(hs, uint16(port))
	hs = appendVarInt(hs, mcNextStatus)
	if err := writeMCPacket(conn, 0x00, hs); err != nil {
		return fail(err)
	}
	if err := writeMCPacket(conn, 0x00, nil); err != nil {
		return fail(err)
	}

	id, data, err := readMCPacket(r)
	if err != nil {
		return fail(err)
	}
	if id != 0x00 {
		return fail(fmt.Errorf("unexpected packet %#x in status response", id))
	}
	payload, err := readMCString(data)
	if err != nil {
		return fail(err)
	}
	var status minecraftStatus
	if err := json.Unmarshal([]byte(payload), &status); err != nil {
		return fail(fmt.Errorf("invalid status response: %v", err))
	}
	if status.Version.Name == "" {
		return fail(fmt.Errorf("invalid status response: missing version"))
	}
	return Result{Success: true, LatencyMS: int(time.Since(start).Milliseconds())}
}

// appendVarInt encodes a non-negative int as a Minecraft VarInt.
func appendVarInt(b []byte, v int) []byte {
	for {
		if v&^0x7F == 0 {
			return append(b, byte(v))
		}
		b = append(b, byte(v&0x7F|0x80))
		v >>= 7
	}
}

func appendMCString(b []byte, s string) []byte {
	b = appendVarInt(b, len(s))
	return append(b, s...)
}

// writeMCPacket frames one packet: length prefix, packet id, data.
func writeMCPacket(w io.Writer, id int, data []byte) error {
	var b []byte
	b = appendVarInt(b, id)
	b = append(b, data...)
	var frame []byte
	frame = appendVarInt(frame, len(b))
	frame = append(frame, b...)
	_, err := w.Write(frame)
	return err
}

func readVarInt(r *bufio.Reader) (int, error) {
	v := 0
	for i := 0; i < 5; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= int(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("varint too long")
}

// readMCPacket reads one framed packet, returning its id and data.
func readMCPacket(r *bufio.Reader) (int, []byte, error) {
	length, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if length <= 0 || length > mcMaxPacket {
		return 0, nil, fmt.Errorf("bad packet length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	rest := buf
	id, n := 0, -1
	for i := 0; i < len(rest) && i < 5; i++ {
		id |= int(rest[i]&0x7F) << (7 * i)
		if rest[i]&0x80 == 0 {
			n = i + 1
			break
		}
	}
	if n < 0 {
		return 0, nil, fmt.Errorf("bad packet id encoding")
	}
	return id, rest[n:], nil
}

func readMCString(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty string field")
	}
	length, n := 0, 0
	for i := 0; i < len(data) && i < 5; i++ {
		length |= int(data[i]&0x7F) << (7 * i)
		n++
		if data[i]&0x80 == 0 {
			break
		}
	}
	if n == len(data) || length < 0 || length > len(data[n:]) {
		return "", fmt.Errorf("bad string prefix")
	}
	return string(data[n : n+length]), nil
}

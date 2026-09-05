package checker

import (
	"bufio"
	"context"
	"net"
	"testing"

	"quorum/internal/config"
)

const fakeStatusJSON = `{"version":{"name":"1.20.4","protocol":765},"players":{"max":20,"online":3},"description":{"text":"fake"}}`

// fakeMCServer speaks just enough SLP for one exchange: it reads the
// handshake and the status request, then answers with a canned status
// response. respond controls what it sends back.
func fakeMCServer(t *testing.T, respond func(conn net.Conn, r *bufio.Reader)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				respond(conn, bufio.NewReader(conn))
			}()
		}
	}()
	return ln.Addr().String()
}

func readTwoPackets(conn net.Conn, r *bufio.Reader) {
	readMCPacket(r) // handshake
	readMCPacket(r) // status request
}

func TestCheckMinecraftSuccess(t *testing.T) {
	addr := fakeMCServer(t, func(conn net.Conn, r *bufio.Reader) {
		readTwoPackets(conn, r)
		var payload []byte
		payload = appendMCString(payload, fakeStatusJSON)
		writeMCPacket(conn, 0x00, payload)
	})

	res := Check(context.Background(), config.Service{Type: config.CheckMinecraft, Target: addr})
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if res.LatencyMS < 0 {
		t.Errorf("got negative latency %d", res.LatencyMS)
	}
}

func TestCheckMinecraftFailures(t *testing.T) {
	garbage := fakeMCServer(t, func(conn net.Conn, r *bufio.Reader) {
		conn.Write([]byte("i am not a minecraft server\n"))
	})
	wrongID := fakeMCServer(t, func(conn net.Conn, r *bufio.Reader) {
		readTwoPackets(conn, r)
		writeMCPacket(conn, 0x01, []byte{0x00})
	})
	badJSON := fakeMCServer(t, func(conn net.Conn, r *bufio.Reader) {
		readTwoPackets(conn, r)
		var payload []byte
		payload = appendMCString(payload, "not json")
		writeMCPacket(conn, 0x00, payload)
	})
	noVersion := fakeMCServer(t, func(conn net.Conn, r *bufio.Reader) {
		readTwoPackets(conn, r)
		var payload []byte
		payload = appendMCString(payload, `{"players":{}}`)
		writeMCPacket(conn, 0x00, payload)
	})

	cases := []struct {
		name   string
		target string
	}{
		{"closed port", "127.0.0.1:1"},
		{"missing port", "example.com"},
		{"bad port", "example.com:notaport"},
		{"garbage response", garbage},
		{"wrong packet id", wrongID},
		{"invalid json", badJSON},
		{"missing version", noVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Check(context.Background(), config.Service{Type: config.CheckMinecraft, Target: c.target})
			if res.Success {
				t.Error("expected failure")
			}
			if res.Error == "" {
				t.Error("expected an error message")
			}
		})
	}
}

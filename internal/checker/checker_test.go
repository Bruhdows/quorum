package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"quorum/internal/config"
)

func TestCheckHTTP(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"200 is up", ok.URL, true},
		{"500 is down", bad.URL, false},
		{"unreachable is down", "http://127.0.0.1:1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Check(context.Background(), config.Service{Type: config.CheckHTTP, Target: c.target})
			if res.Success != c.want {
				t.Errorf("got success=%v, want %v (err=%s)", res.Success, c.want, res.Error)
			}
		})
	}
}

func TestCheckTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	res := Check(context.Background(), config.Service{Type: config.CheckTCP, Target: ln.Addr().String()})
	if !res.Success {
		t.Errorf("expected success against open port, got error: %s", res.Error)
	}

	res = Check(context.Background(), config.Service{Type: config.CheckTCP, Target: "127.0.0.1:1"})
	if res.Success {
		t.Error("expected failure against closed port")
	}
}

func TestCheckUnknownType(t *testing.T) {
	res := Check(context.Background(), config.Service{Type: "bogus", Target: "x"})
	if res.Success {
		t.Error("expected failure for unknown check type")
	}
}

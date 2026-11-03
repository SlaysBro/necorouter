package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"necorouter/internal/svc"
)

type conn struct {
	c   net.Conn
	dec *json.Decoder
	enc *json.Encoder
}

func startServer(t *testing.T) (*Server, func() *conn) {
	t.Helper()
	d := t.TempDir()
	p := svc.Paths{Etc: d + "/etc", Var: d + "/var", Run: d + "/run"}
	s := NewServer(p, log.New(io.Discard, "", 0))
	ln, err := net.Listen("unix", filepath.Join(d, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c.(*net.UnixConn))
		}
	}()
	return s, func() *conn {
		c, err := net.Dial("unix", filepath.Join(d, "s.sock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return &conn{c, json.NewDecoder(bufio.NewReader(c)), json.NewEncoder(c)}
	}
}

// call sends a request and returns the output and the final frame.
func (c *conn) call(t *testing.T, r Req) (string, Resp) {
	t.Helper()
	_ = c.c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.enc.Encode(r); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for {
		var rs Resp
		if err := c.dec.Decode(&rs); err != nil {
			t.Fatalf("read: %v", err)
		}
		out.WriteString(rs.Out)
		if rs.Done {
			return out.String(), rs
		}
	}
}

func TestControlCharactersRejected(t *testing.T) {
	s, dial := startServer(t)
	// Exercise the authenticated command path independently of the UID running
	// the test process; root is always a valid router administrator.
	s.peerUID = func(*net.UnixConn) (int, error) { return 0, nil }
	c := dial()
	if _, rs := c.call(t, Req{Op: "hello"}); !rs.OK {
		t.Fatalf("hello: %+v", rs)
	}
	for _, l := range []string{"show version\x00", "hostname a\x1b[2Jb", "x\r\nshow version", "ok\x7f"} {
		out, rs := c.call(t, Req{Op: "exec", Line: l})
		if !strings.Contains(out, "invalid characters") || rs.Quit {
			t.Fatalf("%q was not rejected: %q", l, out)
		}
	}
	if out, _ := c.call(t, Req{Op: "exec", Line: "show version"}); !strings.Contains(out, "necorouter") {
		t.Fatalf("normal command broken: %q", out)
	}
}

func TestOversizedRequestClosesConnection(t *testing.T) {
	_, dial := startServer(t)
	c := dial()
	big := `{"op":"exec","line":"` + strings.Repeat("a", maxLine*2) + `"}` + "\n"
	_, _ = c.c.Write([]byte(big))
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var rs Resp
	if err := c.dec.Decode(&rs); err == nil {
		t.Fatalf("connection must be closed, got %+v", rs)
	}
}

func TestPreLoginIdleConnectionDropped(t *testing.T) {
	old := loginTimeout
	loginTimeout = 300 * time.Millisecond
	defer func() { loginTimeout = old }()
	_, dial := startServer(t)
	c := dial()
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var rs Resp
	if err := c.dec.Decode(&rs); err == nil {
		t.Fatalf("idle connection must be dropped: %+v", rs)
	}
}

func TestExecBeforeHelloIsIgnored(t *testing.T) {
	_, dial := startServer(t)
	c := dial()
	_ = c.enc.Encode(Req{Op: "exec", Line: "show version"})
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var rs Resp
	if err := c.dec.Decode(&rs); err == nil {
		t.Fatalf("no session: connection must close, got %+v", rs)
	}
}

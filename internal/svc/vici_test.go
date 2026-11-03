package svc

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func vsec(name string) []byte { return append([]byte{viciSectionStart, byte(len(name))}, name...) }
func vkv(k, v string) []byte {
	b := []byte{viciKeyValue, byte(len(k))}
	b = append(b, k...)
	b = append(b, byte(len(v)>>8), byte(len(v)))
	return append(b, v...)
}
func vlist(k string, items ...string) []byte {
	b := append([]byte{viciListStart, byte(len(k))}, k...)
	for _, it := range items {
		b = append(b, viciListItem, byte(len(it)>>8), byte(len(it)))
		b = append(b, it...)
	}
	return append(b, viciListEnd)
}
func vcat(p ...[]byte) []byte {
	var o []byte
	for _, x := range p {
		o = append(o, x...)
	}
	return o
}

func fakeVici(t *testing.T, ev string, msg []byte) string {
	sock := filepath.Join(t.TempDir(), "v.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		viciRecv(c)
		viciSend(c, viciEvConfirm, "", nil)
		viciRecv(c)
		viciSend(c, viciEvent, ev, msg)
		viciSend(c, viciCmdResponse, "", nil)
	}()
	return sock
}

func TestViciConns(t *testing.T) {
	msg := vcat(vsec("CIPROFILE0"), vlist("local_addrs", "%any"), vlist("remote_addrs", "198.51.100.2"),
		vkv("version", "IKEv2"), vsec("children"), vsec("CIPROFILE0"), vkv("mode", "TUNNEL"),
		vlist("local-ts", "172.16.0.0/24"), vlist("remote-ts", "172.16.1.0/24"), vkv("rekey_time", "3600"),
		[]byte{viciSectionEnd, viciSectionEnd, viciSectionEnd})
	m, err := viciList(fakeVici(t, "list-conn", msg), "list-conns", "list-conn")
	if err != nil {
		t.Fatal(err)
	}
	got := formatConns(m)
	want := "connections:\nname: CIPROFILE0\nlocal: %any\nremote: 198.51.100.2\nversion: IKEv2\nchildrens:\nname: CIPROFILE0\nmode: TUNNEL\nlocal-ts: 172.16.0.0/24\nremote-ts: 172.16.1.0/24\nrekey-time: 3600"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestViciSAs(t *testing.T) {
	msg := vcat(vsec("CIPROFILE0"), vkv("uniqueid", "2"), vkv("version", "IKEv2"), vkv("state", "ESTABLISHED"),
		vkv("initiator", "no"), vkv("nat-remote", "yes"), vkv("encr-alg", "AES_CBC"),
		vsec("child-sas"), vsec("CIPROFILE0-1"), vkv("name", "CIPROFILE0"), vkv("encap", "yes"),
		vlist("local-ts", "172.16.0.0/24"), []byte{viciSectionEnd, viciSectionEnd, viciSectionEnd})
	m, err := viciList(fakeVici(t, "list-sa", msg), "list-sas", "list-sa")
	if err != nil {
		t.Fatal(err)
	}
	got := formatSAs(m)
	for _, w := range []string{"IPSec security associations:", "name: CIPROFILE0", "state: ESTABLISHED", "Is initiator: false",
		"nat_remote: true", "child-sas:", "encap: true", "esn: false", "local-ts: 172.16.0.0/24"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

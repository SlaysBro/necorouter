package svc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// A minimal vici client (the charon control socket): only needed to read the lists of
// connections and SAs and print them in the manual's format, without parsing swanctl text.

const (
	viciCmdRequest   = 0
	viciCmdResponse  = 1
	viciCmdUnknown   = 2
	viciEvRegister   = 3
	viciEvConfirm    = 5
	viciEvent        = 7
	viciSectionStart = 1
	viciSectionEnd   = 2
	viciKeyValue     = 3
	viciListStart    = 4
	viciListItem     = 5
	viciListEnd      = 6
)

// vnode is an element of a vici message: a section (kids), a list (list) or a key-value pair (val).
type vnode struct {
	key  string
	val  string
	list []string
	kids []*vnode
	kind byte // viciSectionStart, viciKeyValue, viciListStart
}

func (n *vnode) get(k string) string {
	for _, c := range n.kids {
		if c.key == k && c.kind == viciKeyValue {
			return c.val
		}
	}
	return ""
}

func (n *vnode) lst(k string) []string {
	for _, c := range n.kids {
		if c.key == k && c.kind == viciListStart {
			return c.list
		}
	}
	return nil
}

func (n *vnode) sec(k string) *vnode {
	for _, c := range n.kids {
		if c.key == k && c.kind == viciSectionStart {
			return c
		}
	}
	return nil
}

func parseVici(b []byte) (*vnode, error) {
	root := &vnode{}
	stack := []*vnode{root}
	var cur *vnode // open list
	i := 0
	need := func(n int) error {
		if i+n > len(b) {
			return fmt.Errorf("vici: truncated message")
		}
		return nil
	}
	for i < len(b) {
		t := b[i]
		i++
		top := stack[len(stack)-1]
		switch t {
		case viciSectionStart, viciListStart:
			if err := need(1); err != nil {
				return nil, err
			}
			l := int(b[i])
			i++
			if err := need(l); err != nil {
				return nil, err
			}
			n := &vnode{key: string(b[i : i+l]), kind: t}
			i += l
			top.kids = append(top.kids, n)
			if t == viciSectionStart {
				stack = append(stack, n)
			} else {
				cur = n
			}
		case viciSectionEnd:
			if len(stack) < 2 {
				return nil, fmt.Errorf("vici: unbalanced section")
			}
			stack = stack[:len(stack)-1]
		case viciKeyValue:
			if err := need(1); err != nil {
				return nil, err
			}
			l := int(b[i])
			i++
			if err := need(l + 2); err != nil {
				return nil, err
			}
			k := string(b[i : i+l])
			i += l
			vl := int(binary.BigEndian.Uint16(b[i:]))
			i += 2
			if err := need(vl); err != nil {
				return nil, err
			}
			top.kids = append(top.kids, &vnode{key: k, val: string(b[i : i+vl]), kind: viciKeyValue})
			i += vl
		case viciListItem:
			if err := need(2); err != nil {
				return nil, err
			}
			vl := int(binary.BigEndian.Uint16(b[i:]))
			i += 2
			if err := need(vl); err != nil {
				return nil, err
			}
			if cur != nil {
				cur.list = append(cur.list, string(b[i:i+vl]))
			}
			i += vl
		case viciListEnd:
			cur = nil
		default:
			return nil, fmt.Errorf("vici: unknown element %d", t)
		}
	}
	return root, nil
}

func viciSend(c net.Conn, typ byte, name string, msg []byte) error {
	body := []byte{typ}
	if name != "" {
		body = append(body, byte(len(name)))
		body = append(body, name...)
	}
	body = append(body, msg...)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	_, err := c.Write(append(hdr, body...))
	return err
}

func viciRecv(c net.Conn) (typ byte, name string, msg []byte, err error) {
	hdr := make([]byte, 4)
	if _, err = io.ReadFull(c, hdr); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(hdr)
	if n == 0 || n > 16<<20 {
		err = fmt.Errorf("vici: bad packet size")
		return
	}
	buf := make([]byte, n)
	if _, err = io.ReadFull(c, buf); err != nil {
		return
	}
	typ, buf = buf[0], buf[1:]
	if typ == viciEvent {
		if len(buf) < 1 || len(buf) < 1+int(buf[0]) {
			err = fmt.Errorf("vici: bad event")
			return
		}
		l := int(buf[0])
		name, buf = string(buf[1:1+l]), buf[1+l:]
	}
	return typ, name, buf, nil
}

// viciList runs the command cmd (list-conns / list-sas) and returns the event messages ev (list-conn / list-sa).
func viciList(sock, cmd, ev string) ([]*vnode, error) {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := viciSend(c, viciEvRegister, ev, nil); err != nil {
		return nil, err
	}
	if t, _, _, err := viciRecv(c); err != nil || t != viciEvConfirm {
		if err == nil {
			err = fmt.Errorf("vici: event %s not available", ev)
		}
		return nil, err
	}
	if err := viciSend(c, viciCmdRequest, cmd, nil); err != nil {
		return nil, err
	}
	var out []*vnode
	for {
		t, name, msg, err := viciRecv(c)
		if err != nil {
			return nil, err
		}
		switch t {
		case viciEvent:
			if name != ev {
				continue
			}
			n, err := parseVici(msg)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		case viciCmdResponse:
			return out, nil
		case viciCmdUnknown:
			return nil, fmt.Errorf("vici: unknown command %s", cmd)
		}
	}
}

func yesNo(v string) string {
	switch v {
	case "yes":
		return "true"
	case "no":
		return "false"
	}
	return v
}

func pf(b *strings.Builder, label, val string) {
	if val != "" {
		fmt.Fprintf(b, "%s: %s\n", label, val)
	}
}

// formatConns prints show crypto-ipsec ike connections (manual format, 24.3.2).
func formatConns(msgs []*vnode) string {
	var b strings.Builder
	b.WriteString("connections:\n")
	for _, m := range msgs {
		for _, c := range m.kids {
			if c.kind != viciSectionStart {
				continue
			}
			pf(&b, "name", c.key)
			pf(&b, "local", strings.Join(c.lst("local_addrs"), ","))
			pf(&b, "remote", strings.Join(c.lst("remote_addrs"), ","))
			pf(&b, "version", c.get("version"))
			if ch := c.sec("children"); ch != nil {
				b.WriteString("childrens:\n")
				for _, k := range ch.kids {
					if k.kind != viciSectionStart {
						continue
					}
					pf(&b, "name", k.key)
					pf(&b, "mode", k.get("mode"))
					pf(&b, "local-ts", strings.Join(k.lst("local-ts"), ","))
					pf(&b, "remote-ts", strings.Join(k.lst("remote-ts"), ","))
					pf(&b, "rekey-time", k.get("rekey_time"))
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatSAs prints show crypto-ipsec ike security-associations (manual format, 24.3.2).
func formatSAs(msgs []*vnode) string {
	var b strings.Builder
	b.WriteString("IPSec security associations:\n")
	for _, m := range msgs {
		for _, c := range m.kids {
			if c.kind != viciSectionStart {
				continue
			}
			pf(&b, "name", c.key)
			pf(&b, "uniqueid", c.get("uniqueid"))
			pf(&b, "version", c.get("version"))
			pf(&b, "state", c.get("state"))
			pf(&b, "local_host", c.get("local-host"))
			pf(&b, "local_port", c.get("local-port"))
			pf(&b, "local_id", c.get("local-id"))
			pf(&b, "remote_host", c.get("remote-host"))
			pf(&b, "remote_port", c.get("remote-port"))
			pf(&b, "remote_id", c.get("remote-id"))
			pf(&b, "Is initiator", yesNo(c.get("initiator")))
			pf(&b, "initiator_spi", c.get("initiator-spi"))
			pf(&b, "responder_spi", c.get("responder-spi"))
			for _, k := range []string{"nat-local", "nat-remote", "nat-fake", "nat-any"} {
				pf(&b, strings.ReplaceAll(k, "-", "_"), yesNo(c.get(k)))
			}
			for _, k := range []string{"encr-alg", "encr-keysize", "integ-alg", "prf-alg", "dh-group", "established", "rekey-time"} {
				pf(&b, strings.ReplaceAll(k, "-", "_"), c.get(k))
			}
			if ch := c.sec("child-sas"); ch != nil {
				b.WriteString("child-sas:\n")
				for _, k := range ch.kids {
					if k.kind != viciSectionStart {
						continue
					}
					pf(&b, "name", k.get("name"))
					pf(&b, "uniqueid", k.get("uniqueid"))
					pf(&b, "reqid", k.get("reqid"))
					pf(&b, "state", k.get("state"))
					pf(&b, "mode", k.get("mode"))
					pf(&b, "protocol", k.get("protocol"))
					pf(&b, "encap", yesNo(k.get("encap")))
					pf(&b, "spi_in", k.get("spi-in"))
					pf(&b, "spi_out", k.get("spi-out"))
					for _, f := range []string{"encr-alg", "encr-keysize", "integ-alg"} {
						pf(&b, strings.ReplaceAll(f, "-", "_"), k.get(f))
					}
					esn := "false"
					if v := k.get("esn"); v == "yes" {
						esn = "true"
					}
					pf(&b, "esn", esn)
					for _, f := range []string{"rekey-time", "life-time", "install-time"} {
						pf(&b, strings.ReplaceAll(f, "-", "_"), k.get(f))
					}
					pf(&b, "local-ts", strings.Join(k.lst("local-ts"), ","))
					pf(&b, "remote-ts", strings.Join(k.lst("remote-ts"), ","))
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

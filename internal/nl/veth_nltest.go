//go:build nltest

package nl

// AddVeth creates a veth pair. Needed only by the test bench (test/mkveth and the test
// of addresses in internal/cli); not part of the router binary (built without the nltest tag).
func (c *Conn) AddVeth(a, b string) error {
	peer := attr(1, cat(ifinfo(0, 0, 0), attrStr(iflaIfname, b)))
	return c.newLink(a, 0, "veth", peer)
}

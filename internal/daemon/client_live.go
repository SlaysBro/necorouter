package daemon

import (
	"strings"

	"necorouter/internal/proto"
	"necorouter/internal/term"
)

// The live page of a streamed command (show log follow) on the client side: the lines of the stream go to
// the page, which owns the keys until the user leaves it. See term.Follow for the page itself.

// startLive opens the live page for a streamed command.
func (c *Client) startLive() {
	if c.in == nil || c.out == nil || !term.IsTerminal(c.in) {
		return
	}
	if c.stopWatch != nil {
		c.stopWatch()
	}
	c.live = make(chan string, 4096)
	c.livePageDone = make(chan struct{})
	lines, done := c.live, c.livePageDone
	go func() {
		defer close(done)
		term.Follow(c.in, c.out, lines, func() { _ = c.enc.Encode(Req{Op: proto.OpIntr}) })
	}()
}

// feedLive gives the page the complete lines of a chunk; it never waits for a page that has gone.
func (c *Client) feedLive(chunk string) {
	c.partial += chunk
	for {
		i := strings.IndexByte(c.partial, '\n')
		if i < 0 {
			return
		}
		line := strings.TrimSuffix(c.partial[:i], "\r")
		c.partial = c.partial[i+1:]
		select {
		case c.live <- line:
		case <-c.livePageDone:
			return
		}
	}
}

// endLive closes the stream: the page shows the end and returns once the user has left it.
func (c *Client) endLive() {
	if c.live != nil {
		if c.partial != "" {
			select {
			case c.live <- c.partial:
			case <-c.livePageDone:
			}
			c.partial = ""
		}
		close(c.live)
		c.live = nil
	}
	if c.livePageDone != nil {
		<-c.livePageDone
		c.livePageDone = nil
	}
}

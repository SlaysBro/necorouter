package daemon

import (
	"reflect"
	"testing"
	"time"
)

// The live page gets complete lines; a line split across chunks is joined.
func TestFeedLiveSplitsLines(t *testing.T) {
	c := &Client{live: make(chan string, 10), livePageDone: make(chan struct{})}
	c.feedLive("a\nb")
	c.feedLive("c\r\nd\n")
	close(c.live)
	var got []string
	for l := range c.live {
		got = append(got, l)
	}
	if want := []string{"a", "bc", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Client side of the live page: once the page has gone, lines are dropped and never block the client.
func TestFeedLiveDoesNotBlockAfterPageClosed(t *testing.T) {
	c := &Client{live: make(chan string), livePageDone: make(chan struct{})}
	close(c.livePageDone) // the user has left the page
	done := make(chan struct{})
	go func() {
		c.feedLive("a\nb\nc\n")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("feedLive blocked on a page that has gone")
	}
}

// endLive with no page open does nothing; with a partial line it flushes it first, then waits for the page.
func TestEndLiveFlushesPartialLine(t *testing.T) {
	c := &Client{}
	c.endLive() // no page: must not block or panic

	c = &Client{live: make(chan string, 4), livePageDone: make(chan struct{})}
	c.feedLive("tail")
	lines, pageDone := c.live, c.livePageDone
	go func() {
		// the page reads the flushed line, then returns
		if got := <-lines; got != "tail" {
			t.Errorf("flushed %q, want %q", got, "tail")
		}
		close(pageDone)
	}()
	c.endLive()
	if c.live != nil || c.livePageDone != nil {
		t.Fatal("endLive did not clear the page state")
	}
}

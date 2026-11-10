package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"necorouter/internal/proto"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"necorouter/internal/term"
)

type Client struct {
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder

	paging bool // between Ctl "page" and "endpage" the output is accumulated in buf
	paged  bool // the last command produced buffered output
	pageN  int
	buf    strings.Builder

	// live page (show log follow): between Ctl "stream" and "endstream" the lines go to the page
	in           *os.File
	out          io.Writer
	stopWatch    func()        // stops the Ctrl-C watcher, which must not read keys during the page
	typeahead    func([]byte)  // gives input typed during a command back to the editor
	live         chan string   // lines for the page; nil when no page is open
	livePageDone chan struct{} // closed when the page has returned
	partial      string        // the part of a line that has not ended yet
}

// takePaged returns the accumulated output for the pager (ok=false means there is nothing to show).
func (c *Client) takePaged() (text string, n int, ok bool) {
	c.paging = false
	if !c.paged && c.buf.Len() == 0 {
		return "", 0, false
	}
	text, n = c.buf.String(), c.pageN
	c.buf.Reset()
	c.paged = false
	return text, n, true
}

func Dial(sock string) (*Client, error) {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("no access to the router socket (%v)", err)
		}
		return nil, fmt.Errorf("the router is not running (%v)\nAs root start it with: systemctl start necorouter   (Alpine: rc-service necorouter start)", err)
	}
	return &Client{conn: c, dec: json.NewDecoder(bufio.NewReader(c)), enc: json.NewEncoder(c)}, nil
}

func (c *Client) Close() { c.conn.Close() }

// call sends a request and collects the response; streamed output goes to out.
func (c *Client) call(r Req, out func(string)) (Resp, error) {
	if err := c.enc.Encode(r); err != nil {
		return Resp{}, err
	}
	for {
		var rs Resp
		if err := c.dec.Decode(&rs); err != nil {
			return Resp{}, err
		}
		if rs.Done {
			return rs, nil
		}
		if rs.Ctl != "" {
			switch rs.Ctl {
			case proto.CtlPage:
				c.paging, c.pageN = true, rs.N
				c.buf.Reset()
			case proto.CtlEndPage:
				c.paging = false
				c.paged = true
			case proto.CtlStream:
				c.startLive()
			case proto.CtlEndStream:
				c.endLive()
			}
			continue
		}
		if c.live != nil {
			c.feedLive(rs.Out)
		} else if c.paging {
			c.buf.WriteString(rs.Out)
		} else if out != nil {
			out(rs.Out)
		}
	}
}

// Options configures the client shell.
type Options struct {
	Sock string
	Cmds []string // non-interactive mode (ssh host "cmd")
}

// Run is the interactive shell (or runs commands from Options.Cmds).
// The user was already verified by login/sshd via PAM; the role is determined by the daemon from the uid.
func Run(o Options, in *os.File, out io.Writer) error {
	ed := term.NewEditor(in, out)
	c, err := Dial(o.Sock)
	if err != nil {
		return err
	}
	defer c.Close()
	c.typeahead = ed.Typeahead
	c.out = out
	emit := func(t string) { fmt.Fprint(out, t) }
	tty := term.IsTerminal(in) && len(o.Cmds) == 0
	rs, err := c.call(Req{Op: proto.OpHello, TTY: tty}, emit)
	if err != nil {
		return err
	}
	if !rs.OK {
		fmt.Fprintf(out, "%% %s\n", rs.Err)
		return fmt.Errorf("%s", rs.Err)
	}
	loop(c, ed, rs.Prompt, o, out)
	return nil
}

func loop(c *Client, ed *term.Editor, prompt string, o Options, out io.Writer) {
	secret := false
	emit := func(s string) { fmt.Fprint(out, s) }
	if len(o.Cmds) > 0 {
		for _, line := range o.Cmds {
			rs, err := c.call(Req{Op: proto.OpExec, Line: line}, emit)
			if err != nil || rs.Quit {
				break
			}
		}
		return
	}
	hooks := term.Hooks{
		Help: func(l string) string {
			rs, err := c.call(Req{Op: proto.OpHelp, Line: l}, nil)
			if err != nil {
				return "% connection lost\n"
			}
			return rs.Out
		},
		Complete: func(l string) (string, []string) {
			rs, err := c.call(Req{Op: proto.OpComplete, Line: l}, nil)
			if err != nil {
				return l, nil
			}
			return rs.Line, rs.Opts
		},
	}
	for {
		ed.Secret = secret
		line, err := ed.ReadLine(prompt, hooks)
		ed.Secret = false
		if err != nil {
			// Ctrl-D on an empty line in a terminal acts as exit: leaves the mode one level up,
			// and leaves the shell only at the base level. Without a terminal (end of input): exit immediately.
			if err != term.ErrEOF || !term.IsTerminal(ed.In) || secret {
				return
			}
			line = "exit"
		}
		rs, err := c.execInterruptible(ed.In, line, emit)
		if err != nil {
			fmt.Fprintln(out, "% connection to the router daemon lost")
			return
		}
		if text, n, ok := c.takePaged(); ok {
			term.Page(ed.In, out, text, n)
		}
		prompt, secret = rs.Prompt, rs.Secret
		if rs.Quit {
			fmt.Fprint(out, "\r\n")
			return
		}
	}
}

// execInterruptible runs a command; while it runs, the terminal is put in
// raw mode and Ctrl-C (0x03) is sent to the daemon as "intr" instead of killing
// the client shell. This way ping can be stopped while staying in the CLI.
func (c *Client) execInterruptible(in *os.File, line string, emit func(string)) (Resp, error) {
	c.in = in
	if !term.IsTerminal(in) {
		return c.call(Req{Op: proto.OpExec, Line: line}, emit)
	}
	restore, err := term.Raw(in)
	if err != nil {
		return c.call(Req{Op: proto.OpExec, Line: line}, emit)
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		term.WatchInterrupt(in, stop, func() { _ = c.enc.Encode(Req{Op: proto.OpIntr}) }, c.typeahead)
	}()
	var once sync.Once
	c.stopWatch = func() {
		once.Do(func() {
			close(stop)
			<-finished // the watcher must exit before anything else reads the terminal
		})
	}
	rs, err := c.call(Req{Op: proto.OpExec, Line: line}, emit)
	c.stopWatch()
	c.stopWatch = nil
	c.endLive()
	restore()
	return rs, err
}

// Package daemon is the router's background process: it keeps the single configuration,
// starts services on demand and serves CLI sessions over a unix socket.
package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"necorouter/internal/cli"
	"necorouter/internal/config"
	"necorouter/internal/logx"
	"necorouter/internal/svc"
)

type Req struct {
	Op   string `json:"op"`
	Line string `json:"line,omitempty"`
	TTY  bool   `json:"tty,omitempty"`
}

const (
	maxConns = 64        // simultaneous connections to the socket
	maxLine  = 16 * 1024 // bytes in one request
)

// loginTimeout is how long a connection may stay silent before login (tests reduce it).
var loginTimeout = 60 * time.Second

// cleanLine rejects control characters: they have no business in a command line.
func cleanLine(l string) bool {
	for _, r := range l {
		if r != '\t' && (r < 0x20 || r == 0x7f) {
			return false
		}
	}
	return utf8.ValidString(l)
}

type Resp struct {
	Out    string   `json:"out,omitempty"`
	Done   bool     `json:"done,omitempty"`
	OK     bool     `json:"ok,omitempty"`
	Err    string   `json:"err,omitempty"`
	Prompt string   `json:"prompt,omitempty"`
	Quit   bool     `json:"quit,omitempty"`
	Secret bool     `json:"secret,omitempty"` // next input is a password: no echo
	Line   string   `json:"line,omitempty"`
	Opts   []string `json:"opts,omitempty"`
	Ctl    string   `json:"ctl,omitempty"` // "page" / "endpage": pager on the client
	N      int      `json:"n,omitempty"`
}

type Server struct {
	R       *cli.Router
	Mgr     *svc.Manager
	P       svc.Paths
	Log     *log.Logger
	sem     chan struct{}
	peerUID func(*net.UnixConn) (int, error)
}

// lg writes to the router log, or to the standard log.Logger if it is not open.
func (s *Server) lg(skip int, level, f string, a ...any) {
	if logx.Default() != nil {
		logx.Logf(skip+1, level, "DAEMON", f, a...)
	} else if s.Log != nil {
		s.Log.Printf(f, a...)
	}
}

func NewServer(p svc.Paths, l *log.Logger) *Server {
	mgr := svc.NewManager(p, l)
	return &Server{R: cli.NewRouter(mgr, p), Mgr: mgr, P: p, Log: l, sem: make(chan struct{}, maxConns), peerUID: peerUID}
}

// Run starts the daemon and blocks until SIGTERM/SIGINT.
func (s *Server) Run() error {
	if l, err := logx.Open(s.P.LogDir(), os.Stderr); err != nil {
		s.Log.Printf("log: %v", err)
	} else {
		logx.SetDefault(l)
		defer func() {
			logx.SetDefault(nil)
			_ = l.Close()
		}()
	}
	// banner before login: a file for sshd and /etc/issue for the local console (getty/login)
	if err := svc.SyncIssue(s.P); err != nil {
		s.lg(1, logx.LevelWarn, "banner: %v", err)
	}
	// abnormal termination of the previous run: last running-config snapshot -> crash-config
	if _, err := s.R.RecoverCrash(); err != nil {
		s.lg(1, logx.LevelError, "crash backup: %v", err)
	}
	if errs := s.R.LoadStartup(); len(errs) > 0 {
		for _, e := range errs {
			s.lg(1, logx.LevelError, "startup-config: %s", e)
		}
	} else if _, err := os.Stat(s.P.Startup()); err != nil {
		// factory configuration: bring up the built-in admin
		s.R.Replay("")
	}
	_ = os.Remove(s.P.Sock())
	ln, err := net.Listen("unix", s.P.Sock())
	if err != nil {
		return err
	}
	_ = os.Chmod(s.P.Sock(), 0o666) // access is restricted by checking the connection owner
	s.lg(1, logx.LevelInfo, "necorouter %s ready, socket %s", cli.VersionString(), s.P.Sock())
	if s.R.Crashed() {
		s.lg(1, logx.LevelWarn, "previous shutdown was abnormal; running-config backup in %s", s.P.Crash())
	}
	if err := s.R.MarkRunning(); err != nil {
		s.lg(1, logx.LevelError, "running marker: %v", err)
	}
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		s.R.Watch(watchStop, func(f string, a ...any) { s.lg(1, logx.LevelWarn, f, a...) })
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		s.lg(1, logx.LevelInfo, "shutting down: stopping services")
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			break
		}
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.serve(c.(*net.UnixConn))
			}()
		default: // too many connections: refuse rather than accumulate goroutines
			_ = c.Close()
		}
	}
	s.Mgr.Shutdown()
	close(watchStop)
	<-watchDone
	if err := s.R.MarkClean(); err != nil {
		s.lg(1, logx.LevelError, "clean shutdown marker: %v", err)
	}
	_ = os.Remove(s.P.Sock())
	return nil
}

func peerUID(c *net.UnixConn) (int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var e error
	_ = rc.Control(func(fd uintptr) {
		u, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			e = err
			return
		}
		uid = int(u.Uid)
	})
	return uid, e
}

func (s *Server) serve(c *net.UnixConn) {
	defer c.Close()
	defer func() { // an error in one session must not bring down the whole router
		if r := recover(); r != nil {
			s.lg(1, logx.LevelFatal, "session panic: %v\n%s", r, debug.Stack())
		}
	}()
	uid, err := s.peerUID(c)
	if err != nil {
		return
	}
	var wmu sync.Mutex
	enc := json.NewEncoder(c)
	send := func(r Resp) {
		wmu.Lock()
		_ = enc.Encode(r)
		wmu.Unlock()
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 4096), maxLine)
	_ = c.SetReadDeadline(time.Now().Add(loginTimeout)) // silent clients do not hold the connection

	// Requests are read by a separate goroutine: while a command (ping) runs,
	// the server must accept "intr" (Ctrl-C) and notice a dropped connection.
	reqs := make(chan Req)
	go func() {
		defer close(reqs)
		for sc.Scan() {
			var rq Req
			if json.Unmarshal(sc.Bytes(), &rq) == nil {
				reqs <- rq
			}
		}
	}()
	defer func() { // the reader must not hang on a send after serve exits
		c.Close()
		go func() {
			for range reqs {
			}
		}()
	}()

	var sess *cli.Session
	defer func() {
		if sess != nil {
			sess.Close()
			logx.Auditf(sess.User, "logged out")
		}
	}()
	open := func(u *config.User, tty bool) {
		sess = cli.NewSession(s.R, u)
		sess.TTY = tty
		_ = c.SetReadDeadline(time.Time{})
		if m := s.R.Motd(); m != "" { // banner motd greeting: before the warnings and the prompt
			send(Resp{Out: m + "\n"})
		}
		if s.R.Crashed() && u.Role == "admin" {
			send(Resp{Out: "% warning: the previous shutdown was abnormal; running-config backup: " + s.P.Crash() + " (show config file crash)\n"})
		}
		if s.R.Mgr.DefaultPassword(u.Name) {
			send(Resp{Out: "% warning: the default password is still in use; change it: configure terminal / username " + u.Name + " / password ...\n"})
		}
		send(Resp{Done: true, OK: true, Prompt: sess.Prompt()})
	}
	for rq := range reqs {
		switch rq.Op {
		case "hello": // login by the connection owner (the password was checked by getty/sshd via PAM)
			if sess != nil {
				return
			}
			name, err := svc.ResolveUID(uid)
			var u *config.User
			if err == nil {
				if name == "root" && uid == 0 {
					u = &config.User{Name: "root", Role: "admin"}
				} else {
					u = s.R.Lookup(name)
				}
			}
			if u == nil {
				s.R.AAAEvent(cli.AAALoginFailed, 0, logx.LevelWarn, "login denied: account not configured on the router")
				msg := "access denied: this account is not configured on the router"
				if err != nil {
					msg = "access denied: " + err.Error()
				}
				send(Resp{Done: true, Err: msg})
				return
			}
			open(u, rq.TTY)
			logx.Auditf(u.Name, "logged in")
		case "exec":
			if sess == nil {
				return
			}
			if !cleanLine(rq.Line) {
				send(Resp{Out: "% invalid characters in the command line\n"})
				send(Resp{Done: true, Prompt: sess.Prompt(), Secret: sess.PromptSecret()})
				continue
			}
			doneCh := make(chan bool, 1)
			sess.Ctl = func(c string, n int) { send(Resp{Ctl: c, N: n}) }
			go func() {
				defer func() {
					if r := recover(); r != nil {
						s.lg(1, logx.LevelFatal, "command panic: %v\n%s", r, debug.Stack())
						send(Resp{Out: "% internal error\n"})
						doneCh <- false
					}
				}()
				doneCh <- sess.Exec(rq.Line, writerFunc(func(p []byte) { send(Resp{Out: string(p)}) }))
			}()
			var quit bool
		wait:
			for {
				select {
				case q, ok := <-reqs:
					if !ok { // the client disconnected: kill ping and exit
						sess.Interrupt()
						<-doneCh
						return
					}
					if q.Op == "intr" {
						sess.Interrupt()
					}
				case quit = <-doneCh:
					break wait
				}
			}
			send(Resp{Done: true, Prompt: sess.Prompt(), Secret: sess.PromptSecret(), Quit: quit})
			if quit {
				return
			}
		case "help":
			if sess != nil && cleanLine(rq.Line) {
				send(Resp{Done: true, Out: sess.Help(rq.Line)})
			}
		case "complete":
			if sess != nil && cleanLine(rq.Line) {
				l, o := sess.Complete(rq.Line)
				send(Resp{Done: true, Line: l, Opts: o})
			}
		}
	}
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

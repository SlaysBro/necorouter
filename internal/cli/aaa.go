package cli

import (
	"fmt"

	"necorouter/internal/logx"
)

// aaa logging level 1-6: which AAA service events go to the journal.
// Levels: 1 critical, 2 error, 3 warning (default), 4 notice, 5 info, 6 debug.
// Each level includes the lower ones. An event is written if the journal level is not below its threshold.

const (
	aaaDefaultLevel = 3
	aaaMaxLevel     = 6
	// Thresholds of AAA events that the router writes now.
	AAALoginFailed = 3 // failed login attempt — warning
)

// AAALevel returns the current AAA logging level.
func (r *Router) AAALevel() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Cfg.AAALevel == 0 {
		return aaaDefaultLevel
	}
	return r.Cfg.AAALevel
}

// AAAEvent writes an AAA event to the journal if its threshold is not above the current level. skip — frames up to the caller.
func (r *Router) AAAEvent(minLevel, skip int, level, f string, a ...any) {
	if r.AAALevel() < minLevel {
		return
	}
	logx.Logf(skip+1, level, "AAA", f, a...)
}

func aaaNode() *Node {
	return K("aaa", "Authentication, authorization and accounting",
		K("logging", "AAA logging",
			K("level", "Logging level of AAA events (default 3, warning)",
				A(KInt, "NUM", "1 critical, 2 error, 3 warning, 4 notice, 5 info, 6 debug").Rng(1, aaaMaxLevel).R(func(s *Session, v []string) error {
					var n int
					fmt.Sscan(v[0], &n)
					s.cfg().AAALevel = n
					s.journal("aaa logging level %d", n)
					return nil
				})).N(func(s *Session, _ []string) error {
				s.cfg().AAALevel = 0
				s.journal("no aaa logging level")
				return nil
			})))
}

package cli

import (
	"regexp"
)

// trees is built in init: command handlers (copy) call Replay, and it accesses the tree.
var trees map[string][]*Node

func init() { trees = buildTrees() }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:/-]*$`)
var hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func (s *Session) enter(kind, obj string) {
	for len(s.frames) > 1 && s.top().kind != modeConfig {
		s.pop()
	}
	s.push(kind, obj)
}

func buildTrees() map[string][]*Node {
	t := map[string][]*Node{}
	nav := newNav()
	buildUserModes(t, nav)
	g := buildConfig(t, nav)
	buildSubModes(t, nav, g)
	return t
}

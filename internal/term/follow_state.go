package term

// The live page (show log follow) is a small state machine. Its states and transitions are pure
// functions, so every transition is tested without a terminal (follow_state_test.go).
//
//	FOLLOW  the view stays on the last line; new lines scroll it
//	PAUSED  the user moved up; the view stays where it is until the user is back at the end
//
// Events: a new line arrives, a key moves the view, End (or G) goes to the end, and q or Ctrl-C quits.
// Quitting is not a state: the page returns to the console, and the producer of the lines is stopped once.

type followState int

const (
	stateFollow followState = iota
	statePaused
)

// Name is the label shown in the status line.
func (s followState) Name() string {
	if s == stateFollow {
		return "FOLLOW"
	}
	return "PAUSED"
}

// onLine: a new line arrived. The state does not change; the view follows only in FOLLOW.
func onLine(s followState) followState { return s }

// onKey: a key that moves the view. End returns to the end. Any other movement pauses the view unless
// it has brought the view back to the end (then the page follows again).
func onKey(s followState, k int, atEnd bool) followState {
	if k == KeyEnd {
		return stateFollow
	}
	if atEnd {
		return stateFollow
	}
	return statePaused
}

// viewTop is the first line on screen after a transition: in FOLLOW the last page of lines, in PAUSED
// the position the user has chosen (clamped to the lines that exist).
func viewTop(s followState, top, maxTop int) int {
	if s == stateFollow {
		return maxTop
	}
	if top > maxTop {
		return maxTop
	}
	return top
}

// isQuit reports whether the key leaves the page.
func isQuit(k int) bool { return k == KeyQuit }

// isIgnored reports whether the key is not used on the live page.
func isIgnored(k int) bool { return k == KeyNone || k == KeySearch || k == KeyNext }

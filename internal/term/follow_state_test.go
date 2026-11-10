package term

import "testing"

// Transitions of the live page, without a terminal.
func TestFollowTransitions(t *testing.T) {
	cases := []struct {
		name  string
		from  followState
		key   int
		atEnd bool
		want  followState
	}{
		{"FOLLOW, scroll up pauses", stateFollow, KeyLineUp, false, statePaused},
		{"FOLLOW, page up pauses", stateFollow, KeyPageUp, false, statePaused},
		{"PAUSED, scroll up stays paused", statePaused, KeyLineUp, false, statePaused},
		{"PAUSED, scroll down but not at the end stays paused", statePaused, KeyLineDown, false, statePaused},
		{"PAUSED, scrolled back to the end follows again", statePaused, KeyLineDown, true, stateFollow},
		{"PAUSED, End goes to the end", statePaused, KeyEnd, false, stateFollow},
		{"FOLLOW, End stays following", stateFollow, KeyEnd, true, stateFollow},
	}
	for _, c := range cases {
		if got := onKey(c.from, c.key, c.atEnd); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got.Name(), c.want.Name())
		}
	}
}

// A new line never changes the state: FOLLOW follows it through viewTop, PAUSED keeps the view.
func TestNewLineKeepsState(t *testing.T) {
	for _, s := range []followState{stateFollow, statePaused} {
		if got := onLine(s); got != s {
			t.Errorf("new line changed %s to %s", s.Name(), got.Name())
		}
	}
}

// The view: FOLLOW always shows the last page of lines; PAUSED keeps the chosen line and clamps it when
// fewer lines exist.
func TestViewTop(t *testing.T) {
	if got := viewTop(stateFollow, 0, 40); got != 40 {
		t.Errorf("FOLLOW view top = %d, want 40", got)
	}
	if got := viewTop(statePaused, 12, 40); got != 12 {
		t.Errorf("PAUSED view top = %d, want 12", got)
	}
	if got := viewTop(statePaused, 50, 40); got != 40 {
		t.Errorf("PAUSED clamped view top = %d, want 40", got)
	}
}

// Keys: q and Ctrl-C quit; the keys that are not used on the page are ignored; the rest move the view.
func TestFollowKeyClasses(t *testing.T) {
	if !isQuit(KeyQuit) || isQuit(KeyLineUp) {
		t.Error("quit classification")
	}
	for _, k := range []int{KeyNone, KeySearch, KeyNext} {
		if !isIgnored(k) {
			t.Errorf("key %d must be ignored on the live page", k)
		}
	}
	if isIgnored(KeyLineUp) || isIgnored(KeyEnd) {
		t.Error("movement keys must not be ignored")
	}
}

// The status label follows the state.
func TestFollowStateNames(t *testing.T) {
	if stateFollow.Name() != "FOLLOW" || statePaused.Name() != "PAUSED" {
		t.Errorf("names: %q %q", stateFollow.Name(), statePaused.Name())
	}
}

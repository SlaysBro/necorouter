// Package proto holds the names used on the wire between the shell (client) and the daemon, and in the
// control signals that the command engine sends to the client. They are constants so that a misspelt
// name is a compile error, not a signal that is silently ignored.
package proto

// Request operations (Req.Op).
const (
	OpHello    = "hello"    // login by the owner of the connection
	OpExec     = "exec"     // run a command line
	OpHelp     = "help"     // '?' help for a line
	OpComplete = "complete" // Tab completion for a line
	OpIntr     = "intr"     // Ctrl-C: stop the running command
)

// Control signals (Resp.Ctl) sent while a command runs.
const (
	CtlPage      = "page"      // start buffering the output for the pager
	CtlEndPage   = "endpage"   // the buffered output is complete: show it in the pager
	CtlStream    = "stream"    // start a live page (show log follow)
	CtlEndStream = "endstream" // the stream has ended
)

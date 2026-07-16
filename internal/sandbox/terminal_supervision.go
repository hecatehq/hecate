package sandbox

// SupervisedTerminalInputMode identifies whether a terminal process treats
// stdin as executable code. Local terminals retain this mode so interactive
// writes can receive the same best-effort process-ownership validation as the
// command used to start the terminal.
type SupervisedTerminalInputMode uint8

const (
	SupervisedTerminalInputNone SupervisedTerminalInputMode = iota
	SupervisedTerminalInputShell
	SupervisedTerminalInputInterpreter
)

// SupervisedTerminalInputPendingLimit bounds syntax that has been written to a
// shell terminal but has not reached a validated, newline-terminated command
// boundary. Completed commands are discarded from validation state.
const SupervisedTerminalInputPendingLimit = 64 * 1024

// SupervisedTerminalInputState is the bounded cross-Write validation context
// for a supervised terminal. Its fields are intentionally private so callers
// cannot construct a state that skips required shell or interpreter context.
type SupervisedTerminalInputState struct {
	shellPending              string
	interpreterIdentifierTail string
	interpreterCompactTail    string
}

// RetainedBytes reports the bounded state size for diagnostics and tests.
func (s SupervisedTerminalInputState) RetainedBytes() int {
	return len(s.shellPending) + len(s.interpreterIdentifierTail) + len(s.interpreterCompactTail)
}

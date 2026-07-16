//go:build windows

package sandbox

func ValidateSupervisedTerminalCommand(string, []string) error { return nil }

func SupervisedTerminalInputModeForCommand(string, []string) SupervisedTerminalInputMode {
	return SupervisedTerminalInputNone
}

func ValidateSupervisedTerminalInput(SupervisedTerminalInputMode, string) error { return nil }

func ValidateSupervisedTerminalInputWrite(_ SupervisedTerminalInputMode, state SupervisedTerminalInputState, _ string) (SupervisedTerminalInputState, error) {
	return state, nil
}

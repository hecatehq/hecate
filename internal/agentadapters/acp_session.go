package agentadapters

import (
	"context"
	"errors"
)

const DriverKindACP = "acp"

var ErrSessionNotActive = errors.New("agent chat session is not active")

type Runner interface {
	PrepareSession(context.Context, PrepareSessionRequest) (PrepareSessionResult, error)
	Run(context.Context, RunRequest) (RunResult, error)
	SetSessionConfigOption(context.Context, SetSessionConfigOptionRequest) (SetSessionConfigOptionResult, error)
	CloseSession(context.Context, string) error
	Shutdown(context.Context) error
}

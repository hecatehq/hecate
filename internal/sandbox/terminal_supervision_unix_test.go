//go:build !windows

package sandbox

import (
	"strings"
	"testing"
	"time"
)

func TestValidateSupervisedTerminalCommandRejectsProcessGroupEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "direct setsid", command: "/usr/bin/setsid", args: []string{"sleep", "60"}},
		{name: "env wrapped setsid", command: "env", args: []string{"MODE=test", "setsid", "sleep", "60"}},
		{name: "env split string", command: "env", args: []string{"-S", "setsid sleep 60"}},
		{name: "env long split string", command: "env", args: []string{"--split-string=setsid sleep 60"}},
		{name: "quoted path in shell", command: "sh", args: []string{"-c", `/usr/bin/"set"sid sleep 60`}},
		{name: "nested background setsid", command: "bash", args: []string{"-lc", `(nohup setsid sleep 60 >/dev/null 2>&1 &)`}},
		{name: "bash long option before script", command: "bash", args: []string{"--norc", "-c", "setsid sleep 60"}},
		{name: "bash option value before script", command: "bash", args: []string{"-O", "extglob", "-c", "setsid sleep 60"}},
		{name: "nice wrapped setsid", command: "nice", args: []string{"-n", "5", "setsid", "sleep", "60"}},
		{name: "timeout wrapped setsid", command: "timeout", args: []string{"5s", "setsid", "sleep", "60"}},
		{name: "timeout creates process group", command: "timeout", args: []string{"5s", "sleep", "60"}},
		{name: "foreground timeout wrapped setsid", command: "timeout", args: []string{"--foreground", "5s", "setsid", "sleep", "60"}},
		{name: "stdbuf wrapped setsid", command: "stdbuf", args: []string{"-oL", "setsid", "sleep", "60"}},
		{name: "setpgid interpreter", command: "python3", args: []string{"-c", `import os; os.setpgid(0, 0)`}},
		{name: "python combined interactive inline", command: "python3", args: []string{"-ic", `import os; os.setsid()`}},
		{name: "python attached combined interactive inline", command: "python3", args: []string{`-icimport os; os.setsid()`}},
		{name: "python interactive option value before inline", command: "python3", args: []string{"-i", "-W", "ignore", "-c", `import os; os.setsid()`}},
		{name: "node long eval", command: "node", args: []string{"--eval", `require("child_process").spawn("sleep", ["60"], {detached: true})`}},
		{name: "node value option before eval", command: "node", args: []string{"--input-type", "module", "--eval", `require("child_process").spawn("sleep", ["60"], {detached: true})`}},
		{name: "node interactive eval", command: "node", args: []string{"-i", "-e", `require("child_process").spawn("sleep", ["60"], {detached: true})`}},
		{name: "node interactive flag after eval", command: "node", args: []string{"-e", `require("child_process").spawn("sleep", ["60"], {detached: true})`, "-i"}},
		{name: "node interactive preload before eval", command: "node", args: []string{"-i", "-r", "fs", "-e", `require("child_process").spawn("sleep", ["60"], {detached: true})`}},
		{name: "ruby require before eval", command: "ruby", args: []string{"-r", "json", "-e", `Process.setsid`}},
		{name: "perl include before eval", command: "perl", args: []string{"-I", "lib", "-e", `setsid()`}},
		{name: "perl capital eval", command: "perl", args: []string{"-E", `setsid()`}},
		{name: "php config before run", command: "php", args: []string{"-d", "display_errors=1", "-r", `posix_setsid();`}},
		{name: "php run", command: "php", args: []string{"-r", `posix_setsid();`}},
		{name: "lua library before eval", command: "lua", args: []string{"-l", "mod", "-e", `setsid()`}},
		{name: "powershell command", command: "pwsh", args: []string{"-Command", "setsid sleep 60"}},
		{name: "powershell value option before command", command: "pwsh", args: []string{"-WorkingDirectory", "/tmp", "-Command", "setsid sleep 60"}},
		{name: "powershell split command", command: "pwsh", args: []string{"-Command", "printf ok;", "setsid", "sleep", "60"}},
		{name: "powershell encoded command", command: "pwsh", args: []string{"-EncodedCommand", "AAAA"}},
		{name: "daemonize", command: "daemonize", args: []string{"sleep", "60"}},
		{name: "background daemon helper", command: "start-stop-daemon", args: []string{"--start", "--background", "--exec", "/bin/sleep"}},
		{name: "clustered background daemon helper", command: "start-stop-daemon", args: []string{"-Sb", "-x", "/bin/sleep"}},
		{name: "service manager", command: "systemd-run", args: []string{"--user", "sleep", "60"}},
		{name: "shell monitor flag", command: "bash", args: []string{"-mc", "sleep 60 &"}},
		{name: "shell monitor builtin", command: "bash", args: []string{"-c", "set -o monitor; sleep 60 &"}},
		{name: "shell clustered monitor builtin", command: "bash", args: []string{"-c", "set -bm; sleep 60 &"}},
		{name: "dynamic shell command", command: "sh", args: []string{"-c", `$runner sleep 60`}},
		{name: "shell eval", command: "sh", args: []string{"-c", `eval "$command"`}},
		{name: "shell alias", command: "sh", args: []string{"-c", `alias detached='setsid sleep 60'; detached`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSupervisedTerminalCommand(tc.command, tc.args)
			if !IsPolicyDenied(err) {
				t.Fatalf("ValidateSupervisedTerminalCommand() error = %T %v, want PolicyError", err, err)
			}
			if !strings.Contains(err.Error(), "process supervision") && !strings.Contains(err.Error(), "process group") {
				t.Fatalf("error = %q, want supervision reason", err)
			}
		})
	}
}

func TestValidateSupervisedTerminalCommandAllowsOwnedProcessGroupJobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "foreground", command: "sleep", args: []string{"1"}},
		{name: "plain background", command: "sh", args: []string{"-c", "sleep 1 &"}},
		{name: "nohup background", command: "sh", args: []string{"-c", "nohup sleep 1 >/dev/null 2>&1 &"}},
		{name: "disowned background", command: "bash", args: []string{"-c", "sleep 1 & disown"}},
		{name: "detachment word as data", command: "sh", args: []string{"-c", "printf '%s\\n' setsid daemon nohup"}},
		{name: "detachment filename as data", command: "printf", args: []string{"/tmp/daemon.log"}},
		{name: "foreground daemon helper", command: "start-stop-daemon", args: []string{"--start", "--exec", "/bin/sleep"}},
		{name: "bash long option", command: "bash", args: []string{"--norc", "-c", "printf ok"}},
		{name: "nice foreground", command: "nice", args: []string{"-n", "5", "sleep", "1"}},
		{name: "timeout foreground", command: "timeout", args: []string{"--foreground", "5s", "sleep", "1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateSupervisedTerminalCommand(tc.command, tc.args); err != nil {
				t.Fatalf("ValidateSupervisedTerminalCommand() error = %v", err)
			}
		})
	}
}

func TestValidateSupervisedTerminalInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"setsid sleep 60\n",
		"set -m\n",
		"set -bm\n",
		"python3 -c 'import os; os.setsid()'\n",
	} {
		if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputShell, input); !IsPolicyDenied(err) {
			t.Errorf("ValidateSupervisedTerminalInput(%q) error = %v, want PolicyError", input, err)
		}
	}
	for _, input := range []string{
		"printf '%s\\n' setsid\n",
		"nohup sleep 1 >/dev/null 2>&1 &\n",
		"sleep 1 & disown\n",
	} {
		if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputShell, input); err != nil {
			t.Errorf("ValidateSupervisedTerminalInput(%q) error = %v", input, err)
		}
	}
}

func TestValidateSupervisedTerminalInputRejectsAliasMutation(t *testing.T) {
	t.Parallel()

	if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputShell, "alias sleeper='setsid sleep 60'\n"); !IsPolicyDenied(err) {
		t.Fatalf("alias input error = %v, want PolicyError", err)
	}
}

func TestValidateSupervisedTerminalInterpreterInput(t *testing.T) {
	t.Parallel()

	if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputInterpreter, "import os\nos.setsid()\n"); !IsPolicyDenied(err) {
		t.Fatalf("interpreter input error = %v, want PolicyError", err)
	}
	if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputInterpreter, "print('safe input')\n"); err != nil {
		t.Fatalf("safe interpreter input error = %v", err)
	}
	// Interpreter input uses conservative identifier scanning rather than a
	// language parser, so literal detachment words are denied as well.
	if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputInterpreter, "print('setsid is documentation')\n"); !IsPolicyDenied(err) {
		t.Fatalf("literal detachment word error = %v, want PolicyError", err)
	}
}

func TestSupervisedTerminalInterpreterInputStateRejectsSplitDetachment(t *testing.T) {
	t.Parallel()

	state, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, SupervisedTerminalInputState{}, "start_new_")
	if err != nil {
		t.Fatalf("first identifier fragment: %v", err)
	}
	if _, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, state, "session=True\n"); !IsPolicyDenied(err) {
		t.Fatalf("split identifier error = %T %v, want PolicyError", err, err)
	}

	state, err = ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, SupervisedTerminalInputState{}, "spawn({detached"+strings.Repeat(" ", 256))
	if err != nil {
		t.Fatalf("first detached option fragment: %v", err)
	}
	if _, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, state, ": true})\n"); !IsPolicyDenied(err) {
		t.Fatalf("split detached option error = %T %v, want PolicyError", err, err)
	}
}

func TestSupervisedTerminalInterpreterInputStatePreservesWhitespaceBoundaries(t *testing.T) {
	t.Parallel()

	state, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, SupervisedTerminalInputState{}, "set ")
	if err != nil {
		t.Fatalf("first whitespace-separated fragment: %v", err)
	}
	if _, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, state, "sid()\n"); err != nil {
		t.Fatalf("whitespace-separated identifiers were joined: %v", err)
	}
}

func TestSupervisedTerminalInterpreterInputStateRemainsBounded(t *testing.T) {
	t.Parallel()

	var state SupervisedTerminalInputState
	for index := 0; index < 5000; index++ {
		var err error
		state, err = ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputInterpreter, state, "print('safe')\n")
		if err != nil {
			t.Fatalf("interpreter write %d: %v", index, err)
		}
		if got := state.RetainedBytes(); got > 2*supervisedTerminalInterpreterStateTailLimit {
			t.Fatalf("retained interpreter state after write %d = %d bytes, want <= %d", index, got, 2*supervisedTerminalInterpreterStateTailLimit)
		}
	}
}

func TestSupervisedTerminalShellInputStateBoundsIncompleteMultilineWork(t *testing.T) {
	t.Parallel()

	input := "if true; then\n" + strings.Repeat("  true\n", 12000)
	result := make(chan error, 1)
	go func() {
		_, err := ValidateSupervisedTerminalInputWrite(SupervisedTerminalInputShell, SupervisedTerminalInputState{}, input)
		result <- err
	}()
	select {
	case err := <-result:
		if !IsPolicyDenied(err) {
			t.Fatalf("large incomplete multiline input error = %T %v, want PolicyError", err, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("large incomplete multiline validation exceeded its bounded work deadline")
	}
}

func TestSupervisedTerminalInputModeForCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		args    []string
		want    SupervisedTerminalInputMode
	}{
		{name: "default shell", want: SupervisedTerminalInputShell},
		{name: "interactive shell", command: "bash", args: []string{"--norc"}, want: SupervisedTerminalInputShell},
		{name: "shell stdin script", command: "sh", args: []string{"-s", "argument"}, want: SupervisedTerminalInputShell},
		{name: "shell lone dash stdin script", command: "bash", args: []string{"-"}, want: SupervisedTerminalInputShell},
		{name: "inline shell data", command: "sh", args: []string{"-c", "cat"}, want: SupervisedTerminalInputNone},
		{name: "shell file data", command: "sh", args: []string{"script.sh"}, want: SupervisedTerminalInputNone},
		{name: "python repl", command: "python3", want: SupervisedTerminalInputInterpreter},
		{name: "python stdin code", command: "python3", args: []string{"-"}, want: SupervisedTerminalInputInterpreter},
		{name: "python inline data", command: "python3", args: []string{"-c", "print(input())"}, want: SupervisedTerminalInputNone},
		{name: "python interactive inline", command: "python3", args: []string{"-i", "-c", "print('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "python interactive option value before inline", command: "python3", args: []string{"-i", "-W", "ignore", "-c", "print('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "python combined interactive inline", command: "python3", args: []string{"-ic", "print('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "python attached combined interactive inline", command: "python3", args: []string{"-icprint('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "python interactive script", command: "python3", args: []string{"-i", "app.py"}, want: SupervisedTerminalInputInterpreter},
		{name: "python file data", command: "python3", args: []string{"app.py"}, want: SupervisedTerminalInputNone},
		{name: "node interactive eval", command: "node", args: []string{"-i", "-e", "console.log('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "node interactive flag after eval", command: "node", args: []string{"-e", "console.log('ready')", "-i"}, want: SupervisedTerminalInputInterpreter},
		{name: "node interactive preload before eval", command: "node", args: []string{"-i", "-r", "fs", "-e", "console.log('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "node option terminator before script", command: "node", args: []string{"--", "app.js"}, want: SupervisedTerminalInputNone},
		{name: "ruby require before eval", command: "ruby", args: []string{"-r", "json", "-e", "puts 'ready'"}, want: SupervisedTerminalInputNone},
		{name: "ruby option terminator before stdin code", command: "ruby", args: []string{"--"}, want: SupervisedTerminalInputInterpreter},
		{name: "perl include before stdin code", command: "perl", args: []string{"-I", "lib"}, want: SupervisedTerminalInputInterpreter},
		{name: "perl option terminator before stdin code", command: "perl", args: []string{"--"}, want: SupervisedTerminalInputInterpreter},
		{name: "PHP config before stdin code", command: "php", args: []string{"-d", "display_errors=1"}, want: SupervisedTerminalInputInterpreter},
		{name: "lua interactive eval", command: "lua", args: []string{"-i", "-e", "print('ready')"}, want: SupervisedTerminalInputInterpreter},
		{name: "lua library before interactive", command: "lua", args: []string{"-l", "mod", "-i"}, want: SupervisedTerminalInputInterpreter},
		{name: "PowerShell no-exit command", command: "pwsh", args: []string{"-NoExit", "-Command", "Write-Output ready"}, want: SupervisedTerminalInputInterpreter},
		{name: "PowerShell working directory before no-exit", command: "pwsh", args: []string{"-WorkingDirectory", "/tmp", "-NoExit"}, want: SupervisedTerminalInputInterpreter},
		{name: "env shell", command: "env", args: []string{"MODE=test", "sh"}, want: SupervisedTerminalInputShell},
		{name: "timeout shell", command: "timeout", args: []string{"--foreground", "5s", "sh"}, want: SupervisedTerminalInputShell},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SupervisedTerminalInputModeForCommand(tc.command, tc.args); got != tc.want {
				t.Fatalf("input mode = %v, want %v", got, tc.want)
			}
		})
	}
}

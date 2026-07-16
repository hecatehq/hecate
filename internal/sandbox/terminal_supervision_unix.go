//go:build !windows

package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

const supervisedTerminalValidationDepth = 12

type supervisedTerminalWord struct {
	value  string
	static bool
}

// ValidateSupervisedTerminalCommand rejects known ways a Unix terminal
// command can leave the process group Hecate owns. It is deliberately scoped
// to long-lived terminals: one-shot shell execution does not retain a
// workspace writer lease after its command returns.
func ValidateSupervisedTerminalCommand(command string, args []string) error {
	words := make([]supervisedTerminalWord, 0, 1+len(args))
	words = append(words, supervisedTerminalWord{value: command, static: true})
	for _, arg := range args {
		words = append(words, supervisedTerminalWord{value: arg, static: true})
	}
	return validateSupervisedTerminalWords(words, 0)
}

// SupervisedTerminalInputModeForCommand reports whether stdin is executable
// shell or interpreter code. Script and inline-code invocations return none
// when stdin is application data; force-interactive flags keep code scanning
// active after the initial source runs.
func SupervisedTerminalInputModeForCommand(command string, args []string) SupervisedTerminalInputMode {
	words := make([]supervisedTerminalWord, 0, 1+len(args))
	words = append(words, supervisedTerminalWord{value: command, static: true})
	for _, arg := range args {
		words = append(words, supervisedTerminalWord{value: arg, static: true})
	}
	return supervisedTerminalInputMode(words, 0)
}

// ValidateSupervisedTerminalInput checks interactive code written to a
// terminal. Complete shell fragments use the same AST walk as spawn-time
// scripts. Incomplete shell fragments and interpreter input fall back to exact
// known detachment identifiers; the caller retains bounded unfinished syntax
// and token tails so commands split across Write calls are validated as the
// terminal receives them.
func ValidateSupervisedTerminalInput(mode SupervisedTerminalInputMode, input string) error {
	switch mode {
	case SupervisedTerminalInputNone:
		return nil
	case SupervisedTerminalInputInterpreter:
		if mechanism := supervisedTerminalInterpreterDetachment(input); mechanism != "" {
			return supervisedTerminalPolicyError(mechanism)
		}
		return nil
	case SupervisedTerminalInputShell:
		file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(input), "terminal input")
		if err == nil {
			return validateSupervisedTerminalFile(file, 0)
		}
		if mechanism := supervisedTerminalDetachmentIdentifier(input); mechanism != "" {
			return supervisedTerminalPolicyError(mechanism)
		}
		if supervisedTerminalEnablesMonitorText(input) {
			return supervisedTerminalPolicyError("shell job-control monitor mode")
		}
		return nil
	default:
		return &PolicyError{Reason: "unknown supervised terminal input mode"}
	}
}

const supervisedTerminalInterpreterStateTailLimit = 64

// ValidateSupervisedTerminalInputWrite validates one prospective terminal
// write and returns the bounded state to commit if those bytes reach the
// process. Callers must retain the previous state when the pipe writes zero
// bytes and recompute with the actual prefix after a short write.
func ValidateSupervisedTerminalInputWrite(mode SupervisedTerminalInputMode, state SupervisedTerminalInputState, input string) (SupervisedTerminalInputState, error) {
	switch mode {
	case SupervisedTerminalInputNone:
		return SupervisedTerminalInputState{}, nil
	case SupervisedTerminalInputShell:
		candidate := state.shellPending + input
		boundary, err := supervisedTerminalValidatedShellPrefix(candidate)
		if err != nil {
			return state, err
		}
		pending := candidate[boundary:]
		if len(pending) > SupervisedTerminalInputPendingLimit {
			return state, &PolicyError{Reason: fmt.Sprintf("incomplete terminal input exceeds the %d-byte process-supervision validation limit", SupervisedTerminalInputPendingLimit)}
		}
		if err := ValidateSupervisedTerminalInput(SupervisedTerminalInputShell, pending); err != nil {
			return state, err
		}
		return SupervisedTerminalInputState{shellPending: pending}, nil
	case SupervisedTerminalInputInterpreter:
		identifierCandidate := state.interpreterIdentifierTail + input
		if mechanism := supervisedTerminalDetachmentIdentifier(identifierCandidate); mechanism != "" {
			return state, supervisedTerminalPolicyError(mechanism)
		}
		compactCandidate := state.interpreterCompactTail + supervisedTerminalCompactInterpreterInput(input)
		if mechanism := supervisedTerminalCompactInterpreterDetachment(compactCandidate); mechanism != "" {
			return state, supervisedTerminalPolicyError(mechanism)
		}
		return SupervisedTerminalInputState{
			interpreterIdentifierTail: supervisedTerminalTail(identifierCandidate, supervisedTerminalInterpreterStateTailLimit),
			interpreterCompactTail:    supervisedTerminalTail(compactCandidate, supervisedTerminalInterpreterStateTailLimit),
		}, nil
	default:
		return state, &PolicyError{Reason: "unknown supervised terminal input mode"}
	}
}

func supervisedTerminalValidatedShellPrefix(input string) (int, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	boundary := 0
	var validationErr error
	parseErr := parser.Interactive(strings.NewReader(input), func(statements []*syntax.Stmt) bool {
		if len(statements) > 0 {
			if err := validateSupervisedTerminalFile(&syntax.File{Stmts: statements}, 0); err != nil {
				validationErr = err
				return false
			}
		}
		if parser.Incomplete() {
			return true
		}
		searchFrom := boundary
		if len(statements) > 0 {
			searchFrom = int(statements[len(statements)-1].End().Offset())
			if searchFrom > len(input) {
				searchFrom = len(input)
			}
		}
		if newline := strings.IndexByte(input[searchFrom:], '\n'); newline >= 0 {
			boundary = searchFrom + newline + 1
		}
		return true
	})
	if validationErr != nil {
		return boundary, validationErr
	}
	if parseErr != nil && !syntax.IsIncomplete(parseErr) {
		return boundary, &PolicyError{Reason: fmt.Sprintf("terminal shell input cannot be safely validated for process supervision: %v", parseErr)}
	}
	return boundary, validationErr
}

func supervisedTerminalTail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func validateSupervisedTerminalWords(words []supervisedTerminalWord, depth int) error {
	if depth > supervisedTerminalValidationDepth {
		return &PolicyError{Reason: "terminal command nesting is too deep to validate process supervision"}
	}
	words = trimSupervisedTerminalAssignments(words)
	if len(words) == 0 {
		return nil
	}
	if !words[0].static {
		return &PolicyError{Reason: "dynamic terminal command names are not allowed because process supervision cannot be verified"}
	}

	base := supervisedTerminalExecutable(words[0].value)
	switch base {
	case "env":
		unwrapped, ok := unwrapSupervisedEnv(words[1:])
		if !ok {
			return &PolicyError{Reason: "dynamic env terminal wrappers are not allowed because process supervision cannot be verified"}
		}
		return validateSupervisedTerminalWords(unwrapped, depth+1)
	case "command", "builtin":
		return validateSupervisedTerminalWords(trimSupervisedWrapperOptions(words[1:]), depth+1)
	case "exec":
		return validateSupervisedTerminalWords(trimSupervisedExecOptions(words[1:]), depth+1)
	case "nohup":
		// nohup changes signal/stdio behavior but not process-group ownership.
		// Inspect the wrapped command while preserving ordinary nohup jobs.
		return validateSupervisedTerminalWords(trimSupervisedWrapperOptions(words[1:]), depth+1)
	case "nice":
		unwrapped, err := unwrapSupervisedNice(words[1:])
		if err != nil {
			return err
		}
		return validateSupervisedTerminalWords(unwrapped, depth+1)
	case "timeout":
		unwrapped, err := unwrapSupervisedTimeout(words[1:])
		if err != nil {
			return err
		}
		return validateSupervisedTerminalWords(unwrapped, depth+1)
	case "stdbuf":
		unwrapped, err := unwrapSupervisedStdbuf(words[1:])
		if err != nil {
			return err
		}
		return validateSupervisedTerminalWords(unwrapped, depth+1)
	case "setsid", "setpgid", "setpgrp", "daemon", "daemonize", "systemd-run", "launchctl":
		return supervisedTerminalPolicyError(base)
	case "start-stop-daemon":
		for _, word := range words[1:] {
			if !word.static {
				return &PolicyError{Reason: "dynamic start-stop-daemon options are not allowed in supervised terminals"}
			}
			if word.value == "--background" || supervisedTerminalStartStopDaemonBackgroundOption(word.value) {
				return supervisedTerminalPolicyError("start-stop-daemon --background")
			}
		}
		return nil
	case "eval":
		return supervisedTerminalPolicyError("eval")
	case "alias":
		return supervisedTerminalPolicyError("shell alias mutation")
	case "set":
		if supervisedTerminalSetEnablesMonitor(words[1:]) {
			return supervisedTerminalPolicyError("set -m / set -o monitor")
		}
		return nil
	}

	if supervisedTerminalShell(base) {
		invocation, err := supervisedTerminalShellInvocation(words[1:])
		if err != nil {
			return err
		}
		if invocation.hasInlineScript {
			return validateSupervisedTerminalScript(invocation.script, depth+1)
		}
	}
	if supervisedTerminalInterpreter(base) {
		invocation, err := supervisedTerminalInterpreterInvocation(base, words[1:])
		if err != nil {
			return err
		}
		for _, code := range invocation.inlineCode {
			if mechanism := supervisedTerminalInterpreterDetachment(code); mechanism != "" {
				return supervisedTerminalPolicyError(mechanism)
			}
		}
	}
	return nil
}

func supervisedTerminalInputMode(words []supervisedTerminalWord, depth int) SupervisedTerminalInputMode {
	if depth > supervisedTerminalValidationDepth {
		return SupervisedTerminalInputNone
	}
	words = trimSupervisedTerminalAssignments(words)
	if len(words) == 0 || !words[0].static {
		return SupervisedTerminalInputNone
	}
	base := supervisedTerminalExecutable(words[0].value)
	if base == "" {
		return SupervisedTerminalInputShell
	}
	var unwrapped []supervisedTerminalWord
	var ok bool
	switch base {
	case "env":
		unwrapped, ok = unwrapSupervisedEnv(words[1:])
	case "command", "builtin", "nohup":
		unwrapped, ok = trimSupervisedWrapperOptions(words[1:]), true
	case "exec":
		unwrapped, ok = trimSupervisedExecOptions(words[1:]), true
	case "nice":
		var err error
		unwrapped, err = unwrapSupervisedNice(words[1:])
		ok = err == nil
	case "timeout":
		var err error
		unwrapped, err = unwrapSupervisedTimeout(words[1:])
		ok = err == nil
	case "stdbuf":
		var err error
		unwrapped, err = unwrapSupervisedStdbuf(words[1:])
		ok = err == nil
	default:
		if supervisedTerminalShell(base) {
			invocation, err := supervisedTerminalShellInvocation(words[1:])
			if err == nil && invocation.stdinCode {
				return SupervisedTerminalInputShell
			}
			return SupervisedTerminalInputNone
		}
		if supervisedTerminalInterpreter(base) {
			invocation, err := supervisedTerminalInterpreterInvocation(base, words[1:])
			if err == nil && invocation.stdinCode {
				return SupervisedTerminalInputInterpreter
			}
		}
		return SupervisedTerminalInputNone
	}
	if !ok || len(unwrapped) == 0 {
		return SupervisedTerminalInputNone
	}
	return supervisedTerminalInputMode(unwrapped, depth+1)
}

func validateSupervisedTerminalScript(script string, depth int) error {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "terminal -c")
	if err != nil {
		return &PolicyError{Reason: fmt.Sprintf("terminal shell script cannot be validated for process supervision: %v", err)}
	}
	return validateSupervisedTerminalFile(file, depth)
}

func validateSupervisedTerminalFile(file *syntax.File, depth int) error {
	var validationErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if validationErr != nil {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]supervisedTerminalWord, 0, len(call.Args))
		for _, word := range call.Args {
			value, static := supervisedTerminalStaticWord(word)
			words = append(words, supervisedTerminalWord{value: value, static: static})
		}
		validationErr = validateSupervisedTerminalWords(words, depth)
		return validationErr == nil
	})
	return validationErr
}

func supervisedTerminalStaticWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var builder strings.Builder
	var appendParts func([]syntax.WordPart) bool
	appendParts = func(parts []syntax.WordPart) bool {
		for _, part := range parts {
			switch value := part.(type) {
			case *syntax.Lit:
				builder.WriteString(value.Value)
			case *syntax.SglQuoted:
				builder.WriteString(value.Value)
			case *syntax.DblQuoted:
				if !appendParts(value.Parts) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !appendParts(word.Parts) {
		return "", false
	}
	value := builder.String()
	if strings.ContainsAny(value, "*?[") {
		return "", false
	}
	return value, true
}

type supervisedTerminalShellDetails struct {
	script          string
	hasInlineScript bool
	stdinCode       bool
}

func supervisedTerminalShellInvocation(args []supervisedTerminalWord) (supervisedTerminalShellDetails, error) {
	details := supervisedTerminalShellDetails{stdinCode: true}
	forceStdin := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !arg.static {
			return details, &PolicyError{Reason: "dynamic shell options are not allowed in supervised terminals"}
		}
		value := arg.value
		if value == "--" {
			details.stdinCode = forceStdin || index+1 == len(args)
			return details, nil
		}
		if value == "-" {
			// A lone dash is the conventional stdin script operand for the
			// supported shells, not an ordinary script filename.
			details.stdinCode = true
			return details, nil
		}
		if strings.HasPrefix(value, "--") {
			name, _, hasValue := strings.Cut(value, "=")
			switch name {
			case "--norc", "--noprofile", "--posix", "--restricted", "--verbose", "--login", "--noediting", "--debugger", "--dump-po-strings", "--dump-strings", "--help", "--version":
				if hasValue {
					return details, &PolicyError{Reason: fmt.Sprintf("shell option %q cannot be safely validated for process supervision", value)}
				}
				continue
			case "--init-file", "--rcfile":
				if !hasValue {
					if index+1 >= len(args) || !args[index+1].static {
						return details, &PolicyError{Reason: fmt.Sprintf("shell option %q requires a static value", value)}
					}
					index++
				}
				continue
			default:
				return details, &PolicyError{Reason: fmt.Sprintf("shell option %q cannot be safely validated for process supervision", value)}
			}
		}
		if (strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+")) && len(value) > 1 {
			enable := value[0] == '-'
			flags := value[1:]
			for offset := 0; offset < len(flags); offset++ {
				flag := flags[offset]
				switch flag {
				case 'c':
					if !enable {
						return details, &PolicyError{Reason: "shell +c cannot be safely validated for process supervision"}
					}
					if index+1 >= len(args) || !args[index+1].static {
						return details, &PolicyError{Reason: "dynamic shell -c scripts are not allowed because process supervision cannot be verified"}
					}
					details.script = args[index+1].value
					details.hasInlineScript = true
					details.stdinCode = false
					return details, nil
				case 'm':
					if enable {
						return details, supervisedTerminalPolicyError("shell -m")
					}
				case 's':
					if enable {
						forceStdin = true
					}
				case 'o', 'O':
					var option string
					if offset+1 < len(flags) {
						option = flags[offset+1:]
						offset = len(flags)
					} else {
						if index+1 >= len(args) || !args[index+1].static {
							return details, &PolicyError{Reason: fmt.Sprintf("shell %c%c requires a static value", value[0], flag)}
						}
						index++
						option = args[index].value
					}
					if flag == 'o' && enable && option == "monitor" {
						return details, supervisedTerminalPolicyError("shell -o monitor")
					}
				default:
					if !strings.ContainsRune("abefhiklnprstuvxBCEHPTV", rune(flag)) {
						return details, &PolicyError{Reason: fmt.Sprintf("shell option %q cannot be safely validated for process supervision", value)}
					}
				}
			}
			continue
		}
		details.stdinCode = forceStdin
		return details, nil
	}
	details.stdinCode = true
	return details, nil
}

func supervisedTerminalSetEnablesMonitor(args []supervisedTerminalWord) bool {
	for index := 0; index < len(args); index++ {
		if !args[index].static {
			return true
		}
		value := args[index].value
		if value == "--" {
			return false
		}
		if supervisedTerminalShortOption(value, 'm') {
			return true
		}
		if value == "-o" && index+1 < len(args) {
			index++
			if !args[index].static || args[index].value == "monitor" {
				return true
			}
		}
	}
	return false
}

func supervisedTerminalStartStopDaemonBackgroundOption(value string) bool {
	if len(value) < 2 || value[0] != '-' || value[1] == '-' {
		return false
	}
	for _, flag := range value[1:] {
		if flag == 'b' {
			return true
		}
		// These short options consume the rest of this argv element (or the
		// next element) as a value. A later `b` is data, not another flag.
		if strings.ContainsRune("xpusRnaNPIcrdk", flag) {
			return false
		}
	}
	return false
}

func supervisedTerminalShortOption(value string, want rune) bool {
	return len(value) > 1 && value[0] == '-' && value[1] != '-' && strings.ContainsRune(value[1:], want)
}

type supervisedTerminalInterpreterDetails struct {
	inlineCode []string
	stdinCode  bool
}

func supervisedTerminalInterpreterInvocation(base string, args []supervisedTerminalWord) (supervisedTerminalInterpreterDetails, error) {
	details := supervisedTerminalInterpreterDetails{stdinCode: true}
	forceStdin := false
	staticArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if !arg.static {
			return details, &PolicyError{Reason: "dynamic interpreter arguments are not allowed because process supervision cannot be verified"}
		}
		staticArgs = append(staticArgs, arg.value)
	}
	addCode := func(index *int, attached string, hasAttached bool) error {
		code := attached
		if !hasAttached {
			(*index)++
			if *index >= len(staticArgs) {
				return &PolicyError{Reason: "interpreter code option requires a static value"}
			}
			code = staticArgs[*index]
		}
		details.inlineCode = append(details.inlineCode, code)
		details.stdinCode = forceStdin
		return nil
	}

	for index := 0; index < len(staticArgs); index++ {
		value := staticArgs[index]
		lower := strings.ToLower(value)
		switch {
		case strings.HasPrefix(base, "python"):
			switch {
			case value == "--":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					details.stdinCode = true
				} else if index+1 < len(staticArgs) {
					details.stdinCode = forceStdin
				}
				return details, nil
			case value == "-i":
				forceStdin = true
				details.stdinCode = true
				continue
			case value == "--check-hash-based-pycs":
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: "Python --check-hash-based-pycs requires a static value"}
				}
				continue
			case strings.HasPrefix(value, "--check-hash-based-pycs="):
				continue
			case value == "-c":
				if err := addCode(&index, "", false); err != nil {
					return details, err
				}
				return details, nil
			case value == "-m":
				details.stdinCode = forceStdin
				return details, nil
			case value == "-":
				return details, nil
			case strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--"):
				options := supervisedTerminalPythonShortOptions(value)
				if !options.valid {
					return details, &PolicyError{Reason: fmt.Sprintf("Python option %q cannot be safely validated for terminal input supervision", value)}
				}
				if options.interactive {
					forceStdin = true
					details.stdinCode = true
				}
				switch options.source {
				case 'c':
					if err := addCode(&index, options.attached, options.sourceAttached); err != nil {
						return details, err
					}
					return details, nil
				case 'm':
					details.stdinCode = forceStdin
					return details, nil
				}
				if options.consumesNext {
					index++
					if index >= len(staticArgs) {
						return details, &PolicyError{Reason: fmt.Sprintf("Python option %q requires a static value", value)}
					}
				}
				continue
			case supervisedTerminalPythonNoValueOption(value):
				continue
			case strings.HasPrefix(value, "-"):
				return details, &PolicyError{Reason: fmt.Sprintf("Python option %q cannot be safely validated for terminal input supervision", value)}
			default:
				details.stdinCode = forceStdin
				return details, nil
			}
		case base == "node" || base == "nodejs":
			switch {
			case value == "--":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					details.stdinCode = true
				} else if index+1 < len(staticArgs) {
					details.stdinCode = forceStdin
				}
				return details, nil
			case value == "-i" || value == "--interactive":
				forceStdin = true
				details.stdinCode = true
				continue
			case supervisedTerminalNodeOptionTakesNext(value):
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: fmt.Sprintf("Node option %q requires a static value", value)}
				}
				continue
			case value == "-e" || value == "--eval" || value == "-p" || value == "--print":
				if err := addCode(&index, "", false); err != nil {
					return details, err
				}
				continue
			case strings.HasPrefix(value, "--eval=") || strings.HasPrefix(value, "--print="):
				if err := addCode(&index, value[strings.IndexByte(value, '=')+1:], true); err != nil {
					return details, err
				}
				continue
			case (strings.HasPrefix(value, "-e") || strings.HasPrefix(value, "-p")) && len(value) > 2:
				if err := addCode(&index, value[2:], true); err != nil {
					return details, err
				}
				continue
			case supervisedTerminalNodeAttachedValueOption(value):
				continue
			case value == "-":
				return details, nil
			case supervisedTerminalNodeNoValueOption(value):
				continue
			case strings.HasPrefix(value, "-"):
				return details, &PolicyError{Reason: fmt.Sprintf("Node option %q cannot be safely validated for terminal input supervision", value)}
			default:
				details.stdinCode = forceStdin
				return details, nil
			}
		case base == "ruby" || base == "perl":
			attached, hasEval := supervisedTerminalShortEval(base, value)
			switch {
			case value == "--":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					details.stdinCode = true
				} else if index+1 < len(staticArgs) {
					details.stdinCode = false
				}
				return details, nil
			case hasEval:
				if err := addCode(&index, attached, attached != ""); err != nil {
					return details, err
				}
			case supervisedTerminalRubyPerlOptionTakesNext(base, value):
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: fmt.Sprintf("%s option %q requires a static value", base, value)}
				}
			case supervisedTerminalRubyPerlAttachedValueOption(base, value):
				continue
			case value == "-":
				return details, nil
			case supervisedTerminalRubyPerlNoValueOption(base, value):
				continue
			case strings.HasPrefix(value, "-"):
				return details, &PolicyError{Reason: fmt.Sprintf("%s option %q cannot be safely validated for terminal input supervision", base, value)}
			default:
				details.stdinCode = false
				return details, nil
			}
		case base == "php":
			switch {
			case value == "--":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					details.stdinCode = true
				} else if index+1 < len(staticArgs) {
					details.stdinCode = forceStdin
				}
				return details, nil
			case value == "-a" || value == "--interactive":
				forceStdin = true
				details.stdinCode = true
				continue
			case value == "-r" || value == "-R" || value == "-B" || value == "-E" || value == "--run" || value == "--process-begin" || value == "--process-code" || value == "--process-end":
				if err := addCode(&index, "", false); err != nil {
					return details, err
				}
			case strings.HasPrefix(value, "--run=") || strings.HasPrefix(value, "--process-begin=") || strings.HasPrefix(value, "--process-code=") || strings.HasPrefix(value, "--process-end="):
				if err := addCode(&index, value[strings.IndexByte(value, '=')+1:], true); err != nil {
					return details, err
				}
			case len(value) > 2 && (strings.HasPrefix(value, "-r") || strings.HasPrefix(value, "-R") || strings.HasPrefix(value, "-B") || strings.HasPrefix(value, "-E")):
				if err := addCode(&index, value[2:], true); err != nil {
					return details, err
				}
			case value == "-f" || value == "--file" || value == "-F" || value == "--process-file":
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: fmt.Sprintf("PHP option %q requires a static value", value)}
				}
				details.stdinCode = forceStdin || staticArgs[index] == "-"
				return details, nil
			case strings.HasPrefix(value, "--file=") || strings.HasPrefix(value, "--process-file="):
				details.stdinCode = forceStdin || value[strings.IndexByte(value, '=')+1:] == "-"
				return details, nil
			case strings.HasPrefix(value, "-F") && len(value) > 2:
				details.stdinCode = forceStdin || value[2:] == "-"
				return details, nil
			case supervisedTerminalPHPOptionTakesNext(value):
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: fmt.Sprintf("PHP option %q requires a static value", value)}
				}
				continue
			case supervisedTerminalPHPAttachedValueOption(value):
				continue
			case value == "-":
				return details, nil
			case supervisedTerminalPHPNoValueOption(value):
				continue
			case strings.HasPrefix(value, "-"):
				return details, &PolicyError{Reason: fmt.Sprintf("PHP option %q cannot be safely validated for terminal input supervision", value)}
			default:
				details.stdinCode = forceStdin
				return details, nil
			}
		case base == "lua":
			switch {
			case value == "--":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					details.stdinCode = true
				} else if index+1 < len(staticArgs) {
					details.stdinCode = forceStdin
				}
				return details, nil
			case value == "-i":
				forceStdin = true
				details.stdinCode = true
				continue
			case value == "-l":
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: "Lua -l requires a static value"}
				}
				continue
			case strings.HasPrefix(value, "-l") && len(value) > 2:
				continue
			case value == "-e":
				if err := addCode(&index, "", false); err != nil {
					return details, err
				}
			case strings.HasPrefix(value, "-e") && len(value) > 2:
				if err := addCode(&index, value[2:], true); err != nil {
					return details, err
				}
			case value == "-":
				return details, nil
			case value == "-v" || value == "-E" || value == "-W":
				continue
			case strings.HasPrefix(value, "-"):
				return details, &PolicyError{Reason: fmt.Sprintf("Lua option %q cannot be safely validated for terminal input supervision", value)}
			default:
				details.stdinCode = forceStdin
				return details, nil
			}
		case base == "pwsh" || base == "powershell":
			switch lower {
			case "-noexit", "-noe":
				forceStdin = true
				details.stdinCode = true
				continue
			case "-workingdirectory", "-workingdir", "-wd", "-executionpolicy", "-ex", "-ep", "-inputformat", "-inp", "-if", "-outputformat", "-o", "-of", "-windowstyle", "-w", "-configurationname", "-config", "-custompipename", "-settingsfile":
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: fmt.Sprintf("PowerShell option %q requires a static value", value)}
				}
				continue
			case "-encodedcommand", "-enc", "-e":
				return details, supervisedTerminalPolicyError("encoded PowerShell command")
			case "-command", "-c", "-commandwithargs":
				if index+1 < len(staticArgs) && staticArgs[index+1] == "-" {
					return details, nil
				}
				if index+1 >= len(staticArgs) {
					return details, &PolicyError{Reason: "PowerShell command option requires a static value"}
				}
				details.inlineCode = append(details.inlineCode, strings.Join(staticArgs[index+1:], " "))
				details.stdinCode = forceStdin
				return details, nil
			case "-file", "-f":
				index++
				if index >= len(staticArgs) {
					return details, &PolicyError{Reason: "PowerShell -File requires a static value"}
				}
				details.stdinCode = forceStdin || staticArgs[index] == "-"
				return details, nil
			case "-":
				return details, nil
			default:
				if supervisedTerminalPowerShellNoValueOption(lower) {
					continue
				}
				if strings.HasPrefix(value, "-") {
					return details, &PolicyError{Reason: fmt.Sprintf("PowerShell option %q cannot be safely validated for terminal input supervision", value)}
				}
				details.stdinCode = forceStdin
				return details, nil
			}
		}
	}
	return details, nil
}

type supervisedTerminalPythonOptions struct {
	interactive    bool
	source         byte
	attached       string
	sourceAttached bool
	consumesNext   bool
	valid          bool
}

func supervisedTerminalPythonShortOptions(value string) supervisedTerminalPythonOptions {
	var options supervisedTerminalPythonOptions
	if len(value) < 2 || value[0] != '-' || value[1] == '-' {
		return options
	}
	options.valid = true
	flags := value[1:]
	for offset := 0; offset < len(flags); offset++ {
		switch flags[offset] {
		case 'i':
			options.interactive = true
		case 'c', 'm':
			options.source = flags[offset]
			options.attached = flags[offset+1:]
			options.sourceAttached = offset+1 < len(flags)
			return options
		case 'W', 'X', 'Q':
			// These options consume the remaining characters (or the next
			// argv element) as data, so later letters are not flags.
			options.consumesNext = offset+1 == len(flags)
			return options
		default:
			if !strings.ContainsRune("bBdEhiIOPqsSuvVx", rune(flags[offset])) {
				options.valid = false
				return options
			}
		}
	}
	return options
}

func supervisedTerminalPythonNoValueOption(value string) bool {
	switch value {
	case "--debug", "--help", "--help-all", "--help-env", "--help-xoptions", "--version":
		return true
	default:
		return false
	}
}

func supervisedTerminalNodeOptionTakesNext(value string) bool {
	switch value {
	case "-r", "--require", "-C", "--conditions", "--import", "--loader", "--experimental-loader",
		"--input-type", "--inspect-port", "--debug-port", "--openssl-config", "--redirect-warnings",
		"--report-directory", "--report-filename", "--snapshot-blob", "--test-reporter",
		"--test-reporter-destination", "--test-shard", "--title", "--trace-event-categories",
		"--watch-path", "--env-file", "--env-file-if-exists":
		return true
	default:
		return false
	}
}

func supervisedTerminalNodeAttachedValueOption(value string) bool {
	if (strings.HasPrefix(value, "-r") || strings.HasPrefix(value, "-C")) && len(value) > 2 {
		return true
	}
	return strings.HasPrefix(value, "--") && strings.Contains(value, "=")
}

func supervisedTerminalNodeNoValueOption(value string) bool {
	switch value {
	case "-c", "--check", "-h", "--help", "-v", "--version",
		"--abort-on-uncaught-exception", "--allow-addons", "--allow-child-process", "--allow-inspector",
		"--allow-wasi", "--allow-worker", "--build-snapshot", "--completion-bash", "--cpu-prof",
		"--disable-sigusr1", "--disable-wasm-trap-handler", "--disallow-code-generation-from-strings",
		"--enable-fips", "--enable-source-maps", "--entry-url", "--expose-gc", "--force-fips",
		"--frozen-intrinsics", "--heap-prof", "--insecure-http-parser", "--inspect", "--inspect-brk",
		"--inspect-wait", "--jitless", "--no-addons", "--no-deprecation", "--no-warnings",
		"--preserve-symlinks", "--preserve-symlinks-main", "--prof", "--report-compact",
		"--report-on-fatalerror", "--test", "--test-only", "--throw-deprecation", "--trace-deprecation",
		"--trace-exit", "--trace-sigint", "--trace-sync-io", "--trace-uncaught", "--trace-warnings",
		"--use-bundled-ca", "--use-openssl-ca", "--v8-options", "--watch", "--zero-fill-buffers":
		return true
	default:
		return false
	}
}

func supervisedTerminalRubyPerlOptionTakesNext(base, value string) bool {
	if base == "ruby" {
		switch value {
		case "-r", "--require", "-I", "-C", "-X", "-E", "--encoding", "--external-encoding", "--internal-encoding", "-F", "-K", "-T", "-W":
			return true
		}
		return false
	}
	switch value {
	case "-I", "-M", "-m", "-F":
		return true
	default:
		return false
	}
}

func supervisedTerminalRubyPerlAttachedValueOption(base, value string) bool {
	if strings.HasPrefix(value, "--") && strings.Contains(value, "=") {
		return true
	}
	prefixes := []string{"-I", "-F"}
	if base == "ruby" {
		prefixes = append(prefixes, "-r", "-C", "-X", "-E", "-K", "-T", "-W")
	} else {
		prefixes = append(prefixes, "-M", "-m", "-i", "-0", "-l", "-C", "-D", "-V", "-x")
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

func supervisedTerminalRubyPerlNoValueOption(base, value string) bool {
	if strings.HasPrefix(value, "--") {
		if base == "ruby" {
			switch value {
			case "--copyright", "--debug", "--disable-gems", "--enable-frozen-string-literal", "--help", "--verbose", "--version":
				return true
			}
		} else {
			switch value {
			case "--help", "--version":
				return true
			}
		}
		return false
	}
	if len(value) < 2 || value[0] != '-' {
		return false
	}
	allowed := "acdhinpsSuvwxy01234567"
	if base == "perl" {
		allowed = "acCdlnpsStTuUvwx0123456789"
	}
	for _, flag := range value[1:] {
		if !strings.ContainsRune(allowed, flag) {
			return false
		}
	}
	return true
}

func supervisedTerminalPHPOptionTakesNext(value string) bool {
	switch value {
	case "-d", "--define", "-c", "--php-ini", "-z", "--zend-extension", "-S", "--server", "-t", "--docroot":
		return true
	default:
		return false
	}
}

func supervisedTerminalPHPAttachedValueOption(value string) bool {
	if strings.HasPrefix(value, "--") && strings.Contains(value, "=") {
		return true
	}
	for _, prefix := range []string{"-d", "-c", "-z", "-S", "-t"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

func supervisedTerminalPHPNoValueOption(value string) bool {
	switch value {
	case "-h", "--help", "-i", "--info", "-l", "--syntax-check", "-m", "--modules", "-n", "--no-php-ini",
		"-s", "--syntax-highlight", "-v", "--version", "-w", "--strip":
		return true
	default:
		return false
	}
}

func supervisedTerminalPowerShellNoValueOption(value string) bool {
	switch value {
	case "-help", "-h", "-?", "-login", "-l", "-mta", "-nologo", "-nol", "-noprofile", "-nop",
		"-noninteractive", "-noni", "-nointeractive", "-sta", "-sshservermode", "-version":
		return true
	default:
		return false
	}
}

func supervisedTerminalShortEval(base, value string) (string, bool) {
	if !strings.HasPrefix(value, "-") || strings.HasPrefix(value, "--") || len(value) < 2 {
		return "", false
	}
	flags := value[1:]
	allowedPrefix := "wdv"
	if base == "perl" {
		allowedPrefix = "wWtdT"
	}
	for index, flag := range flags {
		if flag != 'e' && (base != "perl" || flag != 'E') {
			continue
		}
		for _, prefix := range flags[:index] {
			if !strings.ContainsRune(allowedPrefix, prefix) {
				return "", false
			}
		}
		return flags[index+1:], true
	}
	return "", false
}

func supervisedTerminalDetachmentIdentifier(value string) string {
	identifiers := []string{
		"start_new_session",
		"start-stop-daemon",
		"systemd-run",
		"setpgid",
		"setpgrp",
		"setsid",
		"daemonize",
		"daemon",
		"launchctl",
	}
	lower := strings.ToLower(value)
	for _, identifier := range identifiers {
		start := 0
		for {
			index := strings.Index(lower[start:], identifier)
			if index < 0 {
				break
			}
			index += start
			beforeOK := index == 0 || !supervisedTerminalIdentifierRune(rune(lower[index-1]))
			after := index + len(identifier)
			afterOK := after == len(lower) || !supervisedTerminalIdentifierRune(rune(lower[after]))
			if beforeOK && afterOK {
				return identifier
			}
			start = index + 1
		}
	}
	return ""
}

func supervisedTerminalInterpreterDetachment(value string) string {
	if mechanism := supervisedTerminalDetachmentIdentifier(value); mechanism != "" {
		return mechanism
	}
	return supervisedTerminalCompactInterpreterDetachment(supervisedTerminalCompactInterpreterInput(value))
}

func supervisedTerminalCompactInterpreterInput(value string) string {
	return strings.Map(func(value rune) rune {
		if unicode.IsSpace(value) {
			return -1
		}
		return unicode.ToLower(value)
	}, value)
}

func supervisedTerminalCompactInterpreterDetachment(compact string) string {
	if strings.Contains(compact, "detached:true") || strings.Contains(compact, `"detached":true`) || strings.Contains(compact, `'detached':true`) {
		return "interpreter detached process option"
	}
	return ""
}

func supervisedTerminalIdentifierRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value)
}

func supervisedTerminalEnablesMonitorText(value string) bool {
	fields := strings.Fields(value)
	for index, field := range fields {
		if field != "set" {
			continue
		}
		if index+1 < len(fields) && supervisedTerminalShortOption(fields[index+1], 'm') {
			return true
		}
		if index+2 < len(fields) && fields[index+1] == "-o" && fields[index+2] == "monitor" {
			return true
		}
	}
	return false
}

func supervisedTerminalPolicyError(mechanism string) error {
	return &PolicyError{Reason: fmt.Sprintf("terminal detachment mechanism %q is not allowed; Unix terminal descendants must remain in Hecate's process group", mechanism)}
}

func supervisedTerminalExecutable(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(command))
}

func supervisedTerminalShell(base string) bool {
	switch base {
	case "sh", "bash", "dash", "zsh", "ksh", "mksh", "ash":
		return true
	default:
		return false
	}
}

func supervisedTerminalInterpreter(base string) bool {
	switch base {
	case "node", "nodejs", "ruby", "perl", "php", "lua", "pwsh", "powershell":
		return true
	}
	return strings.HasPrefix(base, "python")
}

func trimSupervisedTerminalAssignments(words []supervisedTerminalWord) []supervisedTerminalWord {
	for len(words) > 0 && words[0].static && supervisedTerminalAssignment(words[0].value) {
		words = words[1:]
	}
	return words
}

func supervisedTerminalAssignment(value string) bool {
	name, _, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return false
	}
	for index, char := range name {
		if !(char == '_' || unicode.IsLetter(char) || (index > 0 && unicode.IsDigit(char))) {
			return false
		}
	}
	return true
}

func unwrapSupervisedEnv(words []supervisedTerminalWord) ([]supervisedTerminalWord, bool) {
	for len(words) > 0 {
		if !words[0].static {
			return nil, false
		}
		value := words[0].value
		switch {
		case value == "--":
			return trimSupervisedTerminalAssignments(words[1:]), true
		case value == "-S" || value == "--split-string" || strings.HasPrefix(value, "--split-string="):
			return nil, false
		case value == "-u" || value == "--unset" || value == "-C" || value == "--chdir":
			if len(words) < 2 || !words[1].static {
				return nil, false
			}
			words = words[2:]
		case strings.HasPrefix(value, "-"):
			words = words[1:]
		case supervisedTerminalAssignment(value):
			words = words[1:]
		default:
			return words, true
		}
	}
	return nil, true
}

func unwrapSupervisedNice(words []supervisedTerminalWord) ([]supervisedTerminalWord, error) {
	for len(words) > 0 {
		if !words[0].static {
			return nil, &PolicyError{Reason: "dynamic nice options are not allowed because process supervision cannot be verified"}
		}
		value := words[0].value
		switch {
		case value == "--":
			return words[1:], nil
		case value == "-n" || value == "--adjustment":
			if len(words) < 2 || !words[1].static {
				return nil, &PolicyError{Reason: "nice adjustment requires a static value"}
			}
			words = words[2:]
		case strings.HasPrefix(value, "--adjustment=") || (strings.HasPrefix(value, "-n") && len(value) > 2):
			words = words[1:]
		case strings.HasPrefix(value, "-"):
			return nil, &PolicyError{Reason: fmt.Sprintf("nice option %q cannot be safely validated for process supervision", value)}
		default:
			return words, nil
		}
	}
	return nil, nil
}

func unwrapSupervisedTimeout(words []supervisedTerminalWord) ([]supervisedTerminalWord, error) {
	foreground := false
	for len(words) > 0 {
		if !words[0].static {
			return nil, &PolicyError{Reason: "dynamic timeout options are not allowed because process supervision cannot be verified"}
		}
		value := words[0].value
		switch {
		case value == "--":
			words = words[1:]
			goto duration
		case value == "-s" || value == "--signal" || value == "-k" || value == "--kill-after":
			if len(words) < 2 || !words[1].static {
				return nil, &PolicyError{Reason: fmt.Sprintf("timeout option %q requires a static value", value)}
			}
			words = words[2:]
		case strings.HasPrefix(value, "--signal=") || strings.HasPrefix(value, "--kill-after="):
			words = words[1:]
		case value == "--foreground":
			foreground = true
			words = words[1:]
		case value == "--preserve-status" || value == "--verbose":
			words = words[1:]
		case strings.HasPrefix(value, "-"):
			return nil, &PolicyError{Reason: fmt.Sprintf("timeout option %q cannot be safely validated for process supervision", value)}
		default:
			goto duration
		}
	}

duration:
	if !foreground {
		return nil, supervisedTerminalPolicyError("timeout without --foreground")
	}
	if len(words) == 0 || !words[0].static {
		return nil, &PolicyError{Reason: "timeout duration is required before the supervised command"}
	}
	return words[1:], nil
}

func unwrapSupervisedStdbuf(words []supervisedTerminalWord) ([]supervisedTerminalWord, error) {
	for len(words) > 0 {
		if !words[0].static {
			return nil, &PolicyError{Reason: "dynamic stdbuf options are not allowed because process supervision cannot be verified"}
		}
		value := words[0].value
		switch {
		case value == "--":
			return words[1:], nil
		case value == "-i" || value == "-o" || value == "-e" || value == "--input" || value == "--output" || value == "--error":
			if len(words) < 2 || !words[1].static {
				return nil, &PolicyError{Reason: fmt.Sprintf("stdbuf option %q requires a static value", value)}
			}
			words = words[2:]
		case strings.HasPrefix(value, "--input=") || strings.HasPrefix(value, "--output=") || strings.HasPrefix(value, "--error=") ||
			(strings.HasPrefix(value, "-i") && len(value) > 2) || (strings.HasPrefix(value, "-o") && len(value) > 2) || (strings.HasPrefix(value, "-e") && len(value) > 2):
			words = words[1:]
		case strings.HasPrefix(value, "-"):
			return nil, &PolicyError{Reason: fmt.Sprintf("stdbuf option %q cannot be safely validated for process supervision", value)}
		default:
			return words, nil
		}
	}
	return nil, nil
}

func trimSupervisedWrapperOptions(words []supervisedTerminalWord) []supervisedTerminalWord {
	for len(words) > 0 && words[0].static && strings.HasPrefix(words[0].value, "-") {
		if words[0].value == "--" {
			return words[1:]
		}
		words = words[1:]
	}
	return words
}

func trimSupervisedExecOptions(words []supervisedTerminalWord) []supervisedTerminalWord {
	for len(words) > 0 && words[0].static && strings.HasPrefix(words[0].value, "-") {
		if words[0].value == "--" {
			return words[1:]
		}
		if words[0].value == "-a" {
			if len(words) < 2 {
				return nil
			}
			words = words[2:]
			continue
		}
		words = words[1:]
	}
	return words
}

package agentadapters

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/hecatehq/hecate/internal/agentcontrols"
)

const (
	privateACPPromptInputRedaction = "[private prompt input]"
	privateACPRawOutputWithheld    = "[ACP raw output withheld: private prompt inputs active]\n"
	// Fragments shorter than this are not meaningfully identifying and occur
	// constantly in ordinary status/type text. Complete aliases are always
	// redacted regardless of length.
	minACPPromptAliasFragmentBytes = 8
)

// acpPromptRedactor is an immutable, body-free set of aliases for a turn's
// private prompt stage. A terminal may retain this object after RunTurn has
// cleared the staged file bodies, so it must never acquire a reference to the
// stage or its byte slices.
type acpPromptRedactor struct {
	aliases    []string
	stageFiles []string
	stageDirs  []string
	replacer   *strings.Replacer
	foldCase   bool
}

// newACPPromptRedactor derives every operator-visible spelling of the private
// stage that Hecate creates: the supplied absolute path, its canonical path,
// file URIs, the containing stage directories, and their ephemeral basenames.
// Aliases are ordered longest-first so a full path is replaced before a nested
// directory or basename.
func newACPPromptRedactor(paths []string) (*acpPromptRedactor, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	aliases := make(map[string]struct{}, len(paths)*12)
	stageFiles := make(map[string]struct{}, len(paths)*2)
	stageDirs := make(map[string]struct{}, len(paths)*2)
	addAlias := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "." || value == string(filepath.Separator) {
			return
		}
		aliases[value] = struct{}{}
		if slash := filepath.ToSlash(value); slash != value {
			aliases[slash] = struct{}{}
		}
	}
	addPath := func(path string, includeEphemeralBasename bool) {
		path = filepath.Clean(path)
		addAlias(path)
		addAlias(stagedFileURI(path))
		if includeEphemeralBasename {
			addAlias(filepath.Base(path))
		}
	}

	for _, supplied := range paths {
		clean := filepath.Clean(strings.TrimSpace(supplied))
		if clean == "." || !filepath.IsAbs(clean) {
			return nil, errors.New("derive private staged prompt input redaction aliases")
		}
		full, err := filepath.Abs(clean)
		if err != nil {
			return nil, errors.New("derive private staged prompt input redaction aliases")
		}
		canonical, err := filepath.EvalSymlinks(full)
		if err != nil || !filepath.IsAbs(canonical) {
			return nil, errors.New("derive private staged prompt input redaction aliases")
		}
		addPath(full, false)
		addPath(canonical, false)
		addPath(filepath.Dir(full), true)
		addPath(filepath.Dir(canonical), true)
		stageFiles[filepath.Clean(full)] = struct{}{}
		stageFiles[filepath.Clean(canonical)] = struct{}{}
		stageDirs[filepath.Clean(filepath.Dir(full))] = struct{}{}
		stageDirs[filepath.Clean(filepath.Dir(canonical))] = struct{}{}
	}

	ordered := make([]string, 0, len(aliases)*2)
	for alias := range aliases {
		ordered = append(ordered, alias)
		// This spelling is used when a path is embedded in a JSON string and a
		// malformed/truncated raw record prevents structured redaction.
		if encoded, err := json.Marshal(alias); err == nil && len(encoded) >= 2 {
			escaped := string(encoded[1 : len(encoded)-1])
			if escaped != alias {
				ordered = append(ordered, escaped)
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	ordered = compactSortedStrings(ordered)
	replacements := make([]string, 0, len(ordered)*2)
	for _, alias := range ordered {
		replacements = append(replacements, alias, privateACPPromptInputRedaction)
	}
	return &acpPromptRedactor{
		aliases:    ordered,
		stageFiles: sortedStringSet(stageFiles),
		stageDirs:  sortedStringSet(stageDirs),
		replacer:   strings.NewReplacer(replacements...),
		foldCase:   acpPromptAliasesCaseInsensitive(),
	}, nil
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (r *acpPromptRedactor) redact(value string) string {
	if r == nil || r.replacer == nil || value == "" {
		return value
	}
	if !r.foldCase {
		return r.replacer.Replace(value)
	}
	var out strings.Builder
	for offset := 0; offset < len(value); {
		matched := 0
		for _, alias := range r.aliases {
			if len(alias) <= matched || offset+len(alias) > len(value) {
				continue
			}
			if acpPromptAliasEqual(value[offset:offset+len(alias)], alias) {
				matched = len(alias)
				break
			}
		}
		if matched > 0 {
			out.WriteString(privateACPPromptInputRedaction)
			offset += matched
			continue
		}
		out.WriteByte(value[offset])
		offset++
	}
	return out.String()
}

// redactStream masks complete aliases and a provisional trailing alias prefix.
// Output callbacks carry the accumulated assistant message, so withholding a
// possible path prefix until the next chunk prevents the first half of a split
// alias from being persisted. If the next chunk proves it was ordinary text,
// the following accumulated callback restores it.
func (r *acpPromptRedactor) redactStream(value string) string {
	return r.redactFragment(value)
}

// redactFragment is used for individual string fields in accumulated raw ACP
// records. ACP may split one logical path across consecutive chunk values, so
// both a trailing alias prefix and a leading alias suffix are removed.
func (r *acpPromptRedactor) redactFragment(value string) string {
	value = r.redact(value)
	if r == nil || value == "" {
		return value
	}
	leading, trailing := 0, 0
	for _, alias := range r.aliases {
		limit := min(len(value), len(alias)-1)
		for size := limit; size > leading && size >= minACPPromptAliasFragmentBytes; size-- {
			if r.hasPrefix(value, alias[len(alias)-size:]) {
				leading = size
				break
			}
		}
		for size := limit; size > trailing && size >= minACPPromptAliasFragmentBytes; size-- {
			if r.hasSuffix(value, alias[:size]) {
				trailing = size
				break
			}
		}
	}
	if leading == 0 && trailing == 0 {
		return value
	}
	if leading+trailing >= len(value) {
		return privateACPPromptInputRedaction
	}
	var out strings.Builder
	if leading > 0 {
		out.WriteString(privateACPPromptInputRedaction)
	}
	out.WriteString(value[leading : len(value)-trailing])
	if trailing > 0 {
		out.WriteString(privateACPPromptInputRedaction)
	}
	return out.String()
}

func (r *acpPromptRedactor) redactActivity(activity Activity) Activity {
	if r == nil {
		return activity
	}
	activity.ID = r.redactFragment(activity.ID)
	activity.Type = r.redact(activity.Type)
	activity.Status = r.redact(activity.Status)
	activity.Kind = r.redact(activity.Kind)
	activity.Title = r.redactFragment(activity.Title)
	activity.Detail = r.redactFragment(activity.Detail)
	activity.ArtifactPreview = r.redactFragment(activity.ArtifactPreview)
	return activity
}

// redactCommands keeps protocol identifiers byte-for-byte stable. A command
// whose name carries a private stage alias is omitted instead of being changed
// into a token the agent never advertised; human-facing metadata can be safely
// redacted before it reaches the durable session projection.
func (r *acpPromptRedactor) redactCommands(commands []agentcontrols.Command) []agentcontrols.Command {
	if r == nil || len(commands) == 0 {
		return commands
	}
	out := make([]agentcontrols.Command, 0, len(commands))
	for _, command := range commands {
		if !r.protocolIdentifiersSafe(command.Name) {
			continue
		}
		command.Description = r.redactFragment(command.Description)
		command.InputHint = r.redactFragment(command.InputHint)
		out = append(out, command)
	}
	return out
}

// redactConfigOptions follows the same boundary for config IDs and selectable
// values. Dropping an affected option is safer than persisting or later sending
// a redacted identifier that does not exist in the ACP peer.
func (r *acpPromptRedactor) redactConfigOptions(options []agentcontrols.ConfigOption) []agentcontrols.ConfigOption {
	if r == nil || len(options) == 0 {
		return options
	}
	out := make([]agentcontrols.ConfigOption, 0, len(options))
	for _, option := range options {
		if !r.protocolIdentifiersSafe(option.ID, option.Category, option.CurrentValue) {
			continue
		}
		safe := true
		for _, item := range option.Options {
			if !r.protocolIdentifiersSafe(item.Value, item.Group) {
				safe = false
				break
			}
		}
		if !safe {
			continue
		}
		option.Name = r.redactFragment(option.Name)
		option.Description = r.redactFragment(option.Description)
		for index := range option.Options {
			option.Options[index].Name = r.redactFragment(option.Options[index].Name)
			option.Options[index].Description = r.redactFragment(option.Options[index].Description)
			option.Options[index].GroupName = r.redactFragment(option.Options[index].GroupName)
		}
		out = append(out, option)
	}
	return out
}

func (r *acpPromptRedactor) protocolIdentifiersSafe(values ...string) bool {
	for _, value := range values {
		if r.redactFragment(value) != value {
			return false
		}
	}
	return true
}

func (r *acpPromptRedactor) hasPrefix(value, prefix string) bool {
	if len(prefix) > len(value) {
		return false
	}
	if !r.foldCase {
		return strings.HasPrefix(value, prefix)
	}
	return acpPromptAliasEqual(value[:len(prefix)], prefix)
}

func (r *acpPromptRedactor) hasSuffix(value, suffix string) bool {
	if len(suffix) > len(value) {
		return false
	}
	if !r.foldCase {
		return strings.HasSuffix(value, suffix)
	}
	return acpPromptAliasEqual(value[len(value)-len(suffix):], suffix)
}

func (r *acpPromptRedactor) redactError(err error) error {
	if err == nil || r == nil {
		return err
	}
	redacted := r.redactFragment(err.Error())
	if redacted == err.Error() {
		return err
	}
	// Do not wrap the original error: its private message would remain reachable
	// to logging or error inspection code through Unwrap.
	return errors.New(redacted)
}

// containsStagePath recognizes the entire body-free namespace of a private
// prompt stage, not only files listed for the agent. This prevents a callback
// miss from falling through to WorkspaceFS when the OS temp directory overlaps
// the workspace.
func (r *acpPromptRedactor) containsStagePath(value string) bool {
	if r == nil {
		return false
	}
	path := cleanACPReadPath(value)
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	for _, file := range r.stageFiles {
		if acpPromptAliasEqual(path, file) {
			return true
		}
	}
	for _, dir := range r.stageDirs {
		if acpPromptPathWithin(path, dir) {
			return true
		}
	}
	return false
}

// redactRaw withholds ACP protocol diagnostics for staged turns. Unlike the
// visible output stream, raw notifications preserve arbitrary chunk boundaries
// and JSON framing; a private path can therefore be split into fragments that
// are not individually identifiable. Withholding is the only fail-closed way
// to guarantee those fragments cannot be recombined from persisted raw data.
func (r *acpPromptRedactor) redactRaw(raw string) string {
	if r == nil || raw == "" {
		return raw
	}
	return privateACPRawOutputWithheld
}

func (r *acpPromptRedactor) redactJSON(raw []byte, fragments bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	safe, err := r.redactJSONValue(value, fragments)
	if err != nil {
		return nil, err
	}
	return json.Marshal(safe)
}

func (r *acpPromptRedactor) redactJSONValue(value any, fragments bool) (any, error) {
	redactString := r.redact
	if fragments {
		redactString = r.redactFragment
	}
	switch typed := value.(type) {
	case nil, bool, json.Number:
		return typed, nil
	case string:
		return redactString(typed), nil
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			redacted, err := r.redactJSONValue(child, fragments)
			if err != nil {
				return nil, err
			}
			out[index] = redacted
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			safeKey := redactString(key)
			if _, exists := out[safeKey]; exists {
				return nil, errors.New("redacted JSON key collision")
			}
			redacted, err := r.redactJSONValue(child, fragments)
			if err != nil {
				return nil, err
			}
			out[safeKey] = redacted
		}
		return out, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

// redactRequestPermission reconstructs a typed ACP request from a fully
// sanitized JSON representation before the ApprovalCoordinator can persist it.
// Protocol identifiers must remain byte-for-byte stable; if redaction would
// change one, Hecate rejects the request instead of recording an unusable or
// privacy-unsafe approval.
func (r *acpPromptRedactor) redactRequestPermission(params acp.RequestPermissionRequest) (acp.RequestPermissionRequest, error) {
	if r == nil {
		return params, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return acp.RequestPermissionRequest{}, errors.New("sanitize ACP permission request for private prompt inputs")
	}
	safeRaw, err := r.redactJSON(raw, true)
	if err != nil {
		return acp.RequestPermissionRequest{}, errors.New("sanitize ACP permission request for private prompt inputs")
	}
	var safe acp.RequestPermissionRequest
	if err := json.Unmarshal(safeRaw, &safe); err != nil {
		return acp.RequestPermissionRequest{}, errors.New("sanitize ACP permission request for private prompt inputs")
	}
	if err := safe.Validate(); err != nil {
		return acp.RequestPermissionRequest{}, errors.New("sanitize ACP permission request for private prompt inputs")
	}
	if err := safe.ToolCall.Validate(); err != nil {
		return acp.RequestPermissionRequest{}, errors.New("sanitize ACP permission request for private prompt inputs")
	}
	if !sameACPPermissionProtocolFields(params, safe) {
		return acp.RequestPermissionRequest{}, errors.New("reject ACP permission request containing private prompt input in protocol identifiers")
	}
	return safe, nil
}

func sameACPPermissionProtocolFields(left, right acp.RequestPermissionRequest) bool {
	if left.SessionId != right.SessionId ||
		left.ToolCall.ToolCallId != right.ToolCall.ToolCallId ||
		!sameACPToolKind(left.ToolCall.Kind, right.ToolCall.Kind) ||
		!sameACPToolStatus(left.ToolCall.Status, right.ToolCall.Status) ||
		len(left.Options) != len(right.Options) {
		return false
	}
	for index := range left.Options {
		if left.Options[index].OptionId != right.Options[index].OptionId ||
			left.Options[index].Kind != right.Options[index].Kind {
			return false
		}
	}
	return true
}

func sameACPToolKind(left, right *acp.ToolKind) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameACPToolStatus(left, right *acp.ToolCallStatus) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

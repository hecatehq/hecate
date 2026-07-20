package codeintel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	lspInitializeID = 1
	lspQueryID      = 2
	lspShutdownID   = 3
	lspStderrBytes  = 32 * 1024
)

var errDiagnosticsUnavailable = errors.New("diagnostics are unavailable: the language server did not publish diagnostics for the requested file")

type initializeResult struct {
	Capabilities struct {
		PositionEncoding   positionEncoding `json:"positionEncoding"`
		DiagnosticProvider json.RawMessage  `json:"diagnosticProvider"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name string `json:"name"`
	} `json:"serverInfo"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnostic struct {
	Range    lspRange        `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

type lspQueryState struct {
	rootURI     string
	fsys        *workspacefs.FS
	published   map[string][]lspDiagnostic
	omitted     int
	requestFile *sourceFile
}

type lspSession struct {
	cmd      *exec.Cmd
	tree     *lspProcessTree
	stdin    io.WriteCloser
	conn     *lspConn
	frames   chan jsonRPCEnvelope
	readErr  chan error
	done     chan struct{}
	doneOnce sync.Once
	reaped   chan struct{}
	stderr   *tailBuffer
}

func (s *Service) queryLSP(ctx context.Context, fsys *workspacefs.FS, request Request) (Result, error) {
	if request.Operation == OpWorkspaceSymbols {
		if request.Query == "" {
			return Result{}, fmt.Errorf("query is required for workspace_symbols")
		}
		if request.Language == "typescript" && request.Path == "" {
			return Result{}, fmt.Errorf("path is required for TypeScript workspace_symbols so the language server can load the project")
		}
	} else if request.Path == "" {
		return Result{}, fmt.Errorf("path is required for %s", request.Operation)
	}
	if request.Operation == OpDefinition || request.Operation == OpReferences || request.Operation == OpHover {
		if request.Line <= 0 || request.Column <= 0 {
			return Result{}, fmt.Errorf("line and column are required positive 1-based values for %s", request.Operation)
		}
	}

	cache := newSourceCache(fsys)
	var document *sourceFile
	if request.Path != "" {
		opened, openErr := cache.openRelative(request.Path)
		if openErr != nil {
			return Result{}, openErr
		}
		document = opened
	}
	spec, language, err := s.selectServer(ctx, fsys, request)
	if err != nil {
		return Result{}, err
	}
	argv := append([]string{spec.command}, spec.args...)
	argv = sandbox.WrapReadOnlyArgv(argv, fsys.Root(), false)
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("language server command is empty")
	}
	session, err := s.startLSPSession(ctx, fsys.Root(), providerProcessEnv(ctx, spec.provider), argv)
	if err != nil {
		return Result{}, fmt.Errorf("failed to start %s language server", filepath.Base(spec.command))
	}
	defer session.close()

	state := &lspQueryState{
		rootURI:     pathToFileURI(fsys.Root()),
		fsys:        fsys,
		published:   make(map[string][]lspDiagnostic),
		requestFile: document,
	}
	initialization, err := initializeLSP(ctx, session, state, fsys.Root())
	if err != nil {
		return Result{}, session.withStderr(err)
	}
	encoding := initialization.Capabilities.PositionEncoding
	if encoding == "" {
		encoding = positionUTF16
	}
	if encoding != positionUTF8 && encoding != positionUTF16 && encoding != positionUTF32 {
		return Result{}, fmt.Errorf("%s selected an unsupported position encoding", filepath.Base(spec.command))
	}
	if err := session.conn.notify("initialized", map[string]any{}); err != nil {
		return Result{}, err
	}
	if document != nil {
		if err := session.conn.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        document.uri,
				"languageId": languageID(language, request.Path),
				"version":    1,
				"text":       string(document.data),
			},
		}); err != nil {
			return Result{}, err
		}
	}

	result := Result{Operation: request.Operation, Provider: filepath.Base(spec.command)}
	if request.Operation == OpDiagnostics && !supportsPullDiagnostics(initialization.Capabilities.DiagnosticProvider) {
		if err := s.settleDiagnostics(ctx, session, state); err != nil {
			if errors.Is(err, errDiagnosticsUnavailable) {
				return Result{}, err
			}
			return Result{}, session.withStderr(err)
		}
		result.Items, result.OmittedExternal, result.Truncated = normalizePublishedDiagnostics(cache, document, state.published[document.uri], encoding, request.MaxResults)
	} else {
		method, params, err := requestLSPParams(request, document, encoding)
		if err != nil {
			return Result{}, err
		}
		if err := session.conn.request(lspQueryID, method, params); err != nil {
			return Result{}, err
		}
		response, err := awaitLSPResponse(ctx, session, state, lspQueryID)
		if err != nil {
			return Result{}, session.withStderr(err)
		}
		result, err = normalizeLSPResult(request, result, cache, document, encoding, response.Result, state)
		if err != nil {
			return Result{}, err
		}
	}

	shutdownLSP(session, state)
	return result, nil
}

func (s *Service) startLSPSession(ctx context.Context, workspace string, env, argv []string) (*lspSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Waiting is deliberately deferred until close: on Unix the process-group
	// leader must remain unreaped while Hecate signals its group, otherwise its
	// numeric PID/PGID could be reused for an unrelated process. CommandContext
	// still provides asynchronous cancellation for a blocked pipe write; its
	// Cancel callback kills the owned tree but does not reap the leader.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workspace
	cmd.Env = env
	cmd.WaitDelay = time.Second
	tree, err := prepareLSPProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("prepare process supervision: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		tree.close()
		return nil, fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		tree.close()
		return nil, fmt.Errorf("open stdout: %w", err)
	}
	stderr := newTailBuffer(lspStderrBytes)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		tree.close()
		return nil, err
	}
	if err := tree.attach(cmd); err != nil {
		_ = tree.forceKill(cmd)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		tree.close()
		return nil, fmt.Errorf("attach process supervision: %w", err)
	}
	session := &lspSession{
		cmd:     cmd,
		tree:    tree,
		stdin:   stdin,
		conn:    newLSPConn(stdout, stdin, s.maxMessageBytes, s.maxTotalBytes),
		frames:  make(chan jsonRPCEnvelope, 32),
		readErr: make(chan error, 1),
		done:    make(chan struct{}),
		reaped:  make(chan struct{}),
		stderr:  stderr,
	}
	go session.readLoop()
	return session, nil
}

func (s *lspSession) readLoop() {
	for {
		frame, err := s.conn.read()
		if err != nil {
			select {
			case s.readErr <- err:
			case <-s.done:
			}
			return
		}
		select {
		case s.frames <- frame:
		case <-s.done:
			return
		}
	}
}

func (s *lspSession) next(ctx context.Context) (jsonRPCEnvelope, error) {
	select {
	case <-ctx.Done():
		return jsonRPCEnvelope{}, ctx.Err()
	case frame := <-s.frames:
		return frame, nil
	case err := <-s.readErr:
		return jsonRPCEnvelope{}, err
	}
}

func (s *lspSession) close() {
	s.doneOnce.Do(func() {
		close(s.done)
		_ = s.stdin.Close()
		// Signal the owned group / Job Object before Wait can reap the leader.
		// This both removes descendants and makes numeric PGID reuse impossible.
		_ = s.tree.forceKill(s.cmd)
		go func() {
			_ = s.cmd.Wait()
			s.tree.close()
			close(s.reaped)
		}()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-s.reaped:
	case <-timer.C:
	}
}

func (s *lspSession) withStderr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("language server exited before completing the request")
	}
	return fmt.Errorf("language server protocol failed")
}

func initializeLSP(ctx context.Context, session *lspSession, state *lspQueryState, workspace string) (initializeResult, error) {
	params := map[string]any{
		"processId": nil,
		"clientInfo": map[string]any{
			"name":    "hecate",
			"version": "code-intelligence-v1",
		},
		"rootUri": state.rootURI,
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    false,
			},
			"textDocument": map[string]any{
				"definition":     map[string]any{"linkSupport": true},
				"references":     map[string]any{},
				"hover":          map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"diagnostic":     map[string]any{},
			},
		},
		"workspaceFolders": []map[string]string{{
			"uri":  state.rootURI,
			"name": filepath.Base(workspace),
		}},
	}
	if err := session.conn.request(lspInitializeID, "initialize", params); err != nil {
		return initializeResult{}, err
	}
	response, err := awaitLSPResponse(ctx, session, state, lspInitializeID)
	if err != nil {
		return initializeResult{}, fmt.Errorf("initialize language server: %w", err)
	}
	var result initializeResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return result, fmt.Errorf("decode initialize result: %w", err)
	}
	return result, nil
}

func awaitLSPResponse(ctx context.Context, session *lspSession, state *lspQueryState, id int) (jsonRPCEnvelope, error) {
	for {
		frame, err := session.next(ctx)
		if err != nil {
			return jsonRPCEnvelope{}, err
		}
		if frame.Method != "" {
			if _, err := handleServerMessage(session.conn, state, frame); err != nil {
				return jsonRPCEnvelope{}, err
			}
			continue
		}
		responseID, err := parseJSONRPCID(frame.ID)
		if err != nil || responseID != id {
			continue
		}
		if frame.Error != nil {
			return frame, fmt.Errorf("language server rejected the request with JSON-RPC code %d", frame.Error.Code)
		}
		return frame, nil
	}
}

func parseJSONRPCID(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, fmt.Errorf("missing JSON-RPC id")
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return strconv.Atoi(text)
}

func handleServerMessage(conn *lspConn, state *lspQueryState, frame jsonRPCEnvelope) (bool, error) {
	if frame.Method == "textDocument/publishDiagnostics" {
		var params publishDiagnosticsParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return false, fmt.Errorf("language server published malformed diagnostics")
		}
		if state.requestFile != nil && state.fsys != nil {
			relative, err := workspaceRelativeURI(state.fsys, params.URI)
			if err == nil && samePath(filepath.Join(state.fsys.Root(), relative), state.requestFile.absolute) {
				state.published[state.requestFile.uri] = params.Diagnostics
				return true, nil
			}
		}
		if state.requestFile != nil && params.URI == state.requestFile.uri {
			// Retain a conservative exact-match fallback for synthetic states
			// used outside a fully initialized workspace filesystem.
			state.published[state.requestFile.uri] = params.Diagnostics
			return true, nil
		}
		return false, nil
	}
	if len(frame.ID) == 0 || string(frame.ID) == "null" {
		return false, nil
	}
	switch frame.Method {
	case "workspace/configuration":
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(frame.Params, &params)
		values := make([]any, len(params.Items))
		return false, conn.respond(frame.ID, values)
	case "workspace/workspaceFolders":
		return false, conn.respond(frame.ID, []map[string]string{{"uri": state.rootURI, "name": filepath.Base(strings.TrimPrefix(state.rootURI, "file://"))}})
	case "workspace/applyEdit":
		return false, conn.respond(frame.ID, map[string]any{"applied": false, "failureReason": "Hecate code intelligence is read-only"})
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create",
		"workspace/semanticTokens/refresh", "workspace/inlayHint/refresh", "workspace/diagnostic/refresh",
		"workspace/codeLens/refresh", "window/showMessageRequest":
		return false, conn.respond(frame.ID, nil)
	default:
		return false, conn.respondError(frame.ID, -32601, "method not supported by read-only Hecate client")
	}
}

func (s *Service) settleDiagnostics(ctx context.Context, session *lspSession, state *lspQueryState) error {
	initialWait := s.diagnosticInitialWait
	if initialWait <= 0 {
		initialWait = defaultDiagnosticInitialWait
	}
	initial := time.NewTimer(initialWait)
	initialC := initial.C
	defer initial.Stop()

	duration := s.diagnosticSettle
	var debounce *time.Timer
	var debounceC <-chan time.Time
	var maxWait *time.Timer
	var maxWaitC <-chan time.Time
	resetDebounce := func() {
		if duration <= 0 {
			return
		}
		if debounce == nil {
			debounce = time.NewTimer(duration)
		} else {
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(duration)
		}
		debounceC = debounce.C
	}
	if state.requestFile != nil {
		if _, alreadyPublished := state.published[state.requestFile.uri]; alreadyPublished {
			if !initial.Stop() {
				select {
				case <-initial.C:
				default:
				}
			}
			initialC = nil
			if duration <= 0 {
				return nil
			}
			maxWait = time.NewTimer(duration * 4)
			maxWaitC = maxWait.C
			resetDebounce()
		}
	}
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
		if maxWait != nil {
			maxWait.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-initialC:
			return errDiagnosticsUnavailable
		case <-maxWaitC:
			return nil
		case <-debounceC:
			return nil
		case frame := <-session.frames:
			if frame.Method != "" {
				published, err := handleServerMessage(session.conn, state, frame)
				if err != nil {
					return err
				}
				if published {
					if initialC != nil {
						if !initial.Stop() {
							select {
							case <-initial.C:
							default:
							}
						}
						initialC = nil
						if duration <= 0 {
							return nil
						}
						maxWait = time.NewTimer(duration * 4)
						maxWaitC = maxWait.C
					}
					resetDebounce()
				}
			}
		case err := <-session.readErr:
			return err
		}
	}
}

func supportsPullDiagnostics(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "false"
}

func requestLSPParams(request Request, file *sourceFile, encoding positionEncoding) (string, any, error) {
	textDocument := map[string]any{}
	if file != nil {
		textDocument["uri"] = file.uri
	}
	switch request.Operation {
	case OpDefinition, OpReferences, OpHover:
		position, err := file.requestPosition(request.Line, request.Column, encoding)
		if err != nil {
			return "", nil, err
		}
		params := map[string]any{"textDocument": textDocument, "position": position}
		switch request.Operation {
		case OpDefinition:
			return "textDocument/definition", params, nil
		case OpReferences:
			params["context"] = map[string]any{"includeDeclaration": true}
			return "textDocument/references", params, nil
		default:
			return "textDocument/hover", params, nil
		}
	case OpDocumentSymbols:
		return "textDocument/documentSymbol", map[string]any{"textDocument": textDocument}, nil
	case OpWorkspaceSymbols:
		return "workspace/symbol", map[string]any{"query": request.Query}, nil
	case OpDiagnostics:
		return "textDocument/diagnostic", map[string]any{"textDocument": textDocument}, nil
	default:
		return "", nil, fmt.Errorf("unsupported LSP operation %q", request.Operation)
	}
}

func shutdownLSP(session *lspSession, state *lspQueryState) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := session.conn.request(lspShutdownID, "shutdown", nil); err == nil {
		_, _ = awaitLSPResponse(shutdownCtx, session, state, lspShutdownID)
	}
	_ = session.conn.notify("exit", nil)
}

func languageID(language, path string) string {
	if language != "typescript" {
		return language
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	default:
		return "typescript"
	}
}

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, value...)
	if len(b.data) > b.limit {
		b.data = append(b.data[:0], b.data[len(b.data)-b.limit:]...)
	}
	return len(value), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(string(b.data), "")
}

func concise(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(value, "")), " ")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "..."
}

func normalizeLSPResult(request Request, result Result, cache *sourceCache, document *sourceFile, encoding positionEncoding, raw json.RawMessage, state *lspQueryState) (Result, error) {
	var (
		err       error
		truncated bool
	)
	switch request.Operation {
	case OpDefinition:
		result.Items, result.OmittedExternal, truncated, err = normalizeDefinition(cache, encoding, raw, request.MaxResults)
	case OpReferences:
		result.Items, result.OmittedExternal, truncated, err = normalizeLocations(cache, encoding, raw, request.MaxResults)
	case OpHover:
		result.Items, err = normalizeHover(document, encoding, raw)
	case OpDocumentSymbols:
		result.Items, result.OmittedExternal, truncated, err = normalizeDocumentSymbols(cache, document, encoding, raw, request.MaxResults)
	case OpWorkspaceSymbols:
		result.Items, result.OmittedExternal, truncated, err = normalizeWorkspaceSymbols(cache, encoding, raw, request.MaxResults)
	case OpDiagnostics:
		result.Items, result.OmittedExternal, truncated, err = normalizePullDiagnostics(cache, document, encoding, raw, request.MaxResults)
	}
	result.OmittedExternal += state.omitted
	if err != nil {
		return result, err
	}
	result.Truncated = truncated
	return result, nil
}

func normalizeDefinition(cache *sourceCache, encoding positionEncoding, raw json.RawMessage, limit int) ([]Item, int, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, 0, false, nil
	}
	var links []lspLocationLink
	if definitionResultUsesLocationLinks(raw, trimmed) {
		if trimmed[0] == '{' {
			var link lspLocationLink
			if err := json.Unmarshal(raw, &link); err != nil {
				return nil, 0, false, fmt.Errorf("decode definition link: %w", err)
			}
			links = []lspLocationLink{link}
		} else if err := json.Unmarshal(raw, &links); err != nil {
			return nil, 0, false, fmt.Errorf("decode definition links: %w", err)
		}
		records, truncated := normalizationRecordLimit(len(links), limit)
		items := make([]Item, 0, records)
		omitted := 0
		for _, link := range links[:records] {
			item, err := normalizeLocation(cache, encoding, lspLocation{URI: link.TargetURI, Range: link.TargetSelectionRange})
			if err != nil {
				omitted++
				continue
			}
			items = append(items, item)
		}
		return items, omitted, truncated, nil
	}
	return normalizeLocations(cache, encoding, raw, limit)
}

func definitionResultUsesLocationLinks(raw json.RawMessage, trimmed string) bool {
	var first json.RawMessage
	if trimmed[0] == '{' {
		first = raw
	} else {
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil || len(entries) == 0 {
			return false
		}
		first = entries[0]
	}
	var probe struct {
		TargetURI json.RawMessage `json:"targetUri"`
	}
	return json.Unmarshal(first, &probe) == nil && len(probe.TargetURI) > 0
}

func normalizeLocations(cache *sourceCache, encoding positionEncoding, raw json.RawMessage, limit int) ([]Item, int, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, 0, false, nil
	}
	var locations []lspLocation
	if trimmed[0] == '{' {
		var location lspLocation
		if err := json.Unmarshal(raw, &location); err != nil {
			return nil, 0, false, fmt.Errorf("decode LSP location: %w", err)
		}
		locations = []lspLocation{location}
	} else if err := json.Unmarshal(raw, &locations); err != nil {
		return nil, 0, false, fmt.Errorf("decode LSP locations: %w", err)
	}
	records, truncated := normalizationRecordLimit(len(locations), limit)
	items := make([]Item, 0, records)
	omitted := 0
	for _, location := range locations[:records] {
		item, err := normalizeLocation(cache, encoding, location)
		if err != nil {
			omitted++
			continue
		}
		items = append(items, item)
	}
	return items, omitted, truncated, nil
}

func normalizeLocation(cache *sourceCache, encoding positionEncoding, location lspLocation) (Item, error) {
	file, err := cache.openURI(location.URI)
	if err != nil {
		return Item{}, err
	}
	return normalizedRange(file, location.Range, encoding)
}

func normalizeHover(document *sourceFile, encoding positionEncoding, raw json.RawMessage) ([]Item, error) {
	if strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
		Range    *lspRange       `json:"range,omitempty"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return nil, fmt.Errorf("decode hover: %w", err)
	}
	detail := hoverText(hover.Contents)
	if detail == "" {
		return nil, nil
	}
	item := Item{Path: document.relative, Detail: concise(detail, 4096)}
	if hover.Range != nil {
		ranged, err := normalizedRange(document, *hover.Range, encoding)
		if err != nil {
			return nil, err
		}
		item.Path = ranged.Path
		item.StartLine, item.StartColumn = ranged.StartLine, ranged.StartColumn
		item.EndLine, item.EndColumn = ranged.EndLine, ranged.EndColumn
		item.Preview = ranged.Preview
	}
	return []Item{item}, nil
}

func hoverText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var markup struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &markup) == nil && markup.Value != "" {
		return markup.Value
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := hoverText(part); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, "\n")
}

func normalizeDocumentSymbols(cache *sourceCache, document *sourceFile, encoding positionEncoding, raw json.RawMessage, limit int) ([]Item, int, bool, error) {
	var symbols []json.RawMessage
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, 0, false, nil
	}
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return nil, 0, false, fmt.Errorf("decode document symbols: %w", err)
	}
	items := make([]Item, 0, min(len(symbols), limit))
	omitted := 0
	attempted := 0
	type pendingSymbol struct {
		raw   json.RawMessage
		depth int
	}
	pending := make([]pendingSymbol, 0, len(symbols))
	for i := len(symbols) - 1; i >= 0; i-- {
		pending = append(pending, pendingSymbol{raw: symbols[i]})
	}
	for len(pending) > 0 && attempted < limit {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		attempted++
		if entry.depth > 64 {
			omitted++
			continue
		}
		var probe struct {
			Name           string            `json:"name"`
			Detail         string            `json:"detail,omitempty"`
			Kind           int               `json:"kind"`
			Range          lspRange          `json:"range"`
			SelectionRange lspRange          `json:"selectionRange"`
			Children       []json.RawMessage `json:"children,omitempty"`
			Location       *lspLocation      `json:"location,omitempty"`
		}
		if err := json.Unmarshal(entry.raw, &probe); err != nil {
			return nil, omitted, len(pending) > 0, fmt.Errorf("decode document symbol: %w", err)
		}
		var item Item
		var err error
		if probe.Location != nil {
			item, err = normalizeLocation(cache, encoding, *probe.Location)
		} else {
			item, err = normalizedRange(document, probe.SelectionRange, encoding)
		}
		if err != nil {
			omitted++
		} else {
			item.Name, item.Detail, item.Kind = probe.Name, concise(probe.Detail, 512), symbolKind(probe.Kind)
			items = append(items, item)
		}
		for i := len(probe.Children) - 1; i >= 0; i-- {
			pending = append(pending, pendingSymbol{raw: probe.Children[i], depth: entry.depth + 1})
		}
	}
	return items, omitted, len(pending) > 0, nil
}

func normalizeWorkspaceSymbols(cache *sourceCache, encoding positionEncoding, raw json.RawMessage, limit int) ([]Item, int, bool, error) {
	var symbols []struct {
		Name     string          `json:"name"`
		Kind     int             `json:"kind"`
		Location json.RawMessage `json:"location"`
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, 0, false, nil
	}
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return nil, 0, false, fmt.Errorf("decode workspace symbols: %w", err)
	}
	records, truncated := normalizationRecordLimit(len(symbols), limit)
	items := make([]Item, 0, records)
	omitted := 0
	for _, symbol := range symbols[:records] {
		var location lspLocation
		if err := json.Unmarshal(symbol.Location, &location); err != nil || location.URI == "" {
			omitted++
			continue
		}
		item, err := normalizeLocation(cache, encoding, location)
		if err != nil {
			omitted++
			continue
		}
		item.Name, item.Kind = symbol.Name, symbolKind(symbol.Kind)
		items = append(items, item)
	}
	return items, omitted, truncated, nil
}

func normalizePullDiagnostics(cache *sourceCache, document *sourceFile, encoding positionEncoding, raw json.RawMessage, limit int) ([]Item, int, bool, error) {
	var report struct {
		Kind  string          `json:"kind"`
		Items []lspDiagnostic `json:"items"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, 0, false, fmt.Errorf("decode diagnostics: %w", err)
	}
	return diagnosticsForFile(cache, document.uri, report.Items, encoding, limit)
}

func normalizePublishedDiagnostics(cache *sourceCache, document *sourceFile, diagnostics []lspDiagnostic, encoding positionEncoding, limit int) ([]Item, int, bool) {
	items, omitted, truncated, err := diagnosticsForFile(cache, document.uri, diagnostics, encoding, limit)
	if err != nil {
		return nil, omitted, truncated
	}
	return items, omitted, truncated
}

func diagnosticsForFile(cache *sourceCache, uri string, diagnostics []lspDiagnostic, encoding positionEncoding, limit int) ([]Item, int, bool, error) {
	records, truncated := normalizationRecordLimit(len(diagnostics), limit)
	if records == 0 {
		return nil, 0, truncated, nil
	}
	file, err := cache.openURI(uri)
	if err != nil {
		return nil, records, truncated, err
	}
	items := make([]Item, 0, records)
	omitted := 0
	for _, diagnostic := range diagnostics[:records] {
		item, err := normalizedRange(file, diagnostic.Range, encoding)
		if err != nil {
			omitted++
			continue
		}
		item.Message = concise(diagnostic.Message, 2048)
		item.Severity = diagnosticSeverity(diagnostic.Severity)
		item.Source = concise(diagnostic.Source, 128)
		items = append(items, item)
	}
	return items, omitted, truncated, nil
}

// normalizationRecordLimit makes max_results a work budget as well as an
// output bound. Provider records that are malformed, unsafe, or otherwise
// omitted still consume the budget so an adversarial response cannot force
// Hecate to open an unbounded number of workspace files while producing few or
// no normalized results.
func normalizationRecordLimit(total, limit int) (records int, truncated bool) {
	if limit <= 0 {
		return 0, total > 0
	}
	if total <= limit {
		return total, false
	}
	return limit, true
}

func diagnosticSeverity(value int) string {
	switch value {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "information"
	case 4:
		return "hint"
	default:
		return "unknown"
	}
}

func symbolKind(value int) string {
	names := [...]string{
		"", "file", "module", "namespace", "package", "class", "method", "property", "field", "constructor",
		"enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array", "object",
		"key", "null", "enum_member", "struct", "event", "operator", "type_parameter",
	}
	if value > 0 && value < len(names) {
		return names[value]
	}
	return "unknown"
}

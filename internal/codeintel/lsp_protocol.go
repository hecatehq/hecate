package codeintel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

const (
	maxLSPHeaderBytes = 8 * 1024
	maxLSPMessages    = 512
)

type jsonRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type lspConn struct {
	reader          *bufio.Reader
	writer          io.Writer
	writeMu         sync.Mutex
	maxMessageBytes int64
	maxTotalBytes   int64
	readBytes       int64
	readMessages    int
}

func newLSPConn(reader io.Reader, writer io.Writer, maxMessageBytes, maxTotalBytes int64) *lspConn {
	return &lspConn{
		reader:          bufio.NewReaderSize(reader, 16*1024),
		writer:          writer,
		maxMessageBytes: maxMessageBytes,
		maxTotalBytes:   maxTotalBytes,
	}
}

func (c *lspConn) read() (jsonRPCEnvelope, error) {
	var envelope jsonRPCEnvelope
	contentLength := int64(-1)
	headerBytes := 0
	for {
		line, err := c.readHeaderLine(maxLSPHeaderBytes - headerBytes)
		if err != nil {
			return envelope, err
		}
		headerBytes += len(line)
		if headerBytes > maxLSPHeaderBytes {
			return envelope, fmt.Errorf("LSP frame headers exceed %d bytes", maxLSPHeaderBytes)
		}
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return envelope, fmt.Errorf("malformed LSP header")
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return envelope, fmt.Errorf("duplicate LSP Content-Length header")
		}
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if parseErr != nil || parsed < 0 {
			return envelope, fmt.Errorf("invalid LSP Content-Length")
		}
		contentLength = parsed
	}
	if contentLength < 0 {
		return envelope, fmt.Errorf("LSP frame is missing Content-Length")
	}
	if c.maxMessageBytes > 0 && contentLength > c.maxMessageBytes {
		return envelope, fmt.Errorf("LSP frame exceeds %d-byte message limit", c.maxMessageBytes)
	}
	if c.maxTotalBytes > 0 && c.readBytes+contentLength > c.maxTotalBytes {
		return envelope, fmt.Errorf("LSP output exceeds %d-byte query limit", c.maxTotalBytes)
	}
	if c.readMessages >= maxLSPMessages {
		return envelope, fmt.Errorf("LSP output exceeds %d-message query limit", maxLSPMessages)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return envelope, fmt.Errorf("read LSP body: %w", err)
	}
	c.readBytes += contentLength
	c.readMessages++
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("decode LSP message: %w", err)
	}
	if envelope.JSONRPC != "2.0" {
		return envelope, fmt.Errorf("invalid LSP JSON-RPC version")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return envelope, fmt.Errorf("LSP message has trailing JSON data")
	}
	return envelope, nil
}

func (c *lspConn) readHeaderLine(remaining int) (string, error) {
	if remaining <= 0 {
		return "", fmt.Errorf("LSP frame headers exceed %d bytes", maxLSPHeaderBytes)
	}
	var line []byte
	for {
		part, err := c.reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > remaining {
			return "", fmt.Errorf("LSP frame headers exceed %d bytes", maxLSPHeaderBytes)
		}
		switch {
		case err == nil:
			return string(line), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return "", fmt.Errorf("unterminated LSP header")
		default:
			return "", err
		}
	}
}

func (c *lspConn) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode LSP message: %w", err)
	}
	if c.maxMessageBytes > 0 && int64(len(payload)) > c.maxMessageBytes {
		return fmt.Errorf("outbound LSP frame exceeds %d-byte message limit", c.maxMessageBytes)
	}
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload)))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.writer.Write(header); err != nil {
		return fmt.Errorf("write LSP header: %w", err)
	}
	if _, err := c.writer.Write(payload); err != nil {
		return fmt.Errorf("write LSP body: %w", err)
	}
	return nil
}

func (c *lspConn) request(id int, method string, params any) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
}

func (c *lspConn) notify(method string, params any) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *lspConn) respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (c *lspConn) respondError(id json.RawMessage, code int, message string) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

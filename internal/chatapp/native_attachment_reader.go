package chatapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrNativeAttachmentUnavailable = errors.New("attachment is unavailable for this input")
	ErrNativeAttachmentReadBounds  = errors.New("attachment read requires a valid UTF-8 byte offset and byte limit")
	ErrNativeAttachmentSearch      = errors.New("attachment search requires a bounded UTF-8 literal query, byte offset, and match limit")
)

// NativeAttachmentInput is an execution-local ownership fence, not a path or
// authority to read arbitrary attachments from the owning conversation.
type NativeAttachmentInput struct {
	SessionID        string
	MessageID        string
	TaskID           string
	Provider         string
	ProviderInstance types.ProviderInstanceIdentity
	Metadata         chat.MessageAttachment
}

func (app *Application) validateNativeAttachmentInput(ctx context.Context, input NativeAttachmentInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if input.SessionID == "" || input.MessageID == "" || input.TaskID == "" || input.Provider == "" || !input.ProviderInstance.Valid() || input.Metadata.ID == "" ||
		input.Metadata.MediaType != "text/plain" || input.Metadata.SizeBytes <= 0 || input.Metadata.SizeBytes > MaxNativeTextAttachmentBytes {
		return ErrNativeAttachmentUnavailable
	}
	result, err := app.GetSession(ctx, input.SessionID)
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil || result.Session.ID != input.SessionID ||
		(result.Session.AgentID != "" && result.Session.AgentID != chat.DefaultAgentID) {
		return ErrNativeAttachmentUnavailable
	}
	for _, message := range result.Session.Messages {
		if message.ID != input.MessageID {
			continue
		}
		if message.Role != "user" || !message.ToolsEnabled || message.ExecutionMode != chat.ExecutionModeHecateTask ||
			(message.TaskID != "" && message.TaskID != input.TaskID) ||
			(strings.TrimSpace(message.Provider) != "" && strings.TrimSpace(message.Provider) != input.Provider) ||
			(message.ProviderInstance.Valid() && message.ProviderInstance != input.ProviderInstance) {
			return ErrNativeAttachmentUnavailable
		}
		for _, metadata := range message.Attachments {
			if metadata == input.Metadata {
				return nil
			}
		}
		break
	}
	return ErrNativeAttachmentUnavailable
}

// LoadNativeAttachment rechecks the durable message and its claim before every
// transient body read. The caller owns process admission and a session lifecycle
// operation, and separately revalidates the live provider registry generation.
func (app *Application) LoadNativeAttachment(ctx context.Context, input NativeAttachmentInput) ([]byte, error) {
	if err := app.validateNativeAttachmentInput(ctx, input); err != nil {
		return nil, err
	}
	if err := app.ResolveAttachmentClaim(ctx, chatattachments.ClaimRef{
		SessionID: input.SessionID, MessageID: input.MessageID, AttachmentIDs: []string{input.Metadata.ID},
	}, chatattachments.ClaimLinked); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, ErrNativeAttachmentUnavailable
	}
	stored, err := app.GetAttachment(ctx, AttachmentCommand{SessionID: input.SessionID, AttachmentID: input.Metadata.ID})
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, ErrNativeAttachmentUnavailable
	}
	metadata := input.Metadata
	if stored.SessionID != input.SessionID || stored.ID != metadata.ID || stored.Filename != metadata.Filename ||
		stored.MediaType != metadata.MediaType || stored.SizeBytes != metadata.SizeBytes || stored.SHA256 != metadata.SHA256 ||
		!stored.CreatedAt.Equal(metadata.CreatedAt) || int64(len(stored.Data)) != metadata.SizeBytes {
		return nil, ErrNativeAttachmentUnavailable
	}
	digest := sha256.Sum256(stored.Data)
	if hex.EncodeToString(digest[:]) != metadata.SHA256 || ValidateNativeTextAttachment(stored.Data, stored.MediaType) != nil {
		return nil, ErrNativeAttachmentUnavailable
	}
	if err := app.validateNativeAttachmentInput(ctx, input); err != nil {
		return nil, err
	}
	return stored.Data, nil
}

type NativeAttachmentPage struct {
	Offset     int64
	NextOffset int64
	EOF        bool
	Text       string
}

// ReadNativeAttachmentText keeps byte offsets exact: offsets inside a rune are
// rejected, and the end is rounded down rather than splitting a UTF-8 sequence.
func ReadNativeAttachmentText(data []byte, offset int64, maxBytes int) (NativeAttachmentPage, error) {
	if !nativeAttachmentOffsetValid(data, offset) || maxBytes < 1 || maxBytes > 32<<10 {
		return NativeAttachmentPage{}, ErrNativeAttachmentReadBounds
	}
	start := int(offset)
	end := start + min(maxBytes, len(data)-start)
	for end < len(data) && end > start && !utf8.RuneStart(data[end]) {
		end--
	}
	if end == start && start < len(data) {
		return NativeAttachmentPage{}, ErrNativeAttachmentReadBounds
	}
	return NativeAttachmentPage{Offset: offset, NextOffset: int64(end), EOF: end == len(data), Text: string(data[start:end])}, nil
}

type NativeAttachmentMatch struct {
	Offset int64
	Text   string
}

type NativeAttachmentSearchResult struct {
	Matches    []NativeAttachmentMatch
	NextOffset int64
	EOF        bool
}

// SearchNativeAttachmentText is literal, case-sensitive, and non-overlapping.
// Context is a bounded excerpt, never an unbounded matching source line.
func SearchNativeAttachmentText(data []byte, query string, offset int64, maxMatches int) (NativeAttachmentSearchResult, error) {
	if !nativeAttachmentOffsetValid(data, offset) || query == "" || len(query) > 256 || !utf8.ValidString(query) || maxMatches < 1 || maxMatches > 20 {
		return NativeAttachmentSearchResult{}, ErrNativeAttachmentSearch
	}
	result := NativeAttachmentSearchResult{Matches: []NativeAttachmentMatch{}, NextOffset: offset}
	position := int(offset)
	for len(result.Matches) < maxMatches {
		index := bytes.Index(data[position:], []byte(query))
		if index < 0 {
			result.NextOffset, result.EOF = int64(len(data)), true
			return result, nil
		}
		match := position + index
		start, end := max(0, match-128), min(len(data), match+len(query)+128)
		for start < match && !utf8.RuneStart(data[start]) {
			start++
		}
		for end < len(data) && !utf8.RuneStart(data[end]) {
			end--
		}
		result.Matches = append(result.Matches, NativeAttachmentMatch{Offset: int64(match), Text: string(data[start:end])})
		position = match + len(query)
		result.NextOffset, result.EOF = int64(position), position == len(data)
		if result.EOF {
			break
		}
	}
	return result, nil
}

func nativeAttachmentOffsetValid(data []byte, offset int64) bool {
	return offset >= 0 && offset <= int64(len(data)) && (offset == int64(len(data)) || utf8.RuneStart(data[int(offset)]))
}

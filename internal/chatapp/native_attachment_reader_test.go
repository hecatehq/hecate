package chatapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestNativeAttachment_ReadUTF8PagesAndBounds(t *testing.T) {
	data := []byte("aé😀z")
	page, err := ReadNativeAttachmentText(data, 0, 4)
	if err != nil || page.Text != "aé" || page.NextOffset != 3 || page.EOF {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	page, err = ReadNativeAttachmentText(data, page.NextOffset, 4)
	if err != nil || page.Text != "😀" || page.NextOffset != 7 || page.EOF {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	page, err = ReadNativeAttachmentText(data, page.NextOffset, 4)
	if err != nil || page.Text != "z" || page.NextOffset != 8 || !page.EOF {
		t.Fatalf("last page = %+v, %v", page, err)
	}
	page, err = ReadNativeAttachmentText(data, 8, 4)
	if err != nil || page.Text != "" || !page.EOF {
		t.Fatalf("EOF page = %+v, %v", page, err)
	}
	for _, test := range []struct {
		offset int64
		limit  int
	}{{-1, 4}, {2, 4}, {9, 4}, {3, 1}, {0, 0}, {0, (32 << 10) + 1}} {
		if _, err := ReadNativeAttachmentText(data, test.offset, test.limit); !errors.Is(err, ErrNativeAttachmentReadBounds) {
			t.Fatalf("offset=%d, limit=%d: %v", test.offset, test.limit, err)
		}
	}
	large := []byte(strings.Repeat("x", 100<<10))
	page, err = ReadNativeAttachmentText(large, 0, 32<<10)
	if err != nil || len(page.Text) != 32<<10 || page.EOF {
		t.Fatalf("large page len=%d, EOF=%v, err=%v", len(page.Text), page.EOF, err)
	}
}

func TestNativeAttachment_SearchLiteralBoundedUTF8AndPaging(t *testing.T) {
	data := []byte(strings.Repeat("é", 100) + "a.* " + strings.Repeat("😀", 100) + "a.* end")
	first, err := SearchNativeAttachmentText(data, "a.*", 0, 1)
	if err != nil || len(first.Matches) != 1 || first.Matches[0].Offset != 200 || first.NextOffset != 203 || first.EOF {
		t.Fatalf("first search = %+v, %v", first, err)
	}
	if !utf8.ValidString(first.Matches[0].Text) || !strings.Contains(first.Matches[0].Text, "a.*") || len(first.Matches[0].Text) > 259 {
		t.Fatalf("invalid search excerpt %q", first.Matches[0].Text)
	}
	second, err := SearchNativeAttachmentText(data, "a.*", first.NextOffset, 20)
	if err != nil || len(second.Matches) != 1 || second.Matches[0].Offset != 604 || !second.EOF {
		t.Fatalf("second search = %+v, %v", second, err)
	}
	bounded, err := SearchNativeAttachmentText([]byte(strings.Repeat("x ", 100)), "x", 0, 20)
	if err != nil || len(bounded.Matches) != 20 || bounded.EOF {
		t.Fatalf("bounded matches=%d, EOF=%v, err=%v", len(bounded.Matches), bounded.EOF, err)
	}
	for _, test := range []struct {
		query   string
		offset  int64
		matches int
	}{
		{"", 0, 1}, {strings.Repeat("x", 257), 0, 1}, {"\xff", 0, 1}, {"a.*", -1, 1}, {"a.*", 1, 1}, {"a.*", 0, 0}, {"a.*", 0, 21},
	} {
		if _, err := SearchNativeAttachmentText(data, test.query, test.offset, test.matches); !errors.Is(err, ErrNativeAttachmentSearch) {
			t.Fatalf("invalid search error=%v", err)
		}
	}
}

func TestApplication_LoadNativeAttachmentOwnershipAndClaim(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var sessions chat.Store = chat.NewMemoryStore()
			var attachments chatattachments.Store = chatattachments.NewMemoryStore()
			if backend == "sqlite" {
				client, err := storage.NewSQLiteClient(t.Context(), storage.SQLiteConfig{Path: filepath.Join(t.TempDir(), "reader.db"), TablePrefix: "reader"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				sessions, err = chat.NewSQLiteStore(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				attachments, err = chatattachments.NewSQLiteStore(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
			}
			app := New(Options{Store: sessions, Attachments: attachments})
			input := nativeAttachmentReaderFixture(t, sessions, attachments)
			// A matching transcript alone must not turn an unclaimed draft into input.
			if _, err := app.LoadNativeAttachment(t.Context(), input); !errors.Is(err, ErrNativeAttachmentUnavailable) {
				t.Fatalf("draft read=%v", err)
			}
			wrongClaim := chatattachments.ClaimRef{SessionID: input.SessionID, MessageID: "different-message", AttachmentIDs: []string{input.Metadata.ID}}
			if _, err := attachments.Claim(t.Context(), wrongClaim); err != nil {
				t.Fatal(err)
			}
			if _, err := app.LoadNativeAttachment(t.Context(), input); !errors.Is(err, ErrNativeAttachmentUnavailable) {
				t.Fatalf("wrong claim read=%v", err)
			}
			if err := attachments.ResolveClaim(t.Context(), wrongClaim, chatattachments.ClaimReleased); err != nil {
				t.Fatal(err)
			}
			if _, err := attachments.Claim(t.Context(), chatattachments.ClaimRef{SessionID: input.SessionID, MessageID: input.MessageID, AttachmentIDs: []string{input.Metadata.ID}}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				data, err := app.LoadNativeAttachment(t.Context(), input)
				if err != nil || string(data) != "private café\n" {
					t.Fatalf("linked load=%q, %v", data, err)
				}
			}
			for _, mutate := range []func(*NativeAttachmentInput){
				func(in *NativeAttachmentInput) { in.SessionID = "other" },
				func(in *NativeAttachmentInput) { in.MessageID = "other" },
				func(in *NativeAttachmentInput) { in.TaskID = "other" },
				func(in *NativeAttachmentInput) { in.Provider = "other" },
				func(in *NativeAttachmentInput) { in.Provider = "" },
				func(in *NativeAttachmentInput) { in.ProviderInstance = types.ProviderInstanceIdentity{} },
				func(in *NativeAttachmentInput) { in.ProviderInstance.ID = "replacement" },
				func(in *NativeAttachmentInput) { in.Metadata.Filename = "changed.txt" },
				func(in *NativeAttachmentInput) { in.Metadata.SizeBytes++ },
			} {
				changed := input
				mutate(&changed)
				if _, err := app.LoadNativeAttachment(t.Context(), changed); !errors.Is(err, ErrNativeAttachmentUnavailable) {
					t.Fatalf("changed input read=%v", err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := app.LoadNativeAttachment(ctx, input); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled read=%v", err)
			}
			if err := sessions.Delete(t.Context(), input.SessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := app.LoadNativeAttachment(t.Context(), input); !errors.Is(err, ErrNativeAttachmentUnavailable) {
				t.Fatalf("deleted read=%v", err)
			}
		})
	}
}

func nativeAttachmentReaderFixture(t *testing.T, sessions chat.Store, attachments chatattachments.Store) NativeAttachmentInput {
	t.Helper()
	data := []byte("private café\n")
	digest := sha256.Sum256(data)
	metadata := chat.MessageAttachment{ID: "attachment", Filename: "notes.txt", MediaType: "text/plain", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC()}
	input := NativeAttachmentInput{SessionID: "session", MessageID: "message", TaskID: "task", Provider: "provider", ProviderInstance: types.ProviderInstanceIdentity{ID: "generation", Kind: types.ProviderInstanceIdentityRuntime}, Metadata: metadata}
	if _, err := sessions.Create(t.Context(), chat.Session{ID: input.SessionID, AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.AppendMessage(t.Context(), input.SessionID, chat.Message{ID: input.MessageID, Role: "user", ToolsEnabled: true, ExecutionMode: chat.ExecutionModeHecateTask, TaskID: input.TaskID, Provider: input.Provider, ProviderInstance: input.ProviderInstance, Attachments: []chat.MessageAttachment{metadata}}); err != nil {
		t.Fatal(err)
	}
	if _, err := attachments.Create(t.Context(), chatattachments.StoredAttachment{Attachment: chatattachments.Attachment{ID: metadata.ID, SessionID: input.SessionID, Filename: metadata.Filename, MediaType: metadata.MediaType, SizeBytes: metadata.SizeBytes, SHA256: metadata.SHA256, CreatedAt: metadata.CreatedAt}, Data: data}); err != nil {
		t.Fatal(err)
	}
	return input
}

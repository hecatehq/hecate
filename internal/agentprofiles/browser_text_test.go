package agentprofiles

import (
	"errors"
	"testing"
)

func TestBrowserTextEntryGrantRequiresInteractionsAndPersists(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) Store{
		"memory":   func(t *testing.T) Store { return NewMemoryStore() },
		"sqlite":   func(t *testing.T) Store { return newSQLiteProfileTestStore(t) },
		"postgres": func(t *testing.T) Store { return newPostgresProfileTestStore(t) },
	} {
		t.Run(name, func(t *testing.T) {
			store := factory(t)
			profile := Profile{ID: "text", Name: "Text", Surface: SurfaceHecateChat, ToolsEnabled: true, BrowserTextEntryAllowed: true, BrowserAllowedOrigins: []string{"https://app.example.test"}}
			if _, err := store.Create(t.Context(), profile); !errors.Is(err, ErrInvalid) {
				t.Fatalf("text without interaction: %v", err)
			}
			profile.BrowserInteractionsAllowed = true
			if _, err := store.Create(t.Context(), profile); err != nil {
				t.Fatal(err)
			}
			got, found, err := store.Get(t.Context(), profile.ID)
			if err != nil || !found || !got.BrowserTextEntryAllowed {
				t.Fatalf("round trip: %+v %v", got, err)
			}
			if _, err := store.Update(t.Context(), profile.ID, func(p *Profile) { p.BrowserInteractionsAllowed = false }); !errors.Is(err, ErrInvalid) {
				t.Fatalf("orphan text grant: %v", err)
			}
			got, found, err = store.Get(t.Context(), profile.ID)
			if err != nil || !found || !got.BrowserTextEntryAllowed || !got.BrowserInteractionsAllowed {
				t.Fatalf("rejected update changed policy: %+v found=%v err=%v", got, found, err)
			}
			if _, err := store.Update(t.Context(), profile.ID, func(p *Profile) { p.BrowserTextEntryAllowed = false }); err != nil {
				t.Fatalf("revoke text entry: %v", err)
			}
			got, found, err = store.Get(t.Context(), profile.ID)
			if err != nil || !found || got.BrowserTextEntryAllowed || !got.BrowserInteractionsAllowed {
				t.Fatalf("revocation round trip: %+v found=%v err=%v", got, found, err)
			}
			if _, err := store.Update(t.Context(), profile.ID, func(p *Profile) { p.BrowserTextEntryAllowed = true }); err != nil {
				t.Fatalf("grant text entry again: %v", err)
			}
			items, err := store.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			listed := false
			for _, item := range items {
				if item.ID == profile.ID {
					listed = true
					if !item.BrowserTextEntryAllowed || !item.BrowserInteractionsAllowed {
						t.Fatalf("listed policy lost grant: %+v", item)
					}
				}
			}
			if !listed {
				t.Fatal("List omitted stored profile")
			}
		})
	}
}

package agentprofiles

import (
	"errors"
	"testing"
)

func TestBrowserTextEntryGrantRequiresInteractionsAndPersists(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) Store{
		"memory": func(t *testing.T) Store { return NewMemoryStore() },
		"sqlite": func(t *testing.T) Store { return newSQLiteProfileTestStore(t) },
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
		})
	}
}

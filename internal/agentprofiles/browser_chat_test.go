package agentprofiles

import (
	"errors"
	"testing"
)

func TestChatBrowserPolicyStoreValidation(t *testing.T) {
	t.Parallel()
	for _, backend := range []struct {
		name   string
		create func(*testing.T) Store
	}{
		{name: "memory", create: func(*testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", create: func(t *testing.T) Store { return newSQLiteProfileTestStore(t) }},
	} {
		t.Run(backend.name, func(t *testing.T) {
			for _, grants := range []struct {
				name              string
				inspect, interact bool
			}{{"inspect", true, false}, {"interact", false, true}, {"both", true, true}} {
				t.Run(grants.name, func(t *testing.T) {
					store := backend.create(t)
					profile, err := store.Create(t.Context(), Profile{ID: "browser", Name: "Browser", Surface: SurfaceHecateChat, ToolsEnabled: true, BrowserAllowed: grants.inspect, BrowserInteractionsAllowed: grants.interact, BrowserAllowedOrigins: []string{"https://APP.example.test:443/", "https://app.example.test"}})
					if err != nil {
						t.Fatal(err)
					}
					if profile.BrowserAllowed != grants.inspect || profile.BrowserInteractionsAllowed != grants.interact || len(profile.BrowserAllowedOrigins) != 1 || profile.BrowserAllowedOrigins[0] != "https://app.example.test" {
						t.Fatalf("normalized profile = %+v", profile)
					}
					for _, surface := range []string{SurfaceAny, SurfaceHecateTask, SurfaceHecateChat} {
						if _, err := store.Update(t.Context(), profile.ID, func(p *Profile) { p.Surface = surface }); err != nil {
							t.Fatalf("native surface transition %s: %v", surface, err)
						}
					}
					for _, mutate := range []func(*Profile){
						func(p *Profile) { p.ToolsEnabled = false },
						func(p *Profile) { p.Surface = SurfaceExternalAgent },
						func(p *Profile) { p.BrowserAllowedOrigins = []string{"https://app.example.test/path"} },
					} {
						if _, err := store.Update(t.Context(), profile.ID, mutate); !errors.Is(err, ErrInvalid) {
							t.Fatalf("invalid update error = %v", err)
						}
					}
					got, ok, err := store.Get(t.Context(), profile.ID)
					if err != nil || !ok || got.Surface != SurfaceHecateChat || !got.ToolsEnabled || got.BrowserAllowed != grants.inspect || got.BrowserInteractionsAllowed != grants.interact || got.BrowserAllowedOrigins[0] != "https://app.example.test" {
						t.Fatalf("invalid update mutated policy: %+v ok=%v err=%v", got, ok, err)
					}
				})
			}
		})
	}
}

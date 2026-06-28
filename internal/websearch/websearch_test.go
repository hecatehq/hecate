package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBraveClientSearchSendsExpectedRequestAndNormalizesResults(t *testing.T) {
	var gotPath string
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RawQuery
		gotToken = r.Header.Get("X-Subscription-Token")
		if r.URL.Query().Get("q") != "agent protocol" {
			t.Fatalf("q = %q, want agent protocol", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("count") != "3" {
			t.Fatalf("count = %q, want 3", r.URL.Query().Get("count"))
		}
		if r.URL.Query().Get("safesearch") != "strict" {
			t.Fatalf("safesearch = %q, want strict", r.URL.Query().Get("safesearch"))
		}
		if r.URL.Query().Get("country") != "US" {
			t.Fatalf("country = %q, want US", r.URL.Query().Get("country"))
		}
		if r.URL.Query().Get("search_lang") != "en" {
			t.Fatalf("search_lang = %q, want en", r.URL.Query().Get("search_lang"))
		}
		if r.URL.Query().Get("freshness") != "pw" {
			t.Fatalf("freshness = %q, want pw", r.URL.Query().Get("freshness"))
		}
		if r.URL.Query().Get("extra_snippets") != "true" {
			t.Fatalf("extra_snippets = %q, want true", r.URL.Query().Get("extra_snippets"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": map[string]any{
				"original":               "agent protocol",
				"more_results_available": true,
			},
			"web": map[string]any{
				"results": []map[string]any{
					{
						"title":          " Agent Client Protocol ",
						"url":            " https://agentclientprotocol.com ",
						"description":    " ACP docs ",
						"extra_snippets": []string{" snippet one ", "", "snippet two"},
						"age":            "2 days ago",
						"language":       "en",
					},
					{
						"title": "",
						"url":   "",
					},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewBraveClient(Config{
		APIKey:     "test-token",
		Endpoint:   server.URL,
		MaxResults: 3,
		SafeSearch: "strict",
		Country:    "US",
		SearchLang: "en",
	})
	resp, err := client.Search(context.Background(), Query{
		Query:     " agent protocol ",
		Count:     99,
		Freshness: "pw",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotToken != "test-token" {
		t.Fatalf("X-Subscription-Token = %q, want test-token", gotToken)
	}
	if gotPath == "" {
		t.Fatal("request query was not captured")
	}
	if resp.Provider != ProviderBrave || resp.Query != "agent protocol" || !resp.MoreResultsAvailable {
		t.Fatalf("response metadata = %+v, want brave agent protocol with more results", resp)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Title != "Agent Client Protocol" || got.URL != "https://agentclientprotocol.com" || got.Description != "ACP docs" {
		t.Fatalf("result = %+v, want trimmed fields", got)
	}
	if len(got.ExtraSnippets) != 2 || got.ExtraSnippets[0] != "snippet one" || got.ExtraSnippets[1] != "snippet two" {
		t.Fatalf("extra snippets = %#v, want trimmed non-empty snippets", got.ExtraSnippets)
	}
}

func TestNewClientValidatesProviderAndKey(t *testing.T) {
	if client, err := NewClient(Config{}); err != nil || client != nil {
		t.Fatalf("NewClient(empty) = %#v, %v; want nil, nil", client, err)
	}
	if _, err := NewClient(Config{Provider: ProviderBrave}); err == nil {
		t.Fatal("NewClient(brave without key) error = nil, want error")
	}
	if _, err := NewClient(Config{Provider: "unknown", APIKey: "key"}); err == nil {
		t.Fatal("NewClient(unknown) error = nil, want error")
	}
}

func TestBraveClientSearchReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	client := NewBraveClient(Config{APIKey: "test-token", Endpoint: server.URL})
	_, err := client.Search(context.Background(), Query{Query: "agent protocol"})
	if err == nil {
		t.Fatal("Search() error = nil, want status error")
	}
}

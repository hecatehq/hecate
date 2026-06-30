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
						"extra_snippets": []string{" snippet one ", "", "snippet two", "snippet three", "snippet four"},
						"age":            "2 days ago",
						"language":       "en",
					},
					{
						"title": "",
						"url":   "",
					},
					{
						"title": "Result two",
						"url":   "https://example.test/two",
					},
					{
						"title": "Result three",
						"url":   "https://example.test/three",
					},
					{
						"title": "Result four",
						"url":   "https://example.test/four",
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
	if len(resp.Results) != 3 {
		t.Fatalf("results = %d, want effective cap of 3", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Title != "Agent Client Protocol" || got.URL != "https://agentclientprotocol.com" || got.Description != "ACP docs" {
		t.Fatalf("result = %+v, want trimmed fields", got)
	}
	if len(got.ExtraSnippets) != 3 || got.ExtraSnippets[0] != "snippet one" || got.ExtraSnippets[2] != "snippet three" {
		t.Fatalf("extra snippets = %#v, want trimmed non-empty snippets capped at 3", got.ExtraSnippets)
	}
	if resp.Results[2].Title != "Result three" {
		t.Fatalf("last result = %+v, want provider results capped after empty rows are skipped", resp.Results[2])
	}
}

func TestTavilyClientSearchSendsExpectedRequestAndNormalizesResults(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["query"] != "agent protocol" {
			t.Fatalf("query = %#v, want agent protocol", body["query"])
		}
		if body["max_results"] != float64(3) {
			t.Fatalf("max_results = %#v, want 3", body["max_results"])
		}
		if body["search_depth"] != "basic" {
			t.Fatalf("search_depth = %#v, want basic", body["search_depth"])
		}
		if body["include_answer"] != false {
			t.Fatalf("include_answer = %#v, want false", body["include_answer"])
		}
		if body["time_range"] != "week" {
			t.Fatalf("time_range = %#v, want week", body["time_range"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "agent protocol",
			"results": []map[string]any{
				{
					"title":   " Agent Client Protocol ",
					"url":     " https://agentclientprotocol.com ",
					"content": " ACP docs ",
				},
				{
					"title": "",
					"url":   "",
				},
				{
					"title":   "Result two",
					"url":     "https://example.test/two",
					"content": "second",
				},
				{
					"title": "Result three",
					"url":   "https://example.test/three",
				},
				{
					"title": "Result four",
					"url":   "https://example.test/four",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewTavilyClient(Config{
		APIKey:     "test-token",
		Endpoint:   server.URL,
		MaxResults: 3,
	})
	resp, err := client.Search(context.Background(), Query{
		Query:     " agent protocol ",
		Count:     99,
		Freshness: "pw",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotAuthorization != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuthorization)
	}
	if resp.Provider != ProviderTavily || resp.Query != "agent protocol" {
		t.Fatalf("response metadata = %+v, want tavily agent protocol", resp)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %d, want effective cap of 3", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Title != "Agent Client Protocol" || got.URL != "https://agentclientprotocol.com" || got.Description != "ACP docs" {
		t.Fatalf("result = %+v, want trimmed fields", got)
	}
	if resp.Results[2].Title != "Result three" {
		t.Fatalf("last result = %+v, want provider results capped after empty rows are skipped", resp.Results[2])
	}
}

func TestExaClientSearchSendsExpectedRequestAndNormalizesResults(t *testing.T) {
	var gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["query"] != "agent protocol" {
			t.Fatalf("query = %#v, want agent protocol", body["query"])
		}
		if body["numResults"] != float64(3) {
			t.Fatalf("numResults = %#v, want 3", body["numResults"])
		}
		if body["userLocation"] != "US" {
			t.Fatalf("userLocation = %#v, want US", body["userLocation"])
		}
		contents, ok := body["contents"].(map[string]any)
		if !ok || contents["highlights"] != true {
			t.Fatalf("contents = %#v, want highlights enabled", body["contents"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"title":         " Agent Client Protocol ",
					"url":           " https://agentclientprotocol.com ",
					"text":          " ACP docs ",
					"summary":       " ACP summary ",
					"highlights":    []string{" highlight one ", "", "highlight two", "highlight three", "highlight four"},
					"publishedDate": "2026-06-30",
				},
				{
					"title": "",
					"url":   "",
				},
				{
					"title": "Result two",
					"url":   "https://example.test/two",
					"text":  "second",
				},
				{
					"title": "Result three",
					"url":   "https://example.test/three",
				},
				{
					"title": "Result four",
					"url":   "https://example.test/four",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewExaClient(Config{
		APIKey:     "test-token",
		Endpoint:   server.URL,
		MaxResults: 3,
		Country:    "us",
	})
	resp, err := client.Search(context.Background(), Query{
		Query: " agent protocol ",
		Count: 99,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotAPIKey != "test-token" {
		t.Fatalf("x-api-key = %q, want test-token", gotAPIKey)
	}
	if resp.Provider != ProviderExa || resp.Query != "agent protocol" {
		t.Fatalf("response metadata = %+v, want exa agent protocol", resp)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %d, want effective cap of 3", len(resp.Results))
	}
	got := resp.Results[0]
	if got.Title != "Agent Client Protocol" || got.URL != "https://agentclientprotocol.com" || got.Description != "ACP summary" {
		t.Fatalf("result = %+v, want trimmed fields and summary preference", got)
	}
	if len(got.ExtraSnippets) != 3 || got.ExtraSnippets[0] != "highlight one" || got.ExtraSnippets[2] != "highlight three" {
		t.Fatalf("extra snippets = %#v, want trimmed non-empty highlights capped at 3", got.ExtraSnippets)
	}
	if got.Age != "2026-06-30" {
		t.Fatalf("age = %q, want published date", got.Age)
	}
}

func TestNewClientValidatesProviderAndKey(t *testing.T) {
	if client, err := NewClient(Config{}); err != nil || client != nil {
		t.Fatalf("NewClient(empty) = %#v, %v; want nil, nil", client, err)
	}
	for _, provider := range []string{ProviderBrave, ProviderTavily, ProviderExa} {
		if _, err := NewClient(Config{Provider: provider}); err == nil {
			t.Fatalf("NewClient(%s without key) error = nil, want error", provider)
		}
		client, err := NewClient(Config{Provider: provider, APIKey: "key"})
		if err != nil {
			t.Fatalf("NewClient(%s) error = %v", provider, err)
		}
		if client == nil {
			t.Fatalf("NewClient(%s) = nil, want client", provider)
		}
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

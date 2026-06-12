package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ListServersEncodesQueryAndDecodesNestedResponse(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/v0.1/servers" {
			t.Errorf("path = %q, want /registry/v0.1/servers", r.URL.Path)
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"servers":[{
				"server":{
					"name":"io.github/example/weather",
					"title":"Weather",
					"description":"Forecasts",
					"version":"1.2.3",
					"repository":{"url":"https://github.com/example/weather","source":"github"},
					"remotes":[{"type":"streamable-http","url":"https://weather.example/mcp","headers":[{"name":"Authorization","isRequired":true,"isSecret":true}]}],
					"packages":[{"registryType":"npm","identifier":"@example/weather","transport":{"type":"stdio"},"runtimeHint":"npx"}],
					"_meta":{"publisher":"example"}
				},
				"_meta":{"isOfficial":true}
			}],
			"metadata":{"nextCursor":"cursor-2","count":1}
		}`))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL+"/registry", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListServers(context.Background(), ListServersQuery{
		Cursor:         "cursor-1",
		Limit:          25,
		Search:         "weather server",
		UpdatedSince:   "2026-01-02T03:04:05Z",
		Version:        "1.2.3",
		IncludeDeleted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"cursor=cursor-1",
		"include_deleted=true",
		"limit=25",
		"search=weather+server",
		"updated_since=2026-01-02T03%3A04%3A05Z",
		"version=1.2.3",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query = %q, missing %q", gotQuery, want)
		}
	}
	if got.Metadata.NextCursor != "cursor-2" || got.Metadata.Count != 1 {
		t.Fatalf("metadata = %#v", got.Metadata)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("servers len = %d, want 1", len(got.Servers))
	}
	item := got.Servers[0]
	if item.Server.Name != "io.github/example/weather" {
		t.Fatalf("server name = %q", item.Server.Name)
	}
	if string(item.Server.Meta) != `{"publisher":"example"}` {
		t.Fatalf("server _meta = %s", item.Server.Meta)
	}
	if string(item.Meta) != `{"isOfficial":true}` {
		t.Fatalf("item _meta = %s", item.Meta)
	}
	if item.Server.Remotes[0].Headers[0].Name != "Authorization" {
		t.Fatalf("remote header = %#v", item.Server.Remotes[0].Headers[0])
	}
}

func TestClient_ListServersAcceptsFlatPreviewItems(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"servers":[{
				"name":"io.github/example/flat",
				"description":"Flat preview shape",
				"version":"0.1.0",
				"_meta":{"preview":true}
			}],
			"metadata":{"count":1}
		}`))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ListServers(context.Background(), ListServersQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("servers len = %d, want 1", len(got.Servers))
	}
	if got.Servers[0].Server.Name != "io.github/example/flat" {
		t.Fatalf("server name = %q", got.Servers[0].Server.Name)
	}
	if string(got.Servers[0].Server.Meta) != `{"preview":true}` {
		t.Fatalf("server _meta = %s", got.Servers[0].Server.Meta)
	}
}

func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"registry.modelcontextprotocol.io", "ftp://example.test", "https://example.test?x=1"} {
		if _, err := NewClient(raw, nil); err == nil {
			t.Fatalf("NewClient(%q) succeeded, want error", raw)
		}
	}
}

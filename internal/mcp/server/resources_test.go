package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/mcp"
)

func TestDefaultResources_ListAndTemplates(t *testing.T) {
	server := NewServer("t", "0")
	RegisterDefaultResources(server, NewGatewayClient("http://unused"))

	resources := server.resources.list()
	if len(resources) != 2 {
		t.Fatalf("resources count = %d, want 2: %+v", len(resources), resources)
	}
	for _, want := range []string{resourceRecentTasksURI, resourceRecentTracesURI} {
		if !hasResourceURI(resources, want) {
			t.Fatalf("resource %q missing from %+v", want, resources)
		}
	}

	templates := server.resources.listTemplates()
	if len(templates) != 2 {
		t.Fatalf("templates count = %d, want 2: %+v", len(templates), templates)
	}
	for _, want := range []string{"hecate://tasks/{task_id}", "hecate://traces/{request_id}"} {
		if !hasResourceTemplate(templates, want) {
			t.Fatalf("template %q missing from %+v", want, templates)
		}
	}
}

func TestDefaultResource_ReadRecentTasks(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "30" {
				t.Errorf("limit query = %q, want 30", r.URL.Query().Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"task-1","title":"Inspect","status":"running","execution_kind":"agent_loop"}]}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultResources(server, NewGatewayClient(srv.URL))

	result, err := server.resources.read(context.Background(), resourceRecentTasksURI)
	if err != nil {
		t.Fatalf("read recent tasks: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
	if result.Contents[0].MIMEType != resourceMIMEJSON {
		t.Fatalf("mime = %q, want JSON", result.Contents[0].MIMEType)
	}
	if !strings.Contains(result.Contents[0].Text, `"id": "task-1"`) {
		t.Fatalf("resource text missing task id: %s", result.Contents[0].Text)
	}
}

func TestDefaultResource_ReadTaskDetail(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/tasks/task-123": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"task-123","title":"Inspect","status":"completed","execution_kind":"agent_loop","step_count":4}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultResources(server, NewGatewayClient(srv.URL))

	result, err := server.resources.read(context.Background(), "hecate://tasks/task-123")
	if err != nil {
		t.Fatalf("read task detail: %v", err)
	}
	var body getTaskStatusResponse
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &body); err != nil {
		t.Fatalf("resource is not JSON: %v", err)
	}
	if body.Data.ID != "task-123" || body.Data.StepCount != 4 {
		t.Fatalf("task detail = %+v, want task-123 with 4 steps", body.Data)
	}
}

func TestDefaultResource_ReadTraceDetail(t *testing.T) {
	srv := fakeGateway(t, map[string]http.HandlerFunc{
		"/hecate/v1/traces": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("request_id") != "req-1" {
				t.Errorf("request_id query = %q, want req-1", r.URL.Query().Get("request_id"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"request_id":"req-1","trace_id":"trace-1","route":{"final_provider":"openai","final_model":"gpt-5"}}}`))
		},
	})
	server := NewServer("t", "0")
	RegisterDefaultResources(server, NewGatewayClient(srv.URL))

	result, err := server.resources.read(context.Background(), "hecate://traces/req-1")
	if err != nil {
		t.Fatalf("read trace detail: %v", err)
	}
	if !strings.Contains(result.Contents[0].Text, `"request_id": "req-1"`) {
		t.Fatalf("resource text missing request id: %s", result.Contents[0].Text)
	}
}

func hasResourceURI(resources []mcp.Resource, uri string) bool {
	for _, resource := range resources {
		if resource.URI == uri {
			return true
		}
	}
	return false
}

func hasResourceTemplate(templates []mcp.ResourceTemplate, uriTemplate string) bool {
	for _, template := range templates {
		if template.URITemplate == uriTemplate {
			return true
		}
	}
	return false
}

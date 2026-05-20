package telemetry

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

func TestBuildResourcePopulatesServiceIdentity(t *testing.T) {
	res, err := BuildResource(context.Background(), ResourceOptions{
		ServiceName:       "hecate-test",
		ServiceVersion:    "1.2.3",
		ServiceInstanceID: "instance-abc",
		DeploymentEnv:     "staging",
	})
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}

	want := map[string]string{
		"service.name":                "hecate-test",
		"service.version":             "1.2.3",
		"service.instance.id":         "instance-abc",
		"deployment.environment.name": "staging",
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("attribute %q = %q, want %q", key, got[key], expected)
		}
	}

	// Built-in detectors must contribute telemetry.sdk.* attributes so
	// backends can identify which SDK produced the data.
	if got["telemetry.sdk.name"] == "" {
		t.Error("expected telemetry.sdk.name to be populated by WithTelemetrySDK detector")
	}
}

func TestBuildResourceGeneratesInstanceIDByDefault(t *testing.T) {
	res, err := BuildResource(context.Background(), ResourceOptions{ServiceName: "hecate"})
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	for _, kv := range res.Attributes() {
		if string(kv.Key) == "service.instance.id" {
			if v := kv.Value.AsString(); v == "" || strings.ContainsAny(v, " \t\n") {
				t.Errorf("generated service.instance.id is not a valid identifier: %q", v)
			}
			return
		}
	}
	t.Error("service.instance.id not present in resource attributes")
}

func TestBuildResourceFallsBackToDefaultServiceName(t *testing.T) {
	res, err := BuildResource(context.Background(), ResourceOptions{})
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	for _, kv := range res.Attributes() {
		if string(kv.Key) == "service.name" {
			if got := kv.Value.AsString(); got != ServiceName {
				t.Errorf("service.name = %q, want default %q", got, ServiceName)
			}
			return
		}
	}
	t.Error("service.name attribute missing")
}

func TestBuildResourceIncludesExtraAttributes(t *testing.T) {
	res, err := BuildResource(context.Background(), ResourceOptions{
		ServiceName: "hecate",
		ExtraAttributes: []attribute.KeyValue{
			attribute.String("hecate.region", "us-west-2"),
			attribute.Int("hecate.shard", 7),
		},
	})
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}

	if got["hecate.region"] != "us-west-2" {
		t.Errorf("hecate.region = %q, want %q", got["hecate.region"], "us-west-2")
	}
	if got["hecate.shard"] != "7" {
		t.Errorf("hecate.shard = %q, want %q", got["hecate.shard"], "7")
	}
}

// TestBuildResourceHonorsOTELResourceAttributes verifies that values from
// OTEL_RESOURCE_ATTRIBUTES override the typed inputs. This is the standard
// OpenTelemetry escape hatch operators reach for when they need to tag
// instances at deploy time without rebuilding the binary.
func TestBuildResourceHonorsOTELResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=env-override,deployment.environment.name=prod-env")

	res, err := BuildResource(context.Background(), ResourceOptions{
		ServiceName:    "hecate",
		ServiceVersion: "code-set-1.0.0",
		DeploymentEnv:  "code-set-staging",
	})
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}

	if got["service.version"] != "env-override" {
		t.Errorf("service.version = %q, want %q (env should override code)", got["service.version"], "env-override")
	}
	if got["deployment.environment.name"] != "prod-env" {
		t.Errorf("deployment.environment.name = %q, want %q (env should override code)", got["deployment.environment.name"], "prod-env")
	}
}

func TestServiceNameFromResource(t *testing.T) {
	t.Run("nil resource returns default", func(t *testing.T) {
		if got := serviceNameFromResource(nil); got != ServiceName {
			t.Errorf("serviceNameFromResource(nil) = %q, want %q", got, ServiceName)
		}
	})

	t.Run("resource with service.name returns it", func(t *testing.T) {
		res := resource.NewSchemaless(semconv.ServiceName("custom-name"))
		if got := serviceNameFromResource(res); got != "custom-name" {
			t.Errorf("serviceNameFromResource = %q, want %q", got, "custom-name")
		}
	})

	t.Run("resource without service.name falls back", func(t *testing.T) {
		res := resource.NewSchemaless(attribute.String("hecate.region", "us-west-2"))
		if got := serviceNameFromResource(res); got != ServiceName {
			t.Errorf("serviceNameFromResource (no service.name) = %q, want default %q", got, ServiceName)
		}
	})
}

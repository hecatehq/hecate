package browserrunner

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestNormalizeAllowedOrigins(t *testing.T) {
	t.Parallel()

	got, err := NormalizeAllowedOrigins([]string{
		"HTTPS://Example.test/",
		"https://example.test",
		"http://example.test:80",
		"https://[2001:db8::1]:443/",
	})
	if err != nil {
		t.Fatalf("NormalizeAllowedOrigins() error = %v", err)
	}
	want := []string{"http://example.test", "https://[2001:db8::1]", "https://example.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAllowedOrigins() = %#v, want %#v", got, want)
	}
}

func TestIsPublicBrowserIP(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		ip   string
		want bool
	}{
		{ip: "8.8.8.8", want: true},
		{ip: "1.1.1.1", want: true},
		{ip: "0.0.0.1", want: false},
		{ip: "::ffff:0.0.0.1", want: false},
		{ip: "127.0.0.1", want: false},
		{ip: "10.0.0.1", want: false},
		{ip: "100.64.0.1", want: false},
		{ip: "169.254.1.1", want: false},
		{ip: "192.0.2.1", want: false},
		{ip: "198.18.0.1", want: false},
		{ip: "198.51.100.1", want: false},
		{ip: "203.0.113.1", want: false},
		{ip: "240.0.0.1", want: false},
		{ip: "::1", want: false},
		{ip: "fc00::1", want: false},
		{ip: "64:ff9b::7f00:1", want: false},
		{ip: "2001:db8::1", want: false},
	} {
		t.Run(test.ip, func(t *testing.T) {
			t.Parallel()
			if got := isPublicBrowserIP(net.ParseIP(test.ip)); got != test.want {
				t.Fatalf("isPublicBrowserIP(%s) = %v, want %v", test.ip, got, test.want)
			}
		})
	}
}

func TestNormalizeOriginRejectsBroadOrSensitiveEntries(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://example.test/path",
		"https://example.test/?token=secret",
		"https://operator:secret@example.test",
		"https://*.example.test",
		"file:///tmp/evidence.html",
		"https://",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeOrigin(raw)
			if !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("NormalizeOrigin(%q) error = %v, want ErrInvalidURL", raw, err)
			}
		})
	}
}

func TestBrowserNetworkBlockPatternsAllowOnlyConfiguredOrigins(t *testing.T) {
	t.Parallel()
	policy, err := newRequestPolicy(InspectRequest{
		URL: "https://app.example.test/reports",
		AllowedOrigins: []string{
			"https://status.example.test",
			"https://app.example.test",
		},
	})
	if err != nil {
		t.Fatalf("newRequestPolicy() error = %v", err)
	}
	patterns := browserNetworkBlockPatterns(policy)
	if len(patterns) != 3 {
		t.Fatalf("block patterns = %#v, want two allow patterns and one block", patterns)
	}
	for index, want := range []string{"https://app.example.test/*", "https://status.example.test/*"} {
		if patterns[index].URLPattern != want || patterns[index].Block {
			t.Fatalf("patterns[%d] = %#v, want allow %q", index, patterns[index], want)
		}
	}
	if last := patterns[len(patterns)-1]; last.URLPattern != "*://*/*" || !last.Block {
		t.Fatalf("last block pattern = %#v, want catch-all block", last)
	}
}

func TestRequestPolicyRequiresExactOrigin(t *testing.T) {
	t.Parallel()

	policy, err := newRequestPolicy(InspectRequest{
		URL:            "https://app.example.test/reports",
		AllowedOrigins: []string{"https://app.example.test"},
	})
	if err != nil {
		t.Fatalf("newRequestPolicy() error = %v", err)
	}
	if !policy.allowsURL("https://app.example.test/static/site.css") {
		t.Fatal("expected same exact origin to be allowed")
	}
	if policy.allowsURL("https://cdn.example.test/site.css") {
		t.Fatal("unexpected subdomain allowance")
	}
	if policy.allowsURL("http://app.example.test/site.css") {
		t.Fatal("unexpected scheme allowance")
	}
	if policy.allowsURL("file:///tmp/evidence.html") {
		t.Fatal("unexpected non-HTTP(S) allowance")
	}
}

func TestInspectionOriginForURLRejectsSensitiveTargetParts(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://app.example.test/reports?token=secret",
		"https://app.example.test/reports#section",
		"https://app.example.test/reports?",
		"https://operator:secret@app.example.test/reports",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := InspectionOriginForURL(raw); !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("InspectionOriginForURL(%q) error = %v, want ErrInvalidURL", raw, err)
			}
		})
	}
	origin, err := InspectionOriginForURL("https://app.example.test/reports")
	if err != nil || origin != "https://app.example.test" {
		t.Fatalf("InspectionOriginForURL() = %q, %v", origin, err)
	}
}

func TestRequestPolicyAllowsOnlyReadMethods(t *testing.T) {
	t.Parallel()

	policy, err := newRequestPolicy(InspectRequest{
		URL:            "https://app.example.test/reports",
		AllowedOrigins: []string{"https://app.example.test"},
	})
	if err != nil {
		t.Fatalf("newRequestPolicy() error = %v", err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		if !policy.allowsRequest("https://app.example.test/report", method) {
			t.Fatalf("allowsRequest(%q) = false, want allowed", method)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if policy.allowsRequest("https://app.example.test/report", method) {
			t.Fatalf("allowsRequest(%q) = true, want blocked", method)
		}
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	got := RedactURL("https://operator:secret@example.test/path?token=secret#fragment")
	if got != "" {
		t.Fatalf("RedactURL() = %q, want invalid credential URL to be omitted", got)
	}
	got = RedactURL("https://example.test/path?token=secret#fragment")
	if got != "https://example.test/path" {
		t.Fatalf("RedactURL() = %q", got)
	}
}

func TestRequestPolicyPreflightHostMappingsPinsVettedAddresses(t *testing.T) {
	t.Parallel()
	policy := requestPolicy{allowed: map[string]struct{}{
		"https://api.example.test": {},
		"https://ui.example.test":  {},
	}}
	lookup := func(_ context.Context, hostname string) ([]net.IPAddr, error) {
		switch hostname {
		case "api.example.test":
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
		case "ui.example.test":
			return []net.IPAddr{{IP: net.ParseIP("2001:4860:4860::8888")}}, nil
		default:
			t.Fatalf("unexpected DNS lookup for %q", hostname)
			return nil, nil
		}
	}

	mappings, err := policy.preflightHostMappings(context.Background(), false, lookup)
	if err != nil {
		t.Fatalf("preflightHostMappings() error = %v", err)
	}
	want := []browserHostMapping{
		{Hostname: "api.example.test", Address: "1.1.1.1"},
		{Hostname: "ui.example.test", Address: "2001:4860:4860::8888"},
	}
	if !reflect.DeepEqual(mappings, want) {
		t.Fatalf("preflightHostMappings() = %#v, want %#v", mappings, want)
	}
	rules, err := hostResolverRules(mappings)
	if err != nil {
		t.Fatalf("hostResolverRules() error = %v", err)
	}
	if wantRules := "MAP api.example.test 1.1.1.1, MAP ui.example.test [2001:4860:4860::8888]"; rules != wantRules {
		t.Fatalf("hostResolverRules() = %q, want %q", rules, wantRules)
	}
}

func TestRequestPolicyPreflightHostMappingsRejectsPrivateAnswers(t *testing.T) {
	t.Parallel()
	policy := requestPolicy{allowed: map[string]struct{}{"https://app.example.test": {}}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, err := policy.preflightHostMappings(context.Background(), false, lookup); !errors.Is(err, ErrPrivateNetwork) {
		t.Fatalf("preflightHostMappings() error = %v, want ErrPrivateNetwork", err)
	}
	mappings, err := policy.preflightHostMappings(context.Background(), true, lookup)
	if err != nil {
		t.Fatalf("private opt-in preflightHostMappings() error = %v", err)
	}
	if want := []browserHostMapping{{Hostname: "app.example.test", Address: "127.0.0.1"}}; !reflect.DeepEqual(mappings, want) {
		t.Fatalf("private opt-in mappings = %#v, want %#v", mappings, want)
	}
}

func TestRequestPolicyPreflightHostMappingsRejectsPrivateLiteral(t *testing.T) {
	t.Parallel()
	policy := requestPolicy{allowed: map[string]struct{}{"http://127.0.0.1": {}}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("literal IP must not be resolved")
		return nil, nil
	}
	if _, err := policy.preflightHostMappings(context.Background(), false, lookup); !errors.Is(err, ErrPrivateNetwork) {
		t.Fatalf("preflightHostMappings() error = %v, want ErrPrivateNetwork", err)
	}
}

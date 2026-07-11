package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestOperatorURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		publicURL  string
		listenAddr string
		want       string
	}{
		{"explicit PublicURL wins", "https://hecate.example.com", "127.0.0.1:8765", "https://hecate.example.com"},
		{"PublicURL is trimmed", "  http://app.local  ", "127.0.0.1:8765", "http://app.local"},
		{"loopback passthrough", "", "127.0.0.1:8765", "http://127.0.0.1:8765"},
		{"empty host becomes 127.0.0.1", "", ":8765", "http://127.0.0.1:8765"},
		{"0.0.0.0 host becomes 127.0.0.1", "", "0.0.0.0:8765", "http://127.0.0.1:8765"},
		{"IPv6 :: host becomes 127.0.0.1", "", "[::]:8765", "http://127.0.0.1:8765"},
		// A malformed listen address that SplitHostPort rejects should
		// still render — operator sees the raw thing rather than the
		// gateway failing to start up because of a banner bug.
		{"malformed addr falls back to raw", "", "garbage", "http://garbage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{
				Server: config.ServerConfig{PublicURL: tc.publicURL},
			}
			got := operatorURL(cfg, tc.listenAddr)
			if got != tc.want {
				t.Fatalf("operatorURL(%q, %q) = %q, want %q", tc.publicURL, tc.listenAddr, got, tc.want)
			}
		})
	}
}

func TestOtelSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		o    config.OTelConfig
		want string
	}{
		{"all off", config.OTelConfig{}, ""},
		{
			"traces only",
			config.OTelConfig{Traces: config.OTelSignalConfig{Enabled: true}},
			"traces",
		},
		{
			"traces + metrics",
			config.OTelConfig{
				Traces:  config.OTelSignalConfig{Enabled: true},
				Metrics: config.OTelSignalConfig{Enabled: true},
			},
			"traces, metrics",
		},
		{
			"all three",
			config.OTelConfig{
				Traces:  config.OTelSignalConfig{Enabled: true},
				Metrics: config.OTelSignalConfig{Enabled: true},
				Logs:    config.OTelSignalConfig{Enabled: true},
			},
			"traces, metrics, logs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := otelSummary(tc.o); got != tc.want {
				t.Fatalf("otelSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStorageSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"empty defaults to memory", config.Config{}, "memory"},
		{
			"explicit memory",
			config.Config{Server: config.ServerConfig{ControlPlaneBackend: "memory"}},
			"memory",
		},
		{
			"uniform sqlite",
			config.Config{
				Server: config.ServerConfig{
					ControlPlaneBackend: "sqlite",
					TasksBackend:        "sqlite",
					TaskQueueBackend:    "sqlite",
				},
				Chat:      config.ChatConfig{SessionsBackend: "sqlite"},
				Governor:  config.GovernorConfig{UsageBackend: "sqlite"},
				Retention: config.RetentionConfig{HistoryBackend: "sqlite"},
				Provider:  config.ProviderConfig{HistoryBackend: "sqlite"},
			},
			"sqlite",
		},
		{
			"uniform postgres",
			config.Config{
				Server: config.ServerConfig{
					ControlPlaneBackend: "postgres",
					TasksBackend:        "postgres",
					TaskQueueBackend:    "postgres",
				},
				Chat:      config.ChatConfig{SessionsBackend: "postgres"},
				Governor:  config.GovernorConfig{UsageBackend: "postgres"},
				Retention: config.RetentionConfig{HistoryBackend: "postgres"},
				Provider:  config.ProviderConfig{HistoryBackend: "postgres"},
			},
			"postgres",
		},
		{
			"mixed control-plane=memory but tasks=sqlite",
			config.Config{
				Server: config.ServerConfig{
					ControlPlaneBackend: "memory",
					TasksBackend:        "sqlite",
				},
			},
			"memory (mixed)",
		},
		{
			"mixed when chat backend differs",
			config.Config{
				Server: config.ServerConfig{
					ControlPlaneBackend: "postgres",
					TasksBackend:        "postgres",
					TaskQueueBackend:    "postgres",
				},
				Chat:      config.ChatConfig{SessionsBackend: "sqlite"},
				Governor:  config.GovernorConfig{UsageBackend: "postgres"},
				Retention: config.RetentionConfig{HistoryBackend: "postgres"},
				Provider:  config.ProviderConfig{HistoryBackend: "postgres"},
			},
			"postgres (mixed)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := storageSummary(tc.cfg); got != tc.want {
				t.Fatalf("storageSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLClientRequirementCoversEveryStorageSelector(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config, string)
	}{
		{"control plane", func(c *config.Config, backend string) { c.Server.ControlPlaneBackend = backend }},
		{"tasks", func(c *config.Config, backend string) { c.Server.TasksBackend = backend }},
		{"task queue", func(c *config.Config, backend string) { c.Server.TaskQueueBackend = backend }},
		{"chat sessions", func(c *config.Config, backend string) { c.Chat.SessionsBackend = backend }},
		{"usage", func(c *config.Config, backend string) { c.Governor.UsageBackend = backend }},
		{"retention history", func(c *config.Config, backend string) { c.Retention.HistoryBackend = backend }},
		{"provider history", func(c *config.Config, backend string) { c.Provider.HistoryBackend = backend }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cfg config.Config
			tc.mutate(&cfg, "sqlite")
			if !sqliteRequired(cfg) {
				t.Fatal("sqliteRequired = false, want true")
			}
			if postgresRequired(cfg) {
				t.Fatal("postgresRequired = true for sqlite selector, want false")
			}

			cfg = config.Config{}
			tc.mutate(&cfg, "postgres")
			if !postgresRequired(cfg) {
				t.Fatal("postgresRequired = false, want true")
			}
			if sqliteRequired(cfg) {
				t.Fatal("sqliteRequired = true for postgres selector, want false")
			}
		})
	}
}

func TestPrintStartupBanner(t *testing.T) {
	// Cannot t.Parallel() — we swap os.Stderr.
	out := captureStderr(t, func() {
		cfg := config.Config{
			Server: config.ServerConfig{
				DataDir:             ".data",
				ControlPlaneBackend: "sqlite",
			},
			Router: config.RouterConfig{DefaultModel: "gpt-5"},
		}
		printStartupBanner(cfg, "127.0.0.1:8765")
	})
	for _, want := range []string{
		"hecate ·",
		"http://127.0.0.1:8765",
		"data dir       .data",
		"storage        sqlite",
		"default model  gpt-5",
		"providers      0 configured",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("banner missing %q\n---banner---\n%s\n---", want, out)
		}
	}
	// Conditional rows omitted when their feature is off — alpha
	// operators shouldn't see "otel off" / "retention off" noise.
	for _, unwanted := range []string{"otel", "retention"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("banner unexpectedly contains %q\n---banner---\n%s\n---", unwanted, out)
		}
	}
}

func TestPrintStartupBannerShowsOptionalRows(t *testing.T) {
	// Cannot t.Parallel() — we swap os.Stderr.
	out := captureStderr(t, func() {
		cfg := config.Config{
			Server:    config.ServerConfig{DataDir: ".data"},
			Router:    config.RouterConfig{DefaultModel: "gpt-5"},
			Retention: config.RetentionConfig{Enabled: true, Interval: 0},
			OTel: config.OTelConfig{
				Traces:  config.OTelSignalConfig{Enabled: true},
				Metrics: config.OTelSignalConfig{Enabled: true},
			},
		}
		printStartupBanner(cfg, "127.0.0.1:8765")
	})
	for _, want := range []string{
		"otel           traces, metrics",
		"retention      every",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("banner missing optional row %q\n---banner---\n%s\n---", want, out)
		}
	}
}

// captureStderr swaps os.Stderr with an os.Pipe, runs fn, restores
// os.Stderr, and returns whatever fn wrote. Worth the dance because
// printStartupBanner writes via fmt.Fprintln(os.Stderr, …) directly
// rather than through an injected io.Writer — that surface is what
// operators see, and routing tests around it would be a different
// design choice.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	fn()
	_ = w.Close()
	return string(<-done)
}

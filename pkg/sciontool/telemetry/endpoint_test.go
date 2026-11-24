/*
Copyright 2025 The Scion Authors.
*/

package telemetry

import (
	"context"
	"os"
	"strconv"
	"testing"
)

func TestHarnessOTLPEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		current string
		actual  int
		want    string
		wantOK  bool
	}{
		{"unset gets the bound port", "", 40001, "http://localhost:40001", true},
		{"loopback default follows the receiver", "http://localhost:4317", 40001, "http://localhost:40001", true},
		{"loopback IP follows the receiver", "http://127.0.0.1:4317", 40001, "http://127.0.0.1:40001", true},
		{"already on the bound port", "http://localhost:40001", 40001, "", false},
		{"operator collector is left alone", "http://collector.example:4317", 40001, "", false},
		{"receiver not running", "http://localhost:4317", 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := harnessOTLPEndpoint(tc.current, tc.actual)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("harnessOTLPEndpoint(%q, %d) = %q, %v; want %q, %v", tc.current, tc.actual, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// With SCION_OTEL_*_PORT=0 (the tmux runtime's setting) the receiver binds
// ephemeral ports, and AlignHarnessOTLPEndpoint publishes them to the
// environment the harness and its hook processes inherit.
func TestAlignHarnessOTLPEndpoint_PublishesBoundPorts(t *testing.T) {
	t.Setenv(EnvGRPCPort, "0")
	t.Setenv(EnvHTTPPort, "0")
	t.Setenv(EnvHarnessOTLPEndpoint, "http://localhost:4317")
	r := NewReceiver(&Config{GRPCPort: 0, HTTPPort: 0}, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()
	g, h := r.BoundPorts()
	if g == 0 || h == 0 {
		t.Fatalf("BoundPorts() = %d/%d, want ephemeral ports", g, h)
	}

	AlignHarnessOTLPEndpoint(&Pipeline{receiver: r})

	if got := os.Getenv(EnvGRPCPort); got != strconv.Itoa(g) {
		t.Errorf("%s = %q, want %d", EnvGRPCPort, got, g)
	}
	if got := os.Getenv(EnvHTTPPort); got != strconv.Itoa(h) {
		t.Errorf("%s = %q, want %d", EnvHTTPPort, got, h)
	}
	if got, want := os.Getenv(EnvHarnessOTLPEndpoint), "http://localhost:"+strconv.Itoa(g); got != want {
		t.Errorf("%s = %q, want %q", EnvHarnessOTLPEndpoint, got, want)
	}
}

// The provisioner's overlay names the configured port; it must follow the
// receiver like the inherited environment does, in the overlay's own
// spelling, or the native telemetry policy check sees two different
// endpoints and refuses the harness.
func TestAlignOverlayOTLPEndpoint(t *testing.T) {
	r := NewReceiver(&Config{GRPCPort: 0, HTTPPort: 0}, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()
	g, _ := r.BoundPorts()
	p := &Pipeline{receiver: r}
	t.Setenv(EnvHarnessOTLPEndpoint, "http://localhost:"+strconv.Itoa(g))
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	os.Unsetenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")

	overlay := map[string]string{
		EnvHarnessOTLPEndpoint:             "http://127.0.0.1:4317",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://127.0.0.1:4317",
		"OTEL_EXPORTER_OTLP_PROTOCOL":      "grpc",
	}
	AlignOverlayOTLPEndpoint(p, overlay)
	want := "http://127.0.0.1:" + strconv.Itoa(g)
	for _, key := range []string{EnvHarnessOTLPEndpoint, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"} {
		if overlay[key] != want {
			t.Errorf("overlay %s = %q, want %q", key, overlay[key], want)
		}
	}
	if got := os.Getenv(EnvHarnessOTLPEndpoint); got != want {
		t.Errorf("inherited %s = %q, want the overlay value %q", EnvHarnessOTLPEndpoint, got, want)
	}
	if _, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); ok {
		t.Error("a key the environment did not carry must not be added to it")
	}
	if overlay["OTEL_EXPORTER_OTLP_PROTOCOL"] != "grpc" {
		t.Error("non-endpoint keys must be left alone")
	}
	AlignOverlayOTLPEndpoint(nil, map[string]string{EnvHarnessOTLPEndpoint: "http://127.0.0.1:4317"})
}

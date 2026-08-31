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

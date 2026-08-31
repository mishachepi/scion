/*
Copyright 2026 The Scion Authors.
*/

package telemetry

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
)

// occupyPort binds a port and hands back its number, holding it for the test.
func occupyPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	port, ok := listenerPort(lis)
	if !ok {
		t.Fatal("occupied listener is not TCP")
	}
	return port
}

func TestListenLocal_FallsBackWhenPortIsTaken(t *testing.T) {
	taken := occupyPort(t)

	lis, port, err := listenLocal(taken, "gRPC")
	if err != nil {
		t.Fatalf("listenLocal on a taken port should fall back, got error: %v", err)
	}
	defer func() { _ = lis.Close() }()

	if port == taken {
		t.Errorf("listenLocal reported the taken port %d as bound", port)
	}
	if port == 0 {
		t.Error("listenLocal reported port 0 instead of the port it bound")
	}
	if got, _ := listenerPort(lis); got != port {
		t.Errorf("reported port %d but the listener is on %d", port, got)
	}
}

func TestListenLocal_KeepsTheConfiguredPortWhenItIsFree(t *testing.T) {
	// Bind and release to learn a port nothing else is using.
	probe, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	free, ok := listenerPort(probe)
	if !ok {
		t.Fatal("probe listener is not TCP")
	}
	_ = probe.Close()

	lis, port, err := listenLocal(free, "gRPC")
	if err != nil {
		t.Fatalf("listenLocal on a free port: %v", err)
	}
	defer func() { _ = lis.Close() }()

	if port != free {
		t.Errorf("listenLocal moved off the free configured port %d to %d", free, port)
	}
}

// Two agents on one host share a network namespace: the second receiver used
// to fail its whole telemetry start on the first one's port.
func TestReceiver_StartsWhenConfiguredPortsAreTaken(t *testing.T) {
	grpcTaken := occupyPort(t)
	httpTaken := occupyPort(t)

	r := NewReceiver(&Config{GRPCPort: grpcTaken, HTTPPort: httpTaken}, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("receiver must start on fallback ports, got: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	if r.GRPCPort() == grpcTaken || r.GRPCPort() == 0 {
		t.Errorf("gRPC receiver reports port %d, want a fallback port", r.GRPCPort())
	}
	if r.HTTPPort() == httpTaken || r.HTTPPort() == 0 {
		t.Errorf("HTTP receiver reports port %d, want a fallback port", r.HTTPPort())
	}
}

func TestHarnessOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		configured int
		actual     int
		want       string
		wantOK     bool
	}{
		{
			name:       "receiver stayed put",
			current:    "http://localhost:4317",
			configured: 4317, actual: 4317,
			wantOK: false,
		},
		{
			name:       "receiver not running",
			current:    "http://localhost:4317",
			configured: 4317, actual: 0,
			wantOK: false,
		},
		{
			name:       "unset endpoint follows the receiver",
			current:    "",
			configured: 4317, actual: 45123,
			want: "http://localhost:45123", wantOK: true,
		},
		{
			name:       "loopback endpoint is repointed",
			current:    "http://localhost:4317",
			configured: 4317, actual: 45123,
			want: "http://localhost:45123", wantOK: true,
		},
		{
			name:       "127.0.0.1 counts as loopback",
			current:    "http://127.0.0.1:4317",
			configured: 4317, actual: 45123,
			want: "http://127.0.0.1:45123", wantOK: true,
		},
		{
			name:       "a deliberate remote collector is left alone",
			current:    "https://otel.example.com:4317",
			configured: 4317, actual: 45123,
			wantOK: false,
		},
		{
			name:       "a loopback endpoint on some other port is left alone",
			current:    "http://localhost:9999",
			configured: 4317, actual: 45123,
			wantOK: false,
		},
		{
			name:       "an unparseable endpoint is left alone",
			current:    "not a url",
			configured: 4317, actual: 45123,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := harnessOTLPEndpoint(tt.current, tt.configured, tt.actual)
			if ok != tt.wantOK {
				t.Fatalf("harnessOTLPEndpoint(%q, %d, %d) ok = %v, want %v",
					tt.current, tt.configured, tt.actual, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

// The harness inherits sciontool's environment, so the alignment has to land
// in the process env before the harness is started.
func TestAlignHarnessOTLPEndpoint_ExportsTheBoundPort(t *testing.T) {
	grpcTaken := occupyPort(t)
	httpTaken := occupyPort(t)
	t.Setenv(EnvHarnessOTLPEndpoint, fmt.Sprintf("http://localhost:%d", grpcTaken))

	p := &Pipeline{config: &Config{GRPCPort: grpcTaken, HTTPPort: httpTaken}}
	p.receiver = NewReceiver(p.config, nil)
	if err := p.receiver.Start(context.Background()); err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	defer func() { _ = p.receiver.Stop(context.Background()) }()

	AlignHarnessOTLPEndpoint(p)

	want := fmt.Sprintf("http://localhost:%d", p.GRPCPort())
	if got := os.Getenv(EnvHarnessOTLPEndpoint); got != want {
		t.Errorf("%s = %q, want %q", EnvHarnessOTLPEndpoint, got, want)
	}
}

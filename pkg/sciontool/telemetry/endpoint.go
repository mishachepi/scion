/*
Copyright 2025 The Scion Authors.
*/

package telemetry

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
)

// EnvHarnessOTLPEndpoint is the standard OpenTelemetry variable a harness
// reads to find its collector. Harness provisioners default it to the
// receiver's well-known port, so it must follow the receiver when the
// receiver listens elsewhere (SCION_OTEL_GRPC_PORT=0: an ephemeral port).
const EnvHarnessOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// GRPCPort reports the port the local OTLP gRPC receiver actually bound.
// Zero when the pipeline is not running.
func (p *Pipeline) GRPCPort() int {
	if p == nil || p.receiver == nil {
		return 0
	}
	g, _ := p.receiver.BoundPorts()
	return g
}

// HTTPPort reports the port the local OTLP HTTP receiver actually bound.
// Zero when the pipeline is not running.
func (p *Pipeline) HTTPPort() int {
	if p == nil || p.receiver == nil {
		return 0
	}
	_, h := p.receiver.BoundPorts()
	return h
}

// harnessOTLPEndpoint decides what OTEL_EXPORTER_OTLP_ENDPOINT should say once
// the receiver listens on actualPort. It returns the replacement value and
// whether one is needed. An endpoint naming a host other than loopback
// belongs to a collector the operator chose deliberately and is left alone;
// a loopback endpoint can only mean this agent's own receiver, so it is
// pointed at the port actually bound.
func harnessOTLPEndpoint(current string, actualPort int) (string, bool) {
	if actualPort == 0 {
		return "", false
	}
	if current == "" {
		return fmt.Sprintf("http://localhost:%d", actualPort), true
	}
	u, err := url.Parse(current)
	if err != nil || u.Host == "" {
		return "", false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || !isLoopback(host) || port == strconv.Itoa(actualPort) {
		return "", false
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(actualPort))
	return u.String(), true
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AlignHarnessOTLPEndpoint points the environment sciontool's children
// inherit at the ports the receiver actually bound: the harness's
// OTEL_EXPORTER_OTLP_ENDPOINT, and SCION_OTEL_GRPC_PORT/SCION_OTEL_HTTP_PORT,
// which `sciontool hook` processes read to reach this receiver. Under a
// host-execution runtime every agent shares one network namespace, so the
// tmux runtime starts each receiver on an ephemeral port and this keeps
// each agent's harness and hooks paired with its own receiver.
func AlignHarnessOTLPEndpoint(p *Pipeline) {
	if p == nil || p.receiver == nil {
		return
	}
	grpcPort, httpPort := p.receiver.BoundPorts()
	if grpcPort != 0 {
		_ = os.Setenv(EnvGRPCPort, strconv.Itoa(grpcPort))
	}
	if httpPort != 0 {
		_ = os.Setenv(EnvHTTPPort, strconv.Itoa(httpPort))
	}
	if next, ok := harnessOTLPEndpoint(os.Getenv(EnvHarnessOTLPEndpoint), grpcPort); ok {
		_ = os.Setenv(EnvHarnessOTLPEndpoint, next)
	}
}

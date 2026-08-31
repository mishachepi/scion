/*
Copyright 2026 The Scion Authors.
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
// receiver has to move.
const EnvHarnessOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// GRPCPort reports the port the local OTLP gRPC receiver actually bound.
// Zero when the pipeline is not running.
func (p *Pipeline) GRPCPort() int {
	if p == nil || p.receiver == nil {
		return 0
	}
	return p.receiver.GRPCPort()
}

// HTTPPort reports the port the local OTLP HTTP receiver actually bound.
// Zero when the pipeline is not running.
func (p *Pipeline) HTTPPort() int {
	if p == nil || p.receiver == nil {
		return 0
	}
	return p.receiver.HTTPPort()
}

// harnessOTLPEndpoint decides what OTEL_EXPORTER_OTLP_ENDPOINT should say once
// the receiver has bound actualPort instead of configuredPort. It returns the
// replacement value and whether one is needed.
//
// An endpoint that names a host other than loopback belongs to a collector the
// operator chose deliberately — it is left alone. Only the local receiver's own
// address is rewritten, and only when the receiver actually moved.
func harnessOTLPEndpoint(current string, configuredPort, actualPort int) (string, bool) {
	if actualPort == 0 || actualPort == configuredPort {
		return "", false
	}
	local := fmt.Sprintf("http://localhost:%d", actualPort)
	if current == "" {
		return local, true
	}
	u, err := url.Parse(current)
	if err != nil || u.Host == "" {
		return "", false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", false
	}
	if !isLoopback(host) || port != strconv.Itoa(configuredPort) {
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

// AlignHarnessOTLPEndpoint points the environment the harness will inherit at
// the port the receiver actually bound. sciontool is the harness's parent
// process, so exporting the variable here is enough — no broker or hub round
// trip, and nothing to reprovision.
func AlignHarnessOTLPEndpoint(p *Pipeline) {
	if p == nil || p.config == nil {
		return
	}
	next, ok := harnessOTLPEndpoint(os.Getenv(EnvHarnessOTLPEndpoint), p.config.GRPCPort, p.GRPCPort())
	if !ok {
		return
	}
	// Only the harness-facing endpoint is exported. SCION_OTEL_GRPC_PORT is
	// deliberately left alone: it is the receiver's *bind* port, and a child
	// sciontool inheriting the port this process already holds would collide
	// with it — the very conflict this fallback exists to resolve.
	_ = os.Setenv(EnvHarnessOTLPEndpoint, next)
}

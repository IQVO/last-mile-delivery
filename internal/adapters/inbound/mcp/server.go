// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0006-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads last-mile-delivery data (list_waves, get_wave_stops, get_route);
// scheduling waves, planning routes, recording attempts and the partner
// lifecycle stay on REST and on the event consumers, and governance_test.go
// fails the build on a write-verb tool name. This package depends inward on
// the application layer and the domain only, never on an outbound adapter or
// the REST adapter, and nothing else may depend on it
// (internal/architecture's TestHexagonalArchitecture;
// TestMCPServerIsReadOnly pins that no write use case is reachable). The
// composition root (cmd/mcp) wires concrete repositories into the use cases.
// There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthIsRequired).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "last-mile-delivery-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Last-mile delivery of the retail network (read-only): list the delivery waves of a station for a " +
	"service date with list_waves (induct cutoff, delivery window, stop capacity, assigned and remaining stops, state); " +
	"see the packages a wave holds grouped into stops with get_wave_stops (the opaque stopKey, the destination postal " +
	"prefix when the label data had it, the tracking numbers); and read one route with get_route (its stops with their " +
	"derived outcome and every recorded delivery attempt). Nothing here changes data: scheduling a wave, planning a " +
	"route and recording an attempt are REST operations of last-mile-delivery; packages are inducted by the service's " +
	"event consumers."

// NewServer builds the MCP server for this bounded context with every tool
// registered. version identifies this build to MCP clients (cmd/mcp passes
// SERVICE_VERSION, defaulting to 1.0.0).
func NewServer(deps Deps, version string) *mcp.Server {
	if version == "" {
		version = serverVersion
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

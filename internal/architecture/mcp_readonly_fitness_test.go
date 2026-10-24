package architecture

// Read-only MCP sensor (docs/adr/0006, the fleet's MCP governance rule).
// The MCP server is an ADDITIVE, READ-ONLY inbound adapter: it can read
// waves, wave stops and routes and can never change them or emit an event.
// "Read-only" is pinned structurally, not by review:
//
//   - the adapter's Deps struct holds only the three read use cases;
//   - the adapter imports no write-capable seam (ports, the outbox, an event
//     sink, a unit of work);
//   - cmd/mcp wires only those three use cases and imports neither the events
//     encoder's Kafka package, the outbox relay, nor any Kafka adapter,
//     consumer or worker.
//
// TestMCPReadOnlySensorBites proves each detector flags what it should.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// readUseCases is the whole set of use cases the MCP surface may hold or wire.
var readUseCases = map[string]bool{"ListWaves": true, "GetWaveStops": true, "GetRoute": true}

// mcpAllowedUsecaseRefs are the non-use-case names of package usecases the MCP
// sources may refer to: the constructors of the read use cases and the Deps
// bundle they are built from. Everything else in package usecases is a write
// path (or a seam to one) and is refused.
var mcpAllowedUsecaseRefs = map[string]bool{
	"ListWaves": true, "GetWaveStops": true, "GetRoute": true,
	"NewListWaves": true, "NewGetWaveStops": true, "NewGetRoute": true,
	"Deps": true,
}

// mcpForbiddenImportParts are import-path fragments no MCP source may carry:
// the write-capable seams and every moving part of the write side. The ports
// package is NOT on this list: like the REST adapter's error mapping, the
// adapter imports it for the typed READ errors (ports.ErrNotFound) it maps to
// REST slugs; the write seams it carries are refused by name instead
// (mcpWriteSeamSelectors), which keeps the sensor's bite without diverging
// from the REST adapter's own vocabulary.
var mcpForbiddenImportParts = []string{
	"/internal/application/outbox",
	"/internal/adapters/outbound/outbox",
	"/internal/adapters/outbound/kafka",
	"/internal/adapters/outbound/memory",
	"/internal/adapters/inbound/kafka",
	"/internal/adapters/inbound/http",
	"segmentio/kafka-go",
}

// mcpWriteSeamSelectors are ports types that open a write path; no MCP source
// may even name them.
var mcpWriteSeamSelectors = []string{"ports.UnitOfWork", "ports.EventOutbox", "ports.Inbox"}

// mcpWriteSeamViolations returns one finding per write seam named in src.
func mcpWriteSeamViolations(filename string, src string) []string {
	var out []string
	for _, sel := range mcpWriteSeamSelectors {
		if strings.Contains(src, sel) {
			out = append(out, fmt.Sprintf("%s names %s, a write seam the read-only MCP surface may not touch", filename, sel))
		}
	}
	return out
}

// mcpImportViolations returns one finding per forbidden import of one source.
func mcpImportViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, part := range mcpForbiddenImportParts {
			if strings.Contains(path, part) {
				out = append(out, fmt.Sprintf("%s imports %s", filename, path))
			}
		}
	}
	return out, nil
}

// depsFieldViolations returns every field of the struct named Deps in src
// whose type is not *usecases.<read use case>.
func depsFieldViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if name, ok := usecaseName(field.Type); ok && readUseCases[name] {
				continue
			}
			names := make([]string, 0, len(field.Names))
			for _, id := range field.Names {
				names = append(names, id.Name)
			}
			out = append(out, fmt.Sprintf("%s: Deps field %s is not a read use case", filename, strings.Join(names, ",")))
		}
		return false
	})
	return out, nil
}

// usecaseName returns N for the type expression *usecases.N.
func usecaseName(e ast.Expr) (string, bool) {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "usecases" {
		return "", false
	}
	return sel.Sel.Name, true
}

// wiredUseCaseViolations returns every usecases.<Name> a source refers to that
// is not in mcpAllowedUsecaseRefs (the MCP sources must not even name a write
// use case).
func wiredUseCaseViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "usecases" && !mcpAllowedUsecaseRefs[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s refers to usecases.%s, which is not a read use case or a read input/output", filename, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

func mcpReadOnlyFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/mcp", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "mcp"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Fatalf("no non-test Go file under the MCP adapter (%d) or cmd/mcp (%d): the sensor would pass vacuously", len(adapter), len(command))
	}
	sort.Strings(adapter)
	sort.Strings(command)
	return adapter, command
}

// TestMCPServerIsReadOnly: the MCP adapter and cmd/mcp can reach no write
// path.
func TestMCPServerIsReadOnly(t *testing.T) {
	adapter, command := mcpReadOnlyFiles(t)
	for _, path := range append(append([]string{}, adapter...), command...) {
		found, err := mcpImportViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- the MCP surface may read through the use cases only (docs/adr/0006)", v)
		}
	}
	for _, path := range append(append([]string{}, adapter...), command...) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, v := range mcpWriteSeamViolations(path, string(src)) {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- the MCP surface may read through the use cases only (docs/adr/0006)", v)
		}
	}
	for _, path := range adapter {
		found, err := depsFieldViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- the MCP adapter may hold read use cases only (docs/adr/0006)", v)
		}
	}
	for _, path := range append(append([]string{}, adapter...), command...) {
		found, err := wiredUseCaseViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- wire only the read use cases (docs/adr/0006)", v)
		}
	}
}

func TestMCPReadOnlySensorBites(t *testing.T) {
	imports := []struct {
		name string
		src  string
		want int
	}{
		{"ports is allowed for read-error mapping", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/application/ports\"", 0},
		{"the outbox relay is flagged", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox\"", 1},
		{"a kafka adapter is flagged", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka\"", 1},
		{"a kafka consumer is flagged", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka\"", 1},
		{"kafka-go is flagged", "package x\nimport \"github.com/segmentio/kafka-go\"", 1},
		{"the memory doubles are flagged", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory\"", 1},
		{"the REST adapter is flagged", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http\"", 1},
		{"usecases and views pass", "package x\nimport (\n\"github.com/claudioed/last-mile-delivery/internal/application/usecases\"\n\"github.com/claudioed/last-mile-delivery/internal/application/views\"\n)", 0},
		{"the postgres adapter passes", "package x\nimport \"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres\"", 0},
	}
	for _, tc := range imports {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpImportViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	deps := []struct {
		name string
		src  string
		want int
	}{
		{"a write use case field is flagged", "package x\ntype Deps struct{ Schedule *usecases.ScheduleWave }", 1},
		{"a unit of work field is flagged", "package x\ntype Deps struct{ UoW ports.UnitOfWork }", 1},
		{"a non-pointer read use case is flagged", "package x\ntype Deps struct{ List usecases.ListWaves }", 1},
		{"the three read use cases pass", "package x\ntype Deps struct{\nA *usecases.ListWaves\nB *usecases.GetWaveStops\nC *usecases.GetRoute\n}", 0},
		{"other structs are not Deps", "package x\ntype Other struct{ W *usecases.ScheduleWave }", 0},
	}
	for _, tc := range deps {
		t.Run(tc.name, func(t *testing.T) {
			got, err := depsFieldViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	wired := []struct {
		name string
		src  string
		want int
	}{
		{"constructing a write use case is flagged", "package main\nvar _ = usecases.NewScheduleWave(d)", 1},
		{"naming the induction policy is flagged", "package main\nvar _ *usecases.InductOnArrival", 1},
		{"read use cases pass", "package main\nvar _ = usecases.NewGetRoute(d)\nvar _ = usecases.NewListWaves(d)", 0},
	}
	for _, tc := range wired {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wiredUseCaseViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	seams := []struct {
		name string
		src  string
		want int
	}{
		{"a unit of work is flagged", "package x\nvar _ ports.UnitOfWork", 1},
		{"the event outbox is flagged", "package x\nvar _ ports.EventOutbox", 1},
		{"the inbox is flagged", "package x\nvar _ ports.Inbox", 1},
		{"a read repository passes", "package x\nvar _ ports.WaveRepository", 0},
		{"a read error passes", "package x\nvar _ = ports.ErrNotFound", 0},
	}
	for _, tc := range seams {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpWriteSeamViolations("src.go", tc.src); len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}
}

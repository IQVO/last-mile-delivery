package architecture

// A2A inbound adapter sensors (docs/adr/0009). The A2A agent is an ADDITIVE,
// READ-ONLY inbound adapter like the MCP server: it can read waves and routes
// and can never change them, emit an event or call a sibling. That is pinned
// structurally, not by review:
//
//   - the dependency rule: the adapter depends on application + domain only
//     and nothing else depends on it;
//   - the adapter's Deps struct holds only the two read use cases it serves;
//   - the adapter and cmd/a2a name only those use cases (an ALLOWLIST of
//     usecases.X identifiers, so a new write use case is refused by default),
//     import no write-capable seam, no Kafka and no outbox;
//   - neither contains an outbound A2A client (this context calls no sibling).
//
// TestA2AReadOnlySensorBites proves each detector flags what it should.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestA2AAdapterDependencyRule: the A2A inbound adapter is additive, never
// load-bearing. It may depend only on the application and domain layers (never
// on outbound adapters, never on cmd), and nothing else in this codebase may
// depend on it; cmd/a2a wires it up and nothing else touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// a2aReadUseCases is the whole set of use cases the A2A surface may hold or
// wire: the two reads behind its two skills.
var a2aReadUseCases = map[string]bool{"ListWaves": true, "GetRoute": true}

// a2aAllowedUsecaseRefs are the names of package usecases the A2A sources may
// refer to: the read use cases, their constructors and the Deps bundle they are
// built from. Everything else in package usecases is a write path (or a seam to
// one) and is refused.
var a2aAllowedUsecaseRefs = map[string]bool{
	"ListWaves": true, "GetRoute": true, "NewListWaves": true, "NewGetRoute": true, "Deps": true,
}

// a2aForbiddenSnippets are constructs that must never appear in the A2A
// sources (comments stripped): an outbound A2A client (this context calls no
// sibling, in either direction) and the write seams.
var a2aForbiddenSnippets = map[string]string{
	"a2a-go/v2/a2aclient": "an outbound A2A client (AGENTS.md: calls no sibling)",
	"a2aclient.":          "an outbound A2A client (AGENTS.md: calls no sibling)",
	"ports.UnitOfWork":    "a transaction: a read-only agent never writes the outbox",
	"ports.EventOutbox":   "the event outbox: a read-only agent publishes no fact",
	"ports.Inbox":         "the consumer inbox: a read-only agent consumes no event",
}

var (
	lineCommentRE  = regexp.MustCompile(`(?m)//.*$`)
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripComments removes Go comments so a sensor reads code, not prose.
func stripComments(src string) string {
	return lineCommentRE.ReplaceAllString(blockCommentRE.ReplaceAllString(src, ""), "")
}

// a2aSnippetViolations returns one finding per forbidden construct in src.
func a2aSnippetViolations(filename, src string) []string {
	var out []string
	code := stripComments(src)
	for needle, why := range a2aForbiddenSnippets {
		if strings.Contains(code, needle) {
			out = append(out, fmt.Sprintf("%s names %s: %s", filename, needle, why))
		}
	}
	sort.Strings(out)
	return out
}

// a2aDepsViolations returns every field of the struct named Deps in src whose
// type is not *usecases.<A2A read use case>.
func a2aDepsViolations(filename string, src any) ([]string, error) {
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
		if st, ok := spec.Type.(*ast.StructType); ok {
			out = append(out, depsFieldFindings(filename, st)...)
		}
		return false
	})
	return out, nil
}

func depsFieldFindings(filename string, st *ast.StructType) []string {
	var out []string
	for _, field := range st.Fields.List {
		if name, ok := usecaseName(field.Type); ok && a2aReadUseCases[name] {
			continue
		}
		names := make([]string, 0, len(field.Names))
		for _, id := range field.Names {
			names = append(names, id.Name)
		}
		out = append(out, fmt.Sprintf("%s: Deps field %s is not an A2A read use case", filename, strings.Join(names, ",")))
	}
	return out
}

// a2aUsecaseRefViolations returns every usecases.<Name> a source refers to
// that is not in a2aAllowedUsecaseRefs.
func a2aUsecaseRefViolations(filename string, src any) ([]string, error) {
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
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "usecases" && !a2aAllowedUsecaseRefs[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s refers to usecases.%s, which is not an A2A read use case", filename, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

func a2aSourceFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/a2a", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "a2a"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Fatalf("no non-test Go file under the A2A adapter (%d) or cmd/a2a (%d): the sensor would pass vacuously", len(adapter), len(command))
	}
	return adapter, command
}

// checkA2ASource applies every per-file detector to one source file.
func checkA2ASource(t *testing.T, path string, isAdapter bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	found := a2aSnippetViolations(path, string(raw))
	imports, err := mcpImportViolations(path, nil)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found = append(found, imports...)
	refs, err := a2aUsecaseRefViolations(path, nil)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found = append(found, refs...)
	if isAdapter {
		deps, err := a2aDepsViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		found = append(found, deps...)
	}
	for _, v := range found {
		t.Errorf("READ-ONLY A2A VIOLATION: %s -- the A2A surface may read through the two read use cases only (docs/adr/0009)", v)
	}
}

// TestA2AAgentIsReadOnlyAndHasNoOutboundClient: the A2A adapter and cmd/a2a
// can reach no write path, import no Kafka or outbox adapter and hold no
// outbound A2A client.
func TestA2AAgentIsReadOnlyAndHasNoOutboundClient(t *testing.T) {
	adapter, command := a2aSourceFiles(t)
	for _, path := range adapter {
		checkA2ASource(t, path, true)
	}
	for _, path := range command {
		checkA2ASource(t, path, false)
	}
}

// TestA2AReadOnlySensorBites proves each detector flags what it should, so the
// sensor above cannot pass vacuously. Fixtures are code, not comments.
func TestA2AReadOnlySensorBites(t *testing.T) {
	snippets := []struct {
		name, src string
		want      int
	}{
		{"the SDK client import is flagged", "package x\nimport \"github.com/a2aproject/a2a-go/v2/a2aclient\"", 1},
		{"a client call is flagged", "package x\nvar _ = a2aclient.NewFromCard", 1},
		{"a unit of work is flagged", "package x\nvar _ ports.UnitOfWork", 1},
		{"the outbox is flagged", "package x\nvar _ ports.EventOutbox", 1},
		{"the inbox is flagged", "package x\nvar _ ports.Inbox", 1},
		{"a mention in a comment is not code", "package x\n// a2aclient.NewFromCard and ports.UnitOfWork are refused\n/* ports.Inbox */", 0},
		{"a read error passes", "package x\nvar _ = ports.ErrNotFound", 0},
	}
	for _, tc := range snippets {
		t.Run(tc.name, func(t *testing.T) {
			if got := a2aSnippetViolations("src.go", tc.src); len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}

	deps := []struct {
		name, src string
		want      int
	}{
		{"a write use case field is flagged", "package x\ntype Deps struct{ Schedule *usecases.ScheduleWave }", 1},
		{"a unit of work field is flagged", "package x\ntype Deps struct{ UoW ports.UnitOfWork }", 1},
		{"the MCP-only read is not an A2A skill", "package x\ntype Deps struct{ Stops *usecases.GetWaveStops }", 1},
		{"a non-pointer read use case is flagged", "package x\ntype Deps struct{ List usecases.ListWaves }", 1},
		{"the two read use cases pass", "package x\ntype Deps struct{\nA *usecases.ListWaves\nB *usecases.GetRoute\n}", 0},
		{"other structs are not Deps", "package x\ntype Other struct{ W *usecases.ScheduleWave }", 0},
	}
	for _, tc := range deps {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a2aDepsViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	refs := []struct {
		name, src string
		want      int
	}{
		{"constructing a write use case is flagged", "package main\nvar _ = usecases.NewScheduleWave(d)", 1},
		{"naming the induction policy is flagged", "package main\nvar _ *usecases.InductOnArrival", 1},
		{"a write command type is flagged", "package main\nvar _ usecases.RecordAttemptCommand", 1},
		{"the third MCP read is flagged", "package main\nvar _ = usecases.NewGetWaveStops(d)", 1},
		{"the read use cases pass", "package main\nvar _ = usecases.NewGetRoute(d)\nvar _ = usecases.NewListWaves(d)\nvar _ usecases.Deps", 0},
	}
	for _, tc := range refs {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a2aUsecaseRefViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}
}

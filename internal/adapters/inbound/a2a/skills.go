package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card). They are the MCP tool
// names of the same reads (docs/adr/0006), so one name identifies one question
// across both agent-facing surfaces.
const (
	skillListWaves = "list_waves"
	skillGetRoute  = "get_route"
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "stationnodeid"
// still reaches stationNodeId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// listWavesArgs and getRouteArgs are the per-skill argument shapes. Every
// argument is required, like the REST query parameters and path variables.
type listWavesArgs struct {
	StationNodeID string `json:"stationNodeId"`
	ServiceDate   string `json:"serviceDate"`
}

type getRouteArgs struct {
	RouteID string `json:"routeId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (docs/adr/0009).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillListWaves:
		data, problem := deps.listWaves(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Waves: &data, summary: summarizeWaves(data)}, nil
	case skillGetRoute:
		data, problem := deps.getRoute(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Route: &data, summary: summarizeRoute(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// requireArg is the required-argument check shared by both skills: a blank or
// absent value is an invalid-argument problem naming the argument. Whether the
// value is well formed is the use case's call (shared.ErrValidation).
func requireArg(name, value string) *problem {
	if strings.TrimSpace(value) == "" {
		return problemInvalidArgument(fmt.Errorf("%s is required", name))
	}
	return nil
}

// listWaves is the day-of-waves read skill, through the same ListWaves use
// case GET /waves and the MCP tool list_waves use.
func (d Deps) listWaves(ctx context.Context, rawArgs json.RawMessage) (wavesDTO, *problem) {
	var args listWavesArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return wavesDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	for _, check := range []*problem{requireArg("stationNodeId", args.StationNodeID), requireArg("serviceDate", args.ServiceDate)} {
		if check != nil {
			return wavesDTO{}, check
		}
	}
	waves, err := d.ListWaves.Execute(ctx, args.StationNodeID, args.ServiceDate)
	if err != nil {
		return wavesDTO{}, problemFromUseCase(err, skillListWaves, "")
	}
	return toWavesDTO(args.StationNodeID, args.ServiceDate, waves), nil
}

// getRoute is the single-route read skill, through the same GetRoute use case
// GET /routes/{routeId} and the MCP tool get_route use.
func (d Deps) getRoute(ctx context.Context, rawArgs json.RawMessage) (routeDTO, *problem) {
	var args getRouteArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return routeDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if check := requireArg("routeId", args.RouteID); check != nil {
		return routeDTO{}, check
	}
	r, err := d.GetRoute.Execute(ctx, route.ID(args.RouteID))
	if err != nil {
		return routeDTO{}, problemFromUseCase(err, skillGetRoute, args.RouteID)
	}
	return toRouteDTO(r), nil
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: ports.ErrNotFound is 404
// not-found, shared.ErrValidation is 422 validation-failed and anything
// unrecognised is 500 internal-error. (The 409 conflicts and the other 422
// slugs belong to writes this read-only agent does not offer.) The detail is
// adapter-owned text, so the shared errors' own wording never reaches an
// agent client. Domain rejections are EXPECTED business outcomes and are
// answered as problems; anything unrecognised is still answered as one, so
// the calling agent always receives a structured, machine-readable result.
func problemFromUseCase(err error, skill, id string) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return &problem{Type: "not-found", Title: "Resource not found", Detail: notFoundDetail(skill, id), Status: http.StatusNotFound}
	case errors.Is(err, shared.ErrValidation):
		return &problem{Type: "validation-failed", Title: "Validation failed", Detail: validationDetail(skill), Status: http.StatusUnprocessableEntity}
	default:
		slog.Error("a2a skill failed", "skill", skill, "error", err)
		return &problem{Type: "internal-error", Title: "Internal server error", Detail: "the skill failed; see server logs (trace id in the response metadata)", Status: http.StatusInternalServerError}
	}
}

func notFoundDetail(skill, id string) string {
	if skill == skillGetRoute {
		return fmt.Sprintf("no route exists with routeId %q", id)
	}
	return "the requested resource does not exist"
}

func validationDetail(skill string) string {
	if skill == skillListWaves {
		return "stationNodeId must be a station id and serviceDate a real calendar date written YYYY-MM-DD"
	}
	return "the skill arguments are not valid"
}

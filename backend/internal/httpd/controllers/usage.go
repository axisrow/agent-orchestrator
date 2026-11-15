package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// SessionMemoryService samples resident memory per live session.
type SessionMemoryService interface {
	ListMemory(context.Context, domain.ProjectID) ([]domain.SessionMemory, error)
	// SystemMemory reports host RAM for the panel's total bar. Returns
	// procmem.ErrUnsupported where the platform can't be read; callers omit
	// the bar rather than fail the whole request.
	SystemMemory(context.Context) (domain.SystemMemory, error)
	// AppMemory is everything AO runs, for the topbar pressure indicator.
	AppMemory(context.Context) (domain.AppMemory, error)
}

// MemoryPressureReader reads the machine's memory-pressure verdict alone:
// cheap enough to poll while no other memory figure is on screen.
type MemoryPressureReader interface {
	Pressure(context.Context) (procmem.Pressure, error)
}

// SessionStepsReader lists a session's recent tool calls, oldest first.
type SessionStepsReader interface {
	Steps(id domain.SessionID) []domain.SessionStep
}

// UsageController owns session memory usage routes.
type UsageController struct {
	Log    *slog.Logger
	Memory SessionMemoryService
	// Steps is optional: without it rows carry no activity.
	Steps SessionStepsReader
	// Pressure is optional: without it the pressure route is 501.
	Pressure MemoryPressureReader
}

// Register mounts usage routes on the supplied router.
func (c *UsageController) Register(r chi.Router) {
	r.Get("/usage/sessions/memory", c.listMemory)
	r.Get("/usage/memory/pressure", c.getPressure)
}

func (c *UsageController) listMemory(w http.ResponseWriter, r *http.Request) {
	if c.Memory == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions/memory")
		return
	}
	items, err := c.Memory.ListMemory(r.Context(), domain.ProjectID(r.URL.Query().Get("projectId")))
	if err != nil {
		if errors.Is(err, procmem.ErrUnsupported) {
			err = apierr.NotImplemented("MEMORY_UNSUPPORTED", err.Error())
		}
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]SessionMemoryResponse, 0, len(items))
	for _, item := range items {
		row := sessionMemoryResponse(item)
		if c.Steps != nil {
			row.Activity = sessionActivityResponse(c.Steps.Steps(item.SessionID))
		}
		out = append(out, row)
	}
	var system *SystemMemoryResponse
	if sys, sysErr := c.Memory.SystemMemory(r.Context()); sysErr == nil {
		system = &SystemMemoryResponse{
			TotalBytes: sys.TotalBytes, AvailableBytes: sys.AvailableBytes,
			SwapTotalBytes: sys.SwapTotalBytes, SwapUsedBytes: sys.SwapUsedBytes, SwapBytesPerSec: sys.SwapBytesPerSec,
			CPUCount: sys.CPUCount, Load1: sys.Load1, CPUPercent: sys.CPUPercent, CPUMeasured: sys.CPUMeasured,
			PressureRaw: sys.PressureRaw, PressureSource: sys.PressureSource,
		}
	}
	var app *AppMemoryResponse
	if a, appErr := c.Memory.AppMemory(r.Context()); appErr == nil {
		app = &AppMemoryResponse{RSSBytes: a.RSSBytes, ProcessCount: a.ProcessCount, CPUPercent: a.CPUPercent, CPUMeasured: a.CPUMeasured}
		if a.Own.ProcessCount > 0 {
			own := sessionMemoryResponse(a.Own)
			app.Own = &own
		}
		for _, rv := range a.Reviewers {
			app.Reviewers = append(app.Reviewers, ReviewerMemoryResponse{
				ReviewID: rv.ReviewID, SessionID: rv.SessionID, Harness: string(rv.Harness),
				Memory: sessionMemoryResponse(rv.Memory),
			})
		}
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionMemoryResponse{Sessions: out, System: system, App: app})
}

func (c *UsageController) getPressure(w http.ResponseWriter, r *http.Request) {
	if c.Pressure == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/memory/pressure")
		return
	}
	p, err := c.Pressure.Pressure(r.Context())
	if err != nil {
		if errors.Is(err, procmem.ErrUnsupported) {
			err = apierr.NotImplemented("MEMORY_UNSUPPORTED", err.Error())
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MemoryPressureResponse{PressureRaw: p.Raw, PressureSource: p.Source})
}

// recentStepsShown is how many finished steps a row lists; the window is a
// glance, not a log.
const recentStepsShown = 5

// sessionActivityResponse splits the ring into the step still running and the
// last few finished ones, newest first. Nil when the harness reported none.
func sessionActivityResponse(steps []domain.SessionStep) *SessionActivityResponse {
	if len(steps) == 0 {
		return nil
	}
	out := &SessionActivityResponse{Recent: []SessionStepResponse{}}
	for i := len(steps) - 1; i >= 0; i-- {
		step := stepResponse(steps[i])
		if steps[i].EndedAt.IsZero() {
			if out.Current == nil {
				out.Current = &step
			}
			continue
		}
		if len(out.Recent) < recentStepsShown {
			out.Recent = append(out.Recent, step)
		}
	}
	return out
}

func stepResponse(step domain.SessionStep) SessionStepResponse {
	out := SessionStepResponse{Tool: step.Tool, StartedAt: step.StartedAt, Failed: step.Failed}
	if !step.EndedAt.IsZero() {
		ended := step.EndedAt
		out.EndedAt = &ended
	}
	return out
}

func sessionMemoryResponse(item domain.SessionMemory) SessionMemoryResponse {
	procs := make([]SessionMemoryProcessResponse, 0, len(item.Processes))
	for _, p := range item.Processes {
		procs = append(procs, SessionMemoryProcessResponse{PID: p.PID, PPID: p.PPID, RSSBytes: p.RSSBytes, CPUPercent: p.CPUPercent, Command: p.Command})
	}
	return SessionMemoryResponse{
		SessionID: item.SessionID, RSSBytes: item.RSSBytes, ProcessCount: item.ProcessCount, CPUPercent: item.CPUPercent,
		SampledAt: item.SampledAt, Processes: procs,
	}
}

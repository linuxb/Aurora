package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aurora/apps/arqo/internal/events"
	"aurora/apps/arqo/internal/scheduler"
)

type Server struct {
	store  *scheduler.Store
	broker *events.Broker
}

func NewServer(store *scheduler.Store, broker *events.Broker) *Server {
	return &Server{store: store, broker: broker}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sessions/{sessionID}", s.getSession)
	mux.HandleFunc("GET /v1/sessions/{sessionID}/events", s.streamSessionEvents)
	mux.HandleFunc("POST /v1/tasks/pull", s.pullTask)
	mux.HandleFunc("POST /v1/tasks/{taskID}/complete", s.completeTask)
	mux.HandleFunc("POST /v1/telemetry", s.ingestTelemetry)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"service": "arqo", "status": "ok"})
}

type createSessionRequest struct {
	UserID string `json:"user_id"`
	Intent string `json:"intent"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Intent) == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "user_id and intent are required")
		return
	}

	snapshot := s.store.CreateDemoSession(req.UserID, req.Intent)
	s.publishEvent(events.Event{
		SessionID: snapshot.Session.SessionID,
		EventType: "SESSION_CREATED",
		Message:   "session created",
		Source:    "arqo",
		At:        time.Now().UTC(),
	})
	respondJSON(w, http.StatusCreated, snapshot)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	snapshot, err := s.store.GetSessionSnapshot(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

type pullTaskRequest struct {
	WorkerID string `json:"worker_id"`
}

func (s *Server) pullTask(w http.ResponseWriter, r *http.Request) {
	var req pullTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "worker_id is required")
		return
	}

	task, err := s.store.PullReadyTask(req.WorkerID, 60*time.Second)
	if err != nil {
		if errors.Is(err, scheduler.ErrNoReadyTask) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respondError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	if sessionID, ok := s.store.ResolveSessionIDByTaskID(task.TaskID); ok {
		s.publishEvent(events.Event{
			SessionID: sessionID,
			EventType: "TASK_LEASED",
			TaskID:    task.TaskID,
			Message:   fmt.Sprintf("task leased by worker=%s", req.WorkerID),
			Source:    "arqo",
			At:        time.Now().UTC(),
		})
	}

	respondJSON(w, http.StatusOK, task)
}

type completeTaskRequest struct {
	WorkerID     string `json:"worker_id"`
	Success      bool   `json:"success"`
	Summary      string `json:"summary"`
	RawData      any    `json:"raw_data"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

func (s *Server) completeTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	var req completeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "worker_id is required")
		return
	}

	task, err := s.store.CompleteTask(scheduler.CompleteTaskInput{
		TaskID:       taskID,
		WorkerID:     req.WorkerID,
		Success:      req.Success,
		Summary:      req.Summary,
		RawData:      req.RawData,
		ErrorCode:    req.ErrorCode,
		ErrorMessage: req.ErrorMessage,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, scheduler.ErrTaskNotFound):
			status = http.StatusNotFound
		case errors.Is(err, scheduler.ErrTaskNotRunnable):
			status = http.StatusConflict
		}
		respondError(w, status, "task_completion_failed", err.Error())
		return
	}

	if sessionID, ok := s.store.ResolveSessionIDByTaskID(task.TaskID); ok {
		eventType := "TASK_COMPLETED"
		if !req.Success {
			eventType = "TASK_FAILED"
		}
		s.publishEvent(events.Event{
			SessionID: sessionID,
			EventType: eventType,
			TaskID:    task.TaskID,
			Message:   req.Summary,
			Source:    "arqo",
			At:        time.Now().UTC(),
		})
	}

	respondJSON(w, http.StatusOK, task)
}

type telemetryRequest struct {
	SessionID string `json:"session_id"`
	EventType string `json:"event_type"`
	TaskID    string `json:"task_id"`
	Message   string `json:"message"`
	Source    string `json:"source"`
	At        string `json:"at"`
}

func (s *Server) ingestTelemetry(w http.ResponseWriter, r *http.Request) {
	var req telemetryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	req.EventType = strings.TrimSpace(req.EventType)
	req.Message = strings.TrimSpace(req.Message)
	req.Source = strings.TrimSpace(req.Source)

	if req.EventType == "" || req.Message == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "event_type and message are required")
		return
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" && strings.TrimSpace(req.TaskID) != "" {
		if resolved, ok := s.store.ResolveSessionIDByTaskID(req.TaskID); ok {
			sessionID = resolved
		}
	}
	if sessionID == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "session_id is required or resolvable from task_id")
		return
	}

	evtAt := time.Now().UTC()
	if strings.TrimSpace(req.At) != "" {
		if parsedAt, err := time.Parse(time.RFC3339Nano, req.At); err == nil {
			evtAt = parsedAt.UTC()
		}
	}

	if req.Source == "" {
		req.Source = "worker"
	}

	s.publishEvent(events.Event{
		SessionID: sessionID,
		EventType: req.EventType,
		TaskID:    req.TaskID,
		Message:   req.Message,
		Source:    req.Source,
		At:        evtAt,
	})

	respondJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	if _, err := s.store.GetSessionSnapshot(sessionID); err != nil {
		respondError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.broker.Subscribe(sessionID)
	defer cancel()

	_, _ = fmt.Fprint(w, ": stream started\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			payload, _ := json.Marshal(evt)
			_, _ = fmt.Fprintf(w, "event: %s\n", evt.EventType)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (s *Server) publishEvent(evt events.Event) {
	if s.broker == nil {
		return
	}
	s.broker.Publish(evt)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, code, msg string) {
	respondJSON(w, status, map[string]string{
		"code":    code,
		"message": msg,
	})
}

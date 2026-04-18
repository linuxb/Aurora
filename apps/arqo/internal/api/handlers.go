package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"aurora/apps/arqo/internal/scheduler"
)

type Server struct {
	store *scheduler.Store
}

func NewServer(store *scheduler.Store) *Server {
	return &Server{store: store}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sessions/{sessionID}", s.getSession)
	mux.HandleFunc("POST /v1/tasks/pull", s.pullTask)
	mux.HandleFunc("POST /v1/tasks/{taskID}/complete", s.completeTask)
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
			respondJSON(w, http.StatusNoContent, nil)
			return
		}
		respondError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
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
	respondJSON(w, http.StatusOK, task)
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

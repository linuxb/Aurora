package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aurora/apps/flory/internal/events"
	"aurora/apps/flory/internal/model"
	"aurora/apps/flory/internal/planner"
	"aurora/apps/flory/internal/scheduler"
)

type Server struct {
	store   scheduler.Engine
	broker  events.Broker
	planner planner.Router
	memory  MemoryClient
}

func NewServer(store scheduler.Engine, broker events.Broker) *Server {
	return &Server{
		store:   store,
		broker:  broker,
		planner: planner.NewMockRouter(),
		memory:  NewMem3MemoryClientFromEnv(),
	}
}

func NewServerWithPlanner(store scheduler.Engine, broker events.Broker, dagPlanner planner.Router) *Server {
	return NewServerWithPlannerAndMemory(store, broker, dagPlanner, NewMem3MemoryClientFromEnv())
}

func NewServerWithPlannerAndMemory(store scheduler.Engine, broker events.Broker, dagPlanner planner.Router, memory MemoryClient) *Server {
	if dagPlanner == nil {
		dagPlanner = planner.NewMockRouter()
	}
	return &Server{store: store, broker: broker, planner: dagPlanner, memory: memory}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("GET /v1/sessions/{sessionID}", s.getSession)
	mux.HandleFunc("GET /v1/sessions/{sessionID}/events", s.streamSessionEvents)
	mux.HandleFunc("POST /v1/sessions/{sessionID}/replan", s.applyReplanPatch)
	mux.HandleFunc("POST /v1/tasks/pull", s.pullTask)
	mux.HandleFunc("POST /v1/tasks/{taskID}/complete", s.completeTask)
	mux.HandleFunc("POST /v1/telemetry", s.ingestTelemetry)
	mux.HandleFunc("POST /v1/admin/sweep-expired", s.sweepExpired)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"service": "flory", "status": "ok"})
}

type createSessionRequest struct {
	UserID       string `json:"user_id"`
	TenantID     string `json:"tenant_id"`
	AgentID      string `json:"agent_id"`
	Intent       string `json:"intent"`
	PlanningMode string `json:"planning_mode"`
}

type createSessionResponse struct {
	scheduler.Snapshot
	Plan planner.Plan `json:"plan"`
}

func toSessionTaskSpecs(nodes []planner.Node) []scheduler.SessionTaskSpec {
	specs := make([]scheduler.SessionTaskSpec, 0, len(nodes))
	for _, node := range nodes {
		specs = append(specs, scheduler.SessionTaskSpec{
			RefID:        node.NodeID,
			NodeType:     node.NodeType,
			SkillName:    node.SkillName,
			Goal:         node.Goal,
			MemHint:      node.MemHint,
			Parameters:   node.Parameters,
			Dependencies: node.Dependencies,
		})
	}
	return specs
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

	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = req.UserID
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "aurora-default"
	}
	identity, err := scheduler.NewSessionIdentity()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "identity_generation_failed", err.Error())
		return
	}
	intentContext := map[string]any{"original_intent": req.Intent}
	if extractor, ok := s.planner.(planner.IntentExtractor); ok {
		intentContext, err = extractor.ExtractIntent(req.Intent, req.PlanningMode)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "intent_extraction_failed", err.Error())
			return
		}
	}
	scope := Mem3Scope{
		TenantID: tenantID, AgentID: agentID, UserID: req.UserID,
		SessionID: identity.SessionID, DAGID: identity.DAGID,
	}
	if s.memory != nil {
		err = s.memory.Ingest(r.Context(), Mem3IngestRequest{
			Version:        "1.0",
			IdempotencyKey: "dag-context:" + identity.DAGID,
			Kind:           "DAG_CONTEXT",
			Scope:          scope,
			Payload: Mem3DAGContext{
				RawQuery: req.Intent, IntentSlot: intentSlotFromContext(intentContext), ObservedAt: time.Now().UTC(),
			},
		})
		if err != nil {
			respondError(w, http.StatusBadGateway, "mem3_ingest_failed", err.Error())
			return
		}
	}

	var plan planner.Plan
	if contextual, ok := s.planner.(planner.ContextPlanner); ok {
		plan, err = contextual.PlanWithContext(req.Intent, req.PlanningMode, intentContext)
	} else {
		plan, err = s.planner.Plan(req.Intent, req.PlanningMode)
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "plan_generation_failed", err.Error())
		return
	}
	if plan.IntentContext == nil {
		plan.IntentContext = intentContext
	}
	validation := planner.ValidateDAG(plan.Nodes)
	plan.Warnings = validation.Warnings
	if !validation.Valid {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"code":     "invalid_dag_plan",
			"message":  "planner produced an invalid DAG plan",
			"plan":     plan,
			"errors":   validation.Errors,
			"warnings": validation.Warnings,
		})
		return
	}

	var snapshot scheduler.Snapshot
	snapshot, err = s.store.CreateSessionFromPreparedPlan(scheduler.CreateSessionPlanInput{
		Identity: identity, TenantID: tenantID, AgentID: agentID, UserID: req.UserID,
		Intent: req.Intent, IntentContext: plan.IntentContext, Tasks: toSessionTaskSpecs(plan.Nodes),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "create_session_failed", err.Error())
		return
	}
	s.publishEvent(r.Context(), events.Event{
		SessionID: snapshot.Session.SessionID,
		EventType: "SESSION_CREATED",
		Message:   "session created",
		Source:    "flory",
		At:        time.Now().UTC(),
	})
	respondJSON(w, http.StatusCreated, createSessionResponse{
		Snapshot: snapshot,
		Plan:     plan,
	})
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
		task, err = s.enrichTaskWithMemory(r.Context(), task, sessionID)
		if err != nil {
			respondError(w, http.StatusBadGateway, "mem3_search_failed", err.Error())
			return
		}
		s.publishEvent(r.Context(), events.Event{
			SessionID: sessionID,
			EventType: "TASK_LEASED",
			TaskID:    task.TaskID,
			Message:   fmt.Sprintf("task leased by worker=%s", req.WorkerID),
			Source:    "flory",
			At:        time.Now().UTC(),
		})
	}

	respondJSON(w, http.StatusOK, task)
}

func (s *Server) enrichTaskWithMemory(ctx context.Context, task *model.Task, sessionID string) (*model.Task, error) {
	if task == nil || s.memory == nil {
		return task, nil
	}
	snapshot, err := s.store.GetSessionSnapshot(sessionID)
	if err != nil {
		return task, err
	}
	if task.Parameters == nil {
		task.Parameters = map[string]any{}
	}
	memHint := memHintFromTask(task)
	if len(task.Dependencies) > 0 {
		memHint = buildMemHintFromDependencies(snapshot.RawData, task.Dependencies, memHint)
	}
	task.MemHint = memHintToMap(memHint)
	task.Parameters["mem_hint"] = memHint
	response, searchErr := s.memory.Search(ctx, Mem3SearchRequest{
		Version: "1.0",
		Scope: Mem3Scope{
			TenantID: snapshot.DAG.TenantID, AgentID: snapshot.DAG.AgentID,
			UserID: snapshot.Session.UserID, SessionID: sessionID, DAGID: snapshot.DAG.DAGID,
		},
		CurrentTask: Mem3CurrentTask{
			TaskID: task.TaskID, Sequence: task.Sequence, NodeType: string(task.NodeType),
			ParentTaskIDs: task.Dependencies, MemHintSourceTaskIDs: task.Dependencies,
		},
		RecentLimit: 5,
		MemHint:     memHint,
	})
	if searchErr != nil {
		return task, searchErr
	}
	task.Parameters["working_memory"] = response.WorkingMemory
	task.Parameters["memory_retrieval"] = response.Retrieval
	task.Parameters["memory_consistency"] = response.Consistency
	return task, nil
}

func buildMemHintFromDependencies(rawData map[string]any, depTaskIDs []string, initial scheduler.MemHint) scheduler.MemHint {
	semanticParts := make([]string, 0, len(depTaskIDs))
	for _, depID := range depTaskIDs {
		v, ok := rawData[depID]
		if !ok {
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			if summary, ok := typed["summary"]; ok {
				semanticParts = append(semanticParts, fmt.Sprintf("%v", summary))
				continue
			}
			if markdown, ok := typed["markdown"]; ok {
				semanticParts = append(semanticParts, fmt.Sprintf("%v", markdown))
				continue
			}
			semanticParts = append(semanticParts, fmt.Sprintf("%v", typed))
		case string:
			semanticParts = append(semanticParts, typed)
		default:
			semanticParts = append(semanticParts, fmt.Sprintf("%v", typed))
		}
	}
	semanticQuery := strings.TrimSpace(strings.Join(semanticParts, " ; "))
	if semanticQuery == "" {
		semanticQuery = "recent dependency context"
	}
	initial.Version = "1.0"
	if initial.TargetSystem == "" {
		initial.TargetSystem = "AUTO"
	}
	initial.Strategy = scheduler.NormalizeMemHintStrategy(inferHintStrategy(semanticQuery))
	initial.Query.Text = semanticQuery
	if initial.Strategy == scheduler.MemHintStrategyKVPointGet {
		initial.Query.TargetTaskID = firstNonEmpty(depTaskIDs)
	}
	return initial
}

func firstNonEmpty(items []string) string {
	for _, v := range items {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func intentSlotFromContext(intentContext map[string]any) Mem3IntentSlot {
	return Mem3IntentSlot{
		MacroIntent:     stringValue(intentContext["macro_intent"]),
		Entities:        stringSliceValue(intentContext["entities"]),
		TemporalContext: stringValue(intentContext["temporal_context"]),
		ActionVerbs:     stringSliceValue(intentContext["action_verbs"]),
		ExtractionHint:  stringValue(intentContext["extraction_hint"]),
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return []string{}
	}
}

func memHintFromTask(task *model.Task) scheduler.MemHint {
	hint := scheduler.DefaultMemHint()
	if task == nil || len(task.MemHint) == 0 {
		return hint
	}
	raw, err := json.Marshal(task.MemHint)
	if err != nil {
		return hint
	}
	if err := json.Unmarshal(raw, &hint); err != nil {
		return scheduler.DefaultMemHint()
	}
	_ = scheduler.ValidateMemHint(&hint)
	return hint
}

func memHintToMap(hint scheduler.MemHint) map[string]any {
	raw, _ := json.Marshal(hint)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	delete(out, "target_step_id")
	delete(out, "semantic_query")
	return out
}

func inferHintStrategy(query string) string {
	lower := strings.ToLower(strings.TrimSpace(query))
	if strings.Contains(lower, "relation") || strings.Contains(lower, "dependency") || strings.Contains(lower, "impact") {
		return string(scheduler.MemHintStrategyGraphLocal)
	}
	if strings.Contains(lower, "task ") || strings.Contains(lower, "step ") {
		return string(scheduler.MemHintStrategyKVPointGet)
	}
	return string(scheduler.MemHintStrategyNone)
}

type completeTaskRequest struct {
	Status           string                      `json:"status"`
	WorkerID         string                      `json:"worker_id"`
	Success          bool                        `json:"success"`
	Summary          string                      `json:"summary"`
	RawData          any                         `json:"raw_data"`
	ErrorCode        string                      `json:"error_code"`
	ErrorMessage     string                      `json:"error_message"`
	ExpansionPayload *scheduler.ExpansionPayload `json:"expansion_payload"`
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
	if strings.EqualFold(strings.TrimSpace(req.Status), "SUCCESS_AND_EXPAND") {
		req.Success = true
		if req.ExpansionPayload == nil {
			respondError(w, http.StatusBadRequest, "invalid_argument", "expansion_payload is required for SUCCESS_AND_EXPAND")
			return
		}
	}

	task, err := s.store.CompleteTask(scheduler.CompleteTaskInput{
		TaskID:           taskID,
		WorkerID:         req.WorkerID,
		Success:          req.Success,
		Summary:          req.Summary,
		RawData:          req.RawData,
		ErrorCode:        req.ErrorCode,
		ErrorMessage:     req.ErrorMessage,
		ExpansionPayload: req.ExpansionPayload,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, scheduler.ErrTaskNotFound):
			status = http.StatusNotFound
		case errors.Is(err, scheduler.ErrTaskNotRunnable):
			status = http.StatusConflict
		case errors.Is(err, scheduler.ErrExpansionInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, scheduler.ErrExpansionNotAllowed):
			status = http.StatusConflict
		case errors.Is(err, scheduler.ErrExpansionDepthExceeded):
			status = http.StatusConflict
		case errors.Is(err, scheduler.ErrSkillMappingExhausted):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, scheduler.ErrExpansionNotImplemented):
			status = http.StatusNotImplemented
		}
		code := "task_completion_failed"
		if errors.Is(err, scheduler.ErrSkillMappingExhausted) {
			code = "missing_skill"
		}
		respondError(w, status, code, err.Error())
		return
	}

	if sessionID, ok := s.store.ResolveSessionIDByTaskID(task.TaskID); ok {
		if req.Success && s.memory != nil {
			snapshot, snapshotErr := s.store.GetSessionSnapshot(sessionID)
			if snapshotErr == nil {
				ingestErr := s.memory.Ingest(r.Context(), Mem3IngestRequest{
					Version:        "1.0",
					IdempotencyKey: "task-output:" + task.TaskID,
					Kind:           "TASK_OUTPUT",
					Scope: Mem3Scope{
						TenantID: snapshot.DAG.TenantID, AgentID: snapshot.DAG.AgentID,
						UserID: snapshot.Session.UserID, SessionID: sessionID, DAGID: task.DAGID,
					},
					Payload: Mem3TaskOutput{
						TaskID: task.TaskID, ParentTaskIDs: task.Dependencies, Sequence: task.Sequence,
						NodeType: string(task.NodeType), SkillName: task.SkillName, Output: req.RawData,
						SkillSummary: req.Summary, CompletedAt: time.Now().UTC(),
					},
				})
				if ingestErr != nil {
					respondError(w, http.StatusBadGateway, "mem3_ingest_failed", ingestErr.Error())
					return
				}
			}
		}
		eventType := "TASK_COMPLETED"
		if !req.Success {
			eventType = "TASK_FAILED"
		} else if req.ExpansionPayload != nil {
			eventType = "TASK_EXPANDED"
		}
		s.publishEvent(r.Context(), events.Event{
			SessionID: sessionID,
			EventType: eventType,
			TaskID:    task.TaskID,
			Message:   req.Summary,
			Source:    "flory",
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

type replanPatchRequest struct {
	Reason string         `json:"reason"`
	Tasks  []replanTaskIn `json:"tasks"`
}

type replanTaskIn struct {
	RefID        string            `json:"ref_id"`
	NodeType     string            `json:"node_type"`
	SkillName    string            `json:"skill_name"`
	Goal         string            `json:"goal"`
	MemHint      scheduler.MemHint `json:"mem_hint"`
	Parameters   map[string]any    `json:"parameters"`
	Dependencies []string          `json:"dependencies"`
}

func (s *Server) applyReplanPatch(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	var req replanPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if sessionID == "" {
		respondError(w, http.StatusBadRequest, "invalid_argument", "session_id is required")
		return
	}
	if len(req.Tasks) == 0 {
		respondError(w, http.StatusBadRequest, "invalid_argument", "tasks are required")
		return
	}
	specs := make([]scheduler.SessionTaskSpec, 0, len(req.Tasks))
	for _, task := range req.Tasks {
		nt, err := model.ParseNodeType(task.NodeType)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			return
		}
		specs = append(specs, scheduler.SessionTaskSpec{
			RefID:        task.RefID,
			NodeType:     nt,
			SkillName:    task.SkillName,
			Goal:         task.Goal,
			MemHint:      task.MemHint,
			Parameters:   task.Parameters,
			Dependencies: task.Dependencies,
		})
	}

	snapshot, err := s.store.ApplyReplanPatch(sessionID, specs, req.Reason)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, scheduler.ErrReplanNotAllowed) {
			status = http.StatusConflict
		}
		respondError(w, status, "replan_patch_failed", err.Error())
		return
	}
	s.publishEvent(r.Context(), events.Event{
		SessionID: sessionID,
		EventType: "DAG_REPLAN_APPLIED",
		Message:   "replan patch applied",
		Source:    "flory",
		At:        time.Now().UTC(),
	})
	respondJSON(w, http.StatusOK, snapshot)
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

	s.publishEvent(r.Context(), events.Event{
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

	ch, err := s.broker.Subscribe(r.Context(), sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "subscribe_failed", err.Error())
		return
	}

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

func (s *Server) sweepExpired(w http.ResponseWriter, r *http.Request) {
	expired := s.store.ExpireRunningTasks(time.Now().UTC())
	for _, taskID := range expired {
		sessionID, ok := s.store.ResolveSessionIDByTaskID(taskID)
		if !ok {
			continue
		}
		s.publishEvent(r.Context(), events.Event{
			SessionID: sessionID,
			EventType: "TASK_SWEEP_EXPIRED",
			TaskID:    taskID,
			Message:   "task lease expired and recovery policy applied",
			Source:    "flory",
			At:        time.Now().UTC(),
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"expired_task_ids": expired,
		"count":            len(expired),
	})
}

func (s *Server) publishEvent(ctx context.Context, evt events.Event) {
	if s.broker == nil {
		return
	}
	_ = s.broker.Publish(ctx, evt)
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

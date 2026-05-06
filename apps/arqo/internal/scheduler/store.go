package scheduler

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"aurora/apps/arqo/internal/model"
)

var (
	ErrNoReadyTask     = errors.New("no ready task")
	ErrTaskNotFound    = errors.New("task not found")
	ErrTaskNotRunnable = errors.New("task is not running under this worker")
)

type CompleteTaskInput struct {
	TaskID           string
	WorkerID         string
	Success          bool
	Summary          string
	RawData          any
	ErrorCode        string
	ErrorMessage     string
	ExpansionPayload *ExpansionPayload
}

type Snapshot struct {
	Session model.Session  `json:"session"`
	DAG     model.DAG      `json:"dag"`
	Tasks   []model.Task   `json:"tasks"`
	RawData map[string]any `json:"raw_data"`
}

type Store struct {
	mu sync.Mutex

	leaseExpirePolicy LeaseExpirePolicy

	sessionCounter atomic.Uint64
	dagCounter     atomic.Uint64
	taskCounter    atomic.Uint64

	sessions     map[string]model.Session
	dags         map[string]model.DAG
	tasksByID    map[string]*model.Task
	tasksByDAG   map[string][]string
	rawDataByDAG map[string]map[string]any
}

func NewStore() *Store {
	return NewStoreWithLeasePolicy(LeaseExpirePolicyFailedReplan)
}

func NewStoreWithLeasePolicy(policy LeaseExpirePolicy) *Store {
	return &Store{
		leaseExpirePolicy: parseLeaseExpirePolicy(string(policy)),
		sessions:     make(map[string]model.Session),
		dags:         make(map[string]model.DAG),
		tasksByID:    make(map[string]*model.Task),
		tasksByDAG:   make(map[string][]string),
		rawDataByDAG: make(map[string]map[string]any),
	}
}

func (s *Store) CreateDemoSession(userID, intent string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := fmt.Sprintf("sess_%06d", s.sessionCounter.Add(1))
	dagID := fmt.Sprintf("dag_%06d", s.dagCounter.Add(1))
	now := time.Now().UTC()

	session := model.Session{
		SessionID: sessionID,
		DAGID:     dagID,
		UserID:    userID,
		Intent:    intent,
		CreatedAt: now,
	}
	dag := model.DAG{
		DAGID:             dagID,
		SessionID:         sessionID,
		UserID:            userID,
		OriginalIntent:    intent,
		IntentContext:     map[string]any{"original_intent": intent, "source": "demo"},
		Status:            model.DAGStatusRunning,
		CurrentDepth:      1,
		MaxDepth:          10,
		JITUnmappedStreak: 0,
		MaxUnmappedStreak: 3,
		CreatedAt:         now,
	}

	queryTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	summaryTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	mailTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))

	queryTask := &model.Task{
		TaskID:                   queryTaskID,
		DAGID:                    dagID,
		NodeType:                 model.NodeTypeSkillSink,
		SkillName:                "QueryLog",
		Status:                   model.TaskStatusReady,
		PendingDependenciesCount: 0,
		Dependencies:             []string{},
		Children:                 []string{summaryTaskID},
	}
	summaryTask := &model.Task{
		TaskID:                   summaryTaskID,
		DAGID:                    dagID,
		NodeType:                 model.NodeTypeSkillSink,
		SkillName:                "LLMSummarize",
		Status:                   model.TaskStatusPending,
		PendingDependenciesCount: 1,
		Dependencies:             []string{queryTaskID},
		Children:                 []string{mailTaskID},
	}
	mailTask := &model.Task{
		TaskID:                   mailTaskID,
		DAGID:                    dagID,
		NodeType:                 model.NodeTypeSkillSink,
		SkillName:                "SendEmail",
		Status:                   model.TaskStatusPending,
		PendingDependenciesCount: 1,
		Dependencies:             []string{summaryTaskID},
		Children:                 []string{},
	}

	s.sessions[sessionID] = session
	s.dags[dagID] = dag
	s.tasksByID[queryTaskID] = queryTask
	s.tasksByID[summaryTaskID] = summaryTask
	s.tasksByID[mailTaskID] = mailTask
	s.tasksByDAG[dagID] = []string{queryTaskID, summaryTaskID, mailTaskID}
	s.rawDataByDAG[dagID] = make(map[string]any)

	return s.snapshotLocked(sessionID), nil
}

func (s *Store) CreateJITDemoSession(userID, intent string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := fmt.Sprintf("sess_%06d", s.sessionCounter.Add(1))
	dagID := fmt.Sprintf("dag_%06d", s.dagCounter.Add(1))
	now := time.Now().UTC()

	plannerTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	finalTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))

	session := model.Session{
		SessionID: sessionID,
		DAGID:     dagID,
		UserID:    userID,
		Intent:    intent,
		CreatedAt: now,
	}
	dag := model.DAG{
		DAGID:             dagID,
		SessionID:         sessionID,
		UserID:            userID,
		OriginalIntent:    intent,
		IntentContext:     map[string]any{"original_intent": intent, "source": "jit_demo"},
		Status:            model.DAGStatusRunning,
		CurrentDepth:      1,
		MaxDepth:          10,
		JITUnmappedStreak: 0,
		MaxUnmappedStreak: 3,
		CreatedAt:         now,
	}
	plannerTask := &model.Task{
		TaskID:                   plannerTaskID,
		DAGID:                    dagID,
		NodeType:                 model.NodeTypeExpandPlanning,
		SkillName:                "ReActPlanner",
		Status:                   model.TaskStatusReady,
		PendingDependenciesCount: 0,
		Dependencies:             []string{},
		Children:                 []string{finalTaskID},
		Parameters:               map[string]any{"intent_context": dag.IntentContext},
	}
	finalTask := &model.Task{
		TaskID:                   finalTaskID,
		DAGID:                    dagID,
		NodeType:                 model.NodeTypeSkillSink,
		SkillName:                "SendEmail",
		Status:                   model.TaskStatusPending,
		PendingDependenciesCount: 1,
		Dependencies:             []string{plannerTaskID},
		Children:                 []string{},
	}

	s.sessions[sessionID] = session
	s.dags[dagID] = dag
	s.tasksByID[plannerTaskID] = plannerTask
	s.tasksByID[finalTaskID] = finalTask
	s.tasksByDAG[dagID] = []string{plannerTaskID, finalTaskID}
	s.rawDataByDAG[dagID] = make(map[string]any)

	return s.snapshotLocked(sessionID), nil
}

func (s *Store) CreateSessionFromPlan(userID, intent string, intentContext map[string]any, tasks []SessionTaskSpec) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := fmt.Sprintf("sess_%06d", s.sessionCounter.Add(1))
	dagID := fmt.Sprintf("dag_%06d", s.dagCounter.Add(1))
	now := time.Now().UTC()

	session := model.Session{
		SessionID: sessionID,
		DAGID:     dagID,
		UserID:    userID,
		Intent:    intent,
		CreatedAt: now,
	}
	dag := model.DAG{
		DAGID:             dagID,
		SessionID:         sessionID,
		UserID:            userID,
		OriginalIntent:    intent,
		IntentContext:     cloneMap(intentContext),
		Status:            model.DAGStatusRunning,
		CurrentDepth:      1,
		MaxDepth:          10,
		JITUnmappedStreak: 0,
		MaxUnmappedStreak: 3,
		CreatedAt:         now,
	}

	refToTaskID := make(map[string]string, len(tasks))
	for _, spec := range tasks {
		refToTaskID[spec.RefID] = fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	}

	resolvedChildren := make(map[string][]string, len(tasks))
	for _, spec := range tasks {
		for _, depRef := range spec.Dependencies {
			resolvedChildren[depRef] = append(resolvedChildren[depRef], spec.RefID)
		}
	}

	s.sessions[sessionID] = session
	s.dags[dagID] = dag
	s.rawDataByDAG[dagID] = make(map[string]any)
	s.tasksByDAG[dagID] = make([]string, 0, len(tasks))

	for _, spec := range tasks {
		nodeType := spec.NodeType
		parsedNodeType, err := model.ParseNodeType(string(nodeType))
		if err != nil {
			return Snapshot{}, err
		}
		taskID := refToTaskID[spec.RefID]
		deps := make([]string, 0, len(spec.Dependencies))
		for _, depRef := range spec.Dependencies {
			deps = append(deps, refToTaskID[depRef])
		}
		children := make([]string, 0, len(resolvedChildren[spec.RefID]))
		for _, childRef := range resolvedChildren[spec.RefID] {
			children = append(children, refToTaskID[childRef])
		}
		status := model.TaskStatusPending
		if len(deps) == 0 {
			status = model.TaskStatusReady
		}
		task := &model.Task{
			TaskID:                   taskID,
			DAGID:                    dagID,
			NodeType:                 parsedNodeType,
			SkillName:                spec.SkillName,
			Status:                   status,
			PendingDependenciesCount: len(deps),
			Dependencies:             deps,
			Children:                 children,
			Parameters:               spec.Parameters,
		}
		if task.NodeType == model.NodeTypeExpandPlanning {
			task.Parameters = injectIntentContext(task.Parameters, dag.IntentContext)
		}
		s.tasksByID[taskID] = task
		s.tasksByDAG[dagID] = append(s.tasksByDAG[dagID], taskID)
	}

	return s.snapshotLocked(sessionID), nil
}

func (s *Store) PullReadyTask(workerID string, ttl time.Duration) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()

	for _, task := range s.tasksByID {
		if task.Status != model.TaskStatusReady {
			continue
		}
		expires := now.Add(ttl)
		task.Status = model.TaskStatusRunning
		task.OwnerID = workerID
		task.ExpireAt = &expires
		copied := *task
		return &copied, nil
	}
	return nil, ErrNoReadyTask
}

func (s *Store) CompleteTask(input CompleteTaskInput) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasksByID[input.TaskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if task.Status != model.TaskStatusRunning || task.OwnerID != input.WorkerID {
		return nil, ErrTaskNotRunnable
	}

	task.OwnerID = ""
	task.ExpireAt = nil
	task.LastSummary = input.Summary

	if input.Success {
		if input.ExpansionPayload != nil {
			if task.NodeType != model.NodeTypeExpandPlanning {
				return nil, ErrExpansionNotAllowed
			}
			if err := s.applyExpansionLocked(task, input); err != nil {
				return nil, err
			}
			copied := *task
			return &copied, nil
		}

		task.Status = model.TaskStatusSuccess
		s.rawDataByDAG[task.DAGID][task.TaskID] = input.RawData

		for _, childID := range task.Children {
			child := s.tasksByID[childID]
			if child.PendingDependenciesCount > 0 {
				child.PendingDependenciesCount--
			}
			if child.PendingDependenciesCount == 0 && child.Status == model.TaskStatusPending {
				child.Status = model.TaskStatusReady
			}
		}

		s.refreshDAGStatusLocked(task.DAGID)
		copied := *task
		return &copied, nil
	}

	task.Status = model.TaskStatusFailed
	task.LastErrorCode = input.ErrorCode
	task.LastHumanReadableErrorMsg = input.ErrorMessage
	dag := s.dags[task.DAGID]
	dag.Status = model.DAGStatusReplanning
	dag.ReplanCount++
	s.dags[task.DAGID] = dag

	copied := *task
	return &copied, nil
}

func (s *Store) applyExpansionLocked(task *model.Task, input CompleteTaskInput) error {
	dag := s.dags[task.DAGID]
	if dag.MaxDepth == 0 {
		dag.MaxDepth = 10
	}
	if dag.CurrentDepth >= dag.MaxDepth {
		task.Status = model.TaskStatusFailed
		task.OwnerID = ""
		task.ExpireAt = nil
		task.LastErrorCode = "MAX_DEPTH_REACHED"
		task.LastHumanReadableErrorMsg = "planner expansion max depth reached"
		dag.Status = model.DAGStatusReplanning
		dag.ReplanCount++
		s.dags[task.DAGID] = dag
		return ErrExpansionDepthExceeded
	}

	payload := input.ExpansionPayload
	if err := s.validateExpansionLocked(task, payload); err != nil {
		return err
	}
	mappingStatus := normalizeMappingStatus(payload.MappingStatus)
	if mappingStatus == ExpansionMappingUnmapped {
		dag.JITUnmappedStreak++
		limit := dag.MaxUnmappedStreak
		if limit <= 0 {
			limit = 3
		}
		if dag.JITUnmappedStreak >= limit {
			task.Status = model.TaskStatusFailed
			task.OwnerID = ""
			task.ExpireAt = nil
			task.LastErrorCode = "MISSING_SKILL"
			task.LastHumanReadableErrorMsg = "skill mapping exhausted, missing required skill"
			dag.Status = model.DAGStatusReplanning
			dag.ReplanCount++
			s.dags[task.DAGID] = dag
			return ErrSkillMappingExhausted
		}
	} else {
		dag.JITUnmappedStreak = 0
	}

	originalChildren := append([]string{}, task.Children...)

	task.Status = model.TaskStatusSuccess
	task.OwnerID = ""
	task.ExpireAt = nil
	task.LastSummary = input.Summary

	newNodeIDs := make([]string, 0, len(payload.NewNodes))
	directNodeIDs := make([]string, 0, len(payload.NewNodes))
	for _, node := range payload.NewNodes {
		pending := 0
		for _, depID := range node.Dependencies {
			dep := s.tasksByID[depID]
			if dep == nil || dep.Status != model.TaskStatusSuccess {
				pending++
			}
		}
		status := model.TaskStatusPending
		if pending == 0 {
			status = model.TaskStatusReady
		}
		taskNode := &model.Task{
			TaskID:                   node.NodeID,
			DAGID:                    task.DAGID,
			NodeType:                 node.NodeType,
			SkillName:                node.SkillName,
			Status:                   status,
			PendingDependenciesCount: pending,
			Dependencies:             append([]string{}, node.Dependencies...),
			Children:                 []string{},
			Parameters:               node.Parameters,
		}
		if taskNode.NodeType == model.NodeTypeExpandPlanning {
			taskNode.Parameters = injectIntentContext(taskNode.Parameters, dag.IntentContext)
		}
		s.tasksByID[node.NodeID] = taskNode
		s.tasksByDAG[task.DAGID] = append(s.tasksByDAG[task.DAGID], node.NodeID)
		newNodeIDs = append(newNodeIDs, node.NodeID)
		if containsString(node.Dependencies, task.TaskID) {
			directNodeIDs = append(directNodeIDs, node.NodeID)
		}
	}

	for _, nodeID := range newNodeIDs {
		node := s.tasksByID[nodeID]
		for _, depID := range node.Dependencies {
			dep := s.tasksByID[depID]
			if dep != nil && !containsString(dep.Children, nodeID) {
				dep.Children = append(dep.Children, nodeID)
			}
		}
	}

	for _, childID := range originalChildren {
		child := s.tasksByID[childID]
		if child == nil || !containsString(child.Dependencies, payload.DownstreamWiring.RedirectFrom) {
			continue
		}
		child.Dependencies = replaceDependency(child.Dependencies, payload.DownstreamWiring.RedirectFrom, payload.DownstreamWiring.RedirectTo)
		child.PendingDependenciesCount = countPendingDependencies(s.tasksByID, child.Dependencies)
		for _, tailID := range payload.DownstreamWiring.RedirectTo {
			tail := s.tasksByID[tailID]
			if tail != nil && !containsString(tail.Children, childID) {
				tail.Children = append(tail.Children, childID)
			}
		}
	}

	task.Children = directNodeIDs
	s.rawDataByDAG[task.DAGID][task.TaskID] = map[string]any{
		"summary":   input.Summary,
		"expansion": payload,
	}

	dag.CurrentDepth++
	s.dags[task.DAGID] = dag
	s.refreshDAGStatusLocked(task.DAGID)
	return nil
}

func (s *Store) validateExpansionLocked(task *model.Task, payload *ExpansionPayload) error {
	if payload == nil || len(payload.NewNodes) == 0 {
		return ErrExpansionInvalid
	}
	mappingStatus := normalizeMappingStatus(payload.MappingStatus)
	if mappingStatus != ExpansionMappingMapped && mappingStatus != ExpansionMappingUnmapped {
		return ErrExpansionInvalid
	}
	if payload.DownstreamWiring.RedirectFrom != task.TaskID {
		return ErrExpansionInvalid
	}
	if len(payload.DownstreamWiring.RedirectTo) == 0 {
		return ErrExpansionInvalid
	}

	seen := make(map[string]struct{}, len(payload.NewNodes))
	newNodes := make(map[string]ExpansionNode, len(payload.NewNodes))
	for _, node := range payload.NewNodes {
		if node.NodeID == "" || node.NodeType == "" {
			return ErrExpansionInvalid
		}
		parsedType, err := model.ParseNodeType(string(node.NodeType))
		if err != nil {
			return ErrExpansionInvalid
		}
		node.NodeType = parsedType
		if node.NodeType == model.NodeTypeSkillSink && node.SkillName == "" {
			return ErrExpansionInvalid
		}
		if node.NodeType == model.NodeTypeExpandPlanning && node.SkillName != "" && node.SkillName != "ReActPlanner" {
			return ErrExpansionInvalid
		}
		if _, exists := s.tasksByID[node.NodeID]; exists {
			return ErrExpansionInvalid
		}
		if _, duplicate := seen[node.NodeID]; duplicate {
			return ErrExpansionInvalid
		}
		seen[node.NodeID] = struct{}{}
		newNodes[node.NodeID] = node
	}

	for _, tailID := range payload.DownstreamWiring.RedirectTo {
		if _, ok := newNodes[tailID]; !ok {
			return ErrExpansionInvalid
		}
	}
	for _, node := range payload.NewNodes {
		for _, depID := range node.Dependencies {
			if _, ok := s.tasksByID[depID]; ok {
				continue
			}
			if _, ok := newNodes[depID]; ok {
				continue
			}
			return ErrExpansionInvalid
		}
	}
	return nil
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func replaceDependency(deps []string, oldID string, newIDs []string) []string {
	out := make([]string, 0, len(deps)-1+len(newIDs))
	for _, dep := range deps {
		if dep == oldID {
			for _, newID := range newIDs {
				if !containsString(out, newID) {
					out = append(out, newID)
				}
			}
			continue
		}
		if !containsString(out, dep) {
			out = append(out, dep)
		}
	}
	return out
}

func countPendingDependencies(tasks map[string]*model.Task, deps []string) int {
	pending := 0
	for _, depID := range deps {
		dep := tasks[depID]
		if dep == nil || dep.Status != model.TaskStatusSuccess {
			pending++
		}
	}
	return pending
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func injectIntentContext(params map[string]any, intentContext map[string]any) map[string]any {
	out := cloneMap(params)
	out["intent_context"] = cloneMap(intentContext)
	return out
}

func (s *Store) ExpireRunningTasks(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expired []string
	for _, task := range s.tasksByID {
		if task.Status != model.TaskStatusRunning || task.ExpireAt == nil {
			continue
		}
		if task.ExpireAt.After(now) {
			continue
		}
		expired = append(expired, task.TaskID)
		switch s.leaseExpirePolicy {
		case LeaseExpirePolicyRetryReady:
			task.Status = model.TaskStatusReady
			task.OwnerID = ""
			task.ExpireAt = nil
			task.LastErrorCode = "WORKER_TIMEOUT_RETRY"
			task.LastHumanReadableErrorMsg = "worker lease expired, task returned to ready queue"
		default:
			task.Status = model.TaskStatusFailed
			task.OwnerID = ""
			task.ExpireAt = nil
			task.LastErrorCode = "WORKER_TIMEOUT"
			task.LastHumanReadableErrorMsg = "worker lease expired"
			dag := s.dags[task.DAGID]
			dag.Status = model.DAGStatusReplanning
			dag.ReplanCount++
			s.dags[task.DAGID] = dag
		}
	}
	return expired
}

func (s *Store) GetSessionSnapshot(sessionID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.sessions[sessionID]
	if !ok {
		return Snapshot{}, errors.New("session not found")
	}
	return s.snapshotLocked(sessionID), nil
}

func (s *Store) ResolveSessionIDByTaskID(taskID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasksByID[taskID]
	if !ok {
		return "", false
	}
	dag, ok := s.dags[task.DAGID]
	if !ok {
		return "", false
	}
	return dag.SessionID, true
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) refreshDAGStatusLocked(dagID string) {
	tasks := s.tasksByDAG[dagID]
	if len(tasks) == 0 {
		return
	}

	allSuccess := true
	for _, taskID := range tasks {
		status := s.tasksByID[taskID].Status
		if status == model.TaskStatusFailed {
			dag := s.dags[dagID]
			dag.Status = model.DAGStatusFailed
			s.dags[dagID] = dag
			return
		}
		if status != model.TaskStatusSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		dag := s.dags[dagID]
		dag.Status = model.DAGStatusSuccess
		s.dags[dagID] = dag
	}
}

func (s *Store) snapshotLocked(sessionID string) Snapshot {
	session := s.sessions[sessionID]
	dag := s.dags[session.DAGID]
	taskIDs := s.tasksByDAG[session.DAGID]

	tasks := make([]model.Task, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		tasks = append(tasks, *s.tasksByID[taskID])
	}

	raw := make(map[string]any, len(s.rawDataByDAG[session.DAGID]))
	for k, v := range s.rawDataByDAG[session.DAGID] {
		raw[k] = v
	}

	return Snapshot{Session: session, DAG: dag, Tasks: tasks, RawData: raw}
}

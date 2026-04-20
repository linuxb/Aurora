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
	TaskID       string
	WorkerID     string
	Success      bool
	Summary      string
	RawData      any
	ErrorCode    string
	ErrorMessage string
}

type Snapshot struct {
	Session model.Session  `json:"session"`
	DAG     model.DAG      `json:"dag"`
	Tasks   []model.Task   `json:"tasks"`
	RawData map[string]any `json:"raw_data"`
}

type Store struct {
	mu sync.Mutex

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
	return &Store{
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
		DAGID:          dagID,
		SessionID:      sessionID,
		UserID:         userID,
		OriginalIntent: intent,
		Status:         model.DAGStatusRunning,
		CreatedAt:      now,
	}

	queryTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	summaryTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))
	mailTaskID := fmt.Sprintf("task_%06d", s.taskCounter.Add(1))

	queryTask := &model.Task{
		TaskID:                   queryTaskID,
		DAGID:                    dagID,
		SkillName:                "QueryLog",
		Status:                   model.TaskStatusReady,
		PendingDependenciesCount: 0,
		Dependencies:             []string{},
		Children:                 []string{summaryTaskID},
	}
	summaryTask := &model.Task{
		TaskID:                   summaryTaskID,
		DAGID:                    dagID,
		SkillName:                "LLMSummarize",
		Status:                   model.TaskStatusPending,
		PendingDependenciesCount: 1,
		Dependencies:             []string{queryTaskID},
		Children:                 []string{mailTaskID},
	}
	mailTask := &model.Task{
		TaskID:                   mailTaskID,
		DAGID:                    dagID,
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
		task.Status = model.TaskStatusFailed
		task.OwnerID = ""
		task.ExpireAt = nil
		task.LastErrorCode = "WORKER_TIMEOUT"
		task.LastHumanReadableErrorMsg = "worker lease expired"
		expired = append(expired, task.TaskID)

		dag := s.dags[task.DAGID]
		dag.Status = model.DAGStatusReplanning
		dag.ReplanCount++
		s.dags[task.DAGID] = dag
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

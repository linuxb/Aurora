package scheduler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"aurora/apps/arqo/internal/model"
)

type MySQLStore struct {
	db                *sql.DB
	dialect           string
	leaseExpirePolicy LeaseExpirePolicy
}

func NewMySQLStoreFromEnv() (*MySQLStore, error) {
	dsn := strings.TrimSpace(os.Getenv("ARQO_MYSQL_DSN"))
	if dsn == "" {
		dsn = "aurora:aurora@tcp(127.0.0.1:3306)/aurora?parseTime=true&multiStatements=true"
	}
	return NewMySQLStore(dsn)
}

func NewTiDBStoreFromEnv() (*MySQLStore, error) {
	dsn := strings.TrimSpace(os.Getenv("ARQO_TIDB_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("ARQO_MYSQL_DSN"))
	}
	if dsn == "" {
		dsn = "root@tcp(127.0.0.1:4000)/aurora?parseTime=true&multiStatements=true"
	}
	return newSQLCompatibleStore(dsn, "tidb")
}

func NewMySQLStore(dsn string) (*MySQLStore, error) {
	return newSQLCompatibleStore(dsn, "mysql")
}

func newSQLCompatibleStore(dsn string, dialect string) (*MySQLStore, error) {
	if !isMySQLDriverRegistered() {
		return nil, errors.New("mysql-compatible driver is not registered; install and import github.com/go-sql-driver/mysql before enabling mysql/tidb scheduler backend")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s failed: %w", dialect, err)
	}

	store := &MySQLStore{db: db, dialect: dialect, leaseExpirePolicy: LeaseExpirePolicyFailedReplan}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s failed: %w", dialect, err)
	}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func isMySQLDriverRegistered() bool {
	for _, driverName := range sql.Drivers() {
		if driverName == "mysql" {
			return true
		}
	}
	return false
}

func newMySQLStoreWithDB(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db, dialect: "mysql", leaseExpirePolicy: LeaseExpirePolicyFailedReplan}
}

func (s *MySQLStore) CreateDemoSession(userID, intent string) (Snapshot, error) {
	sessionID, err := newPrefixedID("sess")
	if err != nil {
		return Snapshot{}, err
	}
	dagID, err := newPrefixedID("dag")
	if err != nil {
		return Snapshot{}, err
	}
	queryTaskID, err := newPrefixedID("task")
	if err != nil {
		return Snapshot{}, err
	}
	summaryTaskID, err := newPrefixedID("task")
	if err != nil {
		return Snapshot{}, err
	}
	mailTaskID, err := newPrefixedID("task")
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = execSQLTx(tx, insertSessionBuilder(sessionID, dagID, userID, intent, now))
	if err != nil {
		return Snapshot{}, err
	}

	intentContextJSON, _ := json.Marshal(map[string]any{"original_intent": intent, "source": "demo"})
	_, err = execSQLTx(tx, insertDAGBuilder(dagID, sessionID, userID, intent, string(intentContextJSON), now))
	if err != nil {
		return Snapshot{}, err
	}

	if err := s.insertTask(tx, queryTaskID, dagID, string(model.NodeTypeSkillSink), "QueryLog", "READY", 0, []string{}, []string{summaryTaskID}, nil, now); err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, summaryTaskID, dagID, string(model.NodeTypeSkillSink), "LLMSummarize", "PENDING", 1, []string{queryTaskID}, []string{mailTaskID}, nil, now); err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, mailTaskID, dagID, string(model.NodeTypeSkillSink), "SendEmail", "PENDING", 1, []string{summaryTaskID}, []string{}, nil, now); err != nil {
		return Snapshot{}, err
	}

	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}

	snapshot, err := s.GetSessionSnapshot(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *MySQLStore) CreateJITDemoSession(userID, intent string) (Snapshot, error) {
	sessionID, err := newPrefixedID("sess")
	if err != nil {
		return Snapshot{}, err
	}
	dagID, err := newPrefixedID("dag")
	if err != nil {
		return Snapshot{}, err
	}
	plannerTaskID, err := newPrefixedID("task")
	if err != nil {
		return Snapshot{}, err
	}
	finalTaskID, err := newPrefixedID("task")
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = execSQLTx(tx, insertSessionBuilder(sessionID, dagID, userID, intent, now))
	if err != nil {
		return Snapshot{}, err
	}
	intentContextJSON, _ := json.Marshal(map[string]any{"original_intent": intent, "source": "jit_demo"})
	_, err = execSQLTx(tx, insertDAGBuilder(dagID, sessionID, userID, intent, string(intentContextJSON), now))
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, plannerTaskID, dagID, string(model.NodeTypeExpandPlanning), "ReActPlanner", "READY", 0, []string{}, []string{finalTaskID}, map[string]any{"intent_context": map[string]any{"original_intent": intent, "source": "jit_demo"}}, now); err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, finalTaskID, dagID, string(model.NodeTypeSkillSink), "SendEmail", "PENDING", 1, []string{plannerTaskID}, []string{}, nil, now); err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return s.GetSessionSnapshot(sessionID)
}

func (s *MySQLStore) CreateSessionFromPlan(userID, intent string, intentContext map[string]any, tasks []SessionTaskSpec) (Snapshot, error) {
	sessionID, err := newPrefixedID("sess")
	if err != nil {
		return Snapshot{}, err
	}
	dagID, err := newPrefixedID("dag")
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now().UTC()

	type runtimeTask struct {
		taskID     string
		spec       SessionTaskSpec
		deps       []string
		children   []string
		pending    int
		taskStatus string
	}

	refToTaskID := make(map[string]string, len(tasks))
	for _, spec := range tasks {
		id, idErr := newPrefixedID("task")
		if idErr != nil {
			return Snapshot{}, idErr
		}
		refToTaskID[spec.RefID] = id
	}

	childRefs := make(map[string][]string, len(tasks))
	for _, spec := range tasks {
		for _, depRef := range spec.Dependencies {
			childRefs[depRef] = append(childRefs[depRef], spec.RefID)
		}
	}

	runtimeTasks := make([]runtimeTask, 0, len(tasks))
	for _, spec := range tasks {
		nodeType := spec.NodeType
		parsedNodeType, parseErr := model.ParseNodeType(string(nodeType))
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		spec.NodeType = parsedNodeType
		deps := make([]string, 0, len(spec.Dependencies))
		for _, depRef := range spec.Dependencies {
			deps = append(deps, refToTaskID[depRef])
		}
		children := make([]string, 0, len(childRefs[spec.RefID]))
		for _, childRef := range childRefs[spec.RefID] {
			children = append(children, refToTaskID[childRef])
		}
		status := "PENDING"
		if len(deps) == 0 {
			status = "READY"
		}
		runtimeTasks = append(runtimeTasks, runtimeTask{
			taskID:     refToTaskID[spec.RefID],
			spec:       spec,
			deps:       deps,
			children:   children,
			pending:    len(deps),
			taskStatus: status,
		})
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = execSQLTx(tx, insertSessionBuilder(sessionID, dagID, userID, intent, now))
	if err != nil {
		return Snapshot{}, err
	}
	intentContextJSON, _ := json.Marshal(cloneMap(intentContext))
	_, err = execSQLTx(tx, insertDAGBuilder(dagID, sessionID, userID, intent, string(intentContextJSON), now))
	if err != nil {
		return Snapshot{}, err
	}
	for _, rt := range runtimeTasks {
		params := rt.spec.Parameters
		if rt.spec.NodeType == model.NodeTypeExpandPlanning {
			params = injectIntentContext(params, intentContext)
		}
		if err := s.insertTask(tx, rt.taskID, dagID, string(rt.spec.NodeType), rt.spec.SkillName, rt.taskStatus, rt.pending, rt.deps, rt.children, params, now); err != nil {
			return Snapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return s.GetSessionSnapshot(sessionID)
}

func (s *MySQLStore) ApplyReplanPatch(sessionID string, tasks []SessionTaskSpec, reason string) (Snapshot, error) {
	_ = reason
	if len(tasks) == 0 {
		return Snapshot{}, errors.New("replan patch tasks are required")
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var dag model.DAG
	var rawIntentContext sql.NullString
	row := tx.QueryRow(`
SELECT d.dag_id, d.session_id, d.user_id, d.original_intent, d.intent_context_json, d.status, d.replan_count, d.current_depth, d.max_depth, d.jit_unmapped_streak, d.max_unmapped_streak, d.created_at
FROM dags d
JOIN sessions s ON s.dag_id = d.dag_id
WHERE s.session_id=?
FOR UPDATE`, sessionID)
	var dagStatus string
	if err := row.Scan(
		&dag.DAGID, &dag.SessionID, &dag.UserID, &dag.OriginalIntent, &rawIntentContext, &dagStatus,
		&dag.ReplanCount, &dag.CurrentDepth, &dag.MaxDepth, &dag.JITUnmappedStreak, &dag.MaxUnmappedStreak, &dag.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, errors.New("session not found")
		}
		return Snapshot{}, err
	}
	dag.Status = model.DAGStatus(dagStatus)
	if dag.Status != model.DAGStatusReplanning {
		return Snapshot{}, ErrReplanNotAllowed
	}
	if rawIntentContext.Valid && rawIntentContext.String != "" && rawIntentContext.String != "null" {
		_ = json.Unmarshal([]byte(rawIntentContext.String), &dag.IntentContext)
	}

	refToTaskID := make(map[string]string, len(tasks))
	for _, spec := range tasks {
		id, idErr := newPrefixedID("task")
		if idErr != nil {
			return Snapshot{}, idErr
		}
		refToTaskID[spec.RefID] = id
	}
	childRefs := make(map[string][]string, len(tasks))
	for _, spec := range tasks {
		for _, depRef := range spec.Dependencies {
			childRefs[depRef] = append(childRefs[depRef], spec.RefID)
		}
	}

	for _, spec := range tasks {
		parsedNodeType, parseErr := model.ParseNodeType(string(spec.NodeType))
		if parseErr != nil {
			return Snapshot{}, parseErr
		}
		taskID := refToTaskID[spec.RefID]
		deps := make([]string, 0, len(spec.Dependencies))
		for _, depRef := range spec.Dependencies {
			if mapped, ok := refToTaskID[depRef]; ok {
				deps = append(deps, mapped)
				continue
			}
			var exists string
			err := tx.QueryRow(`SELECT task_id FROM tasks WHERE task_id=?`, depRef).Scan(&exists)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Snapshot{}, fmt.Errorf("unknown dependency reference: %s", depRef)
				}
				return Snapshot{}, err
			}
			deps = append(deps, depRef)
		}
		children := make([]string, 0, len(childRefs[spec.RefID]))
		for _, childRef := range childRefs[spec.RefID] {
			children = append(children, refToTaskID[childRef])
		}
		pending := 0
		for _, depID := range deps {
			var status string
			if err := tx.QueryRow(`SELECT status FROM tasks WHERE task_id=?`, depID).Scan(&status); err != nil {
				return Snapshot{}, err
			}
			if status != string(model.TaskStatusSuccess) {
				pending++
			}
		}
		taskStatus := "PENDING"
		if pending == 0 {
			taskStatus = "READY"
		}
		params := spec.Parameters
		if parsedNodeType == model.NodeTypeExpandPlanning {
			params = injectIntentContext(params, dag.IntentContext)
		}
		if err := s.insertTask(tx, taskID, dag.DAGID, string(parsedNodeType), spec.SkillName, taskStatus, pending, deps, children, params, time.Now().UTC()); err != nil {
			return Snapshot{}, err
		}
		for _, depID := range deps {
			_, err := tx.Exec(`UPDATE tasks SET children_json = JSON_ARRAY_APPEND(children_json, '$', ?) WHERE task_id=? AND JSON_SEARCH(children_json, 'one', ?) IS NULL`, taskID, depID, taskID)
			if err != nil {
				return Snapshot{}, err
			}
		}
	}

	_, err = tx.Exec(`UPDATE dags SET status='RUNNING' WHERE dag_id=?`, dag.DAGID)
	if err != nil {
		return Snapshot{}, err
	}

	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return s.GetSessionSnapshot(sessionID)
}

func (s *MySQLStore) PullReadyTask(workerID string, ttl time.Duration) (*model.Task, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := queryRowSQLTx(tx, selectReadyTaskForUpdateBuilder())

	task, err := scanReadyTaskRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoReadyTask
		}
		return nil, err
	}

	expireAt := time.Now().UTC().Add(ttl)
	_, err = execSQLTx(tx, leaseTaskBuilder(task.TaskID, workerID, expireAt))
	if err != nil {
		return nil, err
	}
	task.Status = model.TaskStatusRunning
	task.OwnerID = workerID
	task.ExpireAt = &expireAt

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *MySQLStore) CompleteTask(input CompleteTaskInput) (*model.Task, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getTaskByIDTx(tx, input.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	if current.Status != model.TaskStatusRunning || current.OwnerID != input.WorkerID {
		return nil, ErrTaskNotRunnable
	}
	if input.Success && input.ExpansionPayload != nil {
		if current.NodeType != model.NodeTypeExpandPlanning {
			return nil, ErrExpansionNotAllowed
		}
		if err := s.applyExpansionTx(tx, current, input); err != nil {
			return nil, err
		}
		updated, err := s.getTaskByIDTx(tx, input.TaskID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return updated, nil
	}

	if input.Success {
		_, err = execSQLTx(tx, markTaskSuccessBuilder(input.TaskID, input.Summary))
		if err != nil {
			return nil, err
		}

		raw, _ := json.Marshal(input.RawData)
		_, err = execSQLTx(tx, upsertTaskRawDataBuilder(input.TaskID, current.DAGID, string(raw), time.Now().UTC()))
		if err != nil {
			return nil, err
		}

		for _, childID := range current.Children {
			_, err = execSQLTx(tx, decrementTaskPendingBuilder(childID))
			if err != nil {
				return nil, err
			}

			_, err = execSQLTx(tx, readyTaskWhenDependenciesResolvedBuilder(childID))
			if err != nil {
				return nil, err
			}
		}

		if err := s.refreshDAGStatusTx(tx, current.DAGID); err != nil {
			return nil, err
		}
	} else {
		_, err = execSQLTx(tx, markTaskFailedBuilder(input.TaskID, input.Summary, input.ErrorCode, input.ErrorMessage))
		if err != nil {
			return nil, err
		}
		_, err = execSQLTx(tx, markDAGReplanningBuilder(current.DAGID))
		if err != nil {
			return nil, err
		}
	}

	updated, err := s.getTaskByIDTx(tx, input.TaskID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *MySQLStore) ExpireRunningTasks(now time.Time) []string {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := querySQLTx(tx, selectExpiredRunningTasksForUpdateBuilder(now))
	if err != nil {
		return nil
	}
	defer rows.Close()

	taskIDs := make([]string, 0)
	dagSet := make(map[string]struct{})
	for rows.Next() {
		var taskID, dagID string
		if err := rows.Scan(&taskID, &dagID); err != nil {
			continue
		}
		taskIDs = append(taskIDs, taskID)
		dagSet[dagID] = struct{}{}
	}

	for _, taskID := range taskIDs {
		_, err := execSQLTx(tx, expireTaskBuilder(taskID, s.leaseExpirePolicy))
		if err != nil {
			return nil
		}
	}
	if s.leaseExpirePolicy != LeaseExpirePolicyRetryReady {
		for dagID := range dagSet {
			_, err := execSQLTx(tx, markDAGReplanningBuilder(dagID))
			if err != nil {
				return nil
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil
	}
	return taskIDs
}

func (s *MySQLStore) GetSessionSnapshot(sessionID string) (Snapshot, error) {
	var session model.Session
	var dag model.DAG

	row := s.db.QueryRow(`
SELECT s.session_id, s.dag_id, s.user_id, s.intent, s.created_at,
       d.dag_id, d.session_id, d.user_id, d.original_intent, d.intent_context_json, d.status, d.replan_count, d.current_depth, d.max_depth, d.jit_unmapped_streak, d.max_unmapped_streak, d.created_at
FROM sessions s
JOIN dags d ON d.session_id = s.session_id
WHERE s.session_id = ?`, sessionID)
	var dagStatus string
	var rawIntentContext sql.NullString
	if err := row.Scan(
		&session.SessionID, &session.DAGID, &session.UserID, &session.Intent, &session.CreatedAt,
		&dag.DAGID, &dag.SessionID, &dag.UserID, &dag.OriginalIntent, &rawIntentContext, &dagStatus, &dag.ReplanCount, &dag.CurrentDepth, &dag.MaxDepth, &dag.JITUnmappedStreak, &dag.MaxUnmappedStreak, &dag.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, errors.New("session not found")
		}
		return Snapshot{}, err
	}
	dag.Status = model.DAGStatus(dagStatus)
	if rawIntentContext.Valid && rawIntentContext.String != "" && rawIntentContext.String != "null" {
		_ = json.Unmarshal([]byte(rawIntentContext.String), &dag.IntentContext)
	}

	rows, err := s.db.Query(`
SELECT task_id, dag_id, node_type, skill_name, status, pending_dependencies_count, owner_id, expire_at,
       dependencies_json, children_json, parameters_json, last_summary, last_error_code, last_human_readable_error_msg
FROM tasks
WHERE dag_id = ?
ORDER BY created_at ASC`, dag.DAGID)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()

	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanTaskRows(rows)
		if err != nil {
			return Snapshot{}, err
		}
		tasks = append(tasks, *task)
	}

	rawData := make(map[string]any)
	rawRows, err := s.db.Query(`SELECT task_id, raw_data_json FROM task_raw_data WHERE dag_id = ?`, dag.DAGID)
	if err == nil {
		defer rawRows.Close()
		for rawRows.Next() {
			var taskID string
			var raw string
			if err := rawRows.Scan(&taskID, &raw); err != nil {
				continue
			}
			var decoded any
			if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
				rawData[taskID] = decoded
			}
		}
	}

	return Snapshot{Session: session, DAG: dag, Tasks: tasks, RawData: rawData}, nil
}

func (s *MySQLStore) ResolveSessionIDByTaskID(taskID string) (string, bool) {
	row := s.db.QueryRow(`
SELECT d.session_id
FROM tasks t
JOIN dags d ON d.dag_id = t.dag_id
WHERE t.task_id = ?`, taskID)
	var sessionID string
	if err := row.Scan(&sessionID); err != nil {
		return "", false
	}
	return sessionID, true
}

func (s *MySQLStore) Close() error {
	return s.db.Close()
}

func (s *MySQLStore) ensureSchema(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    session_id VARCHAR(64) PRIMARY KEY,
    dag_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    intent TEXT NOT NULL,
    created_at DATETIME(6) NOT NULL
);

CREATE TABLE IF NOT EXISTS dags (
    dag_id VARCHAR(64) PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    original_intent TEXT NOT NULL,
    intent_context_json LONGTEXT NULL,
    status VARCHAR(20) NOT NULL,
    replan_count INT NOT NULL DEFAULT 0,
    current_depth INT NOT NULL DEFAULT 1,
    max_depth INT NOT NULL DEFAULT 10,
    jit_unmapped_streak INT NOT NULL DEFAULT 0,
    max_unmapped_streak INT NOT NULL DEFAULT 3,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_dag_session_id (session_id)
);

CREATE TABLE IF NOT EXISTS tasks (
    task_id VARCHAR(64) PRIMARY KEY,
    dag_id VARCHAR(64) NOT NULL,
    node_type VARCHAR(32) NOT NULL DEFAULT 'SKILL_SINK',
    skill_name VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL,
    pending_dependencies_count INT NOT NULL,
    owner_id VARCHAR(64) NULL,
    expire_at DATETIME(6) NULL,
    dependencies_json TEXT NOT NULL,
    children_json TEXT NOT NULL,
    parameters_json LONGTEXT NULL,
    last_summary TEXT NULL,
    last_error_code VARCHAR(64) NULL,
    last_human_readable_error_msg TEXT NULL,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_tasks_status (status),
    INDEX idx_tasks_dag_id (dag_id)
);

CREATE TABLE IF NOT EXISTS task_raw_data (
    task_id VARCHAR(64) PRIMARY KEY,
    dag_id VARCHAR(64) NOT NULL,
    raw_data_json LONGTEXT NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_raw_data_dag_id (dag_id)
);`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("ensure schema failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN IF NOT EXISTS node_type VARCHAR(32) NOT NULL DEFAULT 'SKILL_SINK'`); err != nil {
		return fmt.Errorf("ensure node_type column failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE dags ADD COLUMN IF NOT EXISTS intent_context_json LONGTEXT NULL`); err != nil {
		return fmt.Errorf("ensure intent_context_json column failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE dags ADD COLUMN IF NOT EXISTS jit_unmapped_streak INT NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("ensure jit_unmapped_streak column failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE dags ADD COLUMN IF NOT EXISTS max_unmapped_streak INT NOT NULL DEFAULT 3`); err != nil {
		return fmt.Errorf("ensure max_unmapped_streak column failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET node_type='EXPAND_PLANNING' WHERE node_type='EXPANDING'`); err != nil {
		return fmt.Errorf("normalize node_type values failed: %w", err)
	}
	return nil
}

func (s *MySQLStore) insertTask(tx *sql.Tx, taskID, dagID, nodeType, skillName, status string, pendingCount int, deps, children []string, params map[string]any, createdAt time.Time) error {
	depsJSON, _ := json.Marshal(deps)
	childrenJSON, _ := json.Marshal(children)
	paramsJSON, _ := json.Marshal(params)
	_, err := execSQLTx(tx, insertTaskBuilder(taskID, dagID, nodeType, skillName, status, pendingCount, string(depsJSON), string(childrenJSON), string(paramsJSON), createdAt))
	return err
}

func (s *MySQLStore) getTaskByIDTx(tx *sql.Tx, taskID string) (*model.Task, error) {
	row := queryRowSQLTx(tx, selectTaskByIDForUpdateBuilder(taskID))
	return scanTaskRow(row)
}

func (s *MySQLStore) refreshDAGStatusTx(tx *sql.Tx, dagID string) error {
	rows, err := querySQLTx(tx, selectTaskStatusesByDAGBuilder(dagID))
	if err != nil {
		return err
	}
	defer rows.Close()

	allSuccess := true
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return err
		}
		if status == string(model.TaskStatusFailed) {
			_, err = execSQLTx(tx, markDAGStatusBuilder(dagID, model.DAGStatusFailed))
			return err
		}
		if status != string(model.TaskStatusSuccess) {
			allSuccess = false
		}
	}

	if allSuccess {
		_, err = execSQLTx(tx, markDAGStatusBuilder(dagID, model.DAGStatusSuccess))
		return err
	}
	return nil
}

func (s *MySQLStore) applyExpansionTx(tx *sql.Tx, planner *model.Task, input CompleteTaskInput) error {
	payload := input.ExpansionPayload
	if payload == nil || len(payload.NewNodes) == 0 {
		return ErrExpansionInvalid
	}
	mappingStatus := normalizeMappingStatus(payload.MappingStatus)
	if mappingStatus != ExpansionMappingMapped && mappingStatus != ExpansionMappingUnmapped {
		return ErrExpansionInvalid
	}
	if payload.DownstreamWiring.RedirectFrom != planner.TaskID || len(payload.DownstreamWiring.RedirectTo) == 0 {
		return ErrExpansionInvalid
	}

	var currentDepth, maxDepth, unmappedStreak, maxUnmappedStreak int
	var rawIntentContext sql.NullString
	row := tx.QueryRow(`SELECT current_depth, max_depth, jit_unmapped_streak, max_unmapped_streak, intent_context_json FROM dags WHERE dag_id=? FOR UPDATE`, planner.DAGID)
	if err := row.Scan(&currentDepth, &maxDepth, &unmappedStreak, &maxUnmappedStreak, &rawIntentContext); err != nil {
		return err
	}
	if maxDepth == 0 {
		maxDepth = 10
	}
	if currentDepth >= maxDepth {
		_, _ = tx.Exec(`
UPDATE tasks
SET status='FAILED', owner_id=NULL, expire_at=NULL, last_error_code='MAX_DEPTH_REACHED', last_human_readable_error_msg='planner expansion max depth reached'
WHERE task_id=?`, planner.TaskID)
		_, _ = tx.Exec(`UPDATE dags SET status='REPLANNING', replan_count = replan_count + 1 WHERE dag_id=?`, planner.DAGID)
		return ErrExpansionDepthExceeded
	}
	if maxUnmappedStreak <= 0 {
		maxUnmappedStreak = 3
	}
	nextUnmappedStreak := 0
	if mappingStatus == ExpansionMappingUnmapped {
		nextUnmappedStreak = unmappedStreak + 1
		if nextUnmappedStreak >= maxUnmappedStreak {
			_, _ = tx.Exec(`
UPDATE tasks
SET status='FAILED', owner_id=NULL, expire_at=NULL, last_error_code='MISSING_SKILL', last_human_readable_error_msg='skill mapping exhausted, missing required skill'
WHERE task_id=?`, planner.TaskID)
			_, _ = tx.Exec(`UPDATE dags SET status='REPLANNING', replan_count = replan_count + 1, jit_unmapped_streak=? WHERE dag_id=?`, nextUnmappedStreak, planner.DAGID)
			return ErrSkillMappingExhausted
		}
	}

	// Validate node IDs and dependencies.
	newNodes := make(map[string]ExpansionNode, len(payload.NewNodes))
	for _, node := range payload.NewNodes {
		if node.NodeID == "" || node.NodeType == "" {
			return ErrExpansionInvalid
		}
		parsedType, parseErr := model.ParseNodeType(string(node.NodeType))
		if parseErr != nil {
			return ErrExpansionInvalid
		}
		node.NodeType = parsedType
		if node.NodeType == model.NodeTypeSkillSink && node.SkillName == "" {
			return ErrExpansionInvalid
		}
		if node.NodeType == model.NodeTypeExpandPlanning && node.SkillName != "" && node.SkillName != "ReActPlanner" {
			return ErrExpansionInvalid
		}
		if _, exists := newNodes[node.NodeID]; exists {
			return ErrExpansionInvalid
		}
		var existsTaskID string
		err := tx.QueryRow(`SELECT task_id FROM tasks WHERE task_id=?`, node.NodeID).Scan(&existsTaskID)
		if err == nil {
			return ErrExpansionInvalid
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		newNodes[node.NodeID] = node
	}
	for _, tailID := range payload.DownstreamWiring.RedirectTo {
		if _, ok := newNodes[tailID]; !ok {
			return ErrExpansionInvalid
		}
	}
	for _, node := range payload.NewNodes {
		for _, depID := range node.Dependencies {
			if _, ok := newNodes[depID]; ok {
				continue
			}
			var existingTask string
			err := tx.QueryRow(`SELECT task_id FROM tasks WHERE task_id=?`, depID).Scan(&existingTask)
			if err != nil {
				return ErrExpansionInvalid
			}
		}
	}

	_, err := tx.Exec(`
UPDATE tasks
SET status='SUCCESS', owner_id=NULL, expire_at=NULL, last_summary=?, last_error_code=NULL, last_human_readable_error_msg=NULL
WHERE task_id=?`, input.Summary, planner.TaskID)
	if err != nil {
		return err
	}

	for _, node := range payload.NewNodes {
		pending := 0
		for _, depID := range node.Dependencies {
			var depStatus string
			if err := tx.QueryRow(`SELECT status FROM tasks WHERE task_id=?`, depID).Scan(&depStatus); err != nil {
				return err
			}
			if depStatus != string(model.TaskStatusSuccess) {
				pending++
			}
		}
		status := "PENDING"
		if pending == 0 {
			status = "READY"
		}
		params := node.Parameters
		if node.NodeType == model.NodeTypeExpandPlanning {
			var intentContext map[string]any
			if rawIntentContext.Valid && rawIntentContext.String != "" && rawIntentContext.String != "null" {
				_ = json.Unmarshal([]byte(rawIntentContext.String), &intentContext)
			}
			params = injectIntentContext(params, intentContext)
		}
		if err := s.insertTask(tx, node.NodeID, planner.DAGID, string(node.NodeType), node.SkillName, status, pending, node.Dependencies, []string{}, params, time.Now().UTC()); err != nil {
			return err
		}
	}

	for _, node := range payload.NewNodes {
		for _, depID := range node.Dependencies {
			var deps []string
			var children []string
			var rawDeps, rawChildren string
			var rawParams sql.NullString
			if err := tx.QueryRow(`SELECT dependencies_json, children_json, parameters_json FROM tasks WHERE task_id=? FOR UPDATE`, depID).Scan(&rawDeps, &rawChildren, &rawParams); err != nil {
				return err
			}
			_ = json.Unmarshal([]byte(rawDeps), &deps)
			_ = json.Unmarshal([]byte(rawChildren), &children)
			if !containsString(children, node.NodeID) {
				children = append(children, node.NodeID)
			}
			updatedChildren, _ := json.Marshal(children)
			if _, err := tx.Exec(`UPDATE tasks SET children_json=? WHERE task_id=?`, string(updatedChildren), depID); err != nil {
				return err
			}
		}
	}

	originalChildren := planner.Children
	for _, childID := range originalChildren {
		var rawDeps string
		var status string
		var currentPending int
		err := tx.QueryRow(`SELECT dependencies_json, status, pending_dependencies_count FROM tasks WHERE task_id=? FOR UPDATE`, childID).Scan(&rawDeps, &status, &currentPending)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		var deps []string
		_ = json.Unmarshal([]byte(rawDeps), &deps)
		if !containsString(deps, payload.DownstreamWiring.RedirectFrom) {
			continue
		}
		deps = replaceDependency(deps, payload.DownstreamWiring.RedirectFrom, payload.DownstreamWiring.RedirectTo)
		newPending := 0
		for _, depID := range deps {
			var depStatus string
			if err := tx.QueryRow(`SELECT status FROM tasks WHERE task_id=?`, depID).Scan(&depStatus); err != nil {
				return err
			}
			if depStatus != string(model.TaskStatusSuccess) {
				newPending++
			}
		}
		newStatus := status
		if newPending == 0 && status == string(model.TaskStatusPending) {
			newStatus = string(model.TaskStatusReady)
		}
		depsJSON, _ := json.Marshal(deps)
		if _, err := tx.Exec(`UPDATE tasks SET dependencies_json=?, pending_dependencies_count=?, status=? WHERE task_id=?`, string(depsJSON), newPending, newStatus, childID); err != nil {
			return err
		}
		for _, tailID := range payload.DownstreamWiring.RedirectTo {
			var rawChildren string
			if err := tx.QueryRow(`SELECT children_json FROM tasks WHERE task_id=? FOR UPDATE`, tailID).Scan(&rawChildren); err != nil {
				return err
			}
			var tailChildren []string
			_ = json.Unmarshal([]byte(rawChildren), &tailChildren)
			if !containsString(tailChildren, childID) {
				tailChildren = append(tailChildren, childID)
				tailChildrenJSON, _ := json.Marshal(tailChildren)
				if _, err := tx.Exec(`UPDATE tasks SET children_json=? WHERE task_id=?`, string(tailChildrenJSON), tailID); err != nil {
					return err
				}
			}
		}
	}

	directChildren := make([]string, 0)
	for _, node := range payload.NewNodes {
		if containsString(node.Dependencies, planner.TaskID) {
			directChildren = append(directChildren, node.NodeID)
		}
	}
	childrenJSON, _ := json.Marshal(directChildren)
	_, err = tx.Exec(`UPDATE tasks SET children_json=? WHERE task_id=?`, string(childrenJSON), planner.TaskID)
	if err != nil {
		return err
	}

	raw, _ := json.Marshal(map[string]any{
		"summary":   input.Summary,
		"expansion": payload,
	})
	_, err = tx.Exec(`
INSERT INTO task_raw_data (task_id, dag_id, raw_data_json, updated_at)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE raw_data_json=VALUES(raw_data_json), updated_at=VALUES(updated_at)`, planner.TaskID, planner.DAGID, string(raw), time.Now().UTC())
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE dags SET current_depth = current_depth + 1, jit_unmapped_streak=? WHERE dag_id=?`, nextUnmappedStreak, planner.DAGID)
	if err != nil {
		return err
	}

	return s.refreshDAGStatusTx(tx, planner.DAGID)
}

func scanTaskRow(row *sql.Row) (*model.Task, error) {
	var task model.Task
	var rawNodeType string
	var status string
	var depsJSON, childrenJSON string
	var paramsJSON sql.NullString
	var owner sql.NullString
	var expireAt sql.NullTime
	var lastSummary, lastErrorCode, lastHumanError sql.NullString

	if err := row.Scan(
		&task.TaskID,
		&task.DAGID,
		&rawNodeType,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&owner,
		&expireAt,
		&depsJSON,
		&childrenJSON,
		&paramsJSON,
		&lastSummary,
		&lastErrorCode,
		&lastHumanError,
	); err != nil {
		return nil, err
	}

	nodeType, err := model.ParseNodeType(rawNodeType)
	if err != nil {
		return nil, err
	}
	task.NodeType = nodeType
	task.Status = model.TaskStatus(status)
	if owner.Valid {
		task.OwnerID = owner.String
	}
	if expireAt.Valid {
		timeVal := expireAt.Time.UTC()
		task.ExpireAt = &timeVal
	}
	_ = json.Unmarshal([]byte(depsJSON), &task.Dependencies)
	_ = json.Unmarshal([]byte(childrenJSON), &task.Children)
	if paramsJSON.Valid && paramsJSON.String != "" && paramsJSON.String != "null" {
		_ = json.Unmarshal([]byte(paramsJSON.String), &task.Parameters)
	}
	if lastSummary.Valid {
		task.LastSummary = lastSummary.String
	}
	if lastErrorCode.Valid {
		task.LastErrorCode = lastErrorCode.String
	}
	if lastHumanError.Valid {
		task.LastHumanReadableErrorMsg = lastHumanError.String
	}

	return &task, nil
}

func scanReadyTaskRow(row *sql.Row) (*model.Task, error) {
	var task model.Task
	var rawNodeType string
	var status string
	var depsJSON, childrenJSON string
	var paramsJSON sql.NullString

	if err := row.Scan(
		&task.TaskID,
		&task.DAGID,
		&rawNodeType,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&depsJSON,
		&childrenJSON,
		&paramsJSON,
	); err != nil {
		return nil, err
	}

	nodeType, err := model.ParseNodeType(rawNodeType)
	if err != nil {
		return nil, err
	}
	task.NodeType = nodeType
	task.Status = model.TaskStatus(status)
	_ = json.Unmarshal([]byte(depsJSON), &task.Dependencies)
	_ = json.Unmarshal([]byte(childrenJSON), &task.Children)
	if paramsJSON.Valid && paramsJSON.String != "" && paramsJSON.String != "null" {
		_ = json.Unmarshal([]byte(paramsJSON.String), &task.Parameters)
	}
	return &task, nil
}

func newPrefixedID(prefix string) (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate id failed: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}

func scanTaskRows(rows *sql.Rows) (*model.Task, error) {
	var task model.Task
	var rawNodeType string
	var status string
	var depsJSON, childrenJSON string
	var paramsJSON sql.NullString
	var owner sql.NullString
	var expireAt sql.NullTime
	var lastSummary, lastErrorCode, lastHumanError sql.NullString

	if err := rows.Scan(
		&task.TaskID,
		&task.DAGID,
		&rawNodeType,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&owner,
		&expireAt,
		&depsJSON,
		&childrenJSON,
		&paramsJSON,
		&lastSummary,
		&lastErrorCode,
		&lastHumanError,
	); err != nil {
		return nil, err
	}

	nodeType, err := model.ParseNodeType(rawNodeType)
	if err != nil {
		return nil, err
	}
	task.NodeType = nodeType
	task.Status = model.TaskStatus(status)
	if owner.Valid {
		task.OwnerID = owner.String
	}
	if expireAt.Valid {
		timeVal := expireAt.Time.UTC()
		task.ExpireAt = &timeVal
	}
	_ = json.Unmarshal([]byte(depsJSON), &task.Dependencies)
	_ = json.Unmarshal([]byte(childrenJSON), &task.Children)
	if paramsJSON.Valid && paramsJSON.String != "" && paramsJSON.String != "null" {
		_ = json.Unmarshal([]byte(paramsJSON.String), &task.Parameters)
	}
	if lastSummary.Valid {
		task.LastSummary = lastSummary.String
	}
	if lastErrorCode.Valid {
		task.LastErrorCode = lastErrorCode.String
	}
	if lastHumanError.Valid {
		task.LastHumanReadableErrorMsg = lastHumanError.String
	}

	return &task, nil
}

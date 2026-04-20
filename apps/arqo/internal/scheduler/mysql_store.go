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
	db *sql.DB
}

func NewMySQLStoreFromEnv() (*MySQLStore, error) {
	dsn := strings.TrimSpace(os.Getenv("ARQO_MYSQL_DSN"))
	if dsn == "" {
		dsn = "aurora:aurora@tcp(127.0.0.1:3306)/aurora?parseTime=true&multiStatements=true"
	}
	return NewMySQLStore(dsn)
}

func NewMySQLStore(dsn string) (*MySQLStore, error) {
	if !isMySQLDriverRegistered() {
		return nil, errors.New("mysql driver is not registered; install and import github.com/go-sql-driver/mysql before enabling mysql scheduler backend")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql failed: %w", err)
	}

	store := &MySQLStore{db: db}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql failed: %w", err)
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
	return &MySQLStore{db: db}
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

	_, err = tx.Exec(
		`INSERT INTO sessions (session_id, dag_id, user_id, intent, created_at) VALUES (?, ?, ?, ?, ?)`,
		sessionID,
		dagID,
		userID,
		intent,
		now,
	)
	if err != nil {
		return Snapshot{}, err
	}

	_, err = tx.Exec(
		`INSERT INTO dags (dag_id, session_id, user_id, original_intent, status, replan_count, created_at) VALUES (?, ?, ?, ?, 'RUNNING', 0, ?)`,
		dagID,
		sessionID,
		userID,
		intent,
		now,
	)
	if err != nil {
		return Snapshot{}, err
	}

	if err := s.insertTask(tx, queryTaskID, dagID, "QueryLog", "READY", 0, []string{}, []string{summaryTaskID}, now); err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, summaryTaskID, dagID, "LLMSummarize", "PENDING", 1, []string{queryTaskID}, []string{mailTaskID}, now); err != nil {
		return Snapshot{}, err
	}
	if err := s.insertTask(tx, mailTaskID, dagID, "SendEmail", "PENDING", 1, []string{summaryTaskID}, []string{}, now); err != nil {
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

func (s *MySQLStore) PullReadyTask(workerID string, ttl time.Duration) (*model.Task, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRow(`
SELECT task_id, dag_id, skill_name, status, pending_dependencies_count, dependencies_json, children_json
FROM tasks
WHERE status = 'READY'
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED`)

	task, err := scanReadyTaskRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoReadyTask
		}
		return nil, err
	}

	expireAt := time.Now().UTC().Add(ttl)
	_, err = tx.Exec(`UPDATE tasks SET status='RUNNING', owner_id=?, expire_at=? WHERE task_id=?`, workerID, expireAt, task.TaskID)
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

	if input.Success {
		_, err = tx.Exec(`
UPDATE tasks
SET status='SUCCESS', owner_id=NULL, expire_at=NULL, last_summary=?, last_error_code=NULL, last_human_readable_error_msg=NULL
WHERE task_id=?`, input.Summary, input.TaskID)
		if err != nil {
			return nil, err
		}

		raw, _ := json.Marshal(input.RawData)
		_, err = tx.Exec(`
INSERT INTO task_raw_data (task_id, dag_id, raw_data_json, updated_at)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE raw_data_json=VALUES(raw_data_json), updated_at=VALUES(updated_at)`, input.TaskID, current.DAGID, string(raw), time.Now().UTC())
		if err != nil {
			return nil, err
		}

		for _, childID := range current.Children {
			_, err = tx.Exec(`
UPDATE tasks
SET pending_dependencies_count = GREATEST(pending_dependencies_count - 1, 0)
WHERE task_id = ?`, childID)
			if err != nil {
				return nil, err
			}

			_, err = tx.Exec(`
UPDATE tasks
SET status='READY'
WHERE task_id = ? AND pending_dependencies_count = 0 AND status = 'PENDING'`, childID)
			if err != nil {
				return nil, err
			}
		}

		if err := s.refreshDAGStatusTx(tx, current.DAGID); err != nil {
			return nil, err
		}
	} else {
		_, err = tx.Exec(`
UPDATE tasks
SET status='FAILED', owner_id=NULL, expire_at=NULL, last_summary=?, last_error_code=?, last_human_readable_error_msg=?
WHERE task_id=?`, input.Summary, input.ErrorCode, input.ErrorMessage, input.TaskID)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(`
UPDATE dags
SET status='REPLANNING', replan_count = replan_count + 1
WHERE dag_id=?`, current.DAGID)
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

	rows, err := tx.Query(`
SELECT task_id, dag_id
FROM tasks
WHERE status='RUNNING' AND expire_at < ?
FOR UPDATE`, now)
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
		_, err := tx.Exec(`
UPDATE tasks
SET status='FAILED', owner_id=NULL, expire_at=NULL, last_error_code='WORKER_TIMEOUT', last_human_readable_error_msg='worker lease expired'
WHERE task_id=?`, taskID)
		if err != nil {
			return nil
		}
	}
	for dagID := range dagSet {
		_, err := tx.Exec(`UPDATE dags SET status='REPLANNING', replan_count = replan_count + 1 WHERE dag_id=?`, dagID)
		if err != nil {
			return nil
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
       d.dag_id, d.session_id, d.user_id, d.original_intent, d.status, d.replan_count, d.created_at
FROM sessions s
JOIN dags d ON d.session_id = s.session_id
WHERE s.session_id = ?`, sessionID)
	var dagStatus string
	if err := row.Scan(
		&session.SessionID, &session.DAGID, &session.UserID, &session.Intent, &session.CreatedAt,
		&dag.DAGID, &dag.SessionID, &dag.UserID, &dag.OriginalIntent, &dagStatus, &dag.ReplanCount, &dag.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, errors.New("session not found")
		}
		return Snapshot{}, err
	}
	dag.Status = model.DAGStatus(dagStatus)

	rows, err := s.db.Query(`
SELECT task_id, dag_id, skill_name, status, pending_dependencies_count, owner_id, expire_at,
       dependencies_json, children_json, last_summary, last_error_code, last_human_readable_error_msg
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
    status VARCHAR(20) NOT NULL,
    replan_count INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL,
    INDEX idx_dag_session_id (session_id)
);

CREATE TABLE IF NOT EXISTS tasks (
    task_id VARCHAR(64) PRIMARY KEY,
    dag_id VARCHAR(64) NOT NULL,
    skill_name VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL,
    pending_dependencies_count INT NOT NULL,
    owner_id VARCHAR(64) NULL,
    expire_at DATETIME(6) NULL,
    dependencies_json TEXT NOT NULL,
    children_json TEXT NOT NULL,
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
	return nil
}

func (s *MySQLStore) insertTask(tx *sql.Tx, taskID, dagID, skillName, status string, pendingCount int, deps, children []string, createdAt time.Time) error {
	depsJSON, _ := json.Marshal(deps)
	childrenJSON, _ := json.Marshal(children)
	_, err := tx.Exec(`
INSERT INTO tasks (task_id, dag_id, skill_name, status, pending_dependencies_count, dependencies_json, children_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, taskID, dagID, skillName, status, pendingCount, string(depsJSON), string(childrenJSON), createdAt)
	return err
}

func (s *MySQLStore) getTaskByIDTx(tx *sql.Tx, taskID string) (*model.Task, error) {
	row := tx.QueryRow(`
SELECT task_id, dag_id, skill_name, status, pending_dependencies_count, owner_id, expire_at,
       dependencies_json, children_json, last_summary, last_error_code, last_human_readable_error_msg
FROM tasks
WHERE task_id = ?
FOR UPDATE`, taskID)
	return scanTaskRow(row)
}

func (s *MySQLStore) refreshDAGStatusTx(tx *sql.Tx, dagID string) error {
	rows, err := tx.Query(`SELECT status FROM tasks WHERE dag_id=?`, dagID)
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
			_, err = tx.Exec(`UPDATE dags SET status='FAILED' WHERE dag_id=?`, dagID)
			return err
		}
		if status != string(model.TaskStatusSuccess) {
			allSuccess = false
		}
	}

	if allSuccess {
		_, err = tx.Exec(`UPDATE dags SET status='SUCCESS' WHERE dag_id=?`, dagID)
		return err
	}
	return nil
}

func scanTaskRow(row *sql.Row) (*model.Task, error) {
	var task model.Task
	var status string
	var depsJSON, childrenJSON string
	var owner sql.NullString
	var expireAt sql.NullTime
	var lastSummary, lastErrorCode, lastHumanError sql.NullString

	if err := row.Scan(
		&task.TaskID,
		&task.DAGID,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&owner,
		&expireAt,
		&depsJSON,
		&childrenJSON,
		&lastSummary,
		&lastErrorCode,
		&lastHumanError,
	); err != nil {
		return nil, err
	}

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
	var status string
	var depsJSON, childrenJSON string

	if err := row.Scan(
		&task.TaskID,
		&task.DAGID,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&depsJSON,
		&childrenJSON,
	); err != nil {
		return nil, err
	}

	task.Status = model.TaskStatus(status)
	_ = json.Unmarshal([]byte(depsJSON), &task.Dependencies)
	_ = json.Unmarshal([]byte(childrenJSON), &task.Children)
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
	var status string
	var depsJSON, childrenJSON string
	var owner sql.NullString
	var expireAt sql.NullTime
	var lastSummary, lastErrorCode, lastHumanError sql.NullString

	if err := rows.Scan(
		&task.TaskID,
		&task.DAGID,
		&task.SkillName,
		&status,
		&task.PendingDependenciesCount,
		&owner,
		&expireAt,
		&depsJSON,
		&childrenJSON,
		&lastSummary,
		&lastErrorCode,
		&lastHumanError,
	); err != nil {
		return nil, err
	}

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

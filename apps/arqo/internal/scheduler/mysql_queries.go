package scheduler

import (
	"database/sql"
	"time"

	"aurora/apps/arqo/internal/model"
	sq "github.com/Masterminds/squirrel"
)

var mysqlBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Question)

var mysqlReadyTaskColumns = []string{
	"task_id",
	"dag_id",
	"sequence",
	"node_type",
	"skill_name",
	"goal",
	"mem_hint_json",
	"status",
	"pending_dependencies_count",
	"dependencies_json",
	"children_json",
	"parameters_json",
}

var mysqlTaskColumns = []string{
	"task_id",
	"dag_id",
	"sequence",
	"node_type",
	"skill_name",
	"goal",
	"mem_hint_json",
	"status",
	"pending_dependencies_count",
	"owner_id",
	"expire_at",
	"dependencies_json",
	"children_json",
	"parameters_json",
	"last_summary",
	"last_error_code",
	"last_human_readable_error_msg",
}

type mysqlSQLizer interface {
	ToSql() (string, []any, error)
}

func execSQLTx(tx *sql.Tx, builder mysqlSQLizer) (sql.Result, error) {
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, err
	}
	return tx.Exec(query, args...)
}

func querySQLTx(tx *sql.Tx, builder mysqlSQLizer) (*sql.Rows, error) {
	query, args, err := builder.ToSql()
	if err != nil {
		return nil, err
	}
	return tx.Query(query, args...)
}

func queryRowSQLTx(tx *sql.Tx, builder mysqlSQLizer) *sql.Row {
	query, args, err := builder.ToSql()
	if err != nil {
		return tx.QueryRow("SELECT 1 WHERE 0 = ?", 1)
	}
	return tx.QueryRow(query, args...)
}

func insertSessionBuilder(sessionID, dagID, tenantID, agentID, userID, intent string, createdAt time.Time) mysqlSQLizer {
	return mysqlBuilder.Insert("sessions").
		Columns("session_id", "dag_id", "tenant_id", "agent_id", "user_id", "intent", "created_at").
		Values(sessionID, dagID, tenantID, agentID, userID, intent, createdAt)
}

func insertDAGBuilder(dagID, sessionID, tenantID, agentID, userID, intent, intentContextJSON string, createdAt time.Time) mysqlSQLizer {
	return mysqlBuilder.Insert("dags").
		Columns(
			"dag_id",
			"session_id",
			"tenant_id",
			"agent_id",
			"user_id",
			"original_intent",
			"intent_context_json",
			"status",
			"replan_count",
			"current_depth",
			"max_depth",
			"jit_unmapped_streak",
			"max_unmapped_streak",
			"created_at",
		).
		Values(dagID, sessionID, tenantID, agentID, userID, intent, intentContextJSON, model.DAGStatusRunning, 0, 1, 10, 0, 3, createdAt)
}

func insertTaskBuilder(taskID, dagID string, sequence int64, nodeType, skillName, goal, memHintJSON, status string, pendingCount int, depsJSON, childrenJSON, paramsJSON string, createdAt time.Time) mysqlSQLizer {
	return mysqlBuilder.Insert("tasks").
		Columns(
			"task_id",
			"dag_id",
			"sequence",
			"node_type",
			"skill_name",
			"goal",
			"mem_hint_json",
			"status",
			"pending_dependencies_count",
			"dependencies_json",
			"children_json",
			"parameters_json",
			"created_at",
		).
		Values(taskID, dagID, sequence, nodeType, skillName, goal, memHintJSON, status, pendingCount, depsJSON, childrenJSON, paramsJSON, createdAt)
}

func selectReadyTaskForUpdateBuilder() mysqlSQLizer {
	return mysqlBuilder.Select(mysqlReadyTaskColumns...).
		From("tasks").
		Where(sq.Eq{"status": model.TaskStatusReady}).
		OrderBy("created_at ASC").
		Limit(1).
		Suffix("FOR UPDATE SKIP LOCKED")
}

func leaseTaskBuilder(taskID, workerID string, expireAt time.Time) mysqlSQLizer {
	return mysqlBuilder.Update("tasks").
		Set("status", model.TaskStatusRunning).
		Set("owner_id", workerID).
		Set("expire_at", expireAt).
		Where(sq.Eq{"task_id": taskID})
}

func selectExpiredRunningTasksForUpdateBuilder(now time.Time) mysqlSQLizer {
	return mysqlBuilder.Select("task_id", "dag_id").
		From("tasks").
		Where(sq.Eq{"status": model.TaskStatusRunning}).
		Where(sq.Lt{"expire_at": now}).
		Suffix("FOR UPDATE")
}

func expireTaskBuilder(taskID string, policy LeaseExpirePolicy) mysqlSQLizer {
	builder := mysqlBuilder.Update("tasks").
		Set("owner_id", nil).
		Set("expire_at", nil).
		Where(sq.Eq{"task_id": taskID})
	if policy == LeaseExpirePolicyRetryReady {
		return builder.
			Set("status", model.TaskStatusReady).
			Set("last_error_code", "WORKER_TIMEOUT_RETRY").
			Set("last_human_readable_error_msg", "worker lease expired, task returned to ready queue")
	}
	return builder.
		Set("status", model.TaskStatusFailed).
		Set("last_error_code", "WORKER_TIMEOUT").
		Set("last_human_readable_error_msg", "worker lease expired")
}

func markDAGReplanningBuilder(dagID string) mysqlSQLizer {
	return mysqlBuilder.Update("dags").
		Set("status", model.DAGStatusReplanning).
		Set("replan_count", sq.Expr("replan_count + 1")).
		Where(sq.Eq{"dag_id": dagID})
}

func markDAGStatusBuilder(dagID string, status model.DAGStatus) mysqlSQLizer {
	return mysqlBuilder.Update("dags").
		Set("status", status).
		Where(sq.Eq{"dag_id": dagID})
}

func selectTaskByIDForUpdateBuilder(taskID string) mysqlSQLizer {
	return mysqlBuilder.Select(mysqlTaskColumns...).
		From("tasks").
		Where(sq.Eq{"task_id": taskID}).
		Suffix("FOR UPDATE")
}

func selectTaskStatusesByDAGBuilder(dagID string) mysqlSQLizer {
	return mysqlBuilder.Select("status").
		From("tasks").
		Where(sq.Eq{"dag_id": dagID})
}

func markTaskSuccessBuilder(taskID, summary string) mysqlSQLizer {
	return mysqlBuilder.Update("tasks").
		Set("status", model.TaskStatusSuccess).
		Set("owner_id", nil).
		Set("expire_at", nil).
		Set("last_summary", summary).
		Set("last_error_code", nil).
		Set("last_human_readable_error_msg", nil).
		Where(sq.Eq{"task_id": taskID})
}

func markTaskFailedBuilder(taskID, summary, errorCode, errorMessage string) mysqlSQLizer {
	return mysqlBuilder.Update("tasks").
		Set("status", model.TaskStatusFailed).
		Set("owner_id", nil).
		Set("expire_at", nil).
		Set("last_summary", summary).
		Set("last_error_code", errorCode).
		Set("last_human_readable_error_msg", errorMessage).
		Where(sq.Eq{"task_id": taskID})
}

func upsertTaskRawDataBuilder(taskID, dagID, rawDataJSON string, updatedAt time.Time) mysqlSQLizer {
	return mysqlBuilder.Insert("task_raw_data").
		Columns("task_id", "dag_id", "raw_data_json", "updated_at").
		Values(taskID, dagID, rawDataJSON, updatedAt).
		Suffix("ON DUPLICATE KEY UPDATE raw_data_json=VALUES(raw_data_json), updated_at=VALUES(updated_at)")
}

func decrementTaskPendingBuilder(taskID string) mysqlSQLizer {
	return mysqlBuilder.Update("tasks").
		Set("pending_dependencies_count", sq.Expr("GREATEST(pending_dependencies_count - 1, 0)")).
		Where(sq.Eq{"task_id": taskID})
}

func readyTaskWhenDependenciesResolvedBuilder(taskID string) mysqlSQLizer {
	return mysqlBuilder.Update("tasks").
		Set("status", model.TaskStatusReady).
		Where(sq.Eq{"task_id": taskID}).
		Where(sq.Eq{"pending_dependencies_count": 0}).
		Where(sq.Eq{"status": model.TaskStatusPending})
}

package scheduler

import (
	"errors"
	"testing"
	"time"

	"aurora/apps/arqo/internal/model"
	"github.com/DATA-DOG/go-sqlmock"
)

func newMockMySQLStore(t *testing.T) (*MySQLStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	return newMySQLStoreWithDB(db), mock, func() {
		_ = db.Close()
	}
}

func expectReadyTaskQuery(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery(`SELECT task_id, dag_id, node_type, skill_name, status, pending_dependencies_count, dependencies_json, children_json, parameters_json`).
		WillReturnRows(rows)
}

func readyTaskRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"task_id",
		"dag_id",
		"node_type",
		"skill_name",
		"status",
		"pending_dependencies_count",
		"dependencies_json",
		"children_json",
		"parameters_json",
	})
}

func TestMySQLStorePullReadyTaskSuccess(t *testing.T) {
	store, mock, closeDB := newMockMySQLStore(t)
	defer closeDB()

	mock.ExpectBegin()
	expectReadyTaskQuery(mock, readyTaskRows().AddRow(
		"task_1",
		"dag_1",
		"SKILL_SINK",
		"QueryLog",
		"READY",
		0,
		`[]`,
		`["task_2"]`,
		nil,
	))
	mock.ExpectExec(`UPDATE tasks SET status = \?, owner_id = \?, expire_at = \? WHERE task_id = \?`).
		WithArgs(model.TaskStatusRunning, "worker-1", sqlmock.AnyArg(), "task_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	task, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull ready task failed: %v", err)
	}

	if got, want := task.TaskID, "task_1"; got != want {
		t.Fatalf("unexpected task_id: got=%s want=%s", got, want)
	}
	if got, want := task.Status, model.TaskStatusRunning; got != want {
		t.Fatalf("unexpected task status: got=%s want=%s", got, want)
	}
	if got, want := task.NodeType, model.NodeTypeSkillSink; got != want {
		t.Fatalf("unexpected node_type: got=%s want=%s", got, want)
	}
	if got, want := task.OwnerID, "worker-1"; got != want {
		t.Fatalf("unexpected owner_id: got=%s want=%s", got, want)
	}
	if task.ExpireAt == nil {
		t.Fatal("expected expire_at to be set")
	}
	if len(task.Dependencies) != 0 {
		t.Fatalf("expected empty dependencies: %#v", task.Dependencies)
	}
	if got, want := len(task.Children), 1; got != want {
		t.Fatalf("unexpected children size: got=%d want=%d", got, want)
	}
	if got, want := task.Children[0], "task_2"; got != want {
		t.Fatalf("unexpected child: got=%s want=%s", got, want)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
}

func TestMySQLStorePullReadyTaskNoRows(t *testing.T) {
	store, mock, closeDB := newMockMySQLStore(t)
	defer closeDB()

	mock.ExpectBegin()
	expectReadyTaskQuery(mock, readyTaskRows())
	mock.ExpectRollback()

	_, err := store.PullReadyTask("worker-1", time.Minute)
	if !errors.Is(err, ErrNoReadyTask) {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
}

func TestMySQLStoreExpireRunningTasksFailedReplanPolicy(t *testing.T) {
	store, mock, closeDB := newMockMySQLStore(t)
	defer closeDB()
	store.leaseExpirePolicy = LeaseExpirePolicyFailedReplan

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT task_id, dag_id`).
		WithArgs(model.TaskStatusRunning, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "dag_id"}).AddRow("task_1", "dag_1"))
	mock.ExpectExec(`UPDATE tasks`).
		WithArgs(nil, nil, model.TaskStatusFailed, "WORKER_TIMEOUT", "worker lease expired", "task_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE dags SET status = \?, replan_count = replan_count \+ 1 WHERE dag_id = \?`).
		WithArgs(model.DAGStatusReplanning, "dag_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	expired := store.ExpireRunningTasks(time.Now().UTC())
	if len(expired) != 1 || expired[0] != "task_1" {
		t.Fatalf("unexpected expired task ids: %v", expired)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
}

func TestMySQLStoreExpireRunningTasksRetryReadyPolicy(t *testing.T) {
	store, mock, closeDB := newMockMySQLStore(t)
	defer closeDB()
	store.leaseExpirePolicy = LeaseExpirePolicyRetryReady

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT task_id, dag_id`).
		WithArgs(model.TaskStatusRunning, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "dag_id"}).AddRow("task_1", "dag_1"))
	mock.ExpectExec(`UPDATE tasks`).
		WithArgs(nil, nil, model.TaskStatusReady, "WORKER_TIMEOUT_RETRY", "worker lease expired, task returned to ready queue", "task_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	expired := store.ExpireRunningTasks(time.Now().UTC())
	if len(expired) != 1 || expired[0] != "task_1" {
		t.Fatalf("unexpected expired task ids: %v", expired)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
}

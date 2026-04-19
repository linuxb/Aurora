package scheduler

import (
	"errors"
	"testing"
	"time"

	"aurora/apps/arqo/internal/model"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestMySQLStorePullReadyTaskSuccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	defer db.Close()

	store := newMySQLStoreWithDB(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT task_id, dag_id, skill_name, status, pending_dependencies_count, dependencies_json, children_json`).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"task_id",
				"dag_id",
				"skill_name",
				"status",
				"pending_dependencies_count",
				"dependencies_json",
				"children_json",
			}).AddRow(
				"task_1",
				"dag_1",
				"QueryLog",
				"READY",
				0,
				`[]`,
				`["task_2"]`,
			),
		)
	mock.ExpectExec(`UPDATE tasks SET status='RUNNING', owner_id=\?, expire_at=\? WHERE task_id=\?`).
		WithArgs("worker-1", sqlmock.AnyArg(), "task_1").
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
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	defer db.Close()

	store := newMySQLStoreWithDB(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT task_id, dag_id, skill_name, status, pending_dependencies_count, dependencies_json, children_json`).
		WillReturnRows(sqlmock.NewRows([]string{
			"task_id",
			"dag_id",
			"skill_name",
			"status",
			"pending_dependencies_count",
			"dependencies_json",
			"children_json",
		}))
	mock.ExpectRollback()

	_, err = store.PullReadyTask("worker-1", time.Minute)
	if !errors.Is(err, ErrNoReadyTask) {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations not met: %v", err)
	}
}

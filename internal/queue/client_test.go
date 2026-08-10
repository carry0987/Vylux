package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

type fakeCompensationInspector struct {
	tasks       map[string]map[string]*asynq.TaskInfo
	cancelCalls int
	cancelErr   error
	inspectErr  error
}

func (i *fakeCompensationInspector) CancelProcessing(string) error {
	i.cancelCalls++
	return i.cancelErr
}

func (i *fakeCompensationInspector) DeleteTask(queue, id string) error {
	byQueue, ok := i.tasks[queue]
	if !ok {
		return asynq.ErrQueueNotFound
	}
	info, ok := byQueue[id]
	if !ok {
		return asynq.ErrTaskNotFound
	}
	if info.State == asynq.TaskStateActive {
		return errors.New("still active")
	}
	delete(byQueue, id)
	return nil
}

func (i *fakeCompensationInspector) GetTaskInfo(queue, id string) (*asynq.TaskInfo, error) {
	if i.inspectErr != nil {
		return nil, i.inspectErr
	}
	byQueue, ok := i.tasks[queue]
	if !ok {
		return nil, asynq.ErrQueueNotFound
	}
	info, ok := byQueue[id]
	if !ok {
		return nil, asynq.ErrTaskNotFound
	}
	return info, nil
}

func TestRemoveTaskDeletesExplicitPendingTaskID(t *testing.T) {
	inspector := &fakeCompensationInspector{tasks: map[string]map[string]*asynq.TaskInfo{
		QueueCritical: {
			"operation-token": {ID: "operation-token", Queue: QueueCritical, State: asynq.TaskStatePending},
		},
	}}

	if err := removeTask(context.Background(), inspector, "operation-token"); err != nil {
		t.Fatalf("removeTask returned error: %v", err)
	}
	if _, ok := inspector.tasks[QueueCritical]["operation-token"]; ok {
		t.Fatal("expected compensating delete of explicit task ID")
	}
	if inspector.cancelCalls != 1 {
		t.Fatalf("expected cancellation before inspection, got %d", inspector.cancelCalls)
	}
}

func TestRemoveTaskTreatsProvenAbsenceAsSuccess(t *testing.T) {
	inspector := &fakeCompensationInspector{tasks: map[string]map[string]*asynq.TaskInfo{}}
	if err := removeTask(context.Background(), inspector, "missing"); err != nil {
		t.Fatalf("missing task should be safe compensation: %v", err)
	}
}

func TestRemoveTaskFailsClosedOnInspectorError(t *testing.T) {
	inspector := &fakeCompensationInspector{
		tasks:     map[string]map[string]*asynq.TaskInfo{},
		cancelErr: errors.New("redis unavailable"),
	}
	if err := removeTask(context.Background(), inspector, "ambiguous"); err == nil {
		t.Fatal("inspector failure must preserve durable DB intent")
	}
}

func TestTaskExistsFindsExplicitTaskAcrossQueues(t *testing.T) {
	inspector := &fakeCompensationInspector{tasks: map[string]map[string]*asynq.TaskInfo{
		QueueVideoLarge: {
			"operation-token": {ID: "operation-token", Queue: QueueVideoLarge, State: asynq.TaskStatePending},
		},
	}}

	exists, err := taskExists(t.Context(), inspector, "operation-token")
	if err != nil {
		t.Fatalf("taskExists returned error: %v", err)
	}
	if !exists {
		t.Fatal("expected taskExists to find explicit task ID")
	}
}

func TestTaskExistsPrunesRetainedTerminalTaskForReenqueue(t *testing.T) {
	inspector := &fakeCompensationInspector{tasks: map[string]map[string]*asynq.TaskInfo{
		QueueCritical: {
			"operation-token": {ID: "operation-token", Queue: QueueCritical, State: asynq.TaskStateCompleted},
		},
	}}

	exists, err := taskExists(t.Context(), inspector, "operation-token")
	if err != nil {
		t.Fatalf("taskExists returned error: %v", err)
	}
	if exists {
		t.Fatal("retained completed task is not a live queued intent")
	}
	if _, ok := inspector.tasks[QueueCritical]["operation-token"]; ok {
		t.Fatal("completed task must be removed so its durable ID can be re-enqueued")
	}
}

func TestTaskExistsTreatsProvenAbsenceAsFalse(t *testing.T) {
	exists, err := taskExists(
		t.Context(),
		&fakeCompensationInspector{tasks: map[string]map[string]*asynq.TaskInfo{}},
		"missing",
	)
	if err != nil {
		t.Fatalf("taskExists returned error: %v", err)
	}
	if exists {
		t.Fatal("missing task must not be reported as present")
	}
}

func TestTaskExistsFailsClosedOnInspectorError(t *testing.T) {
	inspector := &fakeCompensationInspector{
		tasks:      map[string]map[string]*asynq.TaskInfo{},
		inspectErr: errors.New("redis unavailable"),
	}
	if _, err := taskExists(t.Context(), inspector, "ambiguous"); err == nil {
		t.Fatal("inspector failure must not be treated as task absence")
	}
}

func TestVideoQueueOptions(t *testing.T) {
	tests := []struct {
		name           string
		fileSize       int64
		largeThreshold int64
		wantQueue      string
		wantRetry      int
	}{
		{name: "small file uses default queue", fileSize: 100, largeThreshold: 200, wantQueue: QueueDefault, wantRetry: 3},
		{name: "threshold match uses large queue", fileSize: 200, largeThreshold: 200, wantQueue: QueueVideoLarge, wantRetry: 2},
		{name: "large file uses large queue", fileSize: 300, largeThreshold: 200, wantQueue: QueueVideoLarge, wantRetry: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queueName, maxRetry := videoQueueOptions(tt.fileSize, tt.largeThreshold)
			if queueName != tt.wantQueue || maxRetry != tt.wantRetry {
				t.Fatalf("videoQueueOptions(%d, %d) = (%q, %d), want (%q, %d)", tt.fileSize, tt.largeThreshold, queueName, maxRetry, tt.wantQueue, tt.wantRetry)
			}
		})
	}
}

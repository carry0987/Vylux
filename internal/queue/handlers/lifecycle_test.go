package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"Vylux/internal/db/dbq"
	"Vylux/internal/queue"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
)

type fakeTaskIntentQueries struct {
	job dbq.Job
	err error
}

func (q fakeTaskIntentQueries) GetJob(context.Context, string) (dbq.Job, error) {
	return q.job, q.err
}

func TestValidateLifecyclePayloadPreservesWhitespaceDistinctSource(t *testing.T) {
	hash := strings.Repeat("f", 64)
	rawSource := "uploads/ " + hash + ".png"
	if err := validateLifecyclePayload(lifecyclePayload{Hash: hash, Source: rawSource}); err != nil {
		t.Fatalf("exact source was rejected: %v", err)
	}
	if err := validateLifecyclePayload(lifecyclePayload{Hash: hash, Source: " \t\n "}); err == nil {
		t.Fatal("whitespace-only source must remain invalid")
	}
}

func TestRequireTaskIntentRejectsMissingMismatchedAndTerminalJobs(t *testing.T) {
	hash := strings.Repeat("a", 64)
	source := "uploads/ " + hash + "-upload-id.png "
	callbackURL := "https://host.example.test/api/vylux/webhook"
	taskID := "6ba7b812-9dad-11d1-80b4-00c04fd430c8"
	outputs := []queue.ThumbnailOutput{{Variant: "thumb", Width: 64, Format: "webp"}}
	options, err := json.Marshal(struct {
		Outputs []queue.ThumbnailOutput `json:"outputs"`
	}{Outputs: outputs})
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	task, err := queue.NewImageThumbnailTask(&queue.ImageThumbnailPayload{
		Hash:        hash,
		Source:      source,
		Outputs:     outputs,
		CallbackURL: callbackURL,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var queuedPayload queue.ImageThumbnailPayload
	if err := json.Unmarshal(task.Payload(), &queuedPayload); err != nil {
		t.Fatalf("decode queued task: %v", err)
	}
	if queuedPayload.Source != source {
		t.Fatalf("queued source = %q, want exact %q", queuedPayload.Source, source)
	}
	payload := lifecyclePayload{Hash: hash, Source: source, CallbackURL: callbackURL}
	base := dbq.Job{
		ID:          taskID,
		Type:        queue.TypeImageThumbnail,
		Hash:        hash,
		Source:      source,
		Options:     options,
		Status:      "queued",
		CallbackUrl: callbackURL,
	}

	if err := requireTaskIntent(
		t.Context(),
		fakeTaskIntentQueries{job: base},
		taskID,
		queue.TypeImageThumbnail,
		payload,
		task.Payload(),
	); err != nil {
		t.Fatalf("matching durable intent was rejected: %v", err)
	}

	tests := []struct {
		name    string
		queries fakeTaskIntentQueries
		jobType string
		raw     []byte
	}{
		{name: "missing row", queries: fakeTaskIntentQueries{err: pgx.ErrNoRows}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "whitespace-distinct source", queries: fakeTaskIntentQueries{job: func() dbq.Job {
			job := base
			job.Source = "uploads/" + hash + "-upload-id.png"
			return job
		}()}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "wrong type", queries: fakeTaskIntentQueries{job: func() dbq.Job {
			job := base
			job.Type = queue.TypeVideoCover
			return job
		}()}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "terminal row", queries: fakeTaskIntentQueries{job: func() dbq.Job {
			job := base
			job.Status = "completed"
			return job
		}()}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "canceled row", queries: fakeTaskIntentQueries{job: func() dbq.Job {
			job := base
			job.Status = "canceled"
			return job
		}()}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "different callback", queries: fakeTaskIntentQueries{job: func() dbq.Job {
			job := base
			job.CallbackUrl = "https://other.example.test/webhook"
			return job
		}()}, jobType: queue.TypeImageThumbnail, raw: task.Payload()},
		{name: "different options", queries: fakeTaskIntentQueries{job: base}, jobType: queue.TypeImageThumbnail, raw: func() []byte {
			changed, createErr := queue.NewImageThumbnailTask(&queue.ImageThumbnailPayload{
				Hash:        hash,
				Source:      source,
				Outputs:     []queue.ThumbnailOutput{{Variant: "large", Width: 1024, Format: "avif"}},
				CallbackURL: callbackURL,
			})
			if createErr != nil {
				t.Fatalf("create changed task: %v", createErr)
			}
			return changed.Payload()
		}()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := requireTaskIntent(t.Context(), test.queries, taskID, test.jobType, payload, test.raw)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("unsafe task must be permanently rejected, got %v", err)
			}
		})
	}
}

func TestRequireTaskIntentRetriesDatabaseFailures(t *testing.T) {
	testErr := errors.New("database unavailable")
	err := requireTaskIntent(
		t.Context(),
		fakeTaskIntentQueries{err: testErr},
		"operation-token",
		queue.TypeImageThumbnail,
		lifecyclePayload{},
		[]byte(`{}`),
	)
	if !errors.Is(err, testErr) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("transient DB error must remain retryable, got %v", err)
	}
}

package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
)

// Client wraps asynq.Client to provide a typed enqueue API.
type Client struct {
	inner     *asynq.Client
	inspector *asynq.Inspector
}

type compensationInspector interface {
	CancelProcessing(id string) error
	DeleteTask(queue, id string) error
	GetTaskInfo(queue, id string) (*asynq.TaskInfo, error)
}

const compensationAttempts = 20
const compensationBackoff = 50 * time.Millisecond

// NewClient creates a new queue client connected to the given Redis URL.
func NewClient(redisURL string) (*Client, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	return &Client{
		inner:     asynq.NewClient(opt),
		inspector: asynq.NewInspector(opt),
	}, nil
}

// Close releases the underlying Redis connection.
func (c *Client) Close() error {
	return errors.Join(c.inner.Close(), c.inspector.Close())
}

// Enqueue submits a task with optional asynq options (queue, max-retry, etc.).
// Returns the asynq.TaskInfo on success.
func (c *Client) Enqueue(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	info, err := c.inner.EnqueueContext(ctx, task, opts...)
	if err != nil {
		return nil, fmt.Errorf("enqueue %s: %w", task.Type(), err)
	}

	slog.Info("task enqueued",
		apptracing.LogFields(ctx,
			"type", task.Type(),
			"id", info.ID,
			"queue", info.Queue,
		)...,
	)

	return info, nil
}

// RemoveTask compensates an enqueue whose result is ambiguous. Callers must
// keep the durable DB intent unless this method proves the explicit task ID is
// absent from every Vylux queue.
func (c *Client) RemoveTask(ctx context.Context, taskID string) error {
	if c == nil || c.inspector == nil {
		return fmt.Errorf("queue compensation inspector is unavailable")
	}
	return removeTask(ctx, c.inspector, taskID)
}

// TaskExists inspects every Vylux queue for a live explicit task ID. Retained
// terminal tasks are removed so a durable queued intent can re-enqueue the same
// ID. An incomplete inspection is an error rather than evidence of absence.
func (c *Client) TaskExists(ctx context.Context, taskID string) (bool, error) {
	if c == nil || c.inspector == nil {
		return false, fmt.Errorf("queue inspector is unavailable")
	}
	return taskExists(ctx, c.inspector, taskID)
}

func removeTask(ctx context.Context, inspector compensationInspector, taskID string) error {
	queues := []string{QueueCritical, QueueDefault, QueueVideoLarge}
	for attempt := 0; attempt < compensationAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("compensate task %s: %w", taskID, err)
		}

		if err := inspector.CancelProcessing(taskID); err != nil && !isCompensationNotFound(err) {
			return fmt.Errorf("cancel compensating task %s: %w", taskID, err)
		}

		isActive := false
		var inspectErrors []error
		for _, queueName := range queues {
			info, err := inspector.GetTaskInfo(queueName, taskID)
			if err != nil {
				if !isCompensationNotFound(err) {
					inspectErrors = append(inspectErrors, fmt.Errorf("inspect queue %s: %w", queueName, err))
				}
				continue
			}
			if info.State == asynq.TaskStateActive {
				isActive = true
				continue
			}
			if err := inspector.DeleteTask(queueName, taskID); err != nil && !isCompensationNotFound(err) {
				inspectErrors = append(inspectErrors, fmt.Errorf("delete task from queue %s: %w", queueName, err))
			}
		}
		if len(inspectErrors) > 0 {
			return fmt.Errorf("compensate task %s: %w", taskID, errors.Join(inspectErrors...))
		}
		if !isActive {
			return nil
		}

		if attempt == compensationAttempts-1 {
			return fmt.Errorf("compensate task %s: task remains active", taskID)
		}
		timer := time.NewTimer(compensationBackoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("compensate task %s: %w", taskID, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("compensate task %s: attempts exhausted", taskID)
}

func taskExists(ctx context.Context, inspector compensationInspector, taskID string) (bool, error) {
	var inspectErrors []error
	for _, queueName := range []string{QueueCritical, QueueDefault, QueueVideoLarge} {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("inspect task %s: %w", taskID, err)
		}
		info, err := inspector.GetTaskInfo(queueName, taskID)
		if err == nil {
			switch info.State {
			case asynq.TaskStateArchived, asynq.TaskStateCompleted:
				if err := inspector.DeleteTask(queueName, taskID); err != nil {
					if isCompensationNotFound(err) {
						continue
					}
					inspectErrors = append(
						inspectErrors,
						fmt.Errorf("delete terminal task from queue %s: %w", queueName, err),
					)
					continue
				}
				continue
			default:
				return true, nil
			}
		}
		if !isCompensationNotFound(err) {
			inspectErrors = append(inspectErrors, fmt.Errorf("inspect queue %s: %w", queueName, err))
		}
	}
	if len(inspectErrors) > 0 {
		return false, fmt.Errorf("inspect task %s: %w", taskID, errors.Join(inspectErrors...))
	}
	return false, nil
}

func isCompensationNotFound(err error) bool {
	return errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound)
}

func videoQueueOptions(fileSize int64, largeThreshold int64) (string, int) {
	if fileSize >= largeThreshold {
		return QueueVideoLarge, 2
	}

	return QueueDefault, 3
}

// EnqueueImageThumbnail enqueues an image:thumbnail task on the critical queue.
func (c *Client) EnqueueImageThumbnail(ctx context.Context, taskID string, p *ImageThumbnailPayload) (*asynq.TaskInfo, error) {
	task, err := NewImageThumbnailTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueCritical),
		asynq.MaxRetry(3),
		asynq.TaskID(taskID),
	)
}

// EnqueueVideoCover enqueues a video:cover task on the default queue.
func (c *Client) EnqueueVideoCover(ctx context.Context, taskID string, p *VideoCoverPayload) (*asynq.TaskInfo, error) {
	task, err := NewVideoCoverTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(3),
		asynq.TaskID(taskID),
	)
}

// EnqueueVideoPreview enqueues a video:preview task on the default queue.
func (c *Client) EnqueueVideoPreview(ctx context.Context, taskID string, p *VideoPreviewPayload) (*asynq.TaskInfo, error) {
	task, err := NewVideoPreviewTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(3),
		asynq.TaskID(taskID),
	)
}

// EnqueueVideoTranscode enqueues a video:transcode task.
// Files >= largeThreshold are routed to the video:large queue with fewer retries.
func (c *Client) EnqueueVideoTranscode(ctx context.Context, taskID string, p *VideoTranscodePayload, fileSize int64, largeThreshold int64) (*asynq.TaskInfo, error) {
	task, err := NewVideoTranscodeTask(p)
	if err != nil {
		return nil, err
	}

	q, maxRetry := videoQueueOptions(fileSize, largeThreshold)

	return c.Enqueue(ctx, task,
		asynq.Queue(q),
		asynq.MaxRetry(maxRetry),
		asynq.TaskID(taskID),
	)
}

// EnqueueVideoFull enqueues a video:full task.
// Files >= largeThreshold are routed to the video:large queue with fewer retries.
func (c *Client) EnqueueVideoFull(ctx context.Context, taskID string, p *VideoFullPayload, fileSize int64, largeThreshold int64) (*asynq.TaskInfo, error) {
	task, err := NewVideoFullTask(p)
	if err != nil {
		return nil, err
	}

	q, maxRetry := videoQueueOptions(fileSize, largeThreshold)

	return c.Enqueue(ctx, task,
		asynq.Queue(q),
		asynq.MaxRetry(maxRetry),
		asynq.TaskID(taskID),
	)
}

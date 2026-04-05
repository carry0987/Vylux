package queue

import (
	"context"
	"fmt"
	"log/slog"

	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
)

// Client wraps asynq.Client to provide a typed enqueue API.
type Client struct {
	inner *asynq.Client
}

// NewClient creates a new queue client connected to the given Redis URL.
func NewClient(redisURL string) (*Client, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	return &Client{inner: asynq.NewClient(opt)}, nil
}

// Close releases the underlying Redis connection.
func (c *Client) Close() error {
	return c.inner.Close()
}

// Enqueue submits a task with optional asynq options (queue, max-retry, etc.).
// Returns the asynq.TaskInfo on success.
func (c *Client) Enqueue(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	info, err := c.inner.Enqueue(task, opts...)
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

func videoQueueOptions(fileSize int64, largeThreshold int64) (string, int) {
	if fileSize >= largeThreshold {
		return QueueVideoLarge, 2
	}

	return QueueDefault, 3
}

// EnqueueImageThumbnail enqueues an image:thumbnail task on the critical queue.
func (c *Client) EnqueueImageThumbnail(ctx context.Context, p ImageThumbnailPayload) (*asynq.TaskInfo, error) {
	task, err := NewImageThumbnailTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueCritical),
		asynq.MaxRetry(3),
	)
}

// EnqueueVideoCover enqueues a video:cover task on the default queue.
func (c *Client) EnqueueVideoCover(ctx context.Context, p VideoCoverPayload) (*asynq.TaskInfo, error) {
	task, err := NewVideoCoverTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(3),
	)
}

// EnqueueVideoPreview enqueues a video:preview task on the default queue.
func (c *Client) EnqueueVideoPreview(ctx context.Context, p VideoPreviewPayload) (*asynq.TaskInfo, error) {
	task, err := NewVideoPreviewTask(p)
	if err != nil {
		return nil, err
	}

	return c.Enqueue(ctx, task,
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(3),
	)
}

// EnqueueVideoTranscode enqueues a video:transcode task.
// Files >= largeThreshold are routed to the video:large queue with fewer retries.
func (c *Client) EnqueueVideoTranscode(ctx context.Context, p VideoTranscodePayload, fileSize int64, largeThreshold int64) (*asynq.TaskInfo, error) {
	task, err := NewVideoTranscodeTask(p)
	if err != nil {
		return nil, err
	}

	q, maxRetry := videoQueueOptions(fileSize, largeThreshold)

	return c.Enqueue(ctx, task,
		asynq.Queue(q),
		asynq.MaxRetry(maxRetry),
	)
}

// EnqueueVideoFull enqueues a video:full task.
// Files >= largeThreshold are routed to the video:large queue with fewer retries.
func (c *Client) EnqueueVideoFull(ctx context.Context, p VideoFullPayload, fileSize int64, largeThreshold int64) (*asynq.TaskInfo, error) {
	task, err := NewVideoFullTask(p)
	if err != nil {
		return nil, err
	}

	q, maxRetry := videoQueueOptions(fileSize, largeThreshold)

	return c.Enqueue(ctx, task,
		asynq.Queue(q),
		asynq.MaxRetry(maxRetry),
	)
}

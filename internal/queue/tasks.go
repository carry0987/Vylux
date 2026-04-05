package queue

import (
	"encoding/json"
	"fmt"

	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
)

// Task type constants — must match the design doc.
const (
	TypeImageThumbnail = "image:thumbnail"
	TypeVideoCover     = "video:cover"
	TypeVideoPreview   = "video:preview"
	TypeVideoTranscode = "video:transcode"
	TypeVideoFull      = "video:full"
)

// Queue name constants — priority-based routing.
const (
	QueueCritical   = "critical"    // fast tasks (image thumbnails)
	QueueDefault    = "default"     // normal video tasks
	QueueVideoLarge = "video:large" // large file transcoding (low concurrency)
)

// ImageThumbnailPayload is the payload for image:thumbnail tasks.
type ImageThumbnailPayload struct {
	apptracing.TraceCarrier
	Hash        string            `json:"hash"`
	Source      string            `json:"source"`
	Outputs     []ThumbnailOutput `json:"outputs"`
	CallbackURL string            `json:"callback_url"`
}

// ThumbnailOutput describes a single thumbnail variant to generate.
type ThumbnailOutput struct {
	Variant string `json:"variant"` // e.g. "thumb", "medium", "large"
	Width   int    `json:"width"`
	Height  int    `json:"height,omitempty"`
	Format  string `json:"format"` // "webp", "avif", "jpg", "png"
}

// VideoCoverOptions describes configurable cover extraction options.
type VideoCoverOptions struct {
	TimestampSec float64 `json:"timestamp_sec,omitempty"` // default: 1
}

// VideoPreviewOptions describes configurable animated preview options.
type VideoPreviewOptions struct {
	StartSec float64 `json:"start_sec,omitempty"` // default: 2
	Duration float64 `json:"duration,omitempty"`  // default: 3
	Width    int     `json:"width,omitempty"`     // default: 480
	FPS      int     `json:"fps,omitempty"`       // default: 10
	Format   string  `json:"format,omitempty"`    // "webp" (default) or "gif"
}

// VideoTranscodeOptions describes configurable transcode options.
type VideoTranscodeOptions struct {
	Encrypt bool `json:"encrypt,omitempty"`
}

// VideoFullOptions groups the per-stage options for video:full jobs.
type VideoFullOptions struct {
	Cover     *VideoCoverOptions     `json:"cover,omitempty"`
	Preview   *VideoPreviewOptions   `json:"preview,omitempty"`
	Transcode *VideoTranscodeOptions `json:"transcode,omitempty"`
}

// VideoCoverPayload is the payload for video:cover tasks.
type VideoCoverPayload struct {
	apptracing.TraceCarrier
	Hash         string  `json:"hash"`
	Source       string  `json:"source"`
	TimestampSec float64 `json:"timestamp_sec,omitempty"` // default: 1
	CallbackURL  string  `json:"callback_url"`
}

// VideoPreviewPayload is the payload for video:preview tasks.
type VideoPreviewPayload struct {
	apptracing.TraceCarrier
	Hash        string  `json:"hash"`
	Source      string  `json:"source"`
	StartSec    float64 `json:"start_sec,omitempty"` // default: 2
	Duration    float64 `json:"duration,omitempty"`  // default: 3
	Width       int     `json:"width,omitempty"`     // default: 480
	FPS         int     `json:"fps,omitempty"`       // default: 10
	Format      string  `json:"format,omitempty"`    // "webp" (default) or "gif"
	CallbackURL string  `json:"callback_url"`
}

// VideoTranscodePayload is the payload for video:transcode tasks.
type VideoTranscodePayload struct {
	apptracing.TraceCarrier
	Hash        string `json:"hash"`
	Source      string `json:"source"`
	Encrypt     bool   `json:"encrypt,omitempty"` // AES-128 encryption
	CallbackURL string `json:"callback_url"`
}

// VideoFullPayload is the payload for video:full tasks.
// The handler executes cover, preview, and transcode as one workflow task.
type VideoFullPayload struct {
	apptracing.TraceCarrier
	Hash        string           `json:"hash"`
	Source      string           `json:"source"`
	Options     VideoFullOptions `json:"options,omitempty"`
	CallbackURL string           `json:"callback_url"`
}

// ---- Task constructors ----

// NewImageThumbnailTask creates an image:thumbnail task.
func NewImageThumbnailTask(p ImageThumbnailPayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal image:thumbnail payload: %w", err)
	}

	return asynq.NewTask(TypeImageThumbnail, data), nil
}

// NewVideoCoverTask creates a video:cover task.
func NewVideoCoverTask(p VideoCoverPayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal video:cover payload: %w", err)
	}

	return asynq.NewTask(TypeVideoCover, data), nil
}

// NewVideoPreviewTask creates a video:preview task.
func NewVideoPreviewTask(p VideoPreviewPayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal video:preview payload: %w", err)
	}

	return asynq.NewTask(TypeVideoPreview, data), nil
}

// NewVideoTranscodeTask creates a video:transcode task.
func NewVideoTranscodeTask(p VideoTranscodePayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal video:transcode payload: %w", err)
	}

	return asynq.NewTask(TypeVideoTranscode, data), nil
}

// NewVideoFullTask creates a video:full task.
func NewVideoFullTask(p VideoFullPayload) (*asynq.Task, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal video:full payload: %w", err)
	}

	return asynq.NewTask(TypeVideoFull, data), nil
}

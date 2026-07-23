package queue

import (
	"encoding/json"
	"testing"

	"github.com/hibiken/asynq"
)

func TestTaskConstructors_JSONShape(t *testing.T) {
	tests := []struct {
		name        string
		wantType    string
		build       func(t *testing.T) *asynq.Task
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:     "image thumbnail payload keeps required fields",
			wantType: TypeImageThumbnail,
			build: func(t *testing.T) *asynq.Task {
				t.Helper()
				task, err := NewImageThumbnailTask(&ImageThumbnailPayload{
					Hash:   "hash",
					Source: "source.jpg",
					Outputs: []ThumbnailOutput{{
						Variant: "thumb",
						Width:   320,
						Format:  "webp",
					}},
					CallbackURL: "https://example.com/callback",
				})
				if err != nil {
					t.Fatalf("NewImageThumbnailTask returned error: %v", err)
				}
				return task
			},
			wantPresent: []string{"hash", "source", "outputs", "callback_url"},
		},
		{
			name:     "video cover omits zero timestamp",
			wantType: TypeVideoCover,
			build: func(t *testing.T) *asynq.Task {
				t.Helper()
				task, err := NewVideoCoverTask(&VideoCoverPayload{
					Hash:        "hash",
					Source:      "source.mp4",
					CallbackURL: "https://example.com/callback",
				})
				if err != nil {
					t.Fatalf("NewVideoCoverTask returned error: %v", err)
				}
				return task
			},
			wantPresent: []string{"hash", "source", "callback_url"},
			wantAbsent:  []string{"timestamp_sec"},
		},
		{
			name:     "video preview omits zero-value tuning fields",
			wantType: TypeVideoPreview,
			build: func(t *testing.T) *asynq.Task {
				t.Helper()
				task, err := NewVideoPreviewTask(&VideoPreviewPayload{
					Hash:        "hash",
					Source:      "source.mp4",
					CallbackURL: "https://example.com/callback",
				})
				if err != nil {
					t.Fatalf("NewVideoPreviewTask returned error: %v", err)
				}
				return task
			},
			wantPresent: []string{"hash", "source", "callback_url"},
			wantAbsent:  []string{"start_sec", "duration", "width", "fps", "format"},
		},
		{
			name:     "video transcode omits false encrypt flag",
			wantType: TypeVideoTranscode,
			build: func(t *testing.T) *asynq.Task {
				t.Helper()
				task, err := NewVideoTranscodeTask(&VideoTranscodePayload{
					Hash:        "hash",
					Source:      "source.mp4",
					CallbackURL: "https://example.com/callback",
				})
				if err != nil {
					t.Fatalf("NewVideoTranscodeTask returned error: %v", err)
				}
				return task
			},
			wantPresent: []string{"hash", "source", "callback_url"},
			wantAbsent:  []string{"encrypt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := tt.build(t)
			if task.Type() != tt.wantType {
				t.Fatalf("task type = %q, want %q", task.Type(), tt.wantType)
			}

			raw := decodeTaskPayload(t, task)
			for _, key := range tt.wantPresent {
				if _, ok := raw[key]; !ok {
					t.Fatalf("task payload missing %q field: %s", key, task.Payload())
				}
			}
			for _, key := range tt.wantAbsent {
				if _, ok := raw[key]; ok {
					t.Fatalf("task payload unexpectedly contains %q field: %s", key, task.Payload())
				}
			}
		})
	}
}

func TestNewVideoFullTask_JSONShape(t *testing.T) {
	tests := []struct {
		name        string
		payload     *VideoFullPayload
		wantOptions string
	}{
		{
			name: "empty options still marshal as object",
			payload: &VideoFullPayload{
				Hash:        "hash",
				Source:      "source.mp4",
				CallbackURL: "https://example.com/callback",
			},
			wantOptions: "{}",
		},
		{
			name: "populated options preserve configured fields",
			payload: &VideoFullPayload{
				Hash:   "hash",
				Source: "source.mp4",
				Options: VideoFullOptions{
					Cover:     &VideoCoverOptions{TimestampSec: 3.5},
					Transcode: &VideoTranscodeOptions{Encrypt: true},
				},
				CallbackURL: "https://example.com/callback",
			},
			wantOptions: `{"cover":{"timestamp_sec":3.5},"transcode":{"encrypt":true}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := NewVideoFullTask(tt.payload)
			if err != nil {
				t.Fatalf("NewVideoFullTask returned error: %v", err)
			}

			raw := decodeTaskPayload(t, task)

			options, ok := raw["options"]
			if !ok {
				t.Fatalf("task payload missing options field: %s", task.Payload())
			}
			if string(options) != tt.wantOptions {
				t.Fatalf("task payload options = %s, want %s", options, tt.wantOptions)
			}
			if _, ok := raw["hash"]; !ok {
				t.Fatalf("task payload missing hash field: %s", task.Payload())
			}
			if _, ok := raw["source"]; !ok {
				t.Fatalf("task payload missing source field: %s", task.Payload())
			}
			if _, ok := raw["callback_url"]; !ok {
				t.Fatalf("task payload missing callback_url field: %s", task.Payload())
			}
		})
	}
}

func decodeTaskPayload(t *testing.T, task *asynq.Task) map[string]json.RawMessage {
	t.Helper()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(task.Payload(), &raw); err != nil {
		t.Fatalf("unmarshal task payload: %v", err)
	}

	return raw
}

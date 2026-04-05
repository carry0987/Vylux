package jobflow

const (
	StageSource    = "source"
	StageCover     = "cover"
	StagePreview   = "preview"
	StageTranscode = "transcode"
)

const (
	StatusPending = "pending"
	StatusReady   = "ready"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

const (
	RetryStrategyNone       = "none"
	RetryStrategyRetryJob   = "retry_job"
	RetryStrategyRetryTasks = "retry_tasks"
)

type StageState struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type CoverArtifact struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Size   int    `json:"size"`
}

type PreviewArtifact struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Size   int    `json:"size"`
}

type StreamingArtifact struct {
	Protocol            string `json:"protocol"`
	Container           string `json:"container"`
	Encrypted           bool   `json:"encrypted"`
	MasterPlaylist      string `json:"master_playlist"`
	DefaultAudioTrackID string `json:"default_audio_track_id,omitempty"`
}

type AudioTrackArtifact struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Language string `json:"language"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Bitrate  int    `json:"bitrate"`
	Playlist string `json:"playlist"`
	Init     string `json:"init"`
	Segments int    `json:"segment_count"`
}

type VideoTrackArtifact struct {
	ID            string   `json:"id"`
	Codec         string   `json:"codec"`
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	Bitrate       int      `json:"bitrate"`
	Playlist      string   `json:"playlist"`
	Init          string   `json:"init"`
	Segments      int      `json:"segment_count"`
	AudioTrackIDs []string `json:"audio_track_ids,omitempty"`
}

type EncryptionArtifact struct {
	Scheme      string `json:"scheme"`
	KID         string `json:"kid"`
	KeyEndpoint string `json:"key_endpoint"`
}

type TranscodeArtifact struct {
	Streaming    StreamingArtifact    `json:"streaming"`
	AudioTracks  []AudioTrackArtifact `json:"audio_tracks,omitempty"`
	VideoTracks  []VideoTrackArtifact `json:"video_tracks"`
	Encryption   *EncryptionArtifact  `json:"encryption,omitempty"`
	UploadedKeys []string             `json:"uploaded_keys"`
}

type VideoFullArtifacts struct {
	Cover     *CoverArtifact     `json:"cover,omitempty"`
	Preview   *PreviewArtifact   `json:"preview,omitempty"`
	Transcode *TranscodeArtifact `json:"transcode,omitempty"`
}

type VideoFullStages struct {
	Source    StageState `json:"source"`
	Cover     StageState `json:"cover"`
	Preview   StageState `json:"preview"`
	Transcode StageState `json:"transcode"`
}

type RetryPlan struct {
	Allowed  bool     `json:"allowed"`
	Strategy string   `json:"strategy,omitempty"`
	JobTypes []string `json:"job_types,omitempty"`
	Stages   []string `json:"stages,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

type VideoFullResult struct {
	Stages    VideoFullStages    `json:"stages"`
	Artifacts VideoFullArtifacts `json:"artifacts"`
	RetryPlan RetryPlan          `json:"retry_plan"`
}

func NewVideoFullResult() VideoFullResult {
	return VideoFullResult{
		Stages: VideoFullStages{
			Source:    StageState{Status: StatusPending},
			Cover:     StageState{Status: StatusPending},
			Preview:   StageState{Status: StatusPending},
			Transcode: StageState{Status: StatusPending},
		},
		Artifacts: VideoFullArtifacts{},
		RetryPlan: RetryPlan{Allowed: false, Strategy: RetryStrategyNone},
	}
}

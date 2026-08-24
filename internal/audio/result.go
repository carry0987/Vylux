package audio

const (
	StageSource    = "source"
	StageProbe     = "probe"
	StagePackage   = "package"
	StageDownloads = "downloads"
	StageWaveform  = "waveform"

	StatusPending = "pending"
	StatusReady   = "ready"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// StageState describes the state of one audio processing stage.
type StageState struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ProcessStages describes the per-stage state of the audio pipeline.
type ProcessStages struct {
	Source    StageState `json:"source"`
	Probe     StageState `json:"probe"`
	Package   StageState `json:"package"`
	Downloads StageState `json:"downloads"`
	Waveform  StageState `json:"waveform"`
}

// FailureContext summarizes the stage that caused the audio job to fail.
type FailureContext struct {
	Stage     string `json:"stage"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// DownloadArtifact describes one downloadable audio output.
type DownloadArtifact struct {
	Format  string `json:"format"`
	Bitrate int    `json:"bitrate,omitempty"`
	Key     string `json:"key"`
	Size    int64  `json:"size"`
}

// WaveformArtifact describes the stored waveform JSON output.
type WaveformArtifact struct {
	Key  string `json:"key"`
	Bins int    `json:"bins"`
}

// HLSStreamingArtifact describes audio-only HLS output.
type HLSStreamingArtifact struct {
	Protocol           string              `json:"protocol"`
	Container          string              `json:"container"`
	MasterPlaylist     string              `json:"master_playlist"`
	DefaultRenditionID string              `json:"default_rendition_id,omitempty"`
	Renditions         []RenditionArtifact `json:"renditions"`
}

// RenditionArtifact describes one published HLS rendition.
type RenditionArtifact struct {
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

// ProcessResult describes the current audio processing outputs.
type ProcessResult struct {
	Analysis  ProbeResult           `json:"analysis"`
	Stages    ProcessStages         `json:"stages"`
	Failure   *FailureContext       `json:"failure,omitempty"`
	Streaming *HLSStreamingArtifact `json:"streaming,omitempty"`
	Downloads []DownloadArtifact    `json:"downloads,omitempty"`
	Waveform  *WaveformArtifact     `json:"waveform,omitempty"`
}

// NewProcessResult creates the audio result envelope with stage defaults.
func NewProcessResult(enablePackage, enableDownloads, enableWaveform bool) ProcessResult {
	return ProcessResult{
		Stages: ProcessStages{
			Source:    PendingStage(),
			Probe:     PendingStage(),
			Package:   stageForEnabled(enablePackage),
			Downloads: stageForEnabled(enableDownloads),
			Waveform:  stageForEnabled(enableWaveform),
		},
	}
}

// PendingStage returns the default state for an enabled stage that has not run yet.
func PendingStage() StageState {
	return StageState{Status: StatusPending}
}

// ReadyStage returns the state for a completed stage.
func ReadyStage() StageState {
	return StageState{Status: StatusReady}
}

// FailedStage returns the state for a failed stage.
func FailedStage(code, message string) StageState {
	return StageState{Status: StatusFailed, ErrorCode: code, Error: message}
}

// SkippedStage returns the state for a skipped stage.
func SkippedStage(reason string) StageState {
	return StageState{Status: StatusSkipped, Reason: reason}
}

// MarkFailure records the summary failure context for the process result.
func (r *ProcessResult) MarkFailure(stage, code, message string) {
	if r == nil {
		return
	}
	r.Failure = &FailureContext{Stage: stage, ErrorCode: code, Message: message}
}

func stageForEnabled(enabled bool) StageState {
	if enabled {
		return PendingStage()
	}
	return SkippedStage("disabled")
}

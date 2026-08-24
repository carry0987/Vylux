package handlers

import (
	"strings"
	"testing"

	"Vylux/internal/audio"
)

func TestSkipPendingAudioStagesLeavesNonPendingStagesUntouched(t *testing.T) {
	result := audio.NewProcessResult(true, true, true)
	result.Stages.Source = audio.ReadyStage()
	result.Stages.Waveform = audio.FailedStage("generate_failed", "generate waveform: boom")

	skipPendingAudioStages(&result, "blocked_by_source_failure")

	if result.Stages.Source.Status != audio.StatusReady {
		t.Fatalf("source status = %q, want ready", result.Stages.Source.Status)
	}
	if result.Stages.Probe.Status != audio.StatusSkipped {
		t.Fatalf("probe status = %q, want skipped", result.Stages.Probe.Status)
	}
	if result.Stages.Package.Reason != "blocked_by_source_failure" {
		t.Fatalf("package reason = %q", result.Stages.Package.Reason)
	}
	if result.Stages.Waveform.Status != audio.StatusFailed {
		t.Fatalf("waveform status = %q, want failed", result.Stages.Waveform.Status)
	}
}

func TestAudioProcessFailureErrorUsesFailureSummary(t *testing.T) {
	result := audio.NewProcessResult(false, false, false)
	result.MarkFailure(audio.StageDownloads, "upload_failed", "upload flac: boom")

	err := audioProcessFailureError(&result)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "audio process failed at downloads") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "upload flac: boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

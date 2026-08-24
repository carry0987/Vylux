package audio

import (
	"encoding/json"
	"testing"
)

func TestProcessResultJSONOmitsMediaKind(t *testing.T) {
	data, err := json.Marshal(ProcessResult{
		Analysis: ProbeResult{
			Container:   "flac",
			DurationSec: 12.5,
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := decoded["media_kind"]; ok {
		t.Fatalf("expected media_kind to be omitted, got %#v", decoded["media_kind"])
	}
	if _, ok := decoded["analysis"]; !ok {
		t.Fatalf("expected analysis field, got %#v", decoded)
	}
}

func TestNewProcessResultInitializesStageStates(t *testing.T) {
	result := NewProcessResult(true, false, true)

	if result.Stages.Source.Status != StatusPending {
		t.Fatalf("source status = %q, want %q", result.Stages.Source.Status, StatusPending)
	}
	if result.Stages.Probe.Status != StatusPending {
		t.Fatalf("probe status = %q, want %q", result.Stages.Probe.Status, StatusPending)
	}
	if result.Stages.Package.Status != StatusPending {
		t.Fatalf("package status = %q, want %q", result.Stages.Package.Status, StatusPending)
	}
	if result.Stages.Downloads.Status != StatusSkipped || result.Stages.Downloads.Reason != "disabled" {
		t.Fatalf("downloads stage = %+v, want skipped disabled", result.Stages.Downloads)
	}
	if result.Stages.Waveform.Status != StatusPending {
		t.Fatalf("waveform status = %q, want %q", result.Stages.Waveform.Status, StatusPending)
	}
}

func TestProcessResultMarkFailureSetsSummary(t *testing.T) {
	result := NewProcessResult(false, false, false)
	result.MarkFailure(StageProbe, "probe_failed", "probe source: boom")

	if result.Failure == nil {
		t.Fatal("expected failure summary")
	}
	if result.Failure.Stage != StageProbe {
		t.Fatalf("failure stage = %q, want %q", result.Failure.Stage, StageProbe)
	}
	if result.Failure.ErrorCode != "probe_failed" {
		t.Fatalf("failure error code = %q", result.Failure.ErrorCode)
	}
}

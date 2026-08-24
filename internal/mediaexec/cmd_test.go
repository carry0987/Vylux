package mediaexec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdOutputCapturesStdout(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "emit-output.sh")
	script := "#!/bin/sh\nprintf 'hello world'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	out, err := New(context.Background(), scriptPath).Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if string(out) != "hello world" {
		t.Fatalf("stdout = %q, want %q", string(out), "hello world")
	}
}

func TestCmdRunTeeStderr(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "emit-stderr.sh")
	script := "#!/bin/sh\nprintf 'warning line' >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	var stderr bytes.Buffer
	if err := New(context.Background(), scriptPath).Run(&stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stderr.String(); got != "warning line" {
		t.Fatalf("stderr = %q, want %q", got, "warning line")
	}
}

func TestCmdOutputIncludesStderrOnFailure(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "fail.sh")
	script := "#!/bin/sh\nprintf 'broken' >&2\nexit 7\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	_, err := New(context.Background(), scriptPath).Output()
	if err == nil {
		t.Fatal("expected Output error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error = %q, want stderr text", err)
	}
}

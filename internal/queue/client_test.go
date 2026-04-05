package queue

import "testing"

func TestVideoQueueOptions(t *testing.T) {
	tests := []struct {
		name           string
		fileSize       int64
		largeThreshold int64
		wantQueue      string
		wantRetry      int
	}{
		{name: "small file uses default queue", fileSize: 100, largeThreshold: 200, wantQueue: QueueDefault, wantRetry: 3},
		{name: "threshold match uses large queue", fileSize: 200, largeThreshold: 200, wantQueue: QueueVideoLarge, wantRetry: 2},
		{name: "large file uses large queue", fileSize: 300, largeThreshold: 200, wantQueue: QueueVideoLarge, wantRetry: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queueName, maxRetry := videoQueueOptions(tt.fileSize, tt.largeThreshold)
			if queueName != tt.wantQueue || maxRetry != tt.wantRetry {
				t.Fatalf("videoQueueOptions(%d, %d) = (%q, %d), want (%q, %d)", tt.fileSize, tt.largeThreshold, queueName, maxRetry, tt.wantQueue, tt.wantRetry)
			}
		})
	}
}

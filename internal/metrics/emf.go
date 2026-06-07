package metrics

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// EMFRecorder emits CloudWatch Embedded Metric Format (EMF) documents to a
// writer (stdout in Fargate, captured by the awslogs driver). CloudWatch parses
// these log lines into real metrics WITHOUT a PutMetricData call on the request
// path — so the per-vendor latency alarm (the one we added after the incident)
// has live data, dimensioned by Provider and Outcome.
//
// It implements the same metrics.Recorder port as the in-memory Collector, so
// swapping it in is a one-line wiring change (the Stage-2 decorator never knows).
type EMFRecorder struct {
	mu        sync.Mutex
	w         io.Writer
	namespace string
	now       func() time.Time
}

func NewEMFRecorder(w io.Writer, namespace string) *EMFRecorder {
	if namespace == "" {
		namespace = "FraudSignals"
	}
	return &EMFRecorder{w: w, namespace: namespace, now: time.Now}
}

func (r *EMFRecorder) ObserveProvider(name string, outcome Outcome, latency time.Duration) {
	doc := map[string]any{
		"_aws": map[string]any{
			"Timestamp": r.now().UnixMilli(),
			"CloudWatchMetrics": []map[string]any{{
				"Namespace": r.namespace,
				// Two dimension sets: (Provider,Outcome) for detail, and Provider
				// alone so the per-vendor latency alarm matches regardless of outcome.
				"Dimensions": [][]string{{"Provider", "Outcome"}, {"Provider"}},
				"Metrics": []map[string]any{
					{"Name": "ProviderLatencyMs", "Unit": "Milliseconds"},
				},
			}},
		},
		"Provider":          name,
		"Outcome":           string(outcome),
		"ProviderLatencyMs": float64(latency.Microseconds()) / 1000.0,
	}

	// One JSON object per line; serialize concurrent writers so lines don't interleave.
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = json.NewEncoder(r.w).Encode(doc)
}

package metrics

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestEMFRecorder_EmitsValidEMF(t *testing.T) {
	var buf bytes.Buffer
	r := NewEMFRecorder(&buf, "FraudSignals")
	r.ObserveProvider("credit_bureau", OutcomeSuccess, 12500*time.Microsecond)

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("EMF output is not valid JSON: %v", err)
	}

	if doc["Provider"] != "credit_bureau" || doc["Outcome"] != "success" {
		t.Errorf("dimensions wrong: %v / %v", doc["Provider"], doc["Outcome"])
	}
	if got := doc["ProviderLatencyMs"].(float64); got != 12.5 {
		t.Errorf("latency = %v ms, want 12.5", got)
	}
	aws, ok := doc["_aws"].(map[string]any)
	if !ok {
		t.Fatal("missing _aws metadata block")
	}
	cwm := aws["CloudWatchMetrics"].([]any)[0].(map[string]any)
	if cwm["Namespace"] != "FraudSignals" {
		t.Errorf("namespace = %v", cwm["Namespace"])
	}
	// Two dimension sets: [Provider,Outcome] and [Provider].
	if dims := cwm["Dimensions"].([]any); len(dims) != 2 {
		t.Errorf("expected 2 dimension sets, got %d", len(dims))
	}
}

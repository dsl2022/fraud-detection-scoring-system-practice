// Package metrics defines a small, pluggable interface for recording per-vendor
// telemetry, plus an in-memory implementation for tests/local use.
//
// We keep this behind an interface so the call sites (the guardedProvider) never
// know whether telemetry goes to memory, Prometheus, or CloudWatch EMF. Stage 3
// adds an exposition endpoint and Stage 5 wires the cloud backend — neither
// requires touching the providers.
package metrics

import (
	"sync"
	"time"
)

// Outcome is the categorical result of a single provider call. These become a
// metric dimension/label per vendor, which is exactly what the post-incident
// per-vendor latency alarm needs.
type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeError       Outcome = "error"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeBreakerOpen Outcome = "breaker_open"
)

// Recorder is the port the guardedProvider depends on. One method keeps it
// trivial to implement and to mock.
type Recorder interface {
	ObserveProvider(name string, outcome Outcome, latency time.Duration)
}

// Nop discards everything. Useful as a default when telemetry isn't wired.
type Nop struct{}

func (Nop) ObserveProvider(string, Outcome, time.Duration) {}

// Collector is a concurrency-safe in-memory Recorder. It aggregates per
// (vendor, outcome): a count and total latency, so tests can assert "the slow
// vendor's breaker opened" and local runs can eyeball behaviour.
type Collector struct {
	mu      sync.Mutex
	entries map[string]map[Outcome]*stat
}

type stat struct {
	Count      int64
	TotalNanos int64
}

func NewCollector() *Collector {
	return &Collector{entries: make(map[string]map[Outcome]*stat)}
}

func (c *Collector) ObserveProvider(name string, outcome Outcome, latency time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	byOutcome, ok := c.entries[name]
	if !ok {
		byOutcome = make(map[Outcome]*stat)
		c.entries[name] = byOutcome
	}
	s, ok := byOutcome[outcome]
	if !ok {
		s = &stat{}
		byOutcome[outcome] = s
	}
	s.Count++
	s.TotalNanos += latency.Nanoseconds()
}

// Count returns how many times (vendor, outcome) was observed.
func (c *Collector) Count(name string, outcome Outcome) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if byOutcome, ok := c.entries[name]; ok {
		if s, ok := byOutcome[outcome]; ok {
			return s.Count
		}
	}
	return 0
}

// AvgLatency returns the mean latency for (vendor, outcome), or 0 if none.
func (c *Collector) AvgLatency(name string, outcome Outcome) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if byOutcome, ok := c.entries[name]; ok {
		if s, ok := byOutcome[outcome]; ok && s.Count > 0 {
			return time.Duration(s.TotalNanos / s.Count)
		}
	}
	return 0
}

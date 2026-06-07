// Package domain holds the core business types of the fraud-signals platform.
//
// In hexagonal architecture this is the very center of the hexagon: it has NO
// imports of infrastructure (no net/http, no AWS, no gRPC). Everything else
// (ports, adapters, transport) depends inward on these types; these types
// depend on nothing. That keeps the scoring logic testable in isolation and
// lets us swap transports/vendors without touching the rules.
package domain

// Decision is the categorical outcome returned to the caller.
//
// We model it as a small set of string constants (not a bool / not an int)
// because the value is serialized over JSON and gRPC, persisted in the audit
// record, and reasoned about by humans during a review. A typed string keeps it
// self-describing on the wire while still giving us compile-time constants.
type Decision string

const (
	DecisionApprove      Decision = "APPROVE"
	DecisionDecline      Decision = "DECLINE"
	DecisionManualReview Decision = "MANUAL_REVIEW"
)

// Application is the inbound thing we score: an account-opening or payment
// application. Fields are deliberately split into "demographic" and
// "transaction" groups because different vendors care about different subsets.
//
// JSON tags use snake_case to match the REST contract. omitempty is used on
// optional fields so a minimal request stays small.
type Application struct {
	ApplicantID string `json:"applicant_id"`
	Product     string `json:"product"`

	// Demographic signals.
	FullName string `json:"full_name,omitempty"`
	Email    string `json:"email,omitempty"`
	Country  string `json:"country,omitempty"`
	AgeYears int    `json:"age_years,omitempty"`

	// Transaction / behavioural signals.
	RequestedAmount float64 `json:"requested_amount,omitempty"`
	AccountAgeDays  int     `json:"account_age_days,omitempty"`
	RecentTxnCount  int     `json:"recent_txn_count,omitempty"`

	// PriorSignals is the INTERNAL enrichment channel for two-phase
	// orchestration. The independent (Stage A) providers see this empty; a
	// dependent provider (the credit bureau in Stage B) is called with the
	// application enriched by Stage A's results, so it can augment its request
	// with upstream identity/transaction signals.
	//
	// json:"-" keeps it strictly off the wire — a client can neither set nor see
	// it; it exists only inside the pipeline. Keeping it on Application (rather
	// than a separate dependent-provider method) lets the Provider port stay a
	// single uniform interface that both provider classes — and Stage 2's
	// guardedProvider wrapper — implement identically.
	PriorSignals []RiskData `json:"-"`
}

// RiskData is what a SINGLE provider returns. The aggregator combines many of
// these into one ScoreResult.
//
//   - Score:      0..100, higher == riskier. Normalised so vendors are comparable.
//   - Confidence: 0..1, how much this vendor trusts its own answer. Used to
//     decide whether we have enough signal to make an automated decision.
//   - Weight:     relative importance of this vendor in the weighted average.
type RiskData struct {
	Source     string  `json:"source"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	Weight     float64 `json:"weight"`
}

// ScoreResult is the response surfaced to the caller (and persisted to audit).
//
// SignalsUsed lists the vendors that ACTUALLY returned by the deadline — this is
// a first-class part of the contract because, under graceful degradation, the
// set of signals used varies per request and an auditor must be able to see
// exactly which sources fed a given decision.
type ScoreResult struct {
	Score        float64  `json:"score"`
	Decision     Decision `json:"decision"`
	SignalsUsed  []string `json:"signals_used"`
	LogicVersion string   `json:"logic_version"`
}

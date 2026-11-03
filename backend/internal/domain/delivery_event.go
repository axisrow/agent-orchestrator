package domain

import "time"

// Delivery journal cause classes (fork issue #242, «строгий учёт»). The cause
// is the stable, grouped reason class; the raw human-readable detail travels
// in the event's Reason. A terminal outcome always names its cause: "just
// disappeared" is not a state the journal can represent.
const (
	// DeliveryEventEmitted is the report landing in the mailbox: the row's
	// first journal event, written with CreateReport.
	DeliveryEventEmitted = "emitted"
	// DeliveryEventDeliveryAttempt is a delivery attempt claiming the row.
	DeliveryEventDeliveryAttempt = "delivery_attempt"
	// DeliveryEventEmitVisible stamps the at-most-once visible emission.
	DeliveryEventEmitVisible = "emit_visible"
	// DeliveryEventAcknowledged is the terminal success: the content reached
	// the recipient's channel and was accepted (delivered and settled are one
	// durable fact on the current pipeline).
	DeliveryEventAcknowledged = "acknowledged"
	// DeliveryEventReleased returns a claim to pending after a failed cycle.
	DeliveryEventReleased = "released"
	// DeliveryEventDeferred postpones a claim's next availability.
	DeliveryEventDeferred = "deferred"
	// DeliveryEventExpired is the terminal ledger expiry (pending past the
	// window, currently 7d).
	DeliveryEventExpired = "expired"
	// DeliveryEventRepromptSuperseded is the terminal retirement of a worker's
	// undelivered reports when a new prompt reaches the session (fork issue
	// #240): the reports describe the previous task.
	DeliveryEventRepromptSuperseded = "reprompt_superseded"
	// DeliveryEventRecipientGone is the terminal outcome for a recipient that
	// can never exist again (archived project, fork issue #208).
	DeliveryEventRecipientGone = "recipient_gone"
	// DeliveryEventInjectFailed is the terminal outcome of an injection that
	// must not retry (the target held the text in its message queue, fork
	// issue #205); the raw failure travels in Reason.
	DeliveryEventInjectFailed = "inject_failed"
	// DeliveryEventRestartRequeue returns a claim owned by a prior daemon
	// process to pending.
	DeliveryEventRestartRequeue = "restart_requeue"
	// DeliveryEventBackfill is the one-time migration snapshot of rows that
	// predate the journal; their intermediate history is unknowable.
	DeliveryEventBackfill = "backfill"
)

// DeliveryEvent is one append-only delivery-journal transition for a
// deliveries row: from_state → to_state with a stable cause class and the raw
// reason. Rows are never updated or deleted; a delivery's fate is the chain
// of its events read in id order.
type DeliveryEvent struct {
	ID         int64
	DeliveryID string
	SessionID  SessionID
	ProjectID  ProjectID
	FromState  string
	ToState    string
	Cause      string
	Reason     string
	CreatedAt  time.Time
}

// TerminalDeliveryStates are the ledger states no further transition leaves.
var TerminalDeliveryStates = map[string]bool{
	string(ReportAcknowledged): true,
	string(ReportStale):        true,
}

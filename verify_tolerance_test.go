package dominaite

import (
	"testing"
	"time"
)

// Zero tolerance means "the timestamp must be now", not "skip the check". The
// old behaviour turned the strictest-looking setting into no replay protection
// at all, so anyone who reached for WithWebhookTolerance(0) to tighten things up
// silently switched it off.
func TestVerifyWebhookZeroToleranceRequiresExactTimestamp(t *testing.T) {
	stale := WithWebhookClock(func() time.Time { return time.Unix(1755700000+3600, 0) })
	_, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, stale, WithWebhookTolerance(0))
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)

	// One second off is still off.
	offByOne := WithWebhookClock(func() time.Time { return time.Unix(1755700001, 0) })
	_, err = VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, offByOne, WithWebhookTolerance(0))
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)

	// Exactly now passes.
	if _, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, atVectorTime(), WithWebhookTolerance(0)); err != nil {
		t.Fatalf("timestamp equal to now must verify at zero tolerance: %v", err)
	}
}

// A negative window has no meaning and would reject every delivery. The other
// SDKs refuse it outright rather than letting it look like a working config.
func TestVerifyWebhookRejectsNegativeTolerance(t *testing.T) {
	_, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, atVectorTime(), WithWebhookTolerance(-time.Second))
	assertReason(t, err, WebhookReasonInvalidTolerance)
}

// The freshness check now runs on every delivery, so a timestamp it cannot turn
// into an instant has to fail rather than skip the check. Digits too large for
// an int64 are the only way to get there past the grammar.
func TestVerifyWebhookRejectsUnrepresentableTimestamp(t *testing.T) {
	const huge = "99999999999999999999999"
	header := "t=" + huge + ",v1=" + signWebhook(webhookVector.Secret, huge, webhookVector.Body)
	_, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, atVectorTime())
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)
}

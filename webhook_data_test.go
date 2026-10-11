package dominaite

import (
	"strconv"
	"testing"
	"time"
)

// verifySigned signs body with the vector secret at a pinned instant and
// returns what VerifyWebhook makes of it.
func verifySigned(t *testing.T, body string) (*WebhookEvent, error) {
	t.Helper()
	now := time.Unix(1760173200, 0)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	header := "t=" + timestamp + ",v1=" + signWebhook(webhookVector.Secret, timestamp, body)
	return VerifyWebhook([]byte(body), header, webhookVector.Secret, WithWebhookClock(func() time.Time { return now }))
}

func TestAPaymentFailedEventCarriesItsDeclineClass(t *testing.T) {
	event, err := verifySigned(t, `{"id":"evt-1","type":"payment.failed","createdAt":"2026-10-11T09:00:00Z",`+
		`"data":{"transactionId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","status":"failed","previousStatus":"pending",`+
		`"amount":2500,"currency":"EUR","verifyOnly":false,"declineClass":"soft_funds"}}`)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if event.Data.DeclineClass != DeclineClassSoftFunds || event.Data.VerifyOnly {
		t.Fatalf("data = %+v", event.Data)
	}
}

// An event without a decline class decodes with DeclineClass empty, whether
// the field is absent or null.
func TestAnEventWithoutADeclineClassReadsAsEmpty(t *testing.T) {
	for name, fields := range map[string]string{
		"absent": ``,
		"null":   `,"verifyOnly":null,"declineClass":null`,
	} {
		t.Run(name, func(t *testing.T) {
			event, err := verifySigned(t, `{"id":"evt-2","type":"payment.failed","createdAt":"2026-10-11T09:00:00Z",`+
				`"data":{"transactionId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","status":"failed","amount":2500,"currency":"EUR"`+
				fields+`}}`)
			if err != nil {
				t.Fatalf("VerifyWebhook: %v", err)
			}
			if event.Data.DeclineClass != "" || event.Data.VerifyOnly {
				t.Fatalf("data = %+v", event.Data)
			}
		})
	}
}

func TestAVerificationEventSaysVerifyOnly(t *testing.T) {
	event, err := verifySigned(t, `{"id":"evt-3","type":"verification.failed","createdAt":"2026-10-11T09:00:00Z",`+
		`"data":{"transactionId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","status":"failed","amount":0,"currency":"EUR",`+
		`"verifyOnly":true,"declineClass":"hard"}}`)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if event.Type != EventVerificationFailed || !event.Data.VerifyOnly || event.Data.DeclineClass != DeclineClassHard {
		t.Fatalf("event = %+v", event)
	}
}

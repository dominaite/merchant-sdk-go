package dominaite

import (
	"encoding/json"
	"testing"
)

// Payload shapes follow the gateway's RecurringWebhookEventMapper and the
// payment envelope as of apiVersion 2026-09-25.

func TestVerifyWebhookParsesAPIVersionOnThePaymentEnvelope(t *testing.T) {
	body := `{"id":"d1","type":"payment.succeeded","apiVersion":"2026-09-25","createdAt":"2026-09-25T10:00:00Z","data":{"transactionId":"tx","status":"succeeded","amount":100,"grossAmount":100,"currency":"EUR"}}`
	event := mustVerify(t, body)

	if event.APIVersion != "2026-09-25" {
		t.Errorf("APIVersion = %q, want 2026-09-25", event.APIVersion)
	}
	if event.CreatedAt != "2026-09-25T10:00:00Z" {
		t.Errorf("CreatedAt = %q", event.CreatedAt)
	}
	// payment.* events carry no sequence; the zero value is the documented
	// "absent", which the ordering rule already treats as oldest.
	if event.Data.Sequence != 0 {
		t.Errorf("Sequence = %d on a payment event, want 0", event.Data.Sequence)
	}
}

func TestVerifyWebhookParsesAgreementEventSequenceAndCreatedAt(t *testing.T) {
	body := `{"id":"d2","type":"agreement.past_due","apiVersion":"2026-09-25","createdAt":"2026-09-25T11:00:00Z","data":{"id":"agr_0123456789abcdef0123456789abcdef","planId":"plan_1","customerReference":"cust-1","storedPaymentMethodId":"pm_0123456789abcdef0123456789abcdef","status":"past_due","previousStatus":"active","amount":990,"currency":"EUR","intervalUnit":"month","intervalCount":1,"periodCount":null,"trialDays":0,"nextChargeAt":"2026-09-25T00:00:00Z","activatedAt":"2026-08-25T00:00:00Z","cancelledAt":null,"version":4,"sequence":3}}`
	event := mustVerify(t, body)

	if event.Type != "agreement.past_due" {
		t.Errorf("Type = %q", event.Type)
	}
	if event.APIVersion != "2026-09-25" {
		t.Errorf("APIVersion = %q", event.APIVersion)
	}
	if event.CreatedAt != "2026-09-25T11:00:00Z" {
		t.Errorf("CreatedAt = %q", event.CreatedAt)
	}
	if event.Data.Sequence != 3 {
		t.Errorf("Sequence = %d, want 3", event.Data.Sequence)
	}
	if event.Data.Status != "past_due" || event.Data.PreviousStatus != "active" {
		t.Errorf("status %q, previous %q", event.Data.Status, event.Data.PreviousStatus)
	}

	// The ordering key for agreement.* is data.id, read from Raw.
	var key struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(event.Data.Raw, &key); err != nil || key.ID != "agr_0123456789abcdef0123456789abcdef" {
		t.Errorf("data.id from Raw = %q, err %v", key.ID, err)
	}
}

func TestVerifyWebhookParsesChargeEventSequenceAndCreatedAt(t *testing.T) {
	body := `{"id":"d3","type":"charge.retrying","apiVersion":"2026-09-25","createdAt":"2026-09-25T12:00:00Z","data":{"chargeId":"ch_0123456789abcdef0123456789abcdef","transactionId":"tx-2","storedPaymentMethodId":"pm_0123456789abcdef0123456789abcdef","agreementId":"agr_0123456789abcdef0123456789abcdef","customerReference":"cust-1","outcome":"retrying","periodNumber":2,"attemptNumber":1,"amount":990,"currency":"EUR","paymentMethod":{"brand":"visa","last4":"4242"},"orderReference":"order-9","description":null,"declineClass":"soft_funds","declineCode":"51","nextAttemptAt":"2026-09-26T12:00:00Z","nextChargeAt":null,"sequence":7}}`
	event := mustVerify(t, body)

	if event.Type != "charge.retrying" {
		t.Errorf("Type = %q", event.Type)
	}
	if event.APIVersion != "2026-09-25" {
		t.Errorf("APIVersion = %q", event.APIVersion)
	}
	if event.CreatedAt != "2026-09-25T12:00:00Z" {
		t.Errorf("CreatedAt = %q", event.CreatedAt)
	}
	if event.Data.Sequence != 7 {
		t.Errorf("Sequence = %d, want 7", event.Data.Sequence)
	}
	if event.Data.TransactionID != "tx-2" || event.Data.Amount != 990 || event.Data.Currency != "EUR" {
		t.Errorf("transaction %q, amount %d, currency %q", event.Data.TransactionID, event.Data.Amount, event.Data.Currency)
	}

	// The ordering key for a platform charge is the agreement period, read from Raw.
	var key struct {
		AgreementID  string `json:"agreementId"`
		PeriodNumber int    `json:"periodNumber"`
	}
	if err := json.Unmarshal(event.Data.Raw, &key); err != nil {
		t.Fatalf("decode Raw: %v", err)
	}
	if key.AgreementID != "agr_0123456789abcdef0123456789abcdef" || key.PeriodNumber != 2 {
		t.Errorf("period key = %q / %d", key.AgreementID, key.PeriodNumber)
	}
}

// Deliveries from a gateway that predates apiVersion and sequence must still
// verify and parse, with both fields at their zero value.
func TestVerifyWebhookAcceptsPayloadsWithoutAPIVersionOrSequence(t *testing.T) {
	event, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, atVectorTime())
	if err != nil {
		t.Fatalf("canonical vector must verify, got: %v", err)
	}
	if event.APIVersion != "" || event.Data.Sequence != 0 {
		t.Errorf("APIVersion %q, Sequence %d, want both zero", event.APIVersion, event.Data.Sequence)
	}

	body := `{"id":"d4","type":"charge.succeeded","createdAt":"2026-09-01T00:00:00Z","data":{"chargeId":"ch_0123456789abcdef0123456789abcdef","transactionId":"tx-3","agreementId":null,"periodNumber":null,"amount":500,"currency":"EUR"}}`
	event = mustVerify(t, body)
	if event.APIVersion != "" || event.Data.Sequence != 0 {
		t.Errorf("APIVersion %q, Sequence %d, want both zero", event.APIVersion, event.Data.Sequence)
	}
	if event.Data.TransactionID != "tx-3" || event.Data.Amount != 500 {
		t.Errorf("transaction %q, amount %d", event.Data.TransactionID, event.Data.Amount)
	}

	// A null sequence is the same as an absent one.
	body = `{"id":"d5","type":"agreement.cancelled","createdAt":"2026-09-01T00:00:00Z","data":{"id":"agr_x","status":"cancelled","sequence":null}}`
	event = mustVerify(t, body)
	if event.Data.Sequence != 0 {
		t.Errorf("null sequence became %d", event.Data.Sequence)
	}
}

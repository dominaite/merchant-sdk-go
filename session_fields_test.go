package dominaite

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// Session request fields added for verify-only and subscription sessions:
// what reaches the gateway in the signed body, and which amounts the SDK
// refuses before sending.

func verifyOnlyParams() CreateCheckoutSessionParams {
	params := testParams()
	params.Amount = 0
	params.VerifyOnly = true
	return params
}

func TestAVerifyOnlySessionSendsAmountZeroAndVerifyOnly(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	if _, err := client.CreateCheckoutSession(context.Background(), verifyOnlyParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	want := `{"amount":0,"currency":"EUR","orderReference":"order-1042","verifyOnly":true}`
	if call.Body != want {
		t.Fatalf("body = %s\nwant   %s", call.Body, want)
	}
	assertSignedWith(t, call, http.MethodPost, SessionsPath, vector.IdempotencyKey)
}

// 0.4.0 callers could only send verifyOnly through Extra. That keeps working.
func TestVerifyOnlyThroughExtraStillWorks(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Amount = 0
	params.Extra = map[string]any{"verifyOnly": true}
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if body := sentBody(t, calls()[0]); body["verifyOnly"] != true || body["amount"] != float64(0) {
		t.Fatalf("body = %v", body)
	}
}

// Extra wins over the field, so the body says what Extra says, and the
// amount rule follows the body.
func TestVerifyOnlyFalseInExtraWinsOverTheField(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := verifyOnlyParams()
	params.Extra = map[string]any{"verifyOnly": false}
	_, err := client.CreateCheckoutSession(context.Background(), params)

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("got %v, want *ValidationError: the body is not verify-only, so amount 0 is refused", err)
	}
	if len(calls()) != 0 {
		t.Fatal("a refused session must never reach the network")
	}
}

func TestVerifyOnlyStillRefusesBadParamsBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	negative := verifyOnlyParams()
	negative.Amount = -1
	noCurrency := verifyOnlyParams()
	noCurrency.Currency = ""
	noOrder := verifyOnlyParams()
	noOrder.OrderReference = ""
	noKey := verifyOnlyParams()
	noKey.IdempotencyKey = ""

	for name, params := range map[string]CreateCheckoutSessionParams{
		"negative amount":         negative,
		"missing currency":        noCurrency,
		"missing order reference": noOrder,
		"missing idempotency key": noKey,
	} {
		_, err := client.CreateCheckoutSession(context.Background(), params)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused session must never reach the network")
	}
}

func TestASubscriptionSessionSendsTheAgreementConsentAndStorefront(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.LocationID = "5b0c1d2e-3f40-4152-8637-48596a7b8c9d"
	params.AgreementID = "agr_0123456789abcdef0123456789abcdef"
	params.Consent = &Consent{
		TextVersion: ConsentTextVersionRecurring202609,
		AcceptedAt:  "2026-10-01T09:30:00Z",
		IP:          "203.0.113.7",
	}
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1042",` +
		`"locationId":"5b0c1d2e-3f40-4152-8637-48596a7b8c9d",` +
		`"agreementId":"agr_0123456789abcdef0123456789abcdef",` +
		`"consent":{"textVersion":"recurring-2026-09","acceptedAt":"2026-10-01T09:30:00Z","ip":"203.0.113.7"}}`
	if call.Body != want {
		t.Fatalf("body = %s\nwant   %s", call.Body, want)
	}
	assertSignedWith(t, call, http.MethodPost, SessionsPath, vector.IdempotencyKey)
}

func TestSubscriptionSessionRefusalsAreRefusalErrors(t *testing.T) {
	for _, code := range SubscriptionSessionRefusalCodes {
		t.Run(code, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Body: map[string]any{
				"success":       false,
				"transactionId": testCheckout["transactionId"],
				"errorCode":     code,
				"errorMessage":  "refused",
			}})
			client := newTestClient(t, server.URL)

			_, err := client.CreateCheckoutSession(context.Background(), testParams())

			var refusal *RefusalError
			if !errors.As(err, &refusal) || refusal.ErrorCode != code {
				t.Fatalf("got %v, want *RefusalError with %s", err, code)
			}
			if refusal.TransactionID != testCheckout["transactionId"] {
				t.Fatalf("TransactionID = %q", refusal.TransactionID)
			}
		})
	}
}

package dominaite

import (
	"context"
	"net/http"
	"testing"
)

func TestASessionCarriesVerifyOnlyAndTheAgreement(t *testing.T) {
	checkout := map[string]any{}
	for key, value := range testCheckout {
		checkout[key] = value
	}
	checkout["verifyOnly"] = true
	checkout["agreementId"] = testAgreementID
	server, _ := newTestServer(t, reply{Body: map[string]any{"success": true, "checkout": checkout}})

	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if !session.VerifyOnly || session.AgreementID != testAgreementID {
		t.Fatalf("session = %+v", session)
	}
}

// A one-off payment session leaves the new fields out, or sends them as null.
func TestAPaymentSessionHasNoVerifyOnlyOrAgreement(t *testing.T) {
	checkout := map[string]any{"agreementId": nil}
	for key, value := range testCheckout {
		checkout[key] = value
	}
	server, _ := newTestServer(t, reply{Body: map[string]any{"success": true, "checkout": checkout}})

	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if err != nil || session.VerifyOnly || session.AgreementID != "" {
		t.Fatalf("got %+v, %v", session, err)
	}
}

func TestTheStatusReadCarriesTheDeclineTheAgreementAndTheSequence(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusOK, map[string]any{
		"transactionId": testCheckout["transactionId"],
		"orderId":       "ord_1",
		"status":        "failed",
		"amount":        2500,
		"currency":      "EUR",
		"createdAt":     "2026-10-01T09:30:00Z",
		"declineClass":  "soft_funds",
		"isRecurring":   true,
		"agreement": map[string]any{
			"id":           testAgreementID,
			"status":       "active",
			"nextChargeAt": "2026-11-01T09:30:00Z",
		},
		"sequence": 4,
	}))

	status, err := newTestClient(t, server.URL).GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.DeclineClass != DeclineClassSoftFunds || !status.IsRecurring || status.Sequence != 4 || status.VerifyOnly {
		t.Errorf("status = %+v", status)
	}
	agreement := status.Agreement
	if agreement == nil || agreement.ID != testAgreementID || agreement.Status != AgreementStatusActive || agreement.NextChargeAt != "2026-11-01T09:30:00Z" {
		t.Errorf("agreement = %+v", agreement)
	}
}

func TestAVerifyOnlyStatusReadSaysSo(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusOK, map[string]any{
		"transactionId": testCheckout["transactionId"],
		"status":        "requires_capture",
		"amount":        0,
		"currency":      "EUR",
		"verifyOnly":    true,
		"declineClass":  nil,
		"agreement":     nil,
		"sequence":      0,
	}))

	status, err := newTestClient(t, server.URL).GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.VerifyOnly || status.DeclineClass != "" || status.Agreement != nil || status.IsRecurring || status.Sequence != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestAChargeCarriesTheAgreementAndTheSequence(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusCreated, map[string]any{
		"chargeId":      testChargeID,
		"status":        "succeeded",
		"transactionId": "33333333-3333-4333-8333-333333333333",
		"agreementId":   testAgreementID,
		"sequence":      2,
	}))

	params := testChargeParams()
	params.AgreementID = testAgreementID
	charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, params)
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.AgreementID != testAgreementID || charge.Sequence != 2 {
		t.Fatalf("charge = %+v", charge)
	}
}

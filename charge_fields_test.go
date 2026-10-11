package dominaite

import (
	"context"
	"net/http"
	"testing"
)

// Charge and refund request fields: AgreementID on a charge, and the Extra
// passthrough on every request that has a body.

func TestChargePaymentMethodSendsTheAgreementID(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	params := testChargeParams()
	params.AgreementID = "agr_0123456789abcdef0123456789abcdef"
	if _, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params); err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}

	call := calls()[0]
	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1043","agreementId":"agr_0123456789abcdef0123456789abcdef"}`
	if call.Body != want {
		t.Fatalf("body = %s\nwant   %s", call.Body, want)
	}
	assertSignedWith(t, call, http.MethodPost, PaymentMethodsPath+"/"+testPaymentMethodID+"/charges", chargeVector.IdempotencyKey)
}

func TestChargeExtraFieldsAreSentAndSigned(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	params := testChargeParams()
	params.Description = "modelled"
	params.Extra = map[string]any{"futureField": "a & b", "description": "from Extra"}
	if _, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params); err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}

	call := calls()[0]
	body := sentBody(t, call)
	if body["futureField"] != "a & b" {
		t.Errorf("futureField = %v, want it passed through unescaped", body["futureField"])
	}
	if body["description"] != "from Extra" {
		t.Errorf("description = %v: a key in Extra replaces the modelled field", body["description"])
	}
	if body["amount"] != float64(2500) || body["currency"] != "EUR" || body["orderReference"] != "order-1043" {
		t.Errorf("the modelled fields must stay: %v", body)
	}
	assertSignedWith(t, call, http.MethodPost, PaymentMethodsPath+"/"+testPaymentMethodID+"/charges", chargeVector.IdempotencyKey)
}

func TestRefundExtraFieldsAreSentAndSigned(t *testing.T) {
	server, calls := newTestServer(t, refundReply(http.StatusAccepted, pendingRefund()))
	client := newTestClient(t, server.URL)

	_, err := client.CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{
		IdempotencyKey: testRefundKey,
		Extra:          map[string]any{"futureField": 7},
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}

	call := calls()[0]
	if call.Body != `{"futureField":7}` {
		t.Errorf("body = %s: a nil Amount stays out, Extra goes in", call.Body)
	}
	assertSignedWith(t, call, http.MethodPost, PaymentsPath+"/"+testRefundTransactionID+"/refunds", testRefundKey)
}

// The 402 answer carries a decline class only when the code alone decides it.
// A decline without one is still a result, with DeclineClass empty.
func TestA402WithoutADeclineClassIsStillAResult(t *testing.T) {
	server, _ := newTestServer(t, chargeEnvelope(http.StatusPaymentRequired, "CHARGE_DECLINED", "The charge was declined.", map[string]any{
		"chargeId":      testChargeID,
		"status":        "failed",
		"declineCode":   "05",
		"transactionId": "33333333-3333-4333-8333-333333333333",
		"sequence":      1,
	}))
	client := newTestClient(t, server.URL)

	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("a decline is a result, not an error: %v", err)
	}
	if charge.Status != ChargeStatusFailed || charge.DeclineClass != "" || charge.DeclineCode != "05" {
		t.Fatalf("charge = %+v", charge)
	}
}

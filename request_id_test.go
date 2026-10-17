package dominaite

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// The request id comes from the Dominaite-Request-Id header first. Prod sends
// only metadata.requestId in the body, so that is the fallback.

func withRequestIDHeader(r reply, id string) reply {
	r.Header = map[string]string{RequestIDHeader: id}
	return r
}

func TestTheRequestIDHeaderWinsOverTheBody(t *testing.T) {
	server, _ := newTestServer(t, withRequestIDHeader(reply{Status: http.StatusCreated, Body: testCharge}, "req-header"))

	charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.RequestID != "req-header" {
		t.Fatalf("RequestID = %q, want the header value", charge.RequestID)
	}
}

func TestTheRequestIDFallsBackToTheBody(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})

	charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.RequestID != "r" {
		t.Fatalf("RequestID = %q, want metadata.requestId %q", charge.RequestID, "r")
	}
}

func TestNoRequestIDReadsAsEmpty(t *testing.T) {
	server, _ := newTestServer(t, successReply())

	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if session.RequestID != "" {
		t.Fatalf("RequestID = %q, want empty", session.RequestID)
	}
}

// A metadata value of an unexpected shape costs the request id only, never
// the response.
func TestOddMetadataDoesNotBreakTheResponse(t *testing.T) {
	body := map[string]any{"success": true, "data": testPlan(), "metadata": "not-an-object"}
	server, _ := newTestServer(t, reply{Body: body})

	plan, err := newTestClient(t, server.URL).GetPlan(context.Background(), testPlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.ID != testPlanID || plan.RequestID != "" {
		t.Fatalf("plan %q, RequestID %q", plan.ID, plan.RequestID)
	}
}

func TestEveryResultCarriesTheRequestID(t *testing.T) {
	session := withRequestIDHeader(successReply(), "req-1")
	t.Run("checkout session", func(t *testing.T) {
		server, _ := newTestServer(t, session)
		got, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
		if err != nil || got.RequestID != "req-1" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("refund", func(t *testing.T) {
		server, _ := newTestServer(t, refundReply(http.StatusCreated, pendingRefund()))
		got, err := newTestClient(t, server.URL).CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
		if err != nil || got.RequestID != "r" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("plan list items", func(t *testing.T) {
		server, _ := newTestServer(t, withRequestIDHeader(dataReply(http.StatusOK, []any{testPlan(), testPlan()}), "req-2"))
		plans, err := newTestClient(t, server.URL).ListPlans(context.Background())
		if err != nil || len(plans) != 2 {
			t.Fatalf("ListPlans: %d plans, %v", len(plans), err)
		}
		for i, plan := range plans {
			if plan.RequestID != "req-2" {
				t.Errorf("plans[%d].RequestID = %q", i, plan.RequestID)
			}
		}
	})
}

func TestEveryErrorCarriesTheRequestID(t *testing.T) {
	coded := func(status int, code string) reply {
		r := chargeEnvelope(status, code, "refused", nil)
		r.Body.(map[string]any)["metadata"] = map[string]any{"requestId": "req-body"}
		return r
	}
	cases := []struct {
		name  string
		reply reply
		want  string
	}{
		{"charge error from the body", coded(http.StatusConflict, ChargeErrorPaymentMethodNotActive), "req-body"},
		{"transport error from the body", coded(http.StatusInternalServerError, ErrorCodeInternalError), "req-body"},
		{"api error from the header", withRequestIDHeader(coded(http.StatusBadRequest, ErrorCodeValidationError), "req-header"), "req-header"},
		{"auth error", withRequestIDHeader(reply{Status: http.StatusUnauthorized, Body: map[string]any{}}, "req-401"), "req-401"},
		{"rate limit error", withRequestIDHeader(reply{Status: http.StatusTooManyRequests, Body: map[string]any{}}, "req-429"), "req-429"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, tc.reply)
			_, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
			if err == nil {
				t.Fatal("want an error")
			}
			if got := requestIDOf(err); got != tc.want {
				t.Fatalf("%T RequestID = %q, want %q", err, got, tc.want)
			}
		})
	}
}

func TestARefusalCarriesTheRequestID(t *testing.T) {
	body := map[string]any{"success": false, "errorCode": ErrorCodeAlreadyProcessed, "metadata": map[string]any{"requestId": "req-refusal"}}
	server, _ := newTestServer(t, reply{Body: body})

	_, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.RequestID != "req-refusal" {
		t.Fatalf("got %v, want *RefusalError with RequestID req-refusal", err)
	}
}

func requestIDOf(err error) string {
	var (
		chargeErr    *ChargeError
		transportErr *TransportError
		authErr      *AuthError
		rateErr      *RateLimitError
		apiErr       *APIError
	)
	switch {
	case errors.As(err, &chargeErr):
		return chargeErr.RequestID
	case errors.As(err, &transportErr):
		return transportErr.RequestID
	case errors.As(err, &authErr):
		return authErr.RequestID
	case errors.As(err, &rateErr):
		return rateErr.RequestID
	case errors.As(err, &apiErr):
		return apiErr.RequestID
	}
	return "<untyped>"
}

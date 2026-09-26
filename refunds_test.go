package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// Refunds: POST and GET under /merchant-api/payments/{transactionId}/refunds.
// The contract fixture's examples are covered in contract_test.go; this file
// pins the request side and the status-to-error rules with minimal bodies.

const (
	testRefundTransactionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	testRefundID            = "re_7c1e9a2b4d6f48a0b3c5d7e9f1a2b3c4"
	testRefundKey           = "refund-credit-note-77"
)

func refundReply(status int, data map[string]any) reply {
	return reply{Status: status, Body: map[string]any{
		"success":  true,
		"data":     data,
		"metadata": map[string]any{"requestId": "r", "timestamp": "t", "apiVersion": "1.0", "processingTimeMs": 1},
	}}
}

func pendingRefund() map[string]any {
	return map[string]any{
		"refundId":      testRefundID,
		"transactionId": testRefundTransactionID,
		"status":        "pending",
		"amount":        1500,
		"currency":      "HUF",
	}
}

func int64Ptr(value int64) *int64 { return &value }

// assertSignedWith checks the signature actually sent against the published
// recipe, with the timestamp the client chose.
func assertSignedWith(t *testing.T, call recordedCall, method, path, idempotencyKey string) {
	t.Helper()
	want := Sign(SignInput{
		Secret:         vector.Secret,
		Timestamp:      call.Header.Get("X-Timestamp"),
		Method:         method,
		Path:           path,
		IdempotencyKey: idempotencyKey,
		Body:           call.Body,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Errorf("X-Signature = %s, want %s (key %q, body %q)", got, want, idempotencyKey, call.Body)
	}
}

func TestCreateRefundPartialSendsTheAmountAndSignsTheKey(t *testing.T) {
	server, calls := newTestServer(t, refundReply(http.StatusAccepted, pendingRefund()))
	client := newTestClient(t, server.URL)

	amount, err := ToMinorUnits("1500", "HUF")
	if err != nil {
		t.Fatal(err)
	}
	refund, err := client.CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{
		Amount:         &amount,
		Reason:         "Returned & refunded",
		IdempotencyKey: testRefundKey,
	})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}

	call := calls()[0]
	path := PaymentsPath + "/" + testRefundTransactionID + "/refunds"
	if call.Method != http.MethodPost || call.Path != path {
		t.Fatalf("got %s %s, want POST %s", call.Method, call.Path, path)
	}
	if call.Body != `{"amount":1500,"reason":"Returned & refunded"}` {
		t.Errorf("body = %s", call.Body)
	}
	if got := call.Header.Get("Idempotency-Key"); got != testRefundKey {
		t.Errorf("Idempotency-Key = %q, want %q", got, testRefundKey)
	}
	assertSignedWith(t, call, http.MethodPost, path, testRefundKey)

	if refund.RefundID != testRefundID || refund.TransactionID != testRefundTransactionID || refund.Status != RefundStatusPending {
		t.Errorf("refund = %+v", refund)
	}
	if refund.Amount == nil || *refund.Amount != 1500 || refund.Currency != "HUF" {
		t.Errorf("amount %v, currency %q", refund.Amount, refund.Currency)
	}
	if refund.FailureCode != "" || refund.FailureMessage != "" || refund.CompletedAt != "" {
		t.Errorf("absent fields must read empty: %+v", refund)
	}
	if len(refund.Raw) == 0 {
		t.Error("Raw is empty")
	}
}

func TestCreateRefundFullSendsNoAmountKey(t *testing.T) {
	full := pendingRefund()
	delete(full, "amount")
	server, calls := newTestServer(t, refundReply(http.StatusAccepted, full))
	client := newTestClient(t, server.URL)

	refund, err := client.CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}

	call := calls()[0]
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if _, present := body["amount"]; present {
		t.Errorf("a full refund must send no amount key, body = %s", call.Body)
	}
	if _, present := body["idempotencyKey"]; present {
		t.Error("idempotencyKey must not leak into the body")
	}
	assertSignedWith(t, call, http.MethodPost, PaymentsPath+"/"+testRefundTransactionID+"/refunds", testRefundKey)

	if refund.Amount != nil {
		t.Errorf("Amount = %d, want nil for a full refund before success", *refund.Amount)
	}
}

func TestCreateRefundValidatesBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, refundReply(http.StatusAccepted, pendingRefund()))
	client := newTestClient(t, server.URL)

	cases := map[string]struct {
		transactionID string
		params        CreateRefundParams
	}{
		"missing key":          {testRefundTransactionID, CreateRefundParams{}},
		"non-ASCII key":        {testRefundTransactionID, CreateRefundParams{IdempotencyKey: "refund-ü"}},
		"zero amount":          {testRefundTransactionID, CreateRefundParams{Amount: int64Ptr(0), IdempotencyKey: testRefundKey}},
		"negative amount":      {testRefundTransactionID, CreateRefundParams{Amount: int64Ptr(-5), IdempotencyKey: testRefundKey}},
		"not a transaction id": {"order-1042", CreateRefundParams{IdempotencyKey: testRefundKey}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.CreateRefund(context.Background(), tc.transactionID, tc.params)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("got %T (%v), want *ValidationError", err, err)
			}
		})
	}
	if n := len(calls()); n != 0 {
		t.Fatalf("%d requests sent, want none", n)
	}
}

func TestCreateRefundSignsTheLowercaseTransactionID(t *testing.T) {
	server, calls := newTestServer(t, refundReply(http.StatusAccepted, pendingRefund()))
	upper := "1A2B3C4D-5E6F-4A7B-8C9D-0E1F2A3B4C5D"
	if _, err := newTestClient(t, server.URL).CreateRefund(context.Background(), upper, CreateRefundParams{IdempotencyKey: testRefundKey}); err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if want := PaymentsPath + "/" + testRefundTransactionID + "/refunds"; calls()[0].Path != want {
		t.Errorf("path = %q, want %q", calls()[0].Path, want)
	}
}

func TestGetRefundReadsWithAnEmptyKeyAndBody(t *testing.T) {
	succeeded := pendingRefund()
	succeeded["status"] = "succeeded"
	succeeded["completedAt"] = "2026-09-26T10:05:40.1200000Z"
	server, calls := newTestServer(t, refundReply(http.StatusOK, succeeded))

	refund, err := newTestClient(t, server.URL).GetRefund(context.Background(), testRefundTransactionID, testRefundID)
	if err != nil {
		t.Fatalf("GetRefund: %v", err)
	}

	call := calls()[0]
	path := PaymentsPath + "/" + testRefundTransactionID + "/refunds/" + testRefundID
	if call.Method != http.MethodGet || call.Path != path {
		t.Fatalf("got %s %s, want GET %s", call.Method, call.Path, path)
	}
	if call.Body != "" {
		t.Errorf("body = %q, want empty", call.Body)
	}
	if _, present := call.Header["Idempotency-Key"]; present {
		t.Error("a refund read must not carry an Idempotency-Key")
	}
	assertSignedWith(t, call, http.MethodGet, path, "")

	if refund.Status != RefundStatusSucceeded || !IsRefundTerminal(refund.Status) {
		t.Errorf("Status = %q", refund.Status)
	}
	if refund.Amount == nil || *refund.Amount != 1500 || refund.CompletedAt == "" {
		t.Errorf("refund = %+v", refund)
	}
}

func TestGetRefundParsesAFailedRefund(t *testing.T) {
	failed := map[string]any{
		"refundId":       testRefundID,
		"transactionId":  testRefundTransactionID,
		"status":         "failed",
		"currency":       "EUR",
		"failureCode":    "REFUND_FAILED",
		"failureMessage": "The refund could not be completed.",
		"completedAt":    "2026-09-26T10:05:41Z",
	}
	server, _ := newTestServer(t, refundReply(http.StatusOK, failed))

	refund, err := newTestClient(t, server.URL).GetRefund(context.Background(), testRefundTransactionID, testRefundID)
	if err != nil {
		t.Fatalf("a failed refund is a result, not an error: %v", err)
	}
	if refund.Status != RefundStatusFailed || !IsRefundTerminal(refund.Status) {
		t.Errorf("Status = %q", refund.Status)
	}
	if refund.Amount != nil {
		t.Errorf("Amount = %d, want nil on a failed refund", *refund.Amount)
	}
	if refund.FailureCode != RefundFailureFailed || refund.FailureMessage == "" {
		t.Errorf("failure %q / %q", refund.FailureCode, refund.FailureMessage)
	}
}

func TestGetRefundValidatesBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, refundReply(http.StatusOK, pendingRefund()))
	client := newTestClient(t, server.URL)

	for name, ids := range map[string][2]string{
		"bad transaction id":     {"order-1042", testRefundID},
		"empty refund id":        {testRefundTransactionID, ""},
		"refund id with a slash": {testRefundTransactionID, "re_1/../../x"},
		"refund id with a query": {testRefundTransactionID, "re_1?x=1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.GetRefund(context.Background(), ids[0], ids[1])
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("got %T (%v), want *ValidationError", err, err)
			}
		})
	}
	if n := len(calls()); n != 0 {
		t.Fatalf("%d requests sent, want none", n)
	}
}

func TestIsRefundTerminal(t *testing.T) {
	for status, want := range map[string]bool{
		RefundStatusPending:    false,
		RefundStatusProcessing: false,
		RefundStatusSucceeded:  true,
		RefundStatusFailed:     true,
		"on_hold":              false,
		"":                     false,
	} {
		if got := IsRefundTerminal(status); got != want {
			t.Errorf("IsRefundTerminal(%q) = %v, want %v", status, got, want)
		}
	}
}

// Every refund error code is a *RefundError keeping the status, the code, the
// message and the envelope, with Retryable set only for the two codes the
// gateway asks you to retry with the same key.
func TestRefundErrorCodesMapToRefundError(t *testing.T) {
	cases := []struct {
		status    int
		code      string
		retryable bool
	}{
		{http.StatusNotFound, RefundErrorPaymentNotFound, false},
		{http.StatusNotFound, RefundErrorRefundNotFound, true},
		{http.StatusUnprocessableEntity, RefundErrorPaymentNotRefundable, false},
		{http.StatusUnprocessableEntity, RefundErrorAmountExceeded, false},
		{http.StatusUnprocessableEntity, RefundErrorIdempotencyKeyReused, false},
		{http.StatusConflict, RefundErrorDuplicateRequest, true},
		{http.StatusBadRequest, RefundErrorIdempotencyKeyRequired, false},
	}
	if len(cases) != len(RefundErrorCodes) {
		t.Fatalf("%d cases for %d refund error codes", len(cases), len(RefundErrorCodes))
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			server, _ := newTestServer(t, chargeEnvelope(tc.status, tc.code, "gateway says no", nil))
			client := newTestClient(t, server.URL)

			calls := map[string]func() (*Refund, error){
				"CreateRefund": func() (*Refund, error) {
					return client.CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
				},
				"GetRefund": func() (*Refund, error) {
					return client.GetRefund(context.Background(), testRefundTransactionID, testRefundID)
				},
			}
			for name, call := range calls {
				refund, err := call()
				if refund != nil {
					t.Fatalf("%s: an error must not produce a refund: %+v", name, refund)
				}
				var refundErr *RefundError
				if !errors.As(err, &refundErr) {
					t.Fatalf("%s: got %T (%v), want *RefundError", name, err, err)
				}
				if refundErr.HTTPStatus != tc.status || refundErr.ErrorCode != tc.code || refundErr.Retryable != tc.retryable {
					t.Errorf("%s: got %d %s retryable=%v, want %d %s retryable=%v", name,
						refundErr.HTTPStatus, refundErr.ErrorCode, refundErr.Retryable, tc.status, tc.code, tc.retryable)
				}
				if refundErr.Error() != "gateway says no" || len(refundErr.Raw) == 0 {
					t.Errorf("%s: message %q, raw %d bytes", name, refundErr.Error(), len(refundErr.Raw))
				}
				if !errors.Is(err, ErrDominaite) {
					t.Errorf("%s: must match errors.Is(err, ErrDominaite)", name)
				}
			}
		})
	}
}

// A 5xx on a refund route means nothing was queued: a *TransportError, which
// is retried with the same key, even when it carries a code.
func TestRefundServerErrorsAreTransportErrors(t *testing.T) {
	for _, r := range []reply{
		{Status: http.StatusInternalServerError, Body: map[string]any{"success": false}},
		chargeEnvelope(http.StatusInternalServerError, "INTERNAL_ERROR", "boom", nil),
		{Status: http.StatusServiceUnavailable, Body: "<html>busy</html>"},
	} {
		server, _ := newTestServer(t, r)
		_, err := newTestClient(t, server.URL).CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
		var transportErr *TransportError
		if !errors.As(err, &transportErr) {
			t.Errorf("HTTP %d: got %T (%v), want *TransportError", r.Status, err, err)
		}
	}
}

// Authentication and rate limiting keep their generic errors on refund routes.
func TestRefundAuthAndRateLimitKeepGenericErrors(t *testing.T) {
	server, _ := newTestServer(t, chargeEnvelope(http.StatusUnauthorized, "INVALID_SIGNATURE", "no", nil))
	_, err := newTestClient(t, server.URL).GetRefund(context.Background(), testRefundTransactionID, testRefundID)
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.ErrorCode != "INVALID_SIGNATURE" {
		t.Errorf("401: got %T (%v), want *AuthError", err, err)
	}

	server, _ = newTestServer(t, chargeEnvelope(http.StatusTooManyRequests, "RATE_LIMITED", "slow down", nil))
	_, err = newTestClient(t, server.URL).CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
	var rateErr *RateLimitError
	if !errors.As(err, &rateErr) {
		t.Errorf("429: got %T (%v), want *RateLimitError", err, err)
	}
}

// A 2xx that is not a refund is not quietly turned into an empty one.
func TestRefundWithoutARefundBodyIsAnAPIError(t *testing.T) {
	for _, r := range []reply{
		{Status: http.StatusAccepted, Body: map[string]any{"success": true}},
		{Status: http.StatusAccepted, Body: map[string]any{"success": true, "data": map[string]any{"status": "pending"}}},
		{Status: http.StatusAccepted, Body: "not json"},
	} {
		server, _ := newTestServer(t, r)
		_, err := newTestClient(t, server.URL).CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Errorf("body %v: got %T (%v), want *APIError", r.Body, err, err)
		}
	}
}

package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Stored payment methods: SaveCard on a session, PaymentMethod on its status,
// then off-session charges and revocation against /merchant-api/payment-methods/{id}.

func testChargeParams() ChargePaymentMethodParams {
	return ChargePaymentMethodParams{
		Amount:         2500,
		Currency:       "EUR",
		OrderReference: "order-1043",
		IdempotencyKey: chargeVector.IdempotencyKey,
	}
}

var testCharge = map[string]any{
	"chargeId":      "chg_1",
	"status":        "succeeded",
	"transactionId": "33333333-3333-4333-8333-333333333333",
}

func TestSaveCardIsSentInTheSessionBodyAndNowhereElse(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.SaveCard = true
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["saveCard"] != true {
		t.Fatalf("saveCard = %v, want true", body["saveCard"])
	}
	if _, present := body["idempotencyKey"]; present {
		t.Fatal("idempotencyKey must not leak into the body")
	}

	want := Sign(SignInput{
		Secret:         vector.Secret,
		Timestamp:      call.Header.Get("X-Timestamp"),
		Method:         http.MethodPost,
		Path:           SessionsPath,
		IdempotencyKey: vector.IdempotencyKey,
		Body:           call.Body,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatalf("X-Signature = %s, want %s", got, want)
	}
}

// A session without SaveCard must keep the exact vector body: the flag is
// omitted, not sent as false, so the signed bytes of every existing integration
// do not move.
func TestSaveCardFalseIsOmittedFromTheBody(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := calls()[0].Body; got != vector.Body {
		t.Fatalf("body = %s, want %s", got, vector.Body)
	}
}

func TestGetStatusPassesTheStoredPaymentMethodThrough(t *testing.T) {
	paymentMethod := map[string]any{
		"id": testPaymentMethodID, "brand": "visa", "last4": "4242", "expiryMonth": 12, "expiryYear": 2029, "status": "active",
	}
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR",
		"paymentMethod": paymentMethod,
	}})
	client := newTestClient(t, server.URL)

	status, err := client.GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.PaymentMethod == nil {
		t.Fatal("PaymentMethod = nil")
	}
	want := PaymentMethod{ID: testPaymentMethodID, Brand: "visa", Last4: "4242", ExpiryMonth: 12, ExpiryYear: 2029, Status: PaymentMethodStatusActive}
	if *status.PaymentMethod != want {
		t.Fatalf("PaymentMethod = %+v, want %+v", *status.PaymentMethod, want)
	}
}

func TestGetStatusWithoutASavedCardLeavesPaymentMethodNil(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR",
		"paymentMethod": nil,
	}})
	client := newTestClient(t, server.URL)

	status, err := client.GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.PaymentMethod != nil {
		t.Fatalf("PaymentMethod = %+v, want nil for a null", *status.PaymentMethod)
	}
}

func TestChargePaymentMethodSignsTheChargeVectorByteForByte(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.ChargeID != "chg_1" || charge.Status != ChargeStatusSucceeded || charge.TransactionID != testCharge["transactionId"] {
		t.Fatalf("unexpected charge: %+v", charge)
	}
	if charge.DeclineClass != "" || charge.DeclineCode != "" {
		t.Fatalf("a succeeded charge carries decline fields: %+v", charge)
	}
	if len(charge.Raw) == 0 {
		t.Fatal("Raw is empty")
	}

	call := calls()[0]
	if call.Method != http.MethodPost || call.Path != chargeVector.Path {
		t.Fatalf("got %s %s, want POST %s", call.Method, call.Path, chargeVector.Path)
	}
	if call.Body != chargeVector.Body {
		t.Fatalf("body = %s, want %s", call.Body, chargeVector.Body)
	}
	if got := call.Header.Get("Idempotency-Key"); got != chargeVector.IdempotencyKey {
		t.Fatalf("Idempotency-Key = %s, want %s", got, chargeVector.IdempotencyKey)
	}
	if strings.Contains(call.Body, "idempotencyKey") {
		t.Fatal("idempotencyKey must not leak into the body")
	}

	// With the vector's timestamp the header is the vector signature; with the
	// live timestamp it is the same recipe over the same bytes.
	in := chargeVector.input()
	in.Timestamp = call.Header.Get("X-Timestamp")
	if got := call.Header.Get("X-Signature"); got != Sign(in) {
		t.Fatalf("X-Signature = %s, want %s", got, Sign(in))
	}
	if got := Sign(chargeVector.input()); got != chargeVector.Signature {
		t.Fatalf("vector signature = %s, want %s", got, chargeVector.Signature)
	}
}

func TestChargePaymentMethodGeneratesAKeyAndSendsDescription(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	params := testChargeParams()
	params.IdempotencyKey = ""
	params.Description = "Monthly plan"
	if _, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params); err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}

	call := calls()[0]
	if !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(call.Header.Get("Idempotency-Key")) {
		t.Fatalf("Idempotency-Key = %q, want a generated UUID", call.Header.Get("Idempotency-Key"))
	}
	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1043","description":"Monthly plan"}`
	if call.Body != want {
		t.Fatalf("body = %s, want %s", call.Body, want)
	}
}

func TestADeclinedChargeIsAResultNotAnError(t *testing.T) {
	declined := map[string]any{
		"chargeId": "chg_2", "status": "failed", "declineClass": "soft_funds", "declineCode": "51",
		"transactionId": testCharge["transactionId"],
	}
	// Through the envelope too: the unwrap path must not eat the decline.
	server, _ := newTestServer(t, reply{Status: http.StatusCreated, Body: map[string]any{"success": true, "data": declined}})
	client := newTestClient(t, server.URL)

	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("a decline must not be an error: %v", err)
	}
	if charge.Status != ChargeStatusFailed {
		t.Fatalf("Status = %q, want failed", charge.Status)
	}
	if charge.DeclineClass != DeclineClassSoftFunds || charge.DeclineCode != "51" {
		t.Fatalf("decline = %q/%q, want soft_funds/51", charge.DeclineClass, charge.DeclineCode)
	}
}

func TestAChargeTheGatewayRefusesToAttemptIsARefusalError(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success":       false,
		"errorCode":     "ALREADY_PROCESSED",
		"errorMessage":  "Already charged",
		"transactionId": testCharge["transactionId"],
	}})
	client := newTestClient(t, server.URL)

	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if charge != nil {
		t.Fatal("a refusal must not produce a charge")
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %T (%v), want *RefusalError", err, err)
	}
	if refusal.ErrorCode != "ALREADY_PROCESSED" {
		t.Fatalf("ErrorCode = %q, want ALREADY_PROCESSED", refusal.ErrorCode)
	}
	if refusal.TransactionID != testCharge["transactionId"] {
		t.Fatalf("TransactionID = %q, want the collided transaction", refusal.TransactionID)
	}
	if !errors.Is(err, ErrDominaite) {
		t.Error("must match errors.Is(err, ErrDominaite)")
	}
}

func TestAChargeAgainstAMethodThatIsNotYoursIsA404(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: http.StatusNotFound, Body: map[string]any{
		"success": false, "error": map[string]any{"code": "NOT_FOUND", "message": "No such payment method"},
	}})
	client := newTestClient(t, server.URL)

	_, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T (%v), want *APIError", err, err)
	}
	if apiErr.HTTPStatus != http.StatusNotFound || apiErr.ErrorCode != "NOT_FOUND" {
		t.Fatalf("got %d %q, want 404 NOT_FOUND", apiErr.HTTPStatus, apiErr.ErrorCode)
	}
}

func TestChargePaymentMethodValidatesMoneyParamsLikeASession(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	cases := map[string]func(*ChargePaymentMethodParams){
		"zero amount":          func(p *ChargePaymentMethodParams) { p.Amount = 0 },
		"negative amount":      func(p *ChargePaymentMethodParams) { p.Amount = -1 },
		"missing currency":     func(p *ChargePaymentMethodParams) { p.Currency = " " },
		"missing reference":    func(p *ChargePaymentMethodParams) { p.OrderReference = "" },
		"reference too long":   func(p *ChargePaymentMethodParams) { p.OrderReference = strings.Repeat("x", 101) },
		"idempotency too long": func(p *ChargePaymentMethodParams) { p.IdempotencyKey = strings.Repeat("k", 101) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := testChargeParams()
			mutate(&params)
			_, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("got %T (%v), want *ValidationError", err, err)
			}
		})
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("%d requests were sent, want 0", got)
	}
}

func TestAPaymentMethodIDThatWouldNotStayOnePathSegmentIsRefusedBeforeSigning(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	for _, bad := range []string{"", " ", "pm_1/charges", "pm_1?x=1", "pm_1#f", "pm 1", "pm_1%2F", strings.Repeat("p", 101)} {
		_, err := client.ChargePaymentMethod(context.Background(), bad, testChargeParams())
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("ChargePaymentMethod accepted %q: %v", bad, err)
		}
		err = client.RevokePaymentMethod(context.Background(), bad)
		if !errors.As(err, &validation) {
			t.Errorf("RevokePaymentMethod accepted %q: %v", bad, err)
		}
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("%d requests were sent, want 0", got)
	}
}

func TestRevokePaymentMethodSignsTheRevokeVector(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	if err := client.RevokePaymentMethod(context.Background(), testPaymentMethodID); err != nil {
		t.Fatalf("RevokePaymentMethod: %v", err)
	}

	call := calls()[0]
	if call.Method != http.MethodDelete || call.Path != revokeVector.Path {
		t.Fatalf("got %s %s, want DELETE %s", call.Method, call.Path, revokeVector.Path)
	}
	if call.Body != "" {
		t.Fatalf("DELETE sent a body: %q", call.Body)
	}
	if _, present := call.Header["Idempotency-Key"]; present {
		t.Fatal("DELETE must not send an Idempotency-Key header")
	}

	in := revokeVector.input()
	in.Timestamp = call.Header.Get("X-Timestamp")
	if got := call.Header.Get("X-Signature"); got != Sign(in) {
		t.Fatalf("X-Signature = %s, want %s", got, Sign(in))
	}
	if got := Sign(revokeVector.input()); got != revokeVector.Signature {
		t.Fatalf("vector signature = %s, want %s", got, revokeVector.Signature)
	}
}

func TestRevokePaymentMethodMapsA404AndA5xx(t *testing.T) {
	notFound, _ := newTestServer(t, reply{Status: http.StatusNotFound, Body: map[string]any{
		"success": false, "error": map[string]any{"code": "NOT_FOUND", "message": "No such payment method"},
	}})
	err := newTestClient(t, notFound.URL).RevokePaymentMethod(context.Background(), testPaymentMethodID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusNotFound {
		t.Fatalf("got %T (%v), want *APIError 404", err, err)
	}

	down, _ := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{"success": false}})
	err = newTestClient(t, down.URL).RevokePaymentMethod(context.Background(), testPaymentMethodID)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %T (%v), want *TransportError", err, err)
	}
}

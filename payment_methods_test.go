package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Stored payment methods: SaveCard on a session, StoredPaymentMethod on its
// status, then off-session charges and revocation against
// /merchant-api/payment-methods/{id}. The contract fixture's examples are
// covered in contract_test.go; this file pins the request side and the
// status-to-outcome rules with minimal bodies.

func testChargeParams() ChargePaymentMethodParams {
	return ChargePaymentMethodParams{
		Amount:         2500,
		Currency:       "EUR",
		OrderReference: "order-1043",
		IdempotencyKey: chargeVector.IdempotencyKey,
	}
}

// The gateway's envelope for a placed charge, as it goes over the wire:
// declineClass and declineCode are omitted, not null.
var testCharge = map[string]any{
	"success": true,
	"data": map[string]any{
		"chargeId":      "ch_33333333333343338333333333333333",
		"status":        "succeeded",
		"transactionId": "33333333-3333-4333-8333-333333333333",
	},
	"metadata": map[string]any{"requestId": "r", "timestamp": "t", "apiVersion": "1.0", "processingTimeMs": 1},
}

const testChargeID = "ch_33333333333343338333333333333333"

func chargeEnvelope(status int, code, message string, data map[string]any) reply {
	body := map[string]any{
		"success": false,
		"error":   map[string]any{"code": code, "message": message, "statusCode": status},
	}
	if data != nil {
		body["data"] = data
	}
	return reply{Status: status, Body: body}
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

func TestGetStatusReadsTheStoredPaymentMethod(t *testing.T) {
	stored := map[string]any{
		"id": testPaymentMethodID, "brand": "visa", "last4": "4242", "expiryMonth": 12, "expiryYear": 2029, "status": "active",
	}
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR",
		"paymentMethod": "card", "storedPaymentMethod": stored,
	}})
	client := newTestClient(t, server.URL)

	status, err := client.GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.StoredPaymentMethod == nil {
		t.Fatal("StoredPaymentMethod = nil")
	}
	want := StoredPaymentMethod{ID: testPaymentMethodID, Brand: "visa", Last4: "4242", ExpiryMonth: 12, ExpiryYear: 2029, Status: StoredPaymentMethodStatusActive}
	if *status.StoredPaymentMethod != want {
		t.Fatalf("StoredPaymentMethod = %+v, want %+v", *status.StoredPaymentMethod, want)
	}
	// The gateway's paymentMethod is the string category of how the payer
	// paid, not the card on file; it stays reachable through Raw only.
	if !strings.Contains(string(status.Raw), `"paymentMethod":"card"`) {
		t.Errorf("Raw must keep the gateway's paymentMethod category: %s", status.Raw)
	}
}

// Without a saved card the gateway omits storedPaymentMethod; a null reads the
// same way.
func TestGetStatusWithoutASavedCardLeavesStoredPaymentMethodNil(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"omitted": {"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR"},
		"null":    {"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR", "storedPaymentMethod": nil},
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Body: body})
			status, err := newTestClient(t, server.URL).GetStatus(context.Background(), testCheckout["transactionId"].(string))
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status.StoredPaymentMethod != nil {
				t.Fatalf("StoredPaymentMethod = %+v, want nil", *status.StoredPaymentMethod)
			}
		})
	}
}

func TestChargePaymentMethodSignsTheChargeVectorByteForByte(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.ChargeID != testChargeID || charge.Status != ChargeStatusSucceeded || charge.TransactionID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("unexpected charge: %+v", charge)
	}
	if charge.DeclineClass != "" || charge.DeclineCode != "" {
		t.Fatalf("a succeeded charge carries decline fields: %+v", charge)
	}
	if len(charge.Raw) == 0 || strings.Contains(string(charge.Raw), "metadata") {
		t.Fatalf("Raw must be the unwrapped charge object: %s", charge.Raw)
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

func TestChargePaymentMethodSendsDescription(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)

	params := testChargeParams()
	params.Description = "Monthly plan"
	if _, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params); err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}

	call := calls()[0]
	if got := call.Header.Get("Idempotency-Key"); got != params.IdempotencyKey {
		t.Fatalf("Idempotency-Key = %q, want the caller's %q", got, params.IdempotencyKey)
	}
	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1043","description":"Monthly plan"}`
	if call.Body != want {
		t.Fatalf("body = %s, want %s", call.Body, want)
	}
}

func TestChargePaymentMethodRequiresAnIdempotencyKey(t *testing.T) {
	for name, key := range map[string]string{"empty": "", "blank": " \t"} {
		t.Run(name, func(t *testing.T) {
			server, calls := newTestServer(t, reply{Status: http.StatusCreated, Body: testCharge})
			client := newTestClient(t, server.URL)

			params := testChargeParams()
			params.IdempotencyKey = key

			_, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("got %v, want *ValidationError", err)
			}
			if len(calls()) != 0 {
				t.Fatalf("sent %d requests without an idempotency key", len(calls()))
			}
		})
	}
}

// A 402 says success=false and CHARGE_DECLINED, but the charge is right there
// with its decline class: it comes back as a result.
func TestA402DeclineIsAResultNotAnError(t *testing.T) {
	declined := map[string]any{
		"chargeId": "ch_33333333333343338333333333333334", "status": "failed", "declineClass": "soft_funds", "declineCode": "51",
		"transactionId": "33333333-3333-4333-8333-333333333334",
	}
	server, _ := newTestServer(t, chargeEnvelope(http.StatusPaymentRequired, "CHARGE_DECLINED", "The payment provider declined the charge.", declined))
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

// A durable replay answers 200 with the first body; still the charge.
func TestA200ReplayIsReturnedAsTheCharge(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: http.StatusOK, Body: testCharge})
	charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if err != nil || charge.ChargeID != testChargeID {
		t.Fatalf("got %+v, %v", charge, err)
	}
}

// 502 CHARGE_OUTCOME_UNKNOWN: the charge may have happened. The error carries
// the row the gateway attached, so the caller can poll instead of retrying
// under a new key.
func TestChargeOutcomeUnknownCarriesTheTransactionToPoll(t *testing.T) {
	pending := map[string]any{
		"chargeId": "ch_33333333333343338333333333333335", "status": "pending",
		"transactionId": "33333333-3333-4333-8333-333333333335",
	}
	server, _ := newTestServer(t, chargeEnvelope(http.StatusBadGateway, "CHARGE_OUTCOME_UNKNOWN", "The payment provider gave no verdict.", pending))
	charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if charge != nil {
		t.Fatal("an unknown outcome must not produce a charge")
	}
	var chargeErr *ChargeError
	if !errors.As(err, &chargeErr) {
		t.Fatalf("got %T (%v), want *ChargeError", err, err)
	}
	if chargeErr.HTTPStatus != 502 || chargeErr.ErrorCode != ChargeErrorOutcomeUnknown {
		t.Fatalf("got %d %s, want 502 CHARGE_OUTCOME_UNKNOWN", chargeErr.HTTPStatus, chargeErr.ErrorCode)
	}
	if chargeErr.Error() != "The payment provider gave no verdict." {
		t.Fatalf("message = %q, want the gateway's", chargeErr.Error())
	}
	if chargeErr.Charge == nil || chargeErr.Charge.ChargeID != "ch_33333333333343338333333333333335" || chargeErr.Charge.Status != ChargeStatusPending {
		t.Fatalf("Charge = %+v, want the attached row", chargeErr.Charge)
	}
	if chargeErr.TransactionID != "33333333-3333-4333-8333-333333333335" {
		t.Fatalf("TransactionID = %q, want the transaction to poll", chargeErr.TransactionID)
	}
	if !strings.Contains(string(chargeErr.Raw), `"error"`) {
		t.Fatalf("Raw must be the whole envelope: %s", chargeErr.Raw)
	}
	if !errors.Is(err, ErrDominaite) {
		t.Error("must match errors.Is(err, ErrDominaite)")
	}
}

// 409, 422 and 503 carry no data: the same error without a charge row.
func TestChargeErrorsWithoutDataHaveNoCharge(t *testing.T) {
	for status, code := range map[int]string{
		http.StatusConflict:            ChargeErrorPaymentMethodNotActive,
		http.StatusUnprocessableEntity: ChargeErrorIdempotencyKeyReused,
		http.StatusServiceUnavailable:  ChargeErrorChargesDisabled,
	} {
		t.Run(code, func(t *testing.T) {
			server, _ := newTestServer(t, chargeEnvelope(status, code, "Refused.", nil))
			_, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
			var chargeErr *ChargeError
			if !errors.As(err, &chargeErr) {
				t.Fatalf("got %T (%v), want *ChargeError", err, err)
			}
			if chargeErr.HTTPStatus != status || chargeErr.ErrorCode != code {
				t.Fatalf("got %d %s, want %d %s", chargeErr.HTTPStatus, chargeErr.ErrorCode, status, code)
			}
			if chargeErr.Charge != nil || chargeErr.TransactionID != "" {
				t.Fatalf("Charge = %+v, TransactionID = %q, want none", chargeErr.Charge, chargeErr.TransactionID)
			}
		})
	}
}

// The generic statuses keep their generic errors, code and all.
func TestAChargeAgainstAMethodThatIsNotYoursIsA404(t *testing.T) {
	server, _ := newTestServer(t, chargeEnvelope(http.StatusNotFound, "PAYMENT_METHOD_NOT_FOUND", "No stored payment method with this id.", nil))
	client := newTestClient(t, server.URL)

	_, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T (%v), want *APIError", err, err)
	}
	if apiErr.HTTPStatus != http.StatusNotFound || apiErr.ErrorCode != "PAYMENT_METHOD_NOT_FOUND" {
		t.Fatalf("got %d %q, want 404 PAYMENT_METHOD_NOT_FOUND", apiErr.HTTPStatus, apiErr.ErrorCode)
	}
}

func TestAChargeKeepsTheGenericErrorsForTheGenericStatuses(t *testing.T) {
	validation, _ := newTestServer(t, chargeEnvelope(http.StatusBadRequest, "VALIDATION_ERROR", "Validation failed", nil))
	_, err := newTestClient(t, validation.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 400 || apiErr.ErrorCode != "VALIDATION_ERROR" {
		t.Fatalf("400: got %T (%v), want *APIError 400 VALIDATION_ERROR", err, err)
	}

	down, _ := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{"success": false}})
	_, err = newTestClient(t, down.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("codeless 503: got %T (%v), want *TransportError", err, err)
	}

	html, _ := newTestServer(t, reply{Status: http.StatusBadGateway, Body: "<html>502</html>"})
	_, err = newTestClient(t, html.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if !errors.As(err, &transportErr) {
		t.Fatalf("HTML 502: got %T (%v), want *TransportError", err, err)
	}

	// A 2xx without a chargeId is not a charge, whatever success says.
	empty, _ := newTestServer(t, reply{Status: http.StatusCreated, Body: map[string]any{"success": true}})
	_, err = newTestClient(t, empty.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	if !errors.As(err, &apiErr) {
		t.Fatalf("201 without a body: got %T (%v), want *APIError", err, err)
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

// 502 and 503 with a code are a *RevokeError; nothing changed under either.
func TestRevokePaymentMethodRaisesRevokeErrorForCodedFailures(t *testing.T) {
	for status, code := range map[int]string{
		http.StatusBadGateway:         RevokeErrorUpstreamContract,
		http.StatusServiceUnavailable: RevokeErrorMerchantAPIUnavailable,
	} {
		t.Run(code, func(t *testing.T) {
			server, _ := newTestServer(t, chargeEnvelope(status, code, "Nothing changed.", nil))
			err := newTestClient(t, server.URL).RevokePaymentMethod(context.Background(), testPaymentMethodID)
			var revokeErr *RevokeError
			if !errors.As(err, &revokeErr) {
				t.Fatalf("got %T (%v), want *RevokeError", err, err)
			}
			if revokeErr.HTTPStatus != status || revokeErr.ErrorCode != code || revokeErr.Error() != "Nothing changed." {
				t.Fatalf("got %d %s %q, want %d %s", revokeErr.HTTPStatus, revokeErr.ErrorCode, revokeErr.Error(), status, code)
			}
			if !errors.Is(err, ErrDominaite) {
				t.Error("must match errors.Is(err, ErrDominaite)")
			}
		})
	}
}

// A 204 for an already revoked method is a success too, so a timed-out revoke
// can be retried.
func TestRevokePaymentMethodMapsA404AndACodeless5xx(t *testing.T) {
	notFound, _ := newTestServer(t, reply{Status: http.StatusNotFound, Body: map[string]any{
		"success": false, "error": map[string]any{
			"code": "VALIDATION_ERROR", "message": "Validation failed", "statusCode": 404,
			"validationErrors": []map[string]any{{"field": "id", "message": "'" + testPaymentMethodID + "' not found", "code": "VALIDATION_FAILED"}},
		},
	}})
	err := newTestClient(t, notFound.URL).RevokePaymentMethod(context.Background(), testPaymentMethodID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusNotFound || apiErr.ErrorCode != "VALIDATION_ERROR" {
		t.Fatalf("got %T (%v), want *APIError 404 VALIDATION_ERROR", err, err)
	}

	down, _ := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{"success": false}})
	err = newTestClient(t, down.URL).RevokePaymentMethod(context.Background(), testPaymentMethodID)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %T (%v), want *TransportError", err, err)
	}
}

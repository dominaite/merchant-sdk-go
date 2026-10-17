package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestErrorCodeConstantsAreTheWireStrings(t *testing.T) {
	want := map[string]string{
		ErrorCodeStorefrontNotWhitelisted:     "STOREFRONT_NOT_WHITELISTED",
		ErrorCodeStorefrontInactive:           "STOREFRONT_INACTIVE",
		ErrorCodeStorefrontMismatch:           "STOREFRONT_MISMATCH",
		ErrorCodeAlreadyProcessed:             "ALREADY_PROCESSED",
		ErrorCodePriorAttemptFailed:           "PRIOR_ATTEMPT_FAILED",
		ErrorCodeDuplicateRequest:             "DUPLICATE_REQUEST",
		ErrorCodePaymentProcessingUnavailable: "PAYMENT_PROCESSING_UNAVAILABLE",
		ErrorCodeIdempotencyKeyReused:         "IDEMPOTENCY_KEY_REUSED",
	}
	if len(want) != 8 {
		t.Fatalf("two constants share a value: %v", want)
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("constant = %q, want %q", got, expected)
		}
	}
}

// A storefront refusal is an HTTP status plus a code. The caller has to be able
// to reach both through errors.As on the typed error, in either spelling the
// gateway uses for the code, and it must not be retried.
func TestStorefrontRefusalsAreMatchableAPIErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"not whitelisted", http.StatusConflict, ErrorCodeStorefrontNotWhitelisted},
		{"inactive", http.StatusConflict, ErrorCodeStorefrontInactive},
		{"mismatch", http.StatusBadRequest, ErrorCodeStorefrontMismatch},
	}
	for _, tc := range cases {
		bodies := map[string]any{
			"envelope error": map[string]any{
				"success": false,
				"error":   map[string]any{"code": tc.code, "message": "This storefront's domain is not yet whitelisted with the payment provider"},
			},
			"flat errorCode": map[string]any{"success": false, "errorCode": tc.code},
		}
		for form, body := range bodies {
			t.Run(tc.name+"/"+form, func(t *testing.T) {
				server, calls := newTestServer(t, reply{Status: tc.status, Body: body})
				client := newTestClient(t, server.URL)

				_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 3, BaseDelay: time.Millisecond})

				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("got %T %v, want *APIError", err, err)
				}
				if apiErr.HTTPStatus != tc.status || apiErr.ErrorCode != tc.code {
					t.Fatalf("got HTTP %d %q, want HTTP %d %q", apiErr.HTTPStatus, apiErr.ErrorCode, tc.status, tc.code)
				}
				if !errors.Is(err, ErrDominaite) {
					t.Fatal("a storefront refusal must still match ErrDominaite")
				}
				if len(calls()) != 1 {
					t.Fatalf("a storefront refusal was retried: %d calls", len(calls()))
				}
			})
		}
	}
}

// On replay the gateway answers a key minted for another storefront with a
// 200 refusal, not a 400, and it names the transaction the key belongs to.
func TestStorefrontMismatchOnReplayIsARefusal(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success":       false,
		"transactionId": "11111111-1111-4111-8111-111111111111",
		"errorCode":     ErrorCodeStorefrontMismatch,
		"errorMessage":  "This API key is bound to a different storefront than the request names.",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.ErrorCode != ErrorCodeStorefrontMismatch {
		t.Fatalf("got %v, want *RefusalError %s", err, ErrorCodeStorefrontMismatch)
	}
	if refusal.TransactionID == "" {
		t.Fatal("the replay refusal must carry the transaction id")
	}
}

// Every error-code group of the wire contract is a vocabulary this SDK
// exports, in the gateway's order. The wire contract is generated from the
// live API, so a new code there fails this test before a merchant meets it.
func TestErrorCodeVocabulariesMatchTheWireContract(t *testing.T) {
	raw, err := os.ReadFile(wireContractPath)
	if err != nil {
		t.Fatalf("read %s: %v", wireContractPath, err)
	}
	var wire struct {
		ErrorCodes map[string][]wireErrorCode `json:"errorCodes"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("parse %s: %v", wireContractPath, err)
	}

	vocabularies := map[string][]string{
		"auth":          AuthenticationErrorCodes,
		"authorization": AuthorizationErrorCodes,
		"idempotency":   IdempotencyErrorCodes,
		"storefront":    StorefrontErrorCodes,
		"transient":     TransientErrorCodes,
		"validation":    ValidationErrorCodes,
	}
	for group, entries := range wire.ErrorCodes {
		sdk, ok := vocabularies[group]
		if !ok {
			t.Errorf("the wire contract has an error-code group %q that this SDK does not export", group)
			continue
		}
		var codes []string
		for _, entry := range entries {
			codes = append(codes, entry.Code)
		}
		if !reflect.DeepEqual(sdk, codes) {
			t.Errorf("%s codes drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", group, sdk, codes)
		}
	}
	for group := range vocabularies {
		if _, ok := wire.ErrorCodes[group]; !ok {
			t.Errorf("the wire contract no longer has the error-code group %q", group)
		}
	}
}

// The route vocabularies the wire contract does not group yet, pinned to the
// codes the gateway answers on each route, in its order. Each must also reach
// the caller unchanged.
func TestRouteErrorCodeVocabularies(t *testing.T) {
	want := map[string][]string{
		"charge": {
			"PAYMENT_METHOD_NOT_ACTIVE", "DUPLICATE_REQUEST", "IDEMPOTENCY_KEY_REUSED",
			"CHARGE_OUTCOME_UNKNOWN", "CHARGE_FAILED", "PAYMENT_METHOD_CHARGES_DISABLED",
			"PAYMENT_PROCESSING_UNAVAILABLE", "PAYMENT_METHOD_NOT_FOUND", "CHARGE_IN_PROGRESS",
			"CHARGE_CURRENCY_MISMATCH", "AGREEMENT_NOT_FOUND", "AGREEMENT_METHOD_MISMATCH",
			"AGREEMENT_CONSENT_MISSING", "AGREEMENT_CURRENCY_MISMATCH", "AGREEMENT_AMOUNT_MISMATCH",
			"CHARGE_AMOUNT_ABOVE_AGREEMENT", "AGREEMENT_PERIOD_ALREADY_CHARGED", "AGREEMENT_ENDED",
			"AGREEMENT_NOT_STARTED", "CHARGE_AMOUNT_ABOVE_CAP",
		},
		"session refusal": {
			"PAYMENT_PROCESSING_UNAVAILABLE", "DUPLICATE_REQUEST", "ALREADY_PROCESSED",
			"IDEMPOTENCY_KEY_REUSED", "PRIOR_ATTEMPT_FAILED", "STOREFRONT_MISMATCH",
			"AGREEMENT_NOT_FOUND", "AGREEMENT_NOT_PENDING", "AGREEMENT_AMOUNT_MISMATCH",
			"AGREEMENT_CONSENT_REQUIRED", "AGREEMENT_SESSION_OPEN", "AGREEMENT_ALREADY_PAID",
			"AGREEMENT_VARIABLE_AMOUNT_UNSUPPORTED", "SUBSCRIPTIONS_NOT_ENABLED",
		},
		"recurring": {
			"PLAN_NOT_FOUND", "PLAN_NOT_ACTIVE", "AGREEMENT_NOT_FOUND", "AGREEMENT_ENDED",
			"PAYMENT_METHOD_NOT_FOUND", "PAYMENT_METHOD_NOT_ACTIVE", "PAYMENT_METHOD_CURRENCY_MISMATCH",
			"PAYMENT_METHOD_CURRENCY_UNKNOWN", "MAX_AMOUNT_BELOW_PLAN_AMOUNT", "AGREEMENT_CONSENT_REQUIRED",
			"IDEMPOTENCY_KEY_REUSED",
		},
		"webhook": {
			"WEBHOOK_NOT_FOUND", "WEBHOOK_LIMIT_REACHED", "WEBHOOK_ALREADY_REGISTERED",
			"WEBHOOK_URL_REJECTED", "IDEMPOTENCY_KEY_REUSED", "DUPLICATE_REQUEST",
		},
	}
	got := map[string][]string{
		"charge":          ChargeErrorCodes,
		"session refusal": SessionRefusalErrorCodes,
		"recurring":       RecurringErrorCodes,
		"webhook":         WebhookErrorCodes,
	}
	for name, codes := range want {
		if !reflect.DeepEqual(got[name], codes) {
			t.Errorf("%s codes\n  sdk:     %v\n  gateway: %v", name, got[name], codes)
		}
	}
}

// Every 4xx charge code arrives as a *ChargeError with the code, and every
// session refusal code as a *RefusalError.
func TestEveryRouteCodeReachesTheCaller(t *testing.T) {
	for _, code := range ChargeErrorCodes {
		if containsCode(chargeServerErrorCodes, code) {
			continue
		}
		t.Run("charge/"+code, func(t *testing.T) {
			server, _ := newTestServer(t, chargeEnvelope(http.StatusUnprocessableEntity, code, "refused", nil))
			_, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
			var chargeErr *ChargeError
			if !errors.As(err, &chargeErr) || chargeErr.ErrorCode != code {
				t.Fatalf("got %T (%v), want *ChargeError %s", err, err, code)
			}
		})
	}
	for _, code := range SessionRefusalErrorCodes {
		t.Run("session/"+code, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Body: map[string]any{"success": false, "errorCode": code, "errorMessage": "refused"}})
			client := newTestClient(t, server.URL)
			_, err := client.CreateCheckoutSession(context.Background(), testParams())
			var refusal *RefusalError
			if !errors.As(err, &refusal) || refusal.ErrorCode != code {
				t.Fatalf("got %T (%v), want *RefusalError %s", err, err, code)
			}
		})
	}
}

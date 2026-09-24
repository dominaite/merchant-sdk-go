package dominaite

import (
	"context"
	"errors"
	"net/http"
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

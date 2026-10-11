package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Access errors: 401 and 403 are both an *AuthError, told apart by
// HTTPStatus, and only the 401 tells you to check the key id, the secret and
// the clock. A 429 is a *RateLimitError with the code and the envelope.

// gatewayError is the gateway's error envelope.
func gatewayError(status int, code, message string, params map[string]any) reply {
	errorObject := map[string]any{
		"code":       code,
		"message":    message,
		"statusCode": status,
		"traceId":    "00-trace",
		"timestamp":  "2026-10-11T09:00:00Z",
	}
	if params != nil {
		errorObject["params"] = params
	}
	return reply{Status: status, Body: map[string]any{"success": false, "error": errorObject}}
}

func TestA401KeepsTheCredentialsHint(t *testing.T) {
	server, _ := newTestServer(t, gatewayError(http.StatusUnauthorized, "INVALID_SIGNATURE", "Invalid signature", nil))
	client := newTestClient(t, server.URL)

	_, err := client.Ping(context.Background())

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want *AuthError", err)
	}
	if authErr.HTTPStatus != http.StatusUnauthorized || authErr.ErrorCode != "INVALID_SIGNATURE" {
		t.Fatalf("HTTPStatus %d, ErrorCode %q", authErr.HTTPStatus, authErr.ErrorCode)
	}
	if !strings.Contains(authErr.Error(), "server clock") {
		t.Fatalf("a 401 keeps the credentials hint, got %q", authErr.Error())
	}
}

func TestA403NeverCarriesTheCredentialsHint(t *testing.T) {
	cases := []struct {
		name     string
		code     string
		apiText  string
		wantText []string
	}{
		{
			name:     "insufficient scope",
			code:     ErrorCodeInsufficientScope,
			apiText:  "This API key does not have the 'webhooks:manage' scope this route requires",
			wantText: []string{"'webhooks:manage'", "use a key that holds this scope"},
		},
		{
			name:     "insufficient scope without an API message",
			code:     ErrorCodeInsufficientScope,
			wantText: []string{"does not have the scope this route requires", "use a key that holds this scope"},
		},
		{
			name:     "verify-only not enabled",
			code:     ErrorCodeVerifyOnlyNotEnabled,
			apiText:  "Verify-only sessions are not enabled for this account.",
			wantText: []string{"Verify-only sessions are not enabled", "We switch it on for your account"},
		},
		{
			name:     "a code this SDK does not know",
			code:     "SOME_FUTURE_REFUSAL",
			apiText:  "Something new",
			wantText: []string{"HTTP 403 SOME_FUTURE_REFUSAL"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, gatewayError(http.StatusForbidden, tc.code, tc.apiText, nil))
			client := newTestClient(t, server.URL)

			_, err := client.CreateCheckoutSession(context.Background(), testParams())

			var authErr *AuthError
			if !errors.As(err, &authErr) {
				t.Fatalf("got %v, want *AuthError", err)
			}
			if authErr.HTTPStatus != http.StatusForbidden || authErr.ErrorCode != tc.code {
				t.Fatalf("HTTPStatus %d, ErrorCode %q", authErr.HTTPStatus, authErr.ErrorCode)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(authErr.Error(), want) {
					t.Errorf("message %q does not contain %q", authErr.Error(), want)
				}
			}
			for _, banned := range []string{"clock", "secret", "key id"} {
				if strings.Contains(authErr.Error(), banned) {
					t.Errorf("a 403 message must not send you to check the %s: %q", banned, authErr.Error())
				}
			}
		})
	}
}

func TestA429CarriesTheCodeTheWaitAndTheEnvelope(t *testing.T) {
	server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(http.StatusTooManyRequests)
		body, _ := json.Marshal(gatewayError(http.StatusTooManyRequests, ErrorCodeChargeVelocityExceeded,
			"Charge limit reached", map[string]any{"window": 86400, "limit": 10}).Body)
		_, _ = w.Write(body)
	})
	client := newTestClient(t, server.URL)

	_, err := client.ChargePaymentMethod(context.Background(), "pm_0123456789abcdef0123456789abcdef", testChargeParams())

	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %v, want *RateLimitError", err)
	}
	if limited.ErrorCode != ErrorCodeChargeVelocityExceeded {
		t.Errorf("ErrorCode = %q", limited.ErrorCode)
	}
	if !limited.HasRetryAfter || limited.RetryAfterSeconds != 1800 {
		t.Errorf("RetryAfterSeconds = %d (have %t), want 1800", limited.RetryAfterSeconds, limited.HasRetryAfter)
	}
	var envelope struct {
		Error struct {
			Params struct {
				Window int `json:"window"`
				Limit  int `json:"limit"`
			} `json:"params"`
		} `json:"error"`
	}
	if err := json.Unmarshal(limited.Raw, &envelope); err != nil || envelope.Error.Params.Window != 86400 || envelope.Error.Params.Limit != 10 {
		t.Errorf("Raw does not carry error.params: %s (%v)", limited.Raw, err)
	}
}

// The lockout after repeated failed sign-ins answers 429 without Retry-After.
func TestA429WithoutRetryAfterSaysSo(t *testing.T) {
	server, _ := newTestServer(t, gatewayError(http.StatusTooManyRequests, ErrorCodeRateLimitExceeded, "Too many requests", nil))
	client := newTestClient(t, server.URL)

	_, err := client.Ping(context.Background())

	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %v, want *RateLimitError", err)
	}
	if limited.ErrorCode != ErrorCodeRateLimitExceeded || limited.HasRetryAfter {
		t.Errorf("ErrorCode %q, HasRetryAfter %t", limited.ErrorCode, limited.HasRetryAfter)
	}
}

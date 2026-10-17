package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// flakyServer drops the connection without an answer for the first drops
// calls, then serves ok. It records every call it received, dropped ones too.
func flakyServer(t *testing.T, drops int, ok reply) (*httptest.Server, func() []recordedCall) {
	t.Helper()
	var mu sync.Mutex
	calls := []recordedCall{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		index := len(calls)
		calls = append(calls, recordedCall{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
		mu.Unlock()
		if index < drops {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ok.Status)
		_ = json.NewEncoder(w).Encode(ok.Body)
	}))
	t.Cleanup(server.Close)
	return server, func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedCall(nil), calls...)
	}
}

// tickingClock returns a later second on every call, so every attempt signs
// a different timestamp.
func tickingClock() func() time.Time {
	var mu sync.Mutex
	at := time.Unix(1_800_000_000, 0)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(time.Second)
		return at
	}
}

// assertSameKeyAndBody checks that every attempt sent the same idempotency
// key and body, and that each was signed for its own timestamp over them.
func assertSameKeyAndBody(t *testing.T, calls []recordedCall, wantKey string) {
	t.Helper()
	if len(calls) < 2 {
		t.Fatalf("%d calls, want a retry", len(calls))
	}
	timestamps := map[string]bool{}
	for i, call := range calls {
		if key := call.Header.Get("Idempotency-Key"); key != wantKey {
			t.Errorf("call %d Idempotency-Key = %q, want %q", i, key, wantKey)
		}
		if call.Body != calls[0].Body {
			t.Errorf("call %d body = %q, want %q", i, call.Body, calls[0].Body)
		}
		timestamp := call.Header.Get("X-Timestamp")
		timestamps[timestamp] = true
		want := Sign(SignInput{
			Secret: vector.Secret, Timestamp: timestamp, Method: call.Method,
			Path: call.Path, IdempotencyKey: wantKey, Body: call.Body,
		})
		if got := call.Header.Get("X-Signature"); got != want {
			t.Errorf("call %d signature does not match its own timestamp", i)
		}
	}
	if len(timestamps) != len(calls) {
		t.Errorf("attempts reused a timestamp: %v", timestamps)
	}
}

func TestATransportFailureOnAChargeIsRetriedWithTheSameKey(t *testing.T) {
	server, calls := flakyServer(t, 1, reply{Status: http.StatusCreated, Body: testCharge})
	client := newTestClient(t, server.URL)
	client.now = tickingClock()

	params := testChargeParams()
	charge, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, params)
	if err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	if charge.ChargeID != testChargeID {
		t.Fatalf("ChargeID = %q", charge.ChargeID)
	}
	if len(calls()) != 2 {
		t.Fatalf("%d calls, want 2", len(calls()))
	}
	assertSameKeyAndBody(t, calls(), params.IdempotencyKey)
}

func TestATransportFailureOnARefundIsRetriedWithTheSameKey(t *testing.T) {
	server, calls := flakyServer(t, 2, refundReply(http.StatusAccepted, pendingRefund()))
	client := newTestClient(t, server.URL)
	client.now = tickingClock()

	refund, err := client.CreateRefund(context.Background(), testRefundTransactionID, CreateRefundParams{IdempotencyKey: testRefundKey})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if refund.RefundID != testRefundID {
		t.Fatalf("RefundID = %q", refund.RefundID)
	}
	if len(calls()) != 3 {
		t.Fatalf("%d calls, want 3", len(calls()))
	}
	assertSameKeyAndBody(t, calls(), testRefundKey)
}

func TestTheDefaultBudgetIsTwoRetries(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})

	_, err := newTestClient(t, server.URL).GetPlan(context.Background(), testPlanID)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || transportErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("got %T %v, want the last *TransportError", err, err)
	}
	if len(calls()) != 3 {
		t.Fatalf("%d calls, want 3", len(calls()))
	}
}

func TestMaxRetriesZeroSendsOnce(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})
	client, err := New(testKeyID, vector.Secret, WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %T %v, want *TransportError", err, err)
	}
	if len(calls()) != 1 {
		t.Fatalf("%d calls, want 1", len(calls()))
	}
}

func TestNegativeMaxRetriesIsRefused(t *testing.T) {
	_, err := New(testKeyID, vector.Secret, WithBaseURL("https://api.example"), WithMaxRetries(-1))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("got %v, want *ValidationError", err)
	}
}

func TestARateLimitIsNeverRetried(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
	var rateErr *RateLimitError
	if !errors.As(err, &rateErr) || !rateErr.HasRetryAfter || rateErr.RetryAfterSeconds != 30 {
		t.Fatalf("got %v, want *RateLimitError with RetryAfterSeconds 30", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("%d calls, want 1", calls)
	}
}

func TestWhatIsRetried(t *testing.T) {
	cases := []struct {
		name  string
		reply reply
		want  int
	}{
		{"a 5xx without a charge code", chargeEnvelope(http.StatusInternalServerError, ErrorCodeInternalError, "boom", nil), 3},
		{"processing unavailable", chargeEnvelope(http.StatusServiceUnavailable, ChargeErrorProcessingUnavailable, "later", nil), 3},
		{"charges disabled", chargeEnvelope(http.StatusServiceUnavailable, ChargeErrorChargesDisabled, "later", nil), 3},
		{"outcome unknown", chargeEnvelope(http.StatusBadGateway, ChargeErrorOutcomeUnknown, "unknown", nil), 1},
		{"charge failed", chargeEnvelope(http.StatusBadGateway, ChargeErrorFailed, "failed", nil), 1},
		{"a 4xx charge error", chargeEnvelope(http.StatusConflict, ChargeErrorPaymentMethodNotActive, "revoked", nil), 1},
		{"a validation error", chargeEnvelope(http.StatusBadRequest, ErrorCodeValidationError, "bad", nil), 1},
		{"a 401", reply{Status: http.StatusUnauthorized, Body: map[string]any{}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, calls := newTestServer(t, tc.reply)
			_, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
			if err == nil {
				t.Fatal("want an error")
			}
			if len(calls()) != tc.want {
				t.Fatalf("%d calls, want %d", len(calls()), tc.want)
			}
		})
	}
}

func TestAProcessingUnavailableRefusalIsRetried(t *testing.T) {
	server, calls := newTestServer(t,
		reply{Body: map[string]any{"success": false, "errorCode": ErrorCodePaymentProcessingUnavailable}},
		successReply(),
	)
	client := newTestClient(t, server.URL)
	client.now = tickingClock()
	session, err := client.CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if session.CashierToken == "" || len(calls()) != 2 {
		t.Fatalf("session %+v after %d calls, want a session after 2", session, len(calls()))
	}
	assertSameKeyAndBody(t, calls(), testParams().IdempotencyKey)
}

// Registering an endpoint twice is refused, so a lost answer must come back
// to the caller, not turn into a second POST.
func TestCreateWebhookEndpointIsNeverRetried(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})

	_, err := newTestClient(t, server.URL).CreateWebhookEndpoint(context.Background(), CreateWebhookEndpointParams{
		URL: "https://merchant.example/hooks", Events: []string{"payment.succeeded"}, IdempotencyKey: "hook-1",
	})
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %T %v, want *TransportError", err, err)
	}
	if len(calls()) != 1 {
		t.Fatalf("%d calls, want 1", len(calls()))
	}
}

func TestDeletesAndPatchesAreNotRetried(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})
	client := newTestClient(t, server.URL)

	if err := client.RevokePaymentMethod(context.Background(), testPaymentMethodID); err == nil {
		t.Fatal("want an error")
	}
	name := "Gold"
	if _, err := client.UpdatePlan(context.Background(), testPlanID, UpdatePlanParams{Name: &name}); err == nil {
		t.Fatal("want an error")
	}
	if len(calls()) != 2 {
		t.Fatalf("%d calls, want one each", len(calls()))
	}
}

func TestRetryWaitsStayInsideTheJitterBounds(t *testing.T) {
	for _, draw := range []float64{0, 0.5, 0.999999} {
		server, _ := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})
		client, err := New(testKeyID, vector.Secret, WithBaseURL(server.URL), WithMaxRetries(6))
		if err != nil {
			t.Fatal(err)
		}
		var waits []time.Duration
		client.sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}
		client.random = func() float64 { return draw }

		_, _ = client.GetPlan(context.Background(), testPlanID)

		bounds := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
		if len(waits) != len(bounds) {
			t.Fatalf("draw %v: %d waits, want %d", draw, len(waits), len(bounds))
		}
		for i, wait := range waits {
			if wait < 0 || wait >= bounds[i] {
				t.Errorf("draw %v: wait %d = %v, want in [0, %v)", draw, i, wait, bounds[i])
			}
			if want := time.Duration(draw * float64(bounds[i])); wait != want {
				t.Errorf("draw %v: wait %d = %v, want %v", draw, i, wait, want)
			}
		}
	}
}

func TestTheJitterBoundNeverOverflows(t *testing.T) {
	client := &Client{random: func() float64 { return 0.999999 }}
	for _, n := range []int{10, 40, 63, 64, 1000} {
		if wait := client.retryDelay(retryBaseDelay, n); wait < 0 || wait >= retryMaxDelay {
			t.Errorf("retry %d waits %v, want in [0, %v)", n, wait, retryMaxDelay)
		}
	}
}

func TestACancelledContextStopsTheRetries(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})
	client := newTestClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	client.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	_, err := client.GetPlan(ctx, testPlanID)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want a *TransportError wrapping context.Canceled", err)
	}
	if len(calls()) != 1 {
		t.Fatalf("%d calls, want 1", len(calls()))
	}
}

func TestEachRetryIsWarned(t *testing.T) {
	logger, records := newCapture(slog.LevelWarn)
	server, _ := newTestServer(t,
		chargeEnvelope(http.StatusServiceUnavailable, ChargeErrorProcessingUnavailable, "later", nil),
		reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}},
		reply{Status: http.StatusCreated, Body: testCharge},
	)
	client := newLoggedClient(t, server.URL, logger)

	if _, err := client.ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams()); err != nil {
		t.Fatalf("ChargePaymentMethod: %v", err)
	}
	want := []string{
		"dominaite-go: retrying ChargePaymentMethod after " + ChargeErrorProcessingUnavailable + " (attempt 2 of 3)",
		"dominaite-go: retrying ChargePaymentMethod after TransportError (attempt 3 of 3)",
	}
	got := messages(records(), slog.LevelWarn)
	if len(got) != len(want) {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("warning %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The deprecated wrapper keeps its own budget and does not stack it on the
// client's.
func TestTheDeprecatedSessionRetryUsesItsOwnBudget(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusServiceUnavailable, Body: map[string]any{}})
	client := newTestClient(t, server.URL)

	_, _ = client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 2})
	if len(calls()) != 2 {
		t.Fatalf("%d calls, want 2", len(calls()))
	}

	client.maxRetries = 0
	_, _ = client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{})
	if got := len(calls()) - 2; got != 1 {
		t.Fatalf("%d calls with maxRetries 0 and no Attempts, want 1", got)
	}
}

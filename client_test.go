package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKeyID = "dmk_0123456789abcdef0123456789abcdef"

var testCheckout = map[string]any{
	"transactionId": "11111111-1111-4111-8111-111111111111",
	"orderId":       "ord_1",
	"cashierKey":    "ck_1",
	"cashierToken":  "ct_1",
	"amount":        2500,
	"currency":      "EUR",
	"expiresAt":     "2026-08-16T12:00:00Z",
}

func testParams() CreateCheckoutSessionParams {
	return CreateCheckoutSessionParams{
		Amount:         2500,
		Currency:       "EUR",
		OrderReference: "order-1042",
		IdempotencyKey: vector.IdempotencyKey,
	}
}

// recordedCall is one request as the server saw it.
type recordedCall struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

// reply is one canned response.
type reply struct {
	Status int
	Body   any
}

// newTestServer serves replies in order, repeating the last one once exhausted,
// and records every request it received.
func newTestServer(t *testing.T, replies ...reply) (*httptest.Server, func() []recordedCall) {
	t.Helper()

	var mu sync.Mutex
	calls := []recordedCall{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		index := len(calls)
		calls = append(calls, recordedCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   string(body),
		})
		mu.Unlock()

		next := replies[len(replies)-1]
		if index < len(replies) {
			next = replies[index]
		}

		status := next.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		switch payload := next.Body.(type) {
		case string:
			_, _ = io.WriteString(w, payload)
		default:
			_ = json.NewEncoder(w).Encode(payload)
		}
	}))
	t.Cleanup(server.Close)

	return server, func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedCall(nil), calls...)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(testKeyID, vector.Secret, WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func successReply() reply {
	return reply{Body: map[string]any{"success": true, "checkout": testCheckout}}
}

func TestCreateCheckoutSessionSignsTheRequest(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	session, err := client.CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if session.TransactionID != testCheckout["transactionId"] || session.CashierToken != "ct_1" || session.Amount != 2500 {
		t.Fatalf("unexpected session: %+v", session)
	}

	recorded := calls()
	if len(recorded) != 1 {
		t.Fatalf("got %d calls, want 1", len(recorded))
	}
	call := recorded[0]

	if call.Method != http.MethodPost || call.Path != SessionsPath {
		t.Fatalf("got %s %s, want POST %s", call.Method, call.Path, SessionsPath)
	}
	if call.Body != vector.Body {
		t.Fatalf("body = %s, want %s", call.Body, vector.Body)
	}
	if got := call.Header.Get("X-Api-Key-Id"); got != testKeyID {
		t.Fatalf("X-Api-Key-Id = %s", got)
	}
	if got := call.Header.Get("Idempotency-Key"); got != vector.IdempotencyKey {
		t.Fatalf("Idempotency-Key = %s", got)
	}
	if got := call.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %s", got)
	}

	// The signature actually sent must be reproducible from the published recipe,
	// using the timestamp the client chose.
	sentTimestamp := call.Header.Get("X-Timestamp")
	want := Sign(SignInput{
		Secret:         vector.Secret,
		Timestamp:      sentTimestamp,
		Method:         http.MethodPost,
		Path:           SessionsPath,
		IdempotencyKey: vector.IdempotencyKey,
		Body:           call.Body,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatalf("X-Signature = %s, want %s", got, want)
	}

	// And X-Timestamp is unix SECONDS, not milliseconds.
	sent, err := strconv.ParseInt(sentTimestamp, 10, 64)
	if err != nil {
		t.Fatalf("X-Timestamp is not an integer: %q", sentTimestamp)
	}
	if delta := time.Now().Unix() - sent; delta > 5 || delta < -5 {
		t.Fatalf("X-Timestamp out of range: %d", sent)
	}
}

func TestSignedBodyIsTheBodySent(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Customer = &Customer{FirstName: "Ana", Email: "ana@example.com"}
	params.Extra = map[string]any{"merchantNote": "gift & wrap"}

	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	want := Sign(SignInput{
		Secret:         vector.Secret,
		Timestamp:      call.Header.Get("X-Timestamp"),
		Method:         http.MethodPost,
		Path:           SessionsPath,
		IdempotencyKey: vector.IdempotencyKey,
		Body:           call.Body,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatal("the signature does not cover the exact bytes sent")
	}
	if strings.Contains(call.Body, "idempotencyKey") {
		t.Fatalf("idempotencyKey leaked into the body: %s", call.Body)
	}
	if !strings.Contains(call.Body, `"merchantNote":"gift & wrap"`) {
		t.Fatalf("Extra fields missing or HTML-escaped: %s", call.Body)
	}
}

func TestGetStatusSignsEmptyKeyAndEmptyBody(t *testing.T) {
	status := map[string]any{"transactionId": testCheckout["transactionId"], "status": "succeeded", "amount": 2500, "currency": "EUR"}
	server, calls := newTestServer(t, reply{Body: status})
	client := newTestClient(t, server.URL)

	got, err := client.GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got.Status != StatusSucceeded || got.Amount != 2500 {
		t.Fatalf("unexpected status: %+v", got)
	}

	call := calls()[0]
	wantPath := SessionsPath + "/" + testCheckout["transactionId"].(string)
	if call.Method != http.MethodGet || call.Path != wantPath {
		t.Fatalf("got %s %s, want GET %s", call.Method, call.Path, wantPath)
	}
	if call.Body != "" {
		t.Fatalf("GET sent a body: %q", call.Body)
	}
	if _, present := call.Header["Idempotency-Key"]; present {
		t.Fatal("GET must not send an Idempotency-Key header")
	}

	want := Sign(SignInput{
		Secret:    vector.Secret,
		Timestamp: call.Header.Get("X-Timestamp"),
		Method:    http.MethodGet,
		Path:      wantPath,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatalf("X-Signature = %s, want %s", got, want)
	}
}

func TestReplayRefusalCarriesTheTransactionID(t *testing.T) {
	// Without this the documented recovery - read it back with GetStatus - is
	// unreachable from the error, leaving a second payment as the only option.
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success":       false,
		"transactionId": testCheckout["transactionId"],
		"errorCode":     "DUPLICATE_REQUEST",
		"errorMessage":  "A checkout session for this idempotency key is already open.",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %v, want *RefusalError", err)
	}
	if refusal.ErrorCode != "DUPLICATE_REQUEST" {
		t.Fatalf("ErrorCode = %s, want DUPLICATE_REQUEST", refusal.ErrorCode)
	}
	if refusal.TransactionID != testCheckout["transactionId"].(string) {
		t.Fatalf("TransactionID = %q, want %q", refusal.TransactionID, testCheckout["transactionId"])
	}
	if !strings.Contains(string(refusal.Raw), "DUPLICATE_REQUEST") {
		t.Fatalf("Raw did not carry the payload: %s", refusal.Raw)
	}
}

func TestRefusalWithoutATransactionIDLeavesItEmpty(t *testing.T) {
	// The concurrent-race DUPLICATE_REQUEST knows the key is taken, not by which row.
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success":   false,
		"errorCode": "DUPLICATE_REQUEST",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %v, want *RefusalError", err)
	}
	if refusal.TransactionID != "" {
		t.Fatalf("TransactionID = %q, want empty", refusal.TransactionID)
	}
}

func TestPingSignsEmptyKeyAndEmptyBodyOnTheCanonicalPath(t *testing.T) {
	// The gateway envelope: the ping read is FLAT inside data, with no inner
	// success and no wrapper object.
	pong := map[string]any{
		"success": true,
		"data": map[string]any{
			"pong":             true,
			"merchantId":       "mer_1",
			"serverTime":       "2026-08-20T12:00:00Z",
			"serverUnixTime":   1755691200,
			"clockSkewSeconds": 2,
		},
	}
	server, calls := newTestServer(t, reply{Body: pong})
	client := newTestClient(t, server.URL)

	got, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if !got.Pong || got.MerchantID != "mer_1" || got.ClockSkewSeconds != 2 {
		t.Fatalf("unexpected ping: %+v", got)
	}

	call := calls()[0]
	if call.Method != http.MethodGet || call.Path != PingPath {
		t.Fatalf("got %s %s, want GET %s", call.Method, call.Path, PingPath)
	}
	if call.Body != "" {
		t.Fatalf("GET sent a body: %q", call.Body)
	}
	if _, present := call.Header["Idempotency-Key"]; present {
		t.Fatal("GET must not send an Idempotency-Key header")
	}

	// The signed path is the canonical path only, never the base URL's own prefix.
	want := Sign(SignInput{
		Secret:    vector.Secret,
		Timestamp: call.Header.Get("X-Timestamp"),
		Method:    http.MethodGet,
		Path:      PingPath,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatalf("X-Signature = %s, want %s", got, want)
	}
}

// The User-Agent must carry the package's Version const, so a hardcoded
// identifier cannot silently outlive a release bump.
func TestUserAgentCarriesTheSDKVersion(t *testing.T) {
	server, calls := newTestServer(t, reply{Body: map[string]any{
		"success": true,
		"data":    map[string]any{"pong": true, "merchantId": "mer_1", "clockSkewSeconds": 0},
	}})
	client := newTestClient(t, server.URL)

	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	ua := calls()[0].Header.Get("User-Agent")
	if !strings.HasPrefix(ua, "dominaite-go/"+Version+" ") {
		t.Fatalf("User-Agent %q must carry Version %s - keep the const in client.go in sync with the release tag", ua, Version)
	}
}

func TestPingAuthFailureKeepsTheErrorTaxonomy(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: http.StatusUnauthorized, Body: map[string]any{"errorCode": "IP_NOT_ALLOWED"}})
	client := newTestClient(t, server.URL)

	_, err := client.Ping(context.Background())
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want *AuthError", err)
	}
	if authErr.ErrorCode != "IP_NOT_ALLOWED" {
		t.Fatalf("ErrorCode = %s, want IP_NOT_ALLOWED", authErr.ErrorCode)
	}
}

func TestGetStatusRejectsNonUUID(t *testing.T) {
	server, calls := newTestServer(t, reply{Body: map[string]any{}})
	client := newTestClient(t, server.URL)

	_, err := client.GetStatus(context.Background(), "order-1042")
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("got %v, want *ValidationError", err)
	}
	if len(calls()) != 0 {
		t.Fatal("a malformed transaction id must never reach the network")
	}
}

func TestRefusalIsNotATransportFailure(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success":      false,
		"errorCode":    "PAYMENT_PROCESSING_UNAVAILABLE",
		"errorMessage": "Card payments are off",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %v, want *RefusalError", err)
	}
	if refusal.ErrorCode != "PAYMENT_PROCESSING_UNAVAILABLE" {
		t.Fatalf("ErrorCode = %s", refusal.ErrorCode)
	}
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		t.Fatal("a refusal must not also be a transport error")
	}
}

func TestServerErrorIsATransportFailure(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: 503, Body: map[string]any{
		"success":   false,
		"errorCode": "MERCHANT_API_UNAVAILABLE",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
	var refusal *RefusalError
	if errors.As(err, &refusal) {
		t.Fatal("a 503 must not be reported as a refusal")
	}
}

func TestNetworkFailureIsATransportError(t *testing.T) {
	server, _ := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)
	server.Close() // nothing is listening any more

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
	if transportErr.Unwrap() == nil {
		t.Fatal("a network TransportError must wrap its cause")
	}
}

func TestAuthErrorCodes(t *testing.T) {
	for _, code := range []string{"INVALID_API_KEY", "INVALID_SIGNATURE", "TIMESTAMP_OUT_OF_RANGE", "IP_NOT_ALLOWED"} {
		t.Run(code, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Status: 401, Body: map[string]any{"success": false, "errorCode": code}})
			client := newTestClient(t, server.URL)

			_, err := client.CreateCheckoutSession(context.Background(), testParams())

			var authErr *AuthError
			if !errors.As(err, &authErr) {
				t.Fatalf("got %v, want *AuthError", err)
			}
			if authErr.ErrorCode != code {
				t.Fatalf("ErrorCode = %s, want %s", authErr.ErrorCode, code)
			}
		})
	}
}

func TestAuthErrorFromEnvelopeErrorCode(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: 401, Body: map[string]any{
		"success": false,
		"error":   map[string]any{"code": "IP_NOT_ALLOWED", "message": "caller not allowlisted"},
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.ErrorCode != "IP_NOT_ALLOWED" {
		t.Fatalf("got %v, want *AuthError with IP_NOT_ALLOWED", err)
	}
}

// A rejecting 4xx must carry its machine-readable code through, so callers
// branch on the code instead of matching the message text. Idempotency-key
// replays are NOT this shape - they are HTTP 200 refusals, pinned by
// TestSessionRefusalErrorCodesAreRecognized.
func TestUnexpected4xxCarriesTheErrorCode(t *testing.T) {
	server, _ := newTestServer(t, reply{Status: http.StatusBadRequest, Body: map[string]any{
		"success":      false,
		"errorCode":    "SOME_UNEXPECTED_CODE",
		"errorMessage": "Request rejected for an unmodelled reason",
	}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
	if apiErr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, want 400", apiErr.HTTPStatus)
	}
	if apiErr.ErrorCode != "SOME_UNEXPECTED_CODE" {
		t.Fatalf("ErrorCode = %q, want SOME_UNEXPECTED_CODE", apiErr.ErrorCode)
	}
	if apiErr.Error() != "Request rejected for an unmodelled reason" {
		t.Fatalf("message = %q", apiErr.Error())
	}
}

func TestEnvelopeDataIsUnwrapped(t *testing.T) {
	status := map[string]any{"transactionId": testCheckout["transactionId"], "status": "pending", "amount": 2500, "currency": "EUR"}
	server, _ := newTestServer(t, reply{Body: map[string]any{"success": true, "data": status}})
	client := newTestClient(t, server.URL)

	got, err := client.GetStatus(context.Background(), testCheckout["transactionId"].(string))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got.Status != StatusPending || got.TransactionID != testCheckout["transactionId"] {
		t.Fatalf("envelope was not unwrapped: %+v", got)
	}
}

func TestEnvelopeDataIsUnwrappedForCreate(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{
		"success": true,
		"data":    map[string]any{"success": true, "checkout": testCheckout},
	}})
	client := newTestClient(t, server.URL)

	session, err := client.CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if session.CashierKey != "ck_1" {
		t.Fatalf("envelope was not unwrapped: %+v", session)
	}
}

func TestRetryReusesOneIdempotencyKey(t *testing.T) {
	server, calls := newTestServer(t,
		reply{Status: 503, Body: map[string]any{"errorCode": "MERCHANT_API_UNAVAILABLE"}},
		reply{Status: 503, Body: map[string]any{"errorCode": "MERCHANT_API_UNAVAILABLE"}},
		successReply(),
	)
	client := newTestClient(t, server.URL)

	params := testParams()
	params.IdempotencyKey = "" // let the SDK mint one, then pin it across attempts

	session, err := client.CreateCheckoutSessionWithRetry(context.Background(), params, RetryOptions{Attempts: 3, BaseDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("CreateCheckoutSessionWithRetry: %v", err)
	}
	if session.CashierKey != "ck_1" {
		t.Fatalf("unexpected session: %+v", session)
	}

	recorded := calls()
	if len(recorded) != 3 {
		t.Fatalf("got %d attempts, want 3", len(recorded))
	}
	first := recorded[0].Header.Get("Idempotency-Key")
	if first == "" {
		t.Fatal("no idempotency key was sent")
	}
	for i, call := range recorded {
		if got := call.Header.Get("Idempotency-Key"); got != first {
			t.Fatalf("attempt %d used key %s, want the pinned %s", i, got, first)
		}
	}
}

func TestRetryDoesNotRetryRefusals(t *testing.T) {
	server, calls := newTestServer(t, reply{Body: map[string]any{"success": false, "errorCode": "DUPLICATE_REQUEST"}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 3, BaseDelay: time.Millisecond})

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %v, want *RefusalError", err)
	}
	if len(calls()) != 1 {
		t.Fatalf("a refusal was retried %d times", len(calls())-1)
	}
}

func TestRetryDoesNotRetryAuthFailures(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: 401, Body: map[string]any{"errorCode": "INVALID_SIGNATURE"}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 3, BaseDelay: time.Millisecond})

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want *AuthError", err)
	}
	if len(calls()) != 1 {
		t.Fatalf("an auth failure was retried %d times", len(calls())-1)
	}
}

func TestRetryGivesUpWithTheLastTransportError(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: 503, Body: map[string]any{"errorCode": "MERCHANT_API_UNAVAILABLE"}})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 2, BaseDelay: time.Millisecond})

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
	if len(calls()) != 2 {
		t.Fatalf("got %d attempts, want 2", len(calls()))
	}
}

func TestAmountsMustBePositiveMinorUnits(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	for _, amount := range []int64{0, -100} {
		params := testParams()
		params.Amount = amount
		_, err := client.CreateCheckoutSession(context.Background(), params)

		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("amount %d: got %v, want *ValidationError", amount, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("invalid amounts must never reach the network")
	}
}

func TestRequiredParams(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	missingCurrency := testParams()
	missingCurrency.Currency = ""
	missingReference := testParams()
	missingReference.OrderReference = ""
	longReference := testParams()
	longReference.OrderReference = strings.Repeat("x", 101)

	for name, params := range map[string]CreateCheckoutSessionParams{
		"currency":       missingCurrency,
		"orderReference": missingReference,
		"long reference": longReference,
	} {
		var validationErr *ValidationError
		if _, err := client.CreateCheckoutSession(context.Background(), params); !errors.As(err, &validationErr) {
			t.Fatalf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("invalid params must never reach the network")
	}
}

func TestCredentialsArePrefixChecked(t *testing.T) {
	if _, err := New("nope", vector.Secret); err == nil {
		t.Fatal("a key id without the dmk_ prefix must be rejected")
	}
	if _, err := New(testKeyID, "nope"); err == nil {
		t.Fatal("a secret without the dms_ prefix must be rejected")
	}
	if _, err := New(testKeyID, vector.Secret); err != nil {
		t.Fatalf("valid credentials rejected: %v", err)
	}
}

func TestNonJSONResponseIsAnAPIError(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: "<html>502 Bad Gateway</html>"})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
}

func TestEverySDKErrorMatchesTheCatchAll(t *testing.T) {
	errs := []error{
		newRefusalError("DUPLICATE_REQUEST", "refused"),
		newAuthError("INVALID_SIGNATURE", "auth"),
		newAPIError(422, "rejected"),
		newTransportError("offline", io.EOF),
		newValidationError("bad amount"),
	}

	for _, err := range errs {
		var sdkErr Error
		if !errors.As(err, &sdkErr) {
			t.Fatalf("%T does not satisfy the Error interface", err)
		}
		if !errors.Is(err, ErrDominaite) {
			t.Fatalf("%T does not match ErrDominaite", err)
		}
		if sdkErr.Error() == "" {
			t.Fatalf("%T has an empty message", err)
		}
	}
}

func TestBaseURLDefaultsToProduction(t *testing.T) {
	client, err := New(testKeyID, vector.Secret, WithBaseURL(""))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Fatalf("baseURL = %s, want %s", client.baseURL, DefaultBaseURL)
	}

	trimmed, err := New(testKeyID, vector.Secret, WithBaseURL("https://example.test/api/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if trimmed.baseURL != "https://example.test/api" {
		t.Fatalf("trailing slash not trimmed: %s", trimmed.baseURL)
	}
}

func TestContextCancellationIsATransportError(t *testing.T) {
	server, _ := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.CreateCheckoutSession(ctx, testParams())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("the cancellation cause must stay reachable with errors.Is")
	}
}

func TestGeneratedIdempotencyKeysAreUniqueUUIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		key, err := newIdempotencyKey()
		if err != nil {
			t.Fatalf("newIdempotencyKey: %v", err)
		}
		if !uuidPattern.MatchString(key) {
			t.Fatalf("not a UUID: %s", key)
		}
		if seen[key] {
			t.Fatalf("duplicate idempotency key: %s", key)
		}
		seen[key] = true
	}
}

// newRedirectServer serves a redirect to a second server that would happily
// answer with a forged, well-formed checkout session. It reports how many times
// the redirect target was hit (never, once the hop is refused) and how many
// times the redirecting server itself was called (once per attempt).
func newRedirectServer(t *testing.T, status int) (server *httptest.Server, targetHits func() int, attempts func() int) {
	t.Helper()

	var mu sync.Mutex
	hits, calls := 0, 0

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "checkout": testCheckout})
	}))
	t.Cleanup(target.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()

		http.Redirect(w, r, target.URL+r.URL.Path, status)
	}))
	t.Cleanup(redirector.Close)

	count := func(of *int) func() int {
		return func() int {
			mu.Lock()
			defer mu.Unlock()
			return *of
		}
	}

	return redirector, count(&hits), count(&calls)
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server, targetHits, _ := newRedirectServer(t, status)
			client := newTestClient(t, server.URL)

			session, err := client.CreateCheckoutSession(context.Background(), testParams())
			if session != nil {
				t.Fatal("a redirect target's response must never be returned as a session")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("got %v, want *APIError", err)
			}
			if apiErr.HTTPStatus != status {
				t.Fatalf("got HTTPStatus %d, want %d", apiErr.HTTPStatus, status)
			}
			if !strings.Contains(apiErr.Error(), "redirect") {
				t.Fatalf("the message must name the redirect: %s", apiErr.Error())
			}
			if got := targetHits(); got != 0 {
				t.Fatalf("the redirect target was hit %d times; credentials leak on the hop", got)
			}
		})
	}
}

func TestRedirectsAreNotRetried(t *testing.T) {
	server, targetHits, attempts := newRedirectServer(t, http.StatusMovedPermanently)
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{
		Attempts:  3,
		BaseDelay: time.Millisecond,
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
	// The point of the test: a 3xx is not a *TransportError, so the helper must
	// give up after ONE attempt rather than replaying signed headers at whatever
	// is answering.
	if got := attempts(); got != 1 {
		t.Fatalf("the API was called %d times, want 1; a redirect must not be retried", got)
	}
	if got := targetHits(); got != 0 {
		t.Fatalf("the redirect target was hit %d times; credentials leak on the hop", got)
	}
}

func TestRedirectsAreNotFollowedWithACustomHTTPClient(t *testing.T) {
	server, targetHits, _ := newRedirectServer(t, http.StatusFound)
	custom := &http.Client{Timeout: 5 * time.Second}

	client, err := New(testKeyID, vector.Secret, WithBaseURL(server.URL), WithHTTPClient(custom))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err == nil {
		t.Fatal("want an error, got none")
	}
	if got := targetHits(); got != 0 {
		t.Fatalf("the redirect target was hit %d times; credentials leak on the hop", got)
	}
	if custom.CheckRedirect != nil {
		t.Fatal("the caller's own client must not be modified")
	}
}

func TestClientNeverPrintsTheSecret(t *testing.T) {
	client := newTestClient(t, "https://example.test")

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		for name, printed := range map[string]string{
			"value":   fmt.Sprintf(verb, *client),
			"pointer": fmt.Sprintf(verb, client),
		} {
			if strings.Contains(printed, vector.Secret) {
				t.Fatalf("%s on a %s leaked the secret: %s", verb, name, printed)
			}
			// A prefix of the secret is still the secret.
			if strings.Contains(printed, vector.Secret[:12]) {
				t.Fatalf("%s on a %s leaked part of the secret: %s", verb, name, printed)
			}
			if !strings.Contains(printed, "redacted") {
				t.Fatalf("%s on a %s does not say the secret was redacted: %s", verb, name, printed)
			}
			if !strings.Contains(printed, testKeyID) {
				t.Fatalf("%s on a %s dropped the key id: %s", verb, name, printed)
			}
		}
	}
}

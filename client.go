// Package dominaite is the server-side Go client for the Dominaite merchant API.
//
// One call from your backend opens a hosted checkout session; a two-line script
// tag renders the payment widget on your page. Card details go straight from your
// customer's browser into the widget. They never touch your server or this SDK,
// which keeps your PCI scope minimal (SAQ A).
//
// Keep your API secret on the server. Never ship it to a browser, never commit
// it, never log it.
//
//	client, err := dominaite.New(os.Getenv("DOMINAITE_KEY_ID"), os.Getenv("DOMINAITE_SECRET"))
//	if err != nil {
//		return err
//	}
//	session, err := client.CreateCheckoutSession(ctx, dominaite.CreateCheckoutSessionParams{
//		Amount:         2500, // minor units: 25.00 EUR
//		Currency:       "EUR",
//		OrderReference: "order-1042",
//	})
//
// Hand session.CashierKey and session.CashierToken to the embed snippet.
package dominaite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the production merchant API.
	DefaultBaseURL = "https://api.dominaite.com/payments"

	// SessionsPath is the canonical path that gets signed. POST creates a
	// session; GET SessionsPath + "/{transactionId}" reads its status.
	SessionsPath = "/merchant-api/bridgerpay/checkout/sessions"

	// PingPath is the credentials-and-clock smoke test. It creates nothing.
	PingPath = "/merchant-api/ping"

	// Version is this SDK's version, reported in the User-Agent.
	Version = "0.1.2"

	defaultTimeout = 45 * time.Second // serverless cold starts hit 10+s on dev; 15s was a coin flip
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Client is a server-side client for the Dominaite merchant API. It is safe for
// concurrent use.
type Client struct {
	keyID      string
	secret     string
	baseURL    string
	httpClient *http.Client
	userAgent  string
	now        func() time.Time
}

// Option configures a Client. Pass options to New.
type Option func(*Client)

// WithBaseURL points the client at a non-production environment. Trailing
// slashes are trimmed. Empty values are ignored, so you can pass an unset
// environment variable straight through and still get production.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
			c.baseURL = trimmed
		}
	}
}

// WithHTTPClient supplies your own *http.Client: a proxy-aware transport,
// custom TLS, or a test double. It replaces WithTimeout, so set the timeout on
// the client you pass.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithTimeout sets the per-request timeout on the default HTTP client. Defaults
// to 45 seconds. A context deadline still wins when it is shorter.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.httpClient.Timeout = timeout
		}
	}
}

// WithUserAgent appends your own identifier to the SDK's User-Agent, which helps
// when Dominaite support reads the access logs for your integration.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) {
		if trimmed := strings.TrimSpace(userAgent); trimmed != "" {
			c.userAgent = c.userAgent + " " + trimmed
		}
	}
}

// New builds a client from your API key id (dmk_...) and secret (dms_...), both
// from the dashboard's Website integration tab. It returns a *ValidationError
// when either credential has the wrong prefix, which catches a swapped key id
// and secret before anything is sent.
func New(keyID, secret string, opts ...Option) (*Client, error) {
	if !strings.HasPrefix(keyID, "dmk_") {
		return nil, newValidationError("keyId must start with dmk_")
	}
	if !strings.HasPrefix(secret, "dms_") {
		return nil, newValidationError("secret must start with dms_")
	}

	client := &Client{
		keyID:      keyID,
		secret:     secret,
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
		userAgent:  fmt.Sprintf("dominaite-go/%s (%s)", Version, runtime.Version()),
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// Ping verifies your credentials, your signing and your clock without creating
// anything. Make this your first live call: it isolates the setup problems from
// the payment ones, so a failure here is the key id, the secret, the signature,
// the clock or an IP allowlist and nothing else.
//
// Check Ping.ClockSkewSeconds. Requests start failing at 300.
//
// Errors it returns:
//   - *AuthError: wrong credentials, bad signature, clock off, IP not allowlisted.
//   - *APIError: an unexpected or rejecting response; inspect HTTPStatus.
//   - *TransportError: network failure or 5xx.
func (c *Client) Ping(ctx context.Context) (*Ping, error) {
	// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
	// Idempotency-Key header.
	payload, err := c.request(ctx, http.MethodGet, PingPath, "", "")
	if err != nil {
		return nil, err
	}

	ping := &Ping{Raw: payload}
	if err := json.Unmarshal(payload, ping); err != nil {
		return nil, newAPIError(http.StatusOK, "The API returned an unexpected ping response")
	}

	return ping, nil
}

// CreateCheckoutSession opens a hosted checkout session for one payment.
//
// Errors it returns:
//   - *ValidationError: bad arguments; nothing was sent.
//   - *AuthError: wrong credentials, bad signature, clock off, IP not allowlisted.
//     Fix the config, do not retry.
//   - *RefusalError: the gateway refused the session; inspect ErrorCode.
//   - *APIError: an unexpected or rejecting response; inspect HTTPStatus.
//   - *TransportError: network failure or 5xx. Safe to retry WITH the same
//     idempotency key, which is what CreateCheckoutSessionWithRetry does.
func (c *Client) CreateCheckoutSession(ctx context.Context, params CreateCheckoutSessionParams) (*CheckoutSession, error) {
	idempotencyKey, body, err := prepareSessionRequest(params)
	if err != nil {
		return nil, err
	}

	payload, err := c.request(ctx, http.MethodPost, SessionsPath, body, idempotencyKey)
	if err != nil {
		return nil, err
	}

	var envelope checkoutSessionEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, newAPIError(http.StatusOK, "The API returned an unexpected create-session response")
	}

	if envelope.Success == nil || !*envelope.Success || len(envelope.Checkout) == 0 || string(envelope.Checkout) == "null" {
		code := envelope.ErrorCode
		if code == "" {
			code = "UNKNOWN"
		}
		message := envelope.ErrorMessage
		if message == "" {
			message = "The checkout session was refused."
		}
		// A replay refusal names the transaction the key collided with. Carry it
		// (and the whole payload) so the caller can reconcile with GetStatus
		// instead of minting a second payment for the same order.
		refusal := newRefusalError(code, message)
		refusal.TransactionID = envelope.TransactionID
		refusal.Raw = payload
		return nil, refusal
	}

	session := &CheckoutSession{Raw: envelope.Checkout}
	if err := json.Unmarshal(envelope.Checkout, session); err != nil {
		return nil, newAPIError(http.StatusOK, "The API returned an unexpected checkout object")
	}

	return session, nil
}

// RetryOptions tunes CreateCheckoutSessionWithRetry.
type RetryOptions struct {
	// Attempts is the total number of attempts including the first. Zero means
	// the default of 3.
	Attempts int
	// BaseDelay is the wait before the first retry; it doubles each attempt.
	// Zero means the default of 500ms.
	BaseDelay time.Duration
}

func (o RetryOptions) withDefaults() (int, time.Duration, error) {
	attempts := o.Attempts
	if attempts == 0 {
		attempts = 3
	}
	if attempts < 1 {
		return 0, 0, newValidationError("Attempts must be a positive integer")
	}

	baseDelay := o.BaseDelay
	if baseDelay == 0 {
		baseDelay = 500 * time.Millisecond
	}
	if baseDelay < 0 {
		return 0, 0, newValidationError("BaseDelay must not be negative")
	}

	return attempts, baseDelay, nil
}

// CreateCheckoutSessionWithRetry creates a session, retrying *TransportError
// only, with THE SAME idempotency key across every attempt.
//
// Reusing the key is what makes the retry safe. A transport failure leaves you
// not knowing whether the request landed; the API returns the original session
// for a key it has already seen instead of opening a second one. Generating a
// fresh key per attempt would be exactly the double-charge bug this method
// exists to prevent, so the key is pinned once before the first attempt.
//
// Refusals and authentication failures are returned immediately. They will not
// change on a retry.
func (c *Client) CreateCheckoutSessionWithRetry(ctx context.Context, params CreateCheckoutSessionParams, opts RetryOptions) (*CheckoutSession, error) {
	attempts, baseDelay, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}

	if params.IdempotencyKey == "" {
		key, err := newIdempotencyKey()
		if err != nil {
			return nil, err
		}
		params.IdempotencyKey = key
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		session, err := c.CreateCheckoutSession(ctx, params)
		if err == nil {
			return session, nil
		}

		var transportErr *TransportError
		if !errors.As(err, &transportErr) {
			return nil, err
		}
		lastErr = err

		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, newTransportError("Retrying was cancelled: "+ctx.Err().Error(), ctx.Err())
		case <-time.After(baseDelay << attempt):
		}
	}

	return nil, lastErr
}

// GetStatus reads the payment status of one of your checkout sessions.
//
// Status is one of the Status* constants: pending, processing, succeeded, failed,
// refunded, partially_refunded, cancelled, disputed, requires_capture, abandoned.
// While a session is still payable the response also carries ExpiresAt; after that
// instant a pending session can only become abandoned. Amounts are integers in
// MINOR units. An unknown transaction id returns an *APIError with HTTPStatus 404.
//
// StatusSucceeded is the only value that means the payment is complete. Keep polling
// on StatusPending, StatusProcessing and StatusRequiresCapture - none of them is
// terminal.
//
// StatusRequiresCapture is NOT "unpaid": the payer has already paid and the funds are
// held awaiting capture. Never treat it as an abandoned order.
//
// Treat any status you do not recognise as still-open too: a value the API adds later
// should make you keep polling, never silently close a live order.
//
// Poll after the payer returns to you, or on your order timeout. Not in a tight
// loop: the endpoint is rate limited per key.
func (c *Client) GetStatus(ctx context.Context, transactionID string) (*CheckoutStatus, error) {
	normalized := strings.ToLower(strings.TrimSpace(transactionID))
	if !uuidPattern.MatchString(normalized) {
		return nil, newValidationError("transactionId must be the UUID returned by CreateCheckoutSession")
	}

	// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
	// Idempotency-Key header.
	payload, err := c.request(ctx, http.MethodGet, SessionsPath+"/"+normalized, "", "")
	if err != nil {
		return nil, err
	}

	status := &CheckoutStatus{Raw: payload}
	if err := json.Unmarshal(payload, status); err != nil {
		return nil, newAPIError(http.StatusOK, "The API returned an unexpected status response")
	}

	return status, nil
}

// prepareSessionRequest validates the params and returns the idempotency key and
// the exact body bytes that will be both signed and sent.
func prepareSessionRequest(params CreateCheckoutSessionParams) (string, string, error) {
	if params.Amount <= 0 {
		return "", "", newValidationError("amount must be a positive integer in MINOR units (e.g. 2500 for 25.00 EUR)")
	}
	if strings.TrimSpace(params.Currency) == "" {
		return "", "", newValidationError("Missing required parameter: currency")
	}
	if strings.TrimSpace(params.OrderReference) == "" {
		return "", "", newValidationError("Missing required parameter: orderReference")
	}
	if len(params.OrderReference) > 100 {
		return "", "", newValidationError("orderReference must be at most 100 characters")
	}

	idempotencyKey := params.IdempotencyKey
	if idempotencyKey == "" {
		key, err := newIdempotencyKey()
		if err != nil {
			return "", "", err
		}
		idempotencyKey = key
	}
	if len(idempotencyKey) > 100 {
		return "", "", newValidationError("idempotencyKey must be a non-empty string of at most 100 characters")
	}

	// marshalNoEscape, not json.Marshal: the latter re-escapes the bytes
	// MarshalJSON returned, so an ampersand in a customer name would reach the
	// gateway as an escaped unicode sequence.
	body, err := marshalNoEscape(params)
	if err != nil {
		return "", "", newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	return idempotencyKey, string(body), nil
}

// request signs and sends one call, and maps the response onto the error
// taxonomy. body and idempotencyKey are both empty for GET.
func (c *Client) request(ctx context.Context, method, path, body, idempotencyKey string) (json.RawMessage, error) {
	if ctx == nil {
		return nil, newValidationError("ctx must not be nil")
	}

	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	signature := Sign(SignInput{
		Secret:         c.secret,
		Timestamp:      timestamp,
		Method:         method,
		Path:           path,
		IdempotencyKey: idempotencyKey,
		Body:           body,
	})

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, newValidationError("Could not build the request: " + err.Error())
	}

	req.Header.Set("Content-Type", "application/json")
	// Some edges block requests without a real User-Agent, so always send one.
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Api-Key-Id", c.keyID)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", signature)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, newTransportError("Could not reach the Dominaite API: "+err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newTransportError("Could not read the Dominaite API response: "+err.Error(), err)
	}

	// The gateway wraps responses as { success, data, ... }; unwrap when present.
	// Error responses carry the machine-readable code at error.code.
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *envelopeError  `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || !isJSONObject(raw) {
		return nil, newAPIError(resp.StatusCode, "The API returned a non-JSON response")
	}

	payload := json.RawMessage(raw)
	if isJSONObject(envelope.Data) {
		payload = envelope.Data
	}

	var inner struct {
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	_ = json.Unmarshal(payload, &inner)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		code := firstNonEmpty(inner.ErrorCode, envelope.Error.code(), "UNAUTHORIZED")
		return nil, newAuthError(code, "Authentication failed - check your key id, secret, and server clock.")
	case resp.StatusCode >= 500:
		return nil, newTransportError(
			fmt.Sprintf("The Dominaite API is unavailable (HTTP %d); retry with the same idempotency key.", resp.StatusCode),
			nil,
		)
	case resp.StatusCode >= 400:
		message := firstNonEmpty(inner.ErrorMessage, envelope.Error.message(), "Request rejected")
		apiErr := newAPIError(resp.StatusCode, message)
		// Input rejections name a machine-readable code the same way refusals
		// do; carry it so callers branch on the code, not on the message text.
		apiErr.ErrorCode = firstNonEmpty(inner.ErrorCode, envelope.Error.code())
		return nil, apiErr
	}

	return payload, nil
}

// newIdempotencyKey mints a random v4 UUID. Keys are per-payment, so a fresh one
// is generated for every call that does not supply its own.
func newIdempotencyKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", newTransportError("Could not generate an idempotency key: "+err.Error(), err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80

	hexed := hex.EncodeToString(buf[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32], nil
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return strings.HasPrefix(trimmed, "{")
}

// envelopeError is the gateway's outer error object: { error: { code, message } }.
type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *envelopeError) code() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *envelopeError) message() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

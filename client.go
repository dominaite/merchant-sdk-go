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
//	client, err := dominaite.New(
//		os.Getenv("DOMINAITE_KEY_ID"),
//		os.Getenv("DOMINAITE_SECRET"),
//		dominaite.WithBaseURL(dominaite.SandboxBaseURL), // required
//	)
//	if err != nil {
//		return err
//	}
//	key, err := dominaite.OrderIdempotencyKey("checkout", "order-1042", 2500, "EUR")
//	if err != nil {
//		return err
//	}
//	session, err := client.CreateCheckoutSession(ctx, dominaite.CreateCheckoutSessionParams{
//		Amount:         2500, // minor units: 25.00 EUR
//		Currency:       "EUR",
//		OrderReference: "order-1042",
//		IdempotencyKey: key, // "checkout-order-1042-2500-EUR"
//	})
//
// Hand session.CashierKey and session.CashierToken to the embed snippet.
package dominaite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// SandboxBaseURL is the sandbox merchant API. Use it with your sandbox keys.
	SandboxBaseURL = "https://api.staging.dominaite.com/payments"

	// ProductionBaseURL is the production merchant API. Use it with your live keys.
	ProductionBaseURL = "https://api.dominaite.com/payments"

	// SessionsPath is the canonical path that gets signed. POST creates a
	// session; GET SessionsPath + "/{transactionId}" reads its status.
	SessionsPath = "/merchant-api/checkout/sessions"

	// PaymentMethodsPath is the canonical path of stored payment methods. POST
	// PaymentMethodsPath + "/{paymentMethodId}/charges" charges one; DELETE
	// PaymentMethodsPath + "/{paymentMethodId}" revokes it.
	PaymentMethodsPath = "/merchant-api/payment-methods"

	// PaymentsPath is the canonical path of payments. POST PaymentsPath +
	// "/{transactionId}/refunds" refunds one, GET on the same path lists its
	// refunds, and GET PaymentsPath + "/{transactionId}/refunds/{refundId}"
	// reads a refund back.
	PaymentsPath = "/merchant-api/payments"

	// PlansPath is the canonical path of subscription plans. POST creates a
	// plan, GET lists them; GET, PATCH and DELETE PlansPath + "/{planId}"
	// read, change and archive one.
	PlansPath = "/merchant-api/plans"

	// AgreementsPath is the canonical path of subscription agreements. POST
	// creates an agreement; GET and DELETE AgreementsPath + "/{agreementId}"
	// read and cancel one.
	AgreementsPath = "/merchant-api/agreements"

	// WebhooksPath is the canonical path of your webhook endpoints. POST
	// registers one, GET lists them, DELETE WebhooksPath + "/{webhookId}"
	// deletes one.
	WebhooksPath = "/merchant-api/webhooks"

	// TransactionsPath is the canonical path of your transaction list. GET
	// reads one page. The query string is never part of the signature.
	TransactionsPath = "/merchant-api/transactions"

	// PingPath is the credentials-and-clock smoke test. It creates nothing.
	PingPath = "/merchant-api/ping"

	// IntegrationContractPath is the API's machine-readable contract. GET
	// reads it. The route is public, so it needs no scope.
	IntegrationContractPath = "/merchant-api/integration/contract"

	// Version is this SDK's version, reported in the User-Agent.
	Version = "0.6.0"

	defaultTimeout = 45 * time.Second // serverless cold starts hit 10+s on dev; 15s was a coin flip

	// maxResponseBytes caps how much of a response the SDK will read. Real
	// merchant API payloads are a few kilobytes; anything at this size is a
	// proxy error page or something hostile, and reading it to the end would
	// let whatever answered decide how much memory this process uses.
	maxResponseBytes = 10 << 20 // 10MB

	// maxIdempotencyKeyLength is the gateway's limit, in characters.
	maxIdempotencyKeyLength = 100
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// A payment method id is opaque (pm_...), so this only pins what keeps it a
// single path segment: no slash, no query, no whitespace, nothing that needs
// percent-encoding. The id goes into the signed canonical path verbatim, so
// anything else would sign one path and request another.
var paymentMethodIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// A refund id (re_...) is opaque too, and goes into the signed path the same way.
var refundIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// Client is a server-side client for the Dominaite merchant API. It is safe for
// concurrent use.
type Client struct {
	keyID      string
	secret     string
	baseURL    string
	httpClient *http.Client
	userAgent  string
	now        func() time.Time
	optionErr  error
	logger     *slog.Logger // nil: warnings go to slog.Default(), no debug lines
	notices    *noticeSet
	maxRetries int
	sleep      func(context.Context, time.Duration) error // the wait between retries
	random     func() float64                             // [0, 1), for the retry jitter
}

// String keeps the secret out of your logs. Printing a Client with %v, %+v or
// %#v (value or pointer) shows the key id and the base URL, never the secret.
func (c Client) String() string {
	return fmt.Sprintf("dominaite.Client{keyID: %q, baseURL: %q, secret: %s}", c.keyID, c.baseURL, redactSecret(c.secret))
}

// GoString covers %#v, which does not use String.
func (c Client) GoString() string { return c.String() }

// redactSecret renders a secret for humans without disclosing any of it. It
// never returns a prefix of the real value.
func redactSecret(secret string) string {
	if secret == "" {
		return `""`
	}
	if strings.HasPrefix(secret, "dms_") {
		return "dms_***redacted***"
	}
	if strings.HasPrefix(secret, "whsec_") {
		return "whsec_***redacted***"
	}
	return "***redacted***"
}

// Option configures a Client. Pass options to New.
type Option func(*Client)

// WithBaseURL sets the API the client calls: SandboxBaseURL, ProductionBaseURL
// or your own value. It is required: New returns a *ValidationError without it,
// and when the value is empty or whitespace. Whitespace and trailing slashes are
// trimmed.
//
// The URL must be https://. Plain http:// is accepted only for localhost,
// 127.0.0.1 and ::1, so a local mock server still works; anything else is
// refused by New with a *ValidationError. Over http:// your API secret's
// signature, key id and the whole request body travel in clear text.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

// WithHTTPClient supplies your own *http.Client: a proxy-aware transport,
// custom TLS, or a test double. It replaces WithTimeout, so set the timeout on
// the client you pass.
//
// The client is shallow-copied, so the SDK can pin its own redirect policy
// without changing the behaviour of the client you keep using elsewhere.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			copied := *httpClient
			c.httpClient = &copied
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
// when Dominaite support reads the access logs for your integration. The SDK
// sends "dominaite-go/<version> (go <go version>) <your suffix>". A suffix with a
// control character makes New return a *ValidationError.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) {
		trimmed := strings.TrimSpace(userAgent)
		if hasControlChar(trimmed) {
			c.optionErr = newValidationError("userAgent must not contain control characters")
			return
		}
		if trimmed != "" {
			c.userAgent = c.userAgent + " " + trimmed
		}
	}
}

// sdkUserAgent is the User-Agent without a caller suffix:
// dominaite-go/<version> (go <go version>). No operating system.
func sdkUserAgent() string {
	goVersion := strings.TrimPrefix(runtime.Version(), "go")
	// A development toolchain reports "devel go1.x-<hash> <date>"; keep one token.
	if fields := strings.Fields(goVersion); len(fields) > 0 {
		goVersion = fields[0]
	}
	return fmt.Sprintf("dominaite-go/%s (go %s)", Version, goVersion)
}

// hasControlChar reports whether s holds a byte below 0x20 or 0x7F, which
// would let a header value break out of its line.
func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// New builds a client from your API key id (dmk_...) and secret (dms_...), both
// from the dashboard's Website integration tab. It returns a *ValidationError
// when either credential has the wrong prefix, which catches a swapped key id
// and secret before anything is sent. It also returns one when WithBaseURL is
// missing, empty or whitespace, or is not https:// outside loopback.
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
		httpClient: &http.Client{Timeout: defaultTimeout},
		userAgent:  sdkUserAgent(),
		now:        time.Now,
		notices:    &noticeSet{},
		maxRetries: defaultMaxRetries,
		sleep:      sleepContext,
		random:     randomFloat,
	}
	for _, opt := range opts {
		opt(client)
	}
	if client.optionErr != nil {
		return nil, client.optionErr
	}

	// There is no default base URL. A fallback to production would turn an unset
	// environment variable into sandbox keys sent to production.
	if client.baseURL == "" {
		return nil, newValidationError("baseURL is required. Use " + SandboxBaseURL +
			" for the sandbox or " + ProductionBaseURL + " for production.")
	}
	if err := validateBaseURL(client.baseURL); err != nil {
		return nil, err
	}

	// Never follow a redirect. Go strips Authorization-class headers on a
	// cross-origin hop but keeps ours, so following one would hand X-Signature,
	// X-Api-Key-Id, X-Timestamp and Idempotency-Key to whatever host the
	// Location points at, and 301/302/303 would turn the POST into a GET. The
	// Dominaite API never redirects, so a 3xx is always an error - see request.
	client.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return client, nil
}

// validateBaseURL refuses a base URL that would put the signed request on the
// wire in clear text. Loopback is the one exception, for local mock servers and
// the SDK's own tests.
func validateBaseURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return newValidationError("baseURL is not a valid URL: " + err.Error())
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "https" {
		return nil
	}
	if scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}

	return newValidationError(
		"baseURL must use https:// - http:// is allowed only for localhost, 127.0.0.1 and ::1. " +
			"Anywhere else it would send your key id, signature and request body in clear text.",
	)
}

// isLoopbackHost reports whether a hostname is one of the three loopback names
// the SDK exempts. url.Hostname already strips the brackets from [::1].
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
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
//   - *RateLimitError: HTTP 429; back off, the SDK never retries it for you.
//   - *TransportError: network failure or 5xx.
func (c *Client) Ping(ctx context.Context) (*Ping, error) {
	return retry(ctx, c, c.retryPolicy("Ping"), func() (*Ping, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.request(ctx, http.MethodGet, PingPath, "", "")
		if err != nil {
			return nil, err
		}

		ping := &Ping{Raw: reply.payload}
		if err := json.Unmarshal(reply.payload, ping); err != nil {
			return nil, reply.apiError("The API returned an unexpected ping response")
		}
		ping.RequestID = reply.requestID
		if skew := ping.ClockSkewSeconds; skew > clockSkewWarnSeconds || skew < -clockSkewWarnSeconds {
			c.warn(ctx, fmt.Sprintf("dominaite-go: clock skew is %d s; requests fail at 300 s. Sync this server's clock.", skew))
		}

		return ping, nil
	})
}

// GetIntegrationContract reads the API's machine-readable contract: its
// routes, request and response fields, error codes and vocabularies, as the
// API serves them now. The route is public, so it needs no scope; the SDK
// signs the call anyway, like every call. Use it to check what the API
// supports, for example a field this SDK does not model yet. Version and the
// vocabularies are typed; everything else is on Raw.
//
// Errors it returns: as Ping.
func (c *Client) GetIntegrationContract(ctx context.Context) (*IntegrationContract, error) {
	return retry(ctx, c, c.retryPolicy("GetIntegrationContract"), func() (*IntegrationContract, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.request(ctx, http.MethodGet, IntegrationContractPath, "", "")
		if err != nil {
			return nil, err
		}

		contract := &IntegrationContract{Raw: reply.payload}
		if err := json.Unmarshal(reply.payload, contract); err != nil || contract.Version == "" {
			return nil, reply.apiError("The API returned an unexpected contract response")
		}
		contract.RequestID = reply.requestID

		return contract, nil
	})
}

// CreateCheckoutSession opens a hosted checkout session for one payment. With
// VerifyOnly it saves the payer's card without a payment; with AgreementID it
// takes the first payment of a subscription.
//
// Errors it returns:
//   - *ValidationError: bad arguments, including a missing IdempotencyKey;
//     nothing was sent.
//   - *AuthError: 401 for wrong credentials, a bad signature, a clock that is
//     off or an IP that is not allowlisted. 403 for a key without the
//     payments scope, or VerifyOnly on an account without verify-only
//     sessions. Fix the config, do not retry.
//   - *RefusalError: the gateway refused the session; inspect ErrorCode.
//   - *APIError: an unexpected or rejecting response; inspect HTTPStatus.
//   - *RateLimitError: HTTP 429; back off and reschedule with the same
//     idempotency key. Never retried for you.
//   - *TransportError: network failure or 5xx, after the client's retries
//     (WithMaxRetries) ran out. Safe to retry WITH the same idempotency key.
//
// The client retries a *TransportError and PAYMENT_PROCESSING_UNAVAILABLE
// with the same idempotency key and body; see WithMaxRetries. A retry after
// an attempt that DID land gets that session back while it is still open.
// When the earlier attempt already moved money, ended, or cannot be handed
// back yet, the API answers with a replay refusal (*RefusalError, ErrorCode
// ALREADY_PROCESSED, PRIOR_ATTEMPT_FAILED or DUPLICATE_REQUEST). Recover
// through RefusalError.TransactionID with GetStatus when the API named one.
func (c *Client) CreateCheckoutSession(ctx context.Context, params CreateCheckoutSessionParams) (*CheckoutSession, error) {
	idempotencyKey, body, err := prepareSessionRequest(params)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("CreateCheckoutSession"), func() (*CheckoutSession, error) {
		return c.sendCheckoutSession(ctx, idempotencyKey, body)
	})
}

// sendCheckoutSession sends one create-session attempt. A refusal arrives as
// HTTP 200 and becomes a *RefusalError.
func (c *Client) sendCheckoutSession(ctx context.Context, idempotencyKey, body string) (*CheckoutSession, error) {
	reply, err := c.request(ctx, http.MethodPost, SessionsPath, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	payload := reply.payload

	var envelope checkoutSessionEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, reply.apiError("The API returned an unexpected create-session response")
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
		refusal.RequestID = reply.requestID
		return nil, refusal
	}

	session := &CheckoutSession{Raw: envelope.Checkout}
	if err := json.Unmarshal(envelope.Checkout, session); err != nil {
		return nil, reply.apiError("The API returned an unexpected checkout object")
	}
	session.RequestID = reply.requestID

	return session, nil
}

// RetryOptions tunes CreateCheckoutSessionWithRetry.
//
// Deprecated: set the retry budget on the client with WithMaxRetries;
// CreateCheckoutSession retries on its own.
type RetryOptions struct {
	// Attempts is the total number of attempts including the first. Zero means
	// the client's policy: WithMaxRetries plus the first attempt, 3 by default.
	Attempts int
	// BaseDelay bounds the wait before the first retry, which is a random time
	// between 0 and BaseDelay; the bound doubles each retry, up to 8 seconds.
	// Zero means the default of 500ms.
	BaseDelay time.Duration
}

func (c *Client) sessionRetryPolicy(o RetryOptions) (retryPolicy, error) {
	policy := c.retryPolicy("CreateCheckoutSessionWithRetry")
	if o.Attempts < 0 {
		return policy, newValidationError("Attempts must be a positive integer")
	}
	if o.Attempts > 0 {
		policy.maxRetries = o.Attempts - 1
	}
	if o.BaseDelay < 0 {
		return policy, newValidationError("BaseDelay must not be negative")
	}
	if o.BaseDelay > 0 {
		policy.baseDelay = o.BaseDelay
	}
	return policy, nil
}

// CreateCheckoutSessionWithRetry is CreateCheckoutSession with its own retry
// budget. It retries the same failures, with THE SAME idempotency key across
// every attempt.
//
// Deprecated: CreateCheckoutSession retries on its own; set the budget with
// WithMaxRetries. This method stays for existing callers.
func (c *Client) CreateCheckoutSessionWithRetry(ctx context.Context, params CreateCheckoutSessionParams, opts RetryOptions) (*CheckoutSession, error) {
	policy, err := c.sessionRetryPolicy(opts)
	if err != nil {
		return nil, err
	}
	idempotencyKey, body, err := prepareSessionRequest(params)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, policy, func() (*CheckoutSession, error) {
		return c.sendCheckoutSession(ctx, idempotencyKey, body)
	})
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
// StoredPaymentMethod is the card kept on file by a session created with
// SaveCard: present once the payment is approved (and it stays after a revoke,
// with Status revoked), nil until then, for sessions without SaveCard and for
// declined or abandoned ones. Its ID is what ChargePaymentMethod and
// RevokePaymentMethod take.
//
// Poll after the payer returns to you, or on your order timeout. Not in a tight
// loop: the endpoint is rate limited per key (60/min per key, 120/min per IP),
// and going over returns a *RateLimitError.
func (c *Client) GetStatus(ctx context.Context, transactionID string) (*CheckoutStatus, error) {
	normalized, err := normalizeTransactionID(transactionID)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("GetStatus"), func() (*CheckoutStatus, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.request(ctx, http.MethodGet, SessionsPath+"/"+normalized, "", "")
		if err != nil {
			return nil, err
		}

		status := &CheckoutStatus{Raw: reply.payload}
		if err := json.Unmarshal(reply.payload, status); err != nil {
			return nil, reply.apiError("The API returned an unexpected status response")
		}
		status.RequestID = reply.requestID

		return status, nil
	})
}

// ChargePaymentMethod charges a card kept on file, off-session: no widget, no
// payer present.
//
// paymentMethodID is the ID from GetStatus().StoredPaymentMethod of a session
// you created with SaveCard. The charge is signed like a session and carries
// an Idempotency-Key, which you must set (derive it from the billing period,
// never per attempt), so retrying after a timeout WITH THE SAME KEY never
// charges the card twice: the gateway replays its first answer, HTTP status
// included.
//
// The HTTP status is the contract on this route. 201 (200 on a replay) returns
// the charge, Status ChargeStatusSucceeded, ChargeStatusPending or
// ChargeStatusCancelled. 402 returns the charge too: a decline is not an
// error. The charge has Status ChargeStatusFailed, and DeclineClass, when it
// is set, tells you whether to give up on the card (hard), wait (soft_funds,
// soft_other) or bring the customer back for a hosted session
// (soft_sca_required).
// ChargeStatusPending is not terminal - poll GetStatus(charge.TransactionID).
//
// params.AgreementID charges under an agreement, on its terms.
//
// Errors it returns:
//   - *ValidationError: bad arguments; nothing was sent.
//   - *ChargeError: 404, 409, 422, 502 or 503 with a code; branch on
//     ErrorCode. 404 PAYMENT_METHOD_NOT_FOUND is an id that is not yours.
//     CHARGE_OUTCOME_UNKNOWN carries the charge row (Charge, TransactionID):
//     poll GetStatus with it, never retry under a new key.
//   - *AuthError: wrong credentials, bad signature, clock off, IP not allowlisted.
//   - *APIError: 400 validation, or an unexpected response; inspect
//     HTTPStatus.
//   - *RateLimitError: HTTP 429; back off and reschedule with the same key.
//   - *TransportError: network failure or a 5xx without a charge code
//     (ErrorCode keeps the code it had). Safe to retry WITH the same key.
func (c *Client) ChargePaymentMethod(ctx context.Context, paymentMethodID string, params ChargePaymentMethodParams) (*PaymentMethodCharge, error) {
	id, err := normalizePaymentMethodID(paymentMethodID)
	if err != nil {
		return nil, err
	}
	idempotencyKey, body, err := prepareChargeRequest(params)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("ChargePaymentMethod"), func() (*PaymentMethodCharge, error) {
		reply, err := c.send(ctx, http.MethodPost, PaymentMethodsPath+"/"+id+"/charges", body, idempotencyKey)
		if err != nil {
			return nil, err
		}

		charge := reply.charge()
		code := reply.err.code()

		// 201 (200 on a durable replay): the charge was placed, whatever its
		// status. 402: the provider declined; the envelope says success=false but
		// the charge is right there with Status failed, so it is a result, not an
		// error.
		if charge != nil && (reply.success() || reply.status == http.StatusPaymentRequired) {
			return charge, nil
		}

		if code != "" && isRouteErrorStatus(reply.status, code, chargeServerErrorCodes) {
			chargeErr := newChargeError(reply.status, code, firstNonEmpty(reply.err.message(), "The charge was refused."), charge, reply.envelope)
			chargeErr.RequestID = reply.requestID
			return nil, chargeErr
		}
		if reply.status >= 400 {
			return nil, reply.rejection()
		}
		return nil, reply.apiError("The API answered the charge without a charge body")
	})
}

// RevokePaymentMethod revokes a card kept on file. The saved credential is
// deleted at the payment provider and the method's status becomes
// StoredPaymentMethodStatusRevoked; a later ChargePaymentMethod on it is
// refused with PAYMENT_METHOD_NOT_ACTIVE. Returns nil on success (HTTP 204),
// and again for an already revoked method, so retrying a timed-out revoke is
// safe. Not a payment operation: no idempotency key is signed.
//
// Errors it returns:
//   - *RevokeError: the gateway refused and nothing changed;
//     MERCHANT_API_UNAVAILABLE (503, retry later) or UPSTREAM_CONTRACT_ERROR
//     (502, the provider refused for good - contact support with the id).
//   - *APIError: 404 for an id that is not yours, or an unexpected response.
//   - *AuthError, *RateLimitError, *TransportError: as everywhere else.
func (c *Client) RevokePaymentMethod(ctx context.Context, paymentMethodID string) error {
	id, err := normalizePaymentMethodID(paymentMethodID)
	if err != nil {
		return err
	}

	// DELETE signs an EMPTY idempotency key and an EMPTY body, like GET, and
	// sends no Idempotency-Key header.
	reply, err := c.send(ctx, http.MethodDelete, PaymentMethodsPath+"/"+id, "", "")
	if err != nil {
		return err
	}
	if reply.status < 400 {
		return nil
	}

	code := reply.err.code()
	if code != "" && reply.status != http.StatusNotFound && isRouteErrorStatus(reply.status, code, RevokeErrorCodes) {
		revokeErr := newRevokeError(reply.status, code, firstNonEmpty(reply.err.message(), "The revoke was refused."), reply.envelope)
		revokeErr.RequestID = reply.requestID
		return revokeErr
	}
	return reply.rejection()
}

// CreateRefund refunds a payment, in full or in part. transactionID is the id
// CreateCheckoutSession or ChargePaymentMethod returned; only card-not-present
// payments of your own account can be refunded.
//
// params.IdempotencyKey is required and signed, exactly as on a charge. Derive
// it from your refund (the return or credit-note id): replaying the same key
// answers the same refund and never refunds twice, so on a timeout retry with
// the same key. The same key with a different amount, reason or payment is
// refused with IDEMPOTENCY_KEY_REUSED.
//
// Leave params.Amount nil to refund everything still refundable. Partial
// refunds add up: the amount may not exceed what is left after earlier refunds
// and refunds still in progress.
//
// The gateway answers HTTP 202: the refund is queued, not done. Read it back
// with GetRefund, or wait for the payment.refunded webhook, which fires once the
// money has moved. A failed refund sends no webhook, so poll GetRefund if you
// need to know about failures. A replay of the same key returns the refund as
// it stands now, so it doubles as a status read.
//
// Errors it returns:
//   - *ValidationError: bad arguments, including a missing IdempotencyKey;
//     nothing was sent.
//   - *RefundError: 400, 404, 409 or 422 with a RefundError* code; branch on
//     ErrorCode, retry with the same key only when Retryable.
//   - *AuthError: wrong credentials, bad signature, clock off, IP not allowlisted.
//   - *APIError: an unexpected response; inspect HTTPStatus.
//   - *RateLimitError: HTTP 429; back off and reschedule with the same key.
//   - *TransportError: network failure or 5xx. Nothing was queued on a 5xx;
//     retry WITH the same key.
func (c *Client) CreateRefund(ctx context.Context, transactionID string, params CreateRefundParams) (*Refund, error) {
	id, err := normalizeTransactionID(transactionID)
	if err != nil {
		return nil, err
	}
	idempotencyKey, body, err := prepareRefundRequest(params)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("CreateRefund"), func() (*Refund, error) {
		reply, err := c.send(ctx, http.MethodPost, PaymentsPath+"/"+id+"/refunds", body, idempotencyKey)
		if err != nil {
			return nil, err
		}
		return reply.refund()
	})
}

// GetRefund reads one refund of a payment: its Status, the Amount refunded
// once it succeeded, and FailureCode once it failed. Stop polling when
// IsRefundTerminal(refund.Status) is true.
//
// Right after CreateRefund the refund may not be picked up yet: GetRefund then
// returns a *RefundError with ErrorCode REFUND_NOT_FOUND and Retryable set.
// Poll again for up to 60 seconds; after that the id is unknown.
//
// Errors it returns: as CreateRefund, without the idempotency key rules. The
// read is signed with an empty idempotency key and an empty body.
func (c *Client) GetRefund(ctx context.Context, transactionID, refundID string) (*Refund, error) {
	id, err := normalizeTransactionID(transactionID)
	if err != nil {
		return nil, err
	}
	normalizedRefundID := strings.TrimSpace(refundID)
	if !refundIDPattern.MatchString(normalizedRefundID) {
		return nil, newValidationError("refundId must be the RefundID returned by CreateRefund")
	}

	return retry(ctx, c, c.retryPolicy("GetRefund"), func() (*Refund, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.send(ctx, http.MethodGet, PaymentsPath+"/"+id+"/refunds/"+normalizedRefundID, "", "")
		if err != nil {
			return nil, err
		}
		return reply.refund()
	})
}

// ListRefunds lists the refunds of one payment that were created through this
// API: pending first, then processing, then succeeded and failed, newest
// first within each group. Each refund has the fields GetRefund returns. A
// refund made in the dashboard or at the provider is not in the list, but
// RefundedAmount counts it. A new refund shows up once it is picked up, the
// same moment GetRefund stops answering REFUND_NOT_FOUND.
//
// RefundableAmount is what a new refund could take right now, so read it
// before a partial refund to size it.
//
// Errors it returns: as GetRefund. An unknown payment is a *RefundError with
// ErrorCode RefundErrorPaymentNotFound.
func (c *Client) ListRefunds(ctx context.Context, transactionID string) (*RefundList, error) {
	id, err := normalizeTransactionID(transactionID)
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("ListRefunds"), func() (*RefundList, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.send(ctx, http.MethodGet, PaymentsPath+"/"+id+"/refunds", "", "")
		if err != nil {
			return nil, err
		}
		if reply.status >= 400 {
			return nil, reply.refundRejection()
		}
		if !reply.parsed || !isJSONObject(reply.data) {
			return nil, reply.apiError("The API answered the refund list without a refund list body")
		}

		var wire struct {
			TransactionID    string          `json:"transactionId"`
			Currency         string          `json:"currency"`
			RefundedAmount   int64           `json:"refundedAmount"`
			RefundableAmount int64           `json:"refundableAmount"`
			Refunds          json.RawMessage `json:"refunds"`
		}
		if json.Unmarshal(reply.data, &wire) != nil || wire.TransactionID == "" {
			return nil, reply.apiError("The API returned an unexpected refund list")
		}
		refunds, err := decodeItems(wire.Refunds, func(refund *Refund, raw json.RawMessage) {
			refund.Raw = raw
			refund.RequestID = reply.requestID
		})
		if err != nil {
			return nil, reply.apiError("The API returned an unexpected refund in the refund list")
		}

		return &RefundList{
			TransactionID:    wire.TransactionID,
			Currency:         wire.Currency,
			RefundedAmount:   wire.RefundedAmount,
			RefundableAmount: wire.RefundableAmount,
			Refunds:          refunds,
			RequestID:        reply.requestID,
			Raw:              reply.data,
		}, nil
	})
}

// decodeItems decodes a JSON array one element at a time, so every item keeps
// its own bytes on its Raw field. null and an absent array decode as empty.
func decodeItems[T any](raw json.RawMessage, keepRaw func(*T, json.RawMessage)) ([]T, error) {
	var elements []json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &elements); err != nil {
			return nil, err
		}
	}
	items := make([]T, len(elements))
	for i, element := range elements {
		if err := json.Unmarshal(element, &items[i]); err != nil {
			return nil, err
		}
		keepRaw(&items[i], element)
	}
	return items, nil
}

// prepareSessionRequest validates the params and returns the idempotency key and
// the exact body bytes that will be both signed and sent.
func prepareSessionRequest(params CreateCheckoutSessionParams) (string, string, error) {
	// A verify-only session has no amount of its own: the platform sets the
	// verification amount, so 0 is the only amount it takes.
	if params.verifyOnly() {
		if params.Amount < 0 {
			return "", "", newValidationError("amount must be 0 with VerifyOnly: the platform sets the verification amount")
		}
		if err := validateOrderParams(params.Currency, params.OrderReference); err != nil {
			return "", "", err
		}
	} else if err := validateMoneyParams(params.Amount, params.Currency, params.OrderReference); err != nil {
		return "", "", err
	}

	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return "", "", err
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

// prepareChargeRequest is prepareSessionRequest for a charge: same money
// checks, same key handling, and the body is exactly the contract's fields in
// declaration order, which is what gets signed.
func prepareChargeRequest(params ChargePaymentMethodParams) (string, string, error) {
	if err := validateMoneyParams(params.Amount, params.Currency, params.OrderReference); err != nil {
		return "", "", err
	}

	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return "", "", err
	}

	body, err := marshalNoEscape(params)
	if err != nil {
		return "", "", newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	return idempotencyKey, string(body), nil
}

// prepareRefundRequest validates the params and returns the idempotency key
// and the exact body bytes that will be both signed and sent. A nil Amount is
// left out of the body entirely: that is what asks for a full refund.
func prepareRefundRequest(params CreateRefundParams) (string, string, error) {
	if params.Amount != nil && *params.Amount <= 0 {
		return "", "", newValidationError("amount must be a positive integer in MINOR units, or nil to refund everything still refundable")
	}

	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return "", "", err
	}

	body, err := marshalNoEscape(params)
	if err != nil {
		return "", "", newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	return idempotencyKey, string(body), nil
}

// validateMoneyParams runs the checks shared by every request that moves
// money: amount, currency, orderReference.
func validateMoneyParams(amount int64, currency, orderReference string) error {
	if amount <= 0 {
		return newValidationError("amount must be a positive integer in MINOR units (e.g. 2500 for 25.00 EUR)")
	}
	return validateOrderParams(currency, orderReference)
}

// validateOrderParams checks currency and orderReference, which every session
// and charge needs, a verify-only session included.
func validateOrderParams(currency, orderReference string) error {
	if strings.TrimSpace(currency) == "" {
		return newValidationError("Missing required parameter: currency")
	}
	if strings.TrimSpace(orderReference) == "" {
		return newValidationError("Missing required parameter: orderReference")
	}
	// Characters, not bytes. len() would count UTF-8 bytes and reject a
	// perfectly valid 60-character Cyrillic or CJK order reference at the
	// client, before the API ever got a say.
	//
	// The server counts UTF-16 code units, so an astral character (emoji, rare
	// CJK) is one here and two there. The gap only shows up within a few
	// characters of the limit, and the API stays the final arbiter: when it
	// disagrees it answers with a 400 the caller already handles.
	if utf8.RuneCountInString(orderReference) > 100 {
		return newValidationError("orderReference must be at most 100 characters")
	}
	return nil
}

// normalizeIdempotencyKey enforces the key rules: 1 to 100 characters, all
// visible ASCII. There is no fallback. A key the SDK made up would differ on
// every call, so a reload or a retry would open a second payment for the same
// order instead of replaying the first.
func normalizeIdempotencyKey(idempotencyKey string) (string, error) {
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return "", err
	}
	return idempotencyKey, nil
}

// validateIdempotencyKey is the one key rule, shared by every call and by
// OrderIdempotencyKey. Visible ASCII (0x21 to 0x7E) only: the key is an HTTP
// header value and part of the signed payload, so a space, a control
// character or a non-ASCII letter is either refused on the wire or signed as
// bytes that another stack encodes differently. With ASCII only, bytes and
// characters are the same count, which also settles the gateway counting
// UTF-16 units where Go counts runes.
func validateIdempotencyKey(idempotencyKey string) error {
	if strings.TrimSpace(idempotencyKey) == "" {
		return newValidationError("Missing required parameter: idempotencyKey. Derive it from the order with OrderIdempotencyKey, never per call.")
	}
	if len(idempotencyKey) > maxIdempotencyKeyLength {
		return newValidationError("idempotencyKey must be a non-empty string of at most 100 characters")
	}
	for i := 0; i < len(idempotencyKey); i++ {
		if c := idempotencyKey[i]; c < 0x21 || c > 0x7e {
			return newValidationError("idempotencyKey must be visible ASCII only (no spaces, control characters or non-ASCII letters)")
		}
	}
	return nil
}

// normalizePaymentMethodID lowercases a payment method id and refuses
// anything that would not survive as one path segment of the signed canonical
// path. The gateway signs the lowercase pm_ id, so an id typed in upper case
// would otherwise fail with INVALID_SIGNATURE.
func normalizePaymentMethodID(paymentMethodID string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(paymentMethodID))
	if !paymentMethodIDPattern.MatchString(normalized) {
		return "", newValidationError("paymentMethodId must be the id from GetStatus().StoredPaymentMethod")
	}
	return normalized, nil
}

// normalizeTransactionID lowercases a transaction id and refuses anything that
// is not a UUID. The gateway signs the lowercase hyphenated form.
func normalizeTransactionID(transactionID string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(transactionID))
	if !uuidPattern.MatchString(normalized) {
		return "", newValidationError("transactionId must be the UUID returned by CreateCheckoutSession")
	}
	return normalized, nil
}

// request signs and sends one call, and applies the generic failure rules:
// 5xx is a *TransportError, 4xx an *APIError. body and idempotencyKey are both
// empty for GET and DELETE. The reply it returns is a 2xx with a JSON payload.
func (c *Client) request(ctx context.Context, method, path, body, idempotencyKey string) (*response, error) {
	reply, err := c.send(ctx, method, path, body, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if reply.status >= 400 {
		return nil, reply.rejection()
	}

	// A 2xx has to be real JSON: the callers unmarshal it into a session, a
	// status or a ping, and there is nothing to retry.
	if !reply.parsed {
		return nil, reply.apiError("The API returned a non-JSON response")
	}

	return reply, nil
}

// response is one parsed reply: the status, the whole envelope, the data
// object when the envelope carried one, the unwrapped payload (data when
// present, the envelope otherwise) and the envelope's error object. parsed is
// false when the body was not a JSON object.
type response struct {
	status    int
	envelope  json.RawMessage
	data      json.RawMessage
	payload   json.RawMessage
	err       *envelopeError
	parsed    bool
	requestID string
}

// apiError is an *APIError for this reply, with its status and request id.
func (r *response) apiError(message string) *APIError {
	apiErr := newAPIError(r.status, message)
	apiErr.RequestID = r.requestID
	return apiErr
}

// success reads the envelope's success flag; false when absent or unparsed.
func (r *response) success() bool {
	var probe struct {
		Success *bool `json:"success"`
	}
	if !r.parsed || json.Unmarshal(r.envelope, &probe) != nil {
		return false
	}
	return probe.Success != nil && *probe.Success
}

// charge reads the charge row under data, when the envelope carries one.
func (r *response) charge() *PaymentMethodCharge {
	if !isJSONObject(r.data) {
		return nil
	}
	charge := &PaymentMethodCharge{Raw: r.data}
	if json.Unmarshal(r.data, charge) != nil || charge.ChargeID == "" {
		return nil
	}
	charge.RequestID = r.requestID
	return charge
}

// refund reads a refund route's reply: the refund on a 2xx, a *RefundError for
// a coded 4xx, and the generic errors for the rest (5xx is a *TransportError).
func (r *response) refund() (*Refund, error) {
	if r.status >= 400 {
		return nil, r.refundRejection()
	}
	if !r.parsed || !isJSONObject(r.data) {
		return nil, r.apiError("The API answered the refund without a refund body")
	}
	refund := &Refund{Raw: r.data}
	if json.Unmarshal(r.data, refund) != nil || refund.RefundID == "" {
		return nil, r.apiError("The API returned an unexpected refund object")
	}
	refund.RequestID = r.requestID
	return refund, nil
}

// refundRejection reads a failed refund route's reply: a *RefundError for a
// coded 4xx, the generic errors for the rest (5xx is a *TransportError).
func (r *response) refundRejection() error {
	code := r.err.code()
	if code != "" && r.status < 500 {
		refundErr := newRefundError(r.status, code, firstNonEmpty(r.err.message(), "The refund was refused."), r.envelope)
		refundErr.RequestID = r.requestID
		return refundErr
	}
	return r.rejection()
}

// rejection is the generic reading of a failed reply: 5xx is the API being
// unavailable, 4xx a rejection carrying the machine-readable code. A 5xx
// keeps its code too, on the *TransportError.
func (r *response) rejection() error {
	var inner struct {
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	if r.parsed {
		_ = json.Unmarshal(r.payload, &inner)
	}
	if r.status >= 500 {
		code := firstNonEmpty(r.err.code(), inner.ErrorCode)
		label := strconv.Itoa(r.status)
		if code != "" {
			label += " " + code
		}
		transportErr := newTransportError(
			fmt.Sprintf("The Dominaite API is unavailable (HTTP %s); retry with the same idempotency key.", label),
			nil,
		)
		transportErr.HTTPStatus = r.status
		transportErr.ErrorCode = code
		transportErr.RequestID = r.requestID
		if r.parsed {
			transportErr.Raw = r.envelope
		}
		return transportErr
	}
	apiErr := r.apiError(firstNonEmpty(inner.ErrorMessage, r.err.message(), "Request rejected"))
	// Input rejections name a machine-readable code the same way refusals
	// do; carry it so callers branch on the code, not on the message text.
	apiErr.ErrorCode = firstNonEmpty(inner.ErrorCode, r.err.code())
	if r.parsed {
		apiErr.Raw = r.envelope
	}
	return apiErr
}

// isRouteErrorStatus decides whether a coded failure on a payment-method
// route is that route's typed error. Validation (400), authentication (401,
// 403) and rate limiting (429) keep their generic errors on every route. A
// 5xx is the route's error only with one of the route's own 5xx codes;
// anything else, the middleware's INTERNAL_ERROR included, stays the
// retryable *TransportError with its code. Every other coded 4xx, a 404
// included, is the route's error.
func isRouteErrorStatus(status int, code string, serverCodes []string) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	}
	if status >= 500 {
		return containsCode(serverCodes, code)
	}
	return status >= 400
}

func containsCode(codes []string, code string) bool {
	for _, candidate := range codes {
		if candidate == code {
			return true
		}
	}
	return false
}

// send signs, sends and parses one call. Transport failures, redirects, 401/403
// and 429 come back as errors here; every other status comes back as a reply
// for the route to read, because the payment-method routes answer 402, 409,
// 422, 502 and 503 with a body the caller needs.
func (c *Client) send(ctx context.Context, method, path, body, idempotencyKey string) (*response, error) {
	return c.sendQuery(ctx, method, path, "", body, idempotencyKey)
}

// sendQuery is send with an encoded query string. The query goes into the
// URL only: the signature covers the canonical path, never the query.
func (c *Client) sendQuery(ctx context.Context, method, path, query, body, idempotencyKey string) (*response, error) {
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
	target := c.baseURL + path
	if query != "" {
		target += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
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

	started := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.debugLine(ctx, method, path, "error", "", idempotencyKey, started)
		return nil, newTransportError("Could not reach the Dominaite API: "+err.Error(), err)
	}
	defer resp.Body.Close()
	// The header wins; the body's metadata.requestId fills in below when the
	// header is absent.
	requestID := cleanHeaderValue(resp.Header.Get(RequestIDHeader), maxRequestIDLength)
	// Runs after the body is read, so the time covers the whole response.
	defer func() {
		c.debugLine(ctx, method, path, strconv.Itoa(resp.StatusCode), requestID, idempotencyKey, started)
	}()
	// Every status carries it, errors included.
	c.readNotice(ctx, resp.Header)

	// The client does not follow redirects, so a 3xx arrives here as the final
	// response. The Dominaite API never emits one: it means a proxy, a captive
	// portal or a hijacked base URL is answering, and its body is not an
	// authentic API response. Fail loudly and do not retry.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		redirectErr := newAPIError(resp.StatusCode, fmt.Sprintf(
			"Unexpected redirect response (HTTP %d); the Dominaite API never redirects. Check your base URL and any proxy in front of it.",
			resp.StatusCode,
		))
		redirectErr.RequestID = requestID
		return nil, redirectErr
	}

	raw, err := readLimited(resp.Body)
	if err != nil {
		transportErr := newTransportError("Could not read the Dominaite API response: "+err.Error(), err)
		transportErr.HTTPStatus = resp.StatusCode
		transportErr.RequestID = requestID
		return nil, transportErr
	}

	// The gateway wraps responses as { success, data, ... }; unwrap when present.
	// Error responses carry the machine-readable code at error.code.
	//
	// Parsing is BEST EFFORT and the status is judged on its own below. An
	// overloaded edge answers 503 with an HTML error page or nothing at all,
	// and that has to stay a retryable transport failure - deciding "non-JSON,
	// therefore a permanent API error" would turn every load-balancer blip
	// into a lost payment the caller never retries.
	var envelope struct {
		Data     json.RawMessage `json:"data"`
		Error    *envelopeError  `json:"error"`
		Metadata json.RawMessage `json:"metadata"`
	}
	parsedJSON := isJSONObject(raw) && json.Unmarshal(raw, &envelope) == nil
	if requestID == "" && isJSONObject(envelope.Metadata) {
		// Read on its own, so a metadata object of an odd shape never costs
		// the parse of the rest.
		var metadata struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(envelope.Metadata, &metadata) == nil {
			requestID = cleanHeaderValue(metadata.RequestID, maxRequestIDLength)
		}
	}

	payload := json.RawMessage(raw)
	if isJSONObject(envelope.Data) {
		payload = envelope.Data
	}

	var inner struct {
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	if parsedJSON {
		_ = json.Unmarshal(payload, &inner)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		rateErr := newRateLimitError(fmt.Sprintf(
			"Rate limited by the Dominaite API (HTTP %d). Slow down and retry later with the same idempotency key.",
			resp.StatusCode,
		))
		rateErr.ErrorCode = firstNonEmpty(inner.ErrorCode, envelope.Error.code())
		rateErr.RequestID = requestID
		rateErr.RetryAfterSeconds, rateErr.HasRetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		if parsedJSON {
			rateErr.Raw = json.RawMessage(raw)
		}
		return nil, rateErr
	case resp.StatusCode == http.StatusUnauthorized:
		code := firstNonEmpty(inner.ErrorCode, envelope.Error.code(), "UNAUTHORIZED")
		authErr := newAuthError(resp.StatusCode, code, unauthorizedMessage(firstNonEmpty(inner.ErrorMessage, envelope.Error.message())))
		authErr.RequestID = requestID
		return nil, authErr
	case resp.StatusCode == http.StatusForbidden:
		code := firstNonEmpty(inner.ErrorCode, envelope.Error.code(), "UNAUTHORIZED")
		authErr := newAuthError(resp.StatusCode, code, forbiddenMessage(code, firstNonEmpty(inner.ErrorMessage, envelope.Error.message())))
		authErr.RequestID = requestID
		return nil, authErr
	}

	return &response{
		status:    resp.StatusCode,
		envelope:  json.RawMessage(raw),
		data:      envelope.Data,
		payload:   payload,
		err:       envelope.Error,
		parsed:    parsedJSON,
		requestID: requestID,
	}, nil
}

// unauthorizedMessage is the message of a 401: the API's own text, which
// names what failed, then the fixed hint.
func unauthorizedMessage(apiMessage string) string {
	const hint = "Authentication failed - check your key id, secret, and server clock."
	apiMessage = strings.TrimSpace(apiMessage)
	if apiMessage == "" {
		return hint
	}
	return strings.TrimRight(apiMessage, ".") + ". " + hint
}

// forbiddenMessage is the message of a 403. The clock hint is for 401 only: a
// 403 means the request was signed correctly, so the key id, the secret and
// the clock are fine, and the key may not do this.
func forbiddenMessage(code, apiMessage string) string {
	switch code {
	case ErrorCodeInsufficientScope:
		// The API's own message names the scope the route needs.
		scope := strings.TrimRight(firstNonEmpty(apiMessage, "This API key does not have the scope this route requires"), ".")
		return scope + ". A key's scopes are fixed when it is issued: use a key that holds this scope."
	case ErrorCodeVerifyOnlyNotEnabled:
		return "Verify-only sessions are not enabled for this account. We switch it on for your account: ask Dominaite support."
	}
	return fmt.Sprintf("Access denied (HTTP 403 %s): this API key may not call this route.", code)
}

// readLimited reads a response body up to maxResponseBytes. A body that runs
// past the cap is reported as a transport failure rather than truncated and
// parsed: a truncated payload is not the response the API sent, and guessing at
// half of one is worse than retrying.
func readLimited(body io.Reader) ([]byte, error) {
	// One byte past the cap, so a body that exactly fills it is still accepted
	// and only a genuinely oversized one trips.
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeds the %d byte limit", maxResponseBytes)
	}
	return raw, nil
}

// parseRetryAfter reads the Retry-After header. Only the integer-seconds form
// is reported; the HTTP-date form and anything unparseable come back absent, so
// a caller can tell "wait 30s" from "the API did not say".
func parseRetryAfter(header string) (int, bool) {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return 0, false
	}
	return seconds, true
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

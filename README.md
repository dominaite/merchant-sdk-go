# dominaite-go

Server-side Go client for the Dominaite merchant API. One call from your backend opens a
hosted checkout session; a two-line script tag renders the payment widget on your page; a
signed webhook tells you when the money actually arrived. Card details go straight from your
customer's browser into the payment widget - they never touch your server, which keeps your
PCI scope minimal (SAQ A).

Go 1.21 or newer. Zero dependencies: `crypto/hmac`, `crypto/sha256`, `net/http`,
`encoding/json`, all standard library.

## Install

```bash
go get github.com/dominaite/merchant-sdk-go
```

```go
import dominaite "github.com/dominaite/merchant-sdk-go"
```

To work on the SDK itself:

```bash
go vet ./...
go test ./...      # includes the offline signing and webhook vectors
```

## Credentials

You get two values from the Dominaite dashboard, **Website integration** tab, when you generate
an API key (shown once - store them like passwords):

- `dmk_...` - your API key id. Identifies you; not secret by itself.
- `dms_...` - your API secret. Server-side only: environment variable or a config file outside
  the web root. Never in a browser, never in git, never in logs.

Every request is signed with the secret (HMAC-SHA256) and timestamped. Keep your server clock
on NTP - signatures older than 5 minutes are rejected with `TIMESTAMP_OUT_OF_RANGE`.

If the key has an IP allowlist, calls from anywhere else fail with `IP_NOT_ALLOWED`. The
allowlist is managed on the same dashboard tab.

A webhook endpoint has a **third** secret, separate from the two above: `whsec_...`, shown once
when you create the endpoint on the **Webhooks** tab. It signs deliveries to you rather than
requests from you, so it is never sent anywhere - see [Webhooks](#webhooks).

## Quickstart (zero to a paid order)

Everything below is copy-paste. It assumes an empty directory and nothing installed.

A complete integration is three moving parts, and you want all three:

1. **Create a session** from your backend, and render the widget with what it returns.
2. **Receive a webhook** when the payment resolves. This is how you learn you were paid.
3. **Reconcile on a schedule**, because no webhook system delivers everything forever.

Steps 1 and 2 are below. Step 3 is not optional; see
[Reconciliation is still mandatory](#reconciliation-is-still-mandatory).

```bash
mkdir my-checkout && cd my-checkout
go mod init example.com/my-checkout
go get github.com/dominaite/merchant-sdk-go
```

Set your credentials and the environment you are pointing at:

```bash
export DOMINAITE_KEY_ID=dmk_...      # Website integration tab
export DOMINAITE_SECRET=dms_...      # shown once when you generated the key
export DOMINAITE_WEBHOOK_SECRET=whsec_...  # shown once when you created the endpoint
# Dev: the payments function app, whose Azure Functions route prefix is /api.
# Confirm the host for your environment before the first call.
export DOMINAITE_BASE_URL=https://func-dom-gw-payments-dev-gwc-01.azurewebsites.net/api
# Production needs no DOMINAITE_BASE_URL - the SDK defaults to
# https://api.dominaite.com/payments
```

Ping before your first mint. It is one signed GET that creates nothing, so anything that
fails here is your credentials, your signing or your clock:

```go
client, err := dominaite.New(
	os.Getenv("DOMINAITE_KEY_ID"),
	os.Getenv("DOMINAITE_SECRET"),
	dominaite.WithBaseURL(os.Getenv("DOMINAITE_BASE_URL")), // no-op in production
)
if err != nil {
	log.Fatal(err)
}

ping, err := client.Ping(context.Background())
if err != nil {
	log.Fatal(err)
}
log.Printf("merchant %s, clock skew %ds", ping.MerchantID, ping.ClockSkewSeconds)
```

Watch `ClockSkewSeconds`: the gateway rejects requests once it passes 300, so a number that
keeps growing is your cue to fix NTP before payments start failing.

### Step 1: create a session

`main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	dominaite "github.com/dominaite/merchant-sdk-go"
)

func main() {
	client, err := dominaite.New(
		os.Getenv("DOMINAITE_KEY_ID"),
		os.Getenv("DOMINAITE_SECRET"),
		dominaite.WithBaseURL(os.Getenv("DOMINAITE_BASE_URL")), // ignored when empty
	)
	if err != nil {
		panic(err)
	}

	session, err := client.CreateCheckoutSession(context.Background(), dominaite.CreateCheckoutSessionParams{
		Amount:         2500,          // minor units: 2500 = 25.00 EUR
		Currency:       "EUR",
		OrderReference: "order-1042",  // your own order id, shows up in your dashboard
		Customer: &dominaite.Customer{
			// Pass everything you already know - prefilled fields are hidden from the
			// payer, so the checkout form stays short.
			FirstName: "Ana",
			LastName:  "Kirova",
			Email:     "ana@example.com",
		},
		Language: "bg",   // widget UI language
		Theme:    "dark",
	})
	if err != nil {
		var refusal *dominaite.RefusalError
		var transport *dominaite.TransportError
		switch {
		case errors.As(err, &refusal):
			// Machine-readable: refusal.ErrorCode - codes listed below.
			fmt.Println("Payment unavailable:", refusal.ErrorCode)
		case errors.As(err, &transport):
			// Network blip - safe to retry with the same idempotency key.
			fmt.Println("Payment temporarily unavailable")
		default:
			panic(err)
		}
		return
	}

	// Store session.TransactionID against your order, then hand CashierKey +
	// CashierToken to the page that renders the widget.
	fmt.Printf("%+v\n", session)
}
```

```bash
go run .
```

A successful run prints `TransactionID`, `OrderID`, `CashierKey`, `CashierToken`, `Amount`,
`Currency`, `ExpiresAt`. Render the widget with the two cashier values:

```html
<div id="checkout"></div>
<script src="https://bp-checkout.dominaite.com/v2/launcher"
        data-cashier-key="CASHIER_KEY_FROM_SESSION"
        data-cashier-token="CASHIER_TOKEN_FROM_SESSION"></script>
```

`CashierKey` and `CashierToken` are per-payment session values, not credentials - but
HTML-escape them when you template them into the page (`html/template` does it for you).

There is a runnable version of the above in `examples/create-session/main.go` in this repo - it
mints a session and reads the status back, using the same environment variables:

```bash
go run ./examples/create-session
```

### Step 2: receive the webhook

The payer finishes on the widget, not on your site, so the session call cannot tell you the
order was paid. A webhook does. Register an endpoint on the dashboard's **Webhooks** tab,
subscribe to `payment.succeeded`, and handle it:

```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	// The RAW bytes, before any decoding. The signature covers exactly these,
	// so decoding and re-encoding first will break verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := dominaite.VerifyWebhook(
		body,
		r.Header.Get(dominaite.WebhookSignatureHeader),
		os.Getenv("DOMINAITE_WEBHOOK_SECRET"),
	)
	if err != nil {
		// Log the reason for yourself; never tell the caller why it failed.
		log.Printf("rejected webhook: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Delivery is at-least-once, so the same event.ID will sometimes arrive
	// twice. Dedupe on it, in your database, before doing anything with money.
	if inserted := recordDelivery(event.ID); !inserted {
		w.WriteHeader(http.StatusOK)
		return
	}

	if event.Type == dominaite.EventPaymentSucceeded {
		// Queue the work. Do not fulfil the order on this goroutine.
		enqueueFulfilment(event.Data.TransactionID, event.Data.IdempotencyKey)
	}

	// Answer fast. A slow 2xx is counted as a failed delivery and retried.
	w.WriteHeader(http.StatusOK)
}
```

A runnable version, with every event type handled, is in
`examples/webhook-handler/main.go`:

```bash
export DOMINAITE_WEBHOOK_SECRET=whsec_...
go run ./examples/webhook-handler
```

That is the whole integration: the session call, the script tag, the webhook, and your domain
bound to your checkout by Dominaite during onboarding.

## Webhooks

`VerifyWebhook` authenticates a delivery and returns the parsed event. It verifies **before**
it parses, and a `WebhookEvent` cannot be obtained any other way, so an unverified payload
cannot reach your business logic by accident.

```go
event, err := dominaite.VerifyWebhook(payload, signatureHeader, secret, opts...)
```

| Argument | What |
|---|---|
| `payload` | The raw request body, byte for byte as received. Not a re-encoded struct. |
| `signatureHeader` | The `X-Webhook-Signature` value, also available as `dominaite.WebhookSignatureHeader`. |
| `secret` | The endpoint's `whsec_...` secret. |

Options: `WithWebhookTolerance(d)` changes the freshness window from its 300 second default
(`dominaite.DefaultWebhookTolerance`), and `WithWebhookClock(fn)` replaces the clock so tests
can verify a recorded delivery at a fixed instant.

### Getting the raw body right

This is the one thing that reliably goes wrong. The signature covers the exact bytes that were
sent, so anything that reserialises the body invalidates it. Read the body first, verify, then
parse. If a framework or middleware has already decoded the body for you, reach for its
raw-body escape hatch rather than re-marshalling the decoded value.

### The signature scheme

The header is `t={unix_seconds},v1={lowercase_hex}`. `v1` is HMAC-SHA256 over the ASCII
concatenation `"{t}.{raw_body}"`, keyed with the UTF-8 bytes of the `whsec_` secret. The SDK
compares in constant time and then checks that `|now - t|` is within tolerance.

The timestamp is checked **after** the MAC, deliberately: `t` is covered by the signature, so
until the MAC passes there is no reason to trust it. That ordering also means a stale but
genuine replay reports `TIMESTAMP_OUT_OF_TOLERANCE` while a forgery reports
`SIGNATURE_MISMATCH`, instead of the two failure modes blurring together.

If deliveries start failing with `TIMESTAMP_OUT_OF_TOLERANCE`, your server clock has drifted.
Fix NTP rather than widening the tolerance.

### Rejections

Every failure is a `*WebhookVerificationError` with a `Reason`. Nothing else comes out and
nothing panics, so a hostile header is an ordinary rejection path:

```go
var verr *dominaite.WebhookVerificationError
if errors.As(err, &verr) {
	log.Printf("rejected: %s", verr.Reason)
	w.WriteHeader(http.StatusBadRequest)
	return
}
```

| `Reason` | Meaning |
|---|---|
| `MALFORMED_SIGNATURE` | Header missing, empty, or not `t=...,v1=...`. Nothing could be checked. |
| `SIGNATURE_MISMATCH` | The MAC did not match. Wrong secret, or the payload is not what was signed. |
| `TIMESTAMP_OUT_OF_TOLERANCE` | MAC valid, timestamp too old or too far ahead. A replay, or clock drift. |
| `MALFORMED_PAYLOAD` | Signature good, body not valid JSON. Surprising - log it rather than dropping it. |

Answer a rejected delivery with `400` and no detail. Never echo the reason back: whoever sent
an unverified request does not get to learn whether the secret or the timestamp was the problem.

### The event

The envelope is flat. There is no `ApiResponse` wrapper and no `success` field, so do not
branch on one.

```go
event.ID                     // delivery id, and YOUR DEDUPE KEY
event.Type                   // one of the Event* constants
event.CreatedAt              // ISO 8601 UTC instant of the transition, not of delivery
event.Data.TransactionID
event.Data.Status            // wire status, one of the Status* constants
event.Data.PreviousStatus    // empty when the wire sent null
event.Data.Amount            // MINOR units: what you are PAID
event.Data.GrossAmount       // MINOR units: what the card was charged
event.Data.SurchargeAmount   // *int64, nil when no surcharge is known
event.Data.Currency
event.Data.OriginalTransactionID  // parent, on refunds and reversals
event.Data.IdempotencyKey    // your own mint key, when the gateway knows it
event.Raw                    // the verified bytes, for anything not modelled above
```

`Amount` and `GrossAmount` are not the same number when a surcharge applies: `Amount` is what
you are paid, `GrossAmount` is what moved on the card. Credit orders from `Amount`.

`SurchargeAmount` is a pointer so that "no surcharge information" stays distinct from "a
surcharge of zero".

`IdempotencyKey` is the cheapest way to match a delivery back to your own order without a
lookup. It is empty when the gateway does not know it, which today includes every refund.

### Event catalog

`payment.succeeded`, `payment.failed`, `payment.requires_capture`, `payment.cancelled`,
`payment.abandoned`, `payment.refunded`, `payment.disputed` (exported as the `Event*`
constants). The set is closed and case-sensitive; endpoint registration rejects anything else.

- `payment.succeeded` is the **only** signal that means money is in hand.
- `payment.refunded` fires once per refund, from the refund row - never from the parent
  payment's status flip.
- `payment.requires_capture` includes approved pre-auth holds. It is not an unpaid order.
- `payment.cancelled` is a pre-completion void only.
- `payment.abandoned` is the terminal sweep verdict on a checkout that was never paid.

`pending` and `processing` are deliberately not webhooked. Poll session status if you want to
drive in-flight UX off them.

Treat a `Type` you do not recognise as a no-op and still answer `2xx`. The catalog can grow,
and a 400 on an unknown type will trip the circuit breaker on your endpoint.

### Delivery semantics

Delivery is **at-least-once**. Dedupe on `event.ID`, persistently - an in-memory set loses on
restart, and retries can arrive hours apart.

Respond `2xx` quickly and queue the real work. A slow response counts as a failed delivery.

Failed deliveries are retried up to the endpoint's `RetryCount` (default 3, max 10, 0 disables)
at 1m, 5m, 30m, 2h, 12h. An endpoint whose initial attempt and every configured retry fail
consecutively is auto-disabled; a later successful delivery re-enables it. An endpoint you
disabled by hand in the dashboard is never re-enabled automatically.

A merchant can have at most 25 active endpoints.

### Reconciliation is still mandatory

Webhooks complement the reconciliation sweep, they do not replace it. There is no publish
outbox, and chains parked against a disabled endpoint are simply lost, so there are windows in
which a delivery never arrives at all. Keep a scheduled job that reads back the status of every
order you believe is still open and settles it from the API.

If you do only one of the two, do reconciliation. It is the one that cannot silently lose money.

### Rotating a secret

Regenerating an endpoint's secret replaces it: the old secret stops verifying immediately.
There is no overlap window, and `VerifyWebhook` rejects a header carrying more than one `v1`
rather than trying candidates in turn. Update the secret in your configuration as part of the
same change that regenerates it.

### Testing your handler

The suite in this repo pins the canonical cross-SDK vector, byte for byte, along with tamper,
wrong-secret, stale-timestamp and malformed-header cases. Every Dominaite SDK pins the same
vector, so the recipe cannot drift between languages. Run `go test ./...` before you point a
real endpoint at your handler.

To test your own handler against a fixed delivery, sign a body yourself and pin the clock:

```go
event, err := dominaite.VerifyWebhook(
	body,
	header,
	secret,
	dominaite.WithWebhookClock(func() time.Time { return time.Unix(1755700000, 0) }),
)
```

## Client options

`dominaite.New(keyID, secret, opts...)` takes functional options:

| Option | What |
|---|---|
| `WithBaseURL(url)` | Point at a non-production environment. Empty values are ignored, so an unset env var still gives you production. |
| `WithTimeout(d)` | Per-request timeout on the default HTTP client. Defaults to 45s (serverless cold starts can take 10+s). |
| `WithHTTPClient(c)` | Your own `*http.Client`: proxy-aware transport, custom TLS, a test double. Replaces `WithTimeout`. |
| `WithUserAgent(s)` | Appends your identifier to the SDK's User-Agent, which helps when support reads the access logs. |

Every call takes a `context.Context`. A context deadline shorter than the client timeout wins,
and cancelling the context returns a `*TransportError` wrapping `context.Canceled`.

## Amounts are minor units

`Amount` is always an integer in the currency's minor unit: `2500` is 25.00 EUR. The field is an
`int64`, so a float will not compile; non-positive values are rejected before anything reaches
the network. The amount is locked server-side - what you pass here is what gets charged; nothing
in the browser can change it.

## Retries and double-charges

Every `CreateCheckoutSession` call carries an idempotency key (auto-generated, or set your own
in `IdempotencyKey`). Retrying with the same key never opens a second payment - on a timeout,
retry with the same key rather than generating a new one.

`CreateCheckoutSessionWithRetry` does that for you: it pins one key up front and reuses it
across attempts, retrying only `*TransportError` (network failures and 5xx, including
`MERCHANT_API_UNAVAILABLE`). Refusals and authentication failures are not retried - they will
not change.

```go
session, err := client.CreateCheckoutSessionWithRetry(
	ctx,
	dominaite.CreateCheckoutSessionParams{Amount: 2500, Currency: "EUR", OrderReference: "order-1042"},
	dominaite.RetryOptions{Attempts: 3, BaseDelay: 500 * time.Millisecond}, // zero values use these defaults
)
```

The delay doubles each attempt, and a cancelled context stops the wait immediately.

## Sessions expire

A session is valid for 2 hours. If the payer comes back later, create a new session.

## Status polling (fallback, and the reconciliation sweep)

**Prefer webhooks for learning that a payment resolved.** Polling is the right tool for three
narrower jobs: the reconciliation sweep, in-flight UX on `pending` and `processing` (which are
never webhooked), and local development before you have a public URL to deliver to.

Reaching for polling as your primary signal means holding an order open until you happen to ask
about it. Reaching for it as your *only* signal means a busy loop against a rate-limited
endpoint. Use both: webhooks for latency, the sweep for completeness.

```go
status, err := client.GetStatus(ctx, session.TransactionID)
// status.Status == "succeeded", status.OrderReference == "order-1042",
// status.Amount == 2500, status.Currency == "EUR", ...
```

`Status` is one of `pending`, `processing`, `succeeded`, `failed`, `refunded`,
`partially_refunded`, `cancelled`, `disputed`, `requires_capture`, `abandoned` (exported as the
`Status*` constants). While the session is still payable the response also carries `ExpiresAt`;
after that instant a `pending` session can only become `abandoned`. An unknown transaction id
returns an `*APIError` with `HTTPStatus` 404.

`succeeded` is the only value that means the payment is complete. Keep polling on `pending`,
`processing` and `requires_capture` - none of them is terminal.

`requires_capture` is **not** "unpaid": the payer has already paid and the funds are held
awaiting capture. Never treat it as an abandoned order.

Treat any status you do not recognise as still-open as well: a value the API adds later should
make you keep polling, never silently close an order that is still live.

Poll after the payer returns to you, or on your order timeout - not in a tight loop; the
endpoint is rate limited per key.

Both response types also carry `Raw` (`json.RawMessage`) with the unparsed payload, for fields
the structs do not model yet.

## Errors

Every error the SDK returns satisfies the `dominaite.Error` interface and matches
`errors.Is(err, dominaite.ErrDominaite)`, so one catch-all works:

```go
var sdkErr dominaite.Error
if errors.As(err, &sdkErr) { ... }
```

For the specific kind, use `errors.As` with the concrete pointer type:

| Error | When | What to do |
|---|---|---|
| `*RefusalError` | The API answered with `success: false`. `ErrorCode` carries the reason. | Branch on `ErrorCode`. Do not blind-retry. |
| `*AuthError` | 401/403. `ErrorCode` is `INVALID_API_KEY`, `INVALID_SIGNATURE`, `TIMESTAMP_OUT_OF_RANGE`, or `IP_NOT_ALLOWED`. | Fix the key id, secret, server clock, or allowlist. Never retry-loop. |
| `*TransportError` | Network failure, timeout, or 5xx (`MERCHANT_API_UNAVAILABLE`). Wraps the cause, reachable with `errors.Unwrap`. | Retry with the **same** idempotency key. |
| `*APIError` | Any other rejecting or unexpected response; `HTTPStatus` carries the code. | Inspect. A 422 means an idempotency key was replayed with a different body - use a fresh key. |
| `*ValidationError` | Bad arguments (non-positive amount, missing field, malformed key id). | Fix the call; nothing was sent. |
| `*WebhookVerificationError` | `VerifyWebhook` rejected an inbound delivery; `Reason` carries which check failed. | Answer 400 with no detail. See [Webhooks](#rejections). |

Refusal codes on `RefusalError.ErrorCode`:

- `PAYMENT_PROCESSING_UNAVAILABLE` - card payments are off right now; retry later.
- `DUPLICATE_REQUEST` - a session for this idempotency key is already open.
- `ALREADY_PROCESSED` - this idempotency key's payment already completed.
- `PRIOR_ATTEMPT_FAILED` - a prior attempt with this key failed terminally; use a fresh key.
- `IDEMPOTENCY_KEY_REUSED` - same key sent with a different body; use a fresh key.

### Recovering from a replay refusal

When your idempotency key collides with an earlier attempt, the refusal names the transaction it
collided with, so you can reconcile instead of minting a second payment:

```go
session, err := client.CreateCheckoutSession(ctx, params)

var refusal *dominaite.RefusalError
if errors.As(err, &refusal) && refusal.TransactionID != "" {
	status, err := client.GetStatus(ctx, refusal.TransactionID)
	// Now you know what the earlier attempt actually did.
}
```

`RefusalError.TransactionID` is empty when the API did not name one (a concurrent-race
`DUPLICATE_REQUEST` knows the key is taken but not yet by which row), so check it before use.
The full refusal payload is on `RefusalError.Raw`.

## Verifying your signing

Run `go test ./...` before you touch the live API. The SDK signs for you, but the recipe is
pinned by an offline known-answer vector shared with the gateway and the dashboard, and the
suite reproduces it byte-for-byte. If that test fails, nothing else matters.

If you ever hand-roll the signing (or debug an `INVALID_SIGNATURE`), `Sign` is exported:

```go
dominaite.Sign(dominaite.SignInput{
	Secret:         "dms_...",
	Timestamp:      "1755302400",                                 // unix SECONDS
	Method:         "POST",
	Path:           "/merchant-api/bridgerpay/checkout/sessions",  // path only, no host
	IdempotencyKey: "00000000-0000-4000-8000-000000000001",        // "" for GET
	Body:           `{"amount":2500,"currency":"EUR","orderReference":"order-1042"}`, // "" for GET
})
// "95759958a0a0a9bd3e6e37101c01e8e7fee1166406e4ac2ff488764f5f742cbf"
```

The signed payload is five lines:
`"{timestamp}\n{METHOD}\n{path}\n{idempotencyKey}\n{sha256hex(body)}"`, signed as lowercase hex
HMAC-SHA256 with your secret, UTF-8 throughout. GET signs an empty idempotency key and an empty
body, and sends no `Idempotency-Key` header.

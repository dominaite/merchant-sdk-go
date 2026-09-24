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

	// Required. Derived from the order, never random: a reload or a retry for the
	// same order and amount replays the session already open instead of opening
	// a second payment. "checkout-order-1042-2500-EUR".
	key, err := dominaite.OrderIdempotencyKey("checkout", "order-1042", 2500, "EUR")
	if err != nil {
		panic(err)
	}

	session, err := client.CreateCheckoutSession(context.Background(), dominaite.CreateCheckoutSessionParams{
		Amount:         2500,          // minor units: 2500 = 25.00 EUR
		Currency:       "EUR",
		OrderReference: "order-1042",  // your own order id, shows up in your dashboard
		IdempotencyKey: key,
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
		var apiErr *dominaite.APIError
		var transport *dominaite.TransportError
		switch {
		case errors.As(err, &refusal):
			// Machine-readable: refusal.ErrorCode - codes listed below.
			fmt.Println("Payment unavailable:", refusal.ErrorCode)
		case errors.As(err, &apiErr) && apiErr.ErrorCode == dominaite.ErrorCodeStorefrontNotWhitelisted:
			// HTTP 409: this site's domain is not whitelisted with the payment
			// provider yet. Not retryable until it is - see Storefront errors.
			fmt.Println("Checkout is not enabled for this site yet")
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

There is no way to switch the freshness check off. `WithWebhookTolerance(0)` is the strictest
setting, not an off switch: it requires `t` to equal your clock to the second, which rejects
anything that spent time in flight. A negative duration is refused with `INVALID_TOLERANCE`.

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
| `WithBaseURL(url)` | Point at a non-production environment. Empty values are ignored, so an unset env var still gives you production. Must be `https://`; plain `http://` is accepted only for `localhost`, `127.0.0.1` and `::1`, and `New` returns a `*ValidationError` for anything else. |
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

Prices usually live as decimals in your catalog. Convert them with `ToMinorUnits`, which does it
exactly on the digits, never through a float, by the exponent the **gateway** uses for the
currency:

```go
amount, err := dominaite.ToMinorUnits("0.30", "EUR") // 30
amount, err = dominaite.ToMinorUnits("1500", "JPY")   // 1500 (no minor unit)
amount, err = dominaite.ToMinorUnits("1500", "HUF")   // 1500 (whole forints, see below)
amount, err = dominaite.ToMinorUnits("1.250", "KWD")  // 1250 (three decimals)
```

It knows EUR, USD, GBP, CAD, AUD, CHF, BGN, RON, PLN, CZK, SEK, DKK, NOK (2 decimals), JPY and
HUF (0), and BHD and KWD (3); `CurrencyExponent` exposes the table. HUF is whole forints on the
gateway, although ISO 4217 gives it 2 decimals: send `1500` for 1500 Ft, not `150000`. ISK, KRW,
OMR, JOD and TND are refused as not supported, because ISO 4217 and the gateway disagree on
them and either reading would be off by 10x or 100x. An unknown currency, a malformed amount
(sign, comma, thousands separator) or more decimals than the currency allows, zeros included
(`"25.000"` EUR, `"100.0"` JPY), is a `*ValidationError`, never a silent rounding.

## Retries and double-charges

Every `CreateCheckoutSession` and `ChargePaymentMethod` call needs an `IdempotencyKey`. The SDK
never makes one up: an empty or blank key is a `*ValidationError` and nothing is sent. Derive
the key from the order with `OrderIdempotencyKey`:

```go
key, err := dominaite.OrderIdempotencyKey("checkout", order.ID, amount, "EUR")
// "checkout-{orderId}-{amountMinor}-{CURRENCY}", e.g. "checkout-order-1042-2500-EUR"
```

Why order-derived: your payment page will be re-entered (reload, Back button, a second tab, a
retry after a timeout). The same order at the same amount gives the same key, and the gateway
answers a replayed key with the session that is already open: same `TransactionID`, same cashier
handles, render the widget again. A random key per call opens a new session on every view
instead, and the order ends up pointing at the wrong one. When the amount or currency changes
(the cart was edited), the helper gives a new key and you get a fresh session; replaying the old
key with the new amount would be refused with `IDEMPOTENCY_KEY_REUSED`.

`scope` is a fixed label for the call site (`"checkout"`, `"renewal"`) so two kinds of payment
for one order never share a key. `SaveCard` is part of a session's identity too: if the payer can
flip it after a session was opened, use a different scope per choice (for example
`"checkout-save"`). Keys are case-insensitive on the gateway, and the helper uppercases the
currency.

When the earlier attempt already moved money, ended, or cannot be handed back yet, the replay
comes back as a `*RefusalError` (`ALREADY_PROCESSED`, `PRIOR_ATTEMPT_FAILED`,
`DUPLICATE_REQUEST`). Recover through `RefusalError.TransactionID` and `GetStatus` - see
[Recovering from a replay refusal](#recovering-from-a-replay-refusal) below.

`CreateCheckoutSessionWithRetry` retries with your key across attempts, retrying only
`*TransportError` (network failures and 5xx, including `MERCHANT_API_UNAVAILABLE` and a 503
carrying `PAYMENT_PROCESSING_UNAVAILABLE`). Refusals and authentication failures are not retried - they will
not change. Rate limits are not retried either: retrying into a full queue only makes it
longer, so a `*RateLimitError` comes straight back for you to reschedule.

```go
session, err := client.CreateCheckoutSessionWithRetry(
	ctx,
	dominaite.CreateCheckoutSessionParams{Amount: 2500, Currency: "EUR", OrderReference: "order-1042", IdempotencyKey: key},
	dominaite.RetryOptions{Attempts: 3, BaseDelay: 500 * time.Millisecond}, // zero values use these defaults
)
```

The delay doubles each attempt, and a cancelled context stops the wait immediately.

## Sessions expire

A session is valid for 2 hours. If the payer comes back later, re-POST `CreateCheckoutSession`
with the same order-derived idempotency key: within a few minutes of expiry that answers
`DUPLICATE_REQUEST` (retry the same key shortly), and past that it succeeds with a fresh
session. See [Recovering from a replay refusal](#recovering-from-a-replay-refusal).

## Stored payment methods (recurring)

Set `SaveCard: true` when you create a session and, once that payment is approved, the gateway
keeps the card on file. You never see the card number or the provider token: `GetStatus` returns a
`StoredPaymentMethod` with an opaque `ID` (`pm_` + 32 hex characters), the `Brand`, the `Last4` and
the expiry, and that `ID` is what you charge and revoke with. Store it against your customer. (The
gateway's `paymentMethod` field on the same status is something else: the string category of how
the payer paid, `card`, `wallet` and so on; it is only reachable through `Raw`.)

```go
key, err := dominaite.OrderIdempotencyKey("checkout-save", "sub-8817-first", 2500, "EUR")
session, err := client.CreateCheckoutSession(ctx, dominaite.CreateCheckoutSessionParams{
	Amount:         2500,
	Currency:       "EUR",
	OrderReference: "sub-8817-first",
	SaveCard:       true,
	IdempotencyKey: key,
})
// ... the payer completes the hosted checkout ...
status, err := client.GetStatus(ctx, session.TransactionID)
if status.Status == dominaite.StatusSucceeded && status.StoredPaymentMethod != nil &&
	status.StoredPaymentMethod.Status == dominaite.StoredPaymentMethodStatusActive {
	db.SaveCard(customerID, status.StoredPaymentMethod.ID) // pm_...
}

// Later, off-session, no payer present:
charge, err := client.ChargePaymentMethod(ctx, paymentMethodID, dominaite.ChargePaymentMethodParams{
	Amount:         2500,
	Currency:       "EUR",
	OrderReference: "sub-8817-2026-10",
	Description:    "Monthly plan, October",
	IdempotencyKey: "sub-8817-2026-10", // derive it from the billing period, never random per attempt
})
var chargeErr *dominaite.ChargeError
if errors.As(err, &chargeErr) {
	switch chargeErr.ErrorCode {
	case dominaite.ChargeErrorOutcomeUnknown:
		// 502: the provider gave no verdict, the charge MAY have happened. Never retry
		// under a new key: poll the transaction the gateway attached instead.
		return pollUntilSettled(ctx, chargeErr.TransactionID)
	case dominaite.ChargeErrorDuplicateRequest, dominaite.ChargeErrorChargesDisabled, dominaite.ChargeErrorProcessingUnavailable:
		// Nothing was charged; retry later with the SAME idempotency key.
	case dominaite.ChargeErrorPaymentMethodNotActive:
		// Revoked or expired: bring the customer back for a hosted session with SaveCard.
	case dominaite.ChargeErrorFailed:
		// 502, nothing was charged. chargeErr.Charge is set when a row exists.
	case dominaite.ChargeErrorIdempotencyKeyReused:
		// Same key, different body or method: a bug on your side.
	}
	return err
}
if err != nil {
	return err // not yours (404), validation, auth, rate limit, transport - see Errors
}

switch charge.Status {
case dominaite.ChargeStatusSucceeded:
case dominaite.ChargeStatusPending:
	// Not terminal. Poll GetStatus(charge.TransactionID), or wait for the webhook.
case dominaite.ChargeStatusFailed:
	// HTTP 402 from the gateway, but not an error: branch on the class, log the code.
	// DeclineClassHard            - give up on this card, ask the customer for another one
	// DeclineClassSoftFunds       - insufficient funds, retry later (not in a loop)
	// DeclineClassSoftSCARequired - the issuer wants the customer present: send them through a
	//                               hosted session with SaveCard and charge the new method
	// DeclineClassSoftOther       - transient, one retry later is reasonable
	handleDecline(charge.DeclineClass, charge.DeclineCode)
case dominaite.ChargeStatusCancelled:
	// An authorization voided before capture; no money moved.
}

// When the customer removes the card:
err = client.RevokePaymentMethod(ctx, paymentMethodID) // 204, returns nil; 204 again if already revoked
var revokeErr *dominaite.RevokeError
if errors.As(err, &revokeErr) && revokeErr.ErrorCode == dominaite.RevokeErrorMerchantAPIUnavailable {
	// 503: nothing changed, retry later.
} else if revokeErr != nil {
	// 502 UPSTREAM_CONTRACT_ERROR: the provider refused for good, nothing changed. Contact support with the id.
}
```

A charge is signed exactly like a session and carries an `Idempotency-Key`, so a retry after a
timeout with the **same** key never charges the card twice: the gateway replays its first answer,
HTTP status included. The HTTP status is the contract on this route: 201 (or 200 on a replay)
returns the charge, 402 returns the charge too (`Status` `failed` plus `DeclineClass`), and 409,
422, 502 and 503 return a `*ChargeError` with `ErrorCode`, `HTTPStatus`, the gateway's message
and, when the gateway attached the charge row, `Charge` and `TransactionID`. Only authentication
(401/403), an id that is not yours (404, `*APIError` with `ErrorCode` `PAYMENT_METHOD_NOT_FOUND`),
validation (400, `*APIError`), rate limiting (429) and network failures or a 5xx without a code
keep their generic errors. `DeclineClass` and `DeclineCode` are empty unless the charge was
declined; the gateway omits them on the wire.

Revoking signs an empty key and an empty body, like `GetStatus`. A revoke that fails with
`*RevokeError` changed nothing: `MERCHANT_API_UNAVAILABLE` (503) is retryable,
`UPSTREAM_CONTRACT_ERROR` (502) is not. After a revoke the status read keeps the
`StoredPaymentMethod` with `Status` `revoked`, and a charge against it is refused with
`PAYMENT_METHOD_NOT_ACTIVE`. An id that is not yours is an `*APIError` with `HTTPStatus` 404.

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
`processing` and `requires_capture` - none of them is terminal. `IsPaid(status)` and
`IsTerminal(status)` encode exactly that: `IsPaid` is true for `succeeded` only; `IsTerminal` is
true for `succeeded`, `failed`, `cancelled`, `abandoned`, `refunded` and `partially_refunded`,
and false for everything else, unknown values included.

```go
if dominaite.IsPaid(status.Status) {
	markPaid(order)
} else if !dominaite.IsTerminal(status.Status) {
	pollAgainLater(order)
}
```

`requires_capture` is **not** "unpaid": the payer has already paid and the funds are held
awaiting capture. Never treat it as an abandoned order.

Treat any status you do not recognise as still-open as well: a value the API adds later should
make you keep polling, never silently close an order that is still live.

Poll after the payer returns to you, or on your order timeout - not in a tight loop; the
endpoint is rate limited at 60 requests per minute per key and 120 per minute per IP. Going
over returns a `*RateLimitError`.

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
| `*ChargeError` | `ChargePaymentMethod` answered 409, 422, 502 or 503 with a code; `ErrorCode`, `HTTPStatus`, and `Charge`/`TransactionID` when the gateway attached the charge row. | Branch on `ErrorCode`; see [Stored payment methods](#stored-payment-methods-recurring). `CHARGE_OUTCOME_UNKNOWN`: poll `TransactionID`, never retry under a new key. |
| `*RevokeError` | `RevokePaymentMethod` answered 502 (`UPSTREAM_CONTRACT_ERROR`) or 503 (`MERCHANT_API_UNAVAILABLE`). Nothing changed. | 503: retry later. 502: contact support with the id. |
| `*AuthError` | 401/403. `ErrorCode` is `INVALID_API_KEY`, `INVALID_SIGNATURE`, `TIMESTAMP_OUT_OF_RANGE`, or `IP_NOT_ALLOWED`. | Fix the key id, secret, server clock, or allowlist. Never retry-loop. |
| `*RateLimitError` | 429. `RetryAfterSeconds` carries the `Retry-After` header when the API sent one as integer seconds; `HasRetryAfter` tells "wait 0s" apart from "the API did not say". | Back off, then reschedule with the **same** idempotency key. The SDK never retries this for you. |
| `*TransportError` | Network failure, timeout, or a 5xx without a code the SDK models. Wraps the cause, reachable with `errors.Unwrap`. A 5xx classifies on the status, so an HTML or empty error page from an overloaded edge is still retryable. | Retry with the **same** idempotency key. |
| `*APIError` | Any other rejecting or unexpected response; `HTTPStatus` and `ErrorCode` carry the details. | Inspect. A storefront refusal lands here (see [Storefront errors](#storefront-errors)). A 3xx means a proxy or a wrong base URL answered with a redirect - the SDK never follows one. A replayed idempotency key does not land here; it comes back as a `*RefusalError`. |
| `*ValidationError` | Bad arguments (non-positive amount, missing field or idempotency key, malformed key id). | Fix the call; nothing was sent. |
| `*WebhookVerificationError` | `VerifyWebhook` rejected an inbound delivery; `Reason` carries which check failed. | Answer 400 with no detail. See [Webhooks](#rejections). |

Refusal codes on `RefusalError.ErrorCode`, exported as `ErrorCode*` constants:

- `PAYMENT_PROCESSING_UNAVAILABLE` (`ErrorCodePaymentProcessingUnavailable`) - card payments are
  off right now; retry later with the same key.
- `DUPLICATE_REQUEST` (`ErrorCodeDuplicateRequest`) - a session for this idempotency key exists
  but cannot be handed back yet, or expired within the last few minutes. Re-POST the same key
  shortly, never a fresh one.
- `ALREADY_PROCESSED` (`ErrorCodeAlreadyProcessed`) - this idempotency key's payment already
  completed.
- `PRIOR_ATTEMPT_FAILED` (`ErrorCodePriorAttemptFailed`) - a prior attempt with this key failed
  terminally; use a fresh key.
- `IDEMPOTENCY_KEY_REUSED` (`ErrorCodeIdempotencyKeyReused`) - same key sent with a different
  amount, currency or `SaveCard`; a re-priced order needs a new key.
- `STOREFRONT_MISMATCH` (`ErrorCodeStorefrontMismatch`) - the key was first used for a different
  storefront.

### Storefront errors

When a merchant runs more than one website, each session is filed under a storefront (an online
location). A storefront refusal comes back as an `*APIError` with `HTTPStatus` and `ErrorCode`,
nothing was minted, and a retry will not change it, so the retry helper returns it at once:

| `ErrorCode` | HTTP | Meaning |
|---|---|---|
| `STOREFRONT_NOT_WHITELISTED` (`ErrorCodeStorefrontNotWhitelisted`) | 409 | The site's domain is not whitelisted with the payment provider yet. Contact Dominaite support to finish it; until then this site cannot take payments. |
| `STOREFRONT_INACTIVE` (`ErrorCodeStorefrontInactive`) | 409 | The storefront was deactivated or deleted. |
| `STOREFRONT_MISMATCH` (`ErrorCodeStorefrontMismatch`) | 400 | The API key is bound to one storefront and the request named another. (On a replay of an existing key this is a `*RefusalError` instead.) |

```go
var apiErr *dominaite.APIError
if errors.As(err, &apiErr) {
	switch apiErr.ErrorCode {
	case dominaite.ErrorCodeStorefrontNotWhitelisted, dominaite.ErrorCodeStorefrontInactive:
		// Show "checkout unavailable" and alert your ops; do not retry in a loop.
	case dominaite.ErrorCodeStorefrontMismatch:
		// Configuration bug: this key belongs to another site.
	}
}
```

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

One replay is not a refusal at all. A session that expired unpaid is superseded: from a few
minutes past its expiry, re-POSTing the same key returns an ordinary success with a fresh
session (a new `TransactionID`, same key), so a customer who comes back late just pays. Keep
the order-derived key for the life of the order to keep that path open. The band is not
endless - once the platform has independently closed the attempt (about an hour past expiry),
the replay answers `PRIOR_ATTEMPT_FAILED` and the key is spent; reconcile through `GetStatus`
and use a fresh key.

## Verifying your signing

Run `go test ./...` before you touch the live API. The SDK signs for you, but the recipe is
pinned by an offline known-answer vector shared with the gateway and the dashboard, and the
suite reproduces it byte-for-byte. If that test fails, nothing else matters.

If you ever hand-roll the signing (or debug an `INVALID_SIGNATURE`), `Sign` is exported:

```go
dominaite.Sign(dominaite.SignInput{
	Secret:         "dms_...",
	Timestamp:      "1755302400",                           // unix SECONDS
	Method:         "POST",
	Path:           "/merchant-api/checkout/sessions",      // path only, no host
	IdempotencyKey: "00000000-0000-4000-8000-000000000001", // "" for GET
	Body:           `{"amount":2500,"currency":"EUR","orderReference":"order-1042"}`, // "" for GET
})
// "8f5fba0b29a8eea81b76a0e6d7119e79ec68f586910f77713b045652e5ce9b74"
```

Printing a `SignInput` or a `Client` **itself** is safe: both redact the secret under `%v`,
`%+v`, `%#v` and `%s`, so a debug log shows `dms_***redacted***` and every other field.
`SignInput` also carries `json:"-"` on `Secret`, so JSON encoders drop it.

Two cases the redaction cannot reach, because neither goes through the `String` method:

- A `SignInput` or `Client` sitting in an **unexported field of your own struct**, printed by
  printing that outer struct. `fmt` cannot call methods on values it reaches by reflection, so
  it dumps the fields raw: a by-value field leaks under `%v`, `%+v` and `%s`, and even the
  usual `client *dominaite.Client` field leaks under `%s`.
- Any encoder that walks the struct instead of printing it. `SignInput` is covered by the tag;
  anything else you build around your secret is not.

Keep the secret out of structured logs entirely rather than relying on redaction.

The signed payload is five lines:
`"{timestamp}\n{METHOD}\n{path}\n{idempotencyKey}\n{sha256hex(body)}"`, signed as lowercase hex
HMAC-SHA256 with your secret, UTF-8 throughout. GET signs an empty idempotency key and an empty
body, and sends no `Idempotency-Key` header.

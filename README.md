# dominaite-go

Server-side Go client for the Dominaite merchant API. One call from your backend opens a
hosted checkout session; a two-line script tag renders the payment widget on your page. Card
details go straight from your customer's browser into the payment widget - they never touch
your server, which keeps your PCI scope minimal (SAQ A).

Go 1.21 or newer. Zero dependencies: `crypto/hmac`, `crypto/sha256`, `net/http`,
`encoding/json`, all standard library.

## Install

The module path is `github.com/dominaite/merchant-sdk-go`, and it is a **placeholder**: the
repo does not exist yet and the final name is an open owner decision. npm settled on
`@dominaite/merchant-sdk` and PyPI on `dominaite` (2026-08-17), so whatever repo this lands in
should keep the same family. Until the repo exists, use a local checkout with a `replace`:

```bash
go mod edit -require=github.com/dominaite/merchant-sdk-go@v0.0.0
go mod edit -replace=github.com/dominaite/merchant-sdk-go=/path/to/dominaite-go-sdk
go mod tidy
```

Once the repo is published, drop the `replace` and `go get` it normally.

To work on the SDK itself:

```bash
cd dominaite-go-sdk
go vet ./...
go test ./...      # includes the offline signing vector
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

## Quickstart (zero to a signed session against dev)

Everything below is copy-paste. It assumes an empty directory and nothing installed.

```bash
mkdir my-checkout && cd my-checkout
go mod init example.com/my-checkout
go mod edit -require=github.com/dominaite/merchant-sdk-go@v0.0.0
go mod edit -replace=github.com/dominaite/merchant-sdk-go=/path/to/dominaite-go-sdk
go mod tidy
```

Set your credentials and the environment you are pointing at:

```bash
export DOMINAITE_KEY_ID=dmk_...      # Website integration tab
export DOMINAITE_SECRET=dms_...      # shown once when you generated the key
# Dev: the payments function app, whose Azure Functions route prefix is /api.
# Confirm the host for your environment before the first call.
export DOMINAITE_BASE_URL=https://func-dom-gw-payments-dev-gwc-01.azurewebsites.net/api
# Production needs no DOMINAITE_BASE_URL - the SDK defaults to
# https://api.dominaite.com/payments
```

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

That's the whole integration: the session call, the script tag, and your domain bound to your
checkout by Dominaite during onboarding.

There is a runnable version of the above in `examples/create-session/main.go` in this repo - it
mints a session and reads the status back, using the same three environment variables:

```bash
go run ./examples/create-session
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

## Status polling

```go
status, err := client.GetStatus(ctx, session.TransactionID)
// status.Status == "succeeded", status.OrderReference == "order-1042",
// status.Amount == 2500, status.Currency == "EUR", ...
```

`Status` is one of `pending`, `processing`, `succeeded`, `failed`, `refunded`,
`partially_refunded`, `cancelled`, `disputed`, `abandoned` (exported as the `Status*`
constants). While the session is still payable the response also carries `ExpiresAt`; after
that instant a `pending` session can only become `abandoned`. An unknown transaction id returns
an `*APIError` with `HTTPStatus` 404.

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

Refusal codes on `RefusalError.ErrorCode`:

- `PAYMENT_PROCESSING_UNAVAILABLE` - card payments are off right now; retry later.
- `DUPLICATE_REQUEST` - a session for this idempotency key is already open.
- `ALREADY_PROCESSED` - this idempotency key's payment already completed.
- `IDEMPOTENCY_KEY_REUSED` - same key sent with a different body; use a fresh key.

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

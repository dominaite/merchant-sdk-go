# Changelog

## 0.3.0 (unreleased)

### Breaking

- `IdempotencyKey` is required on `CreateCheckoutSession`, `CreateCheckoutSessionWithRetry` and
  `ChargePaymentMethod`, and must be 1 to 100 visible ASCII characters (`0x21` to `0x7E`). The
  SDK no longer generates a random key; an empty, blank, too long or non-ASCII key is a
  `*ValidationError` and nothing is sent.

  Migration: set `IdempotencyKey` on every call with
  `dominaite.OrderIdempotencyKey("checkout", orderID, amountMinor, currency)` (for charges, derive
  it from the billing period). Never generate a fresh key per call: the same order at the same
  amount must replay the open session.

The module path does not change; this ships as tag `v0.3.0`.

### Added

- `OrderIdempotencyKey` builds `{scope}-{orderId}-{amountMinor}-{CURRENCY}`.
- `ErrorCode*` constants: `STOREFRONT_NOT_WHITELISTED` (409), `STOREFRONT_INACTIVE` (409),
  `STOREFRONT_MISMATCH` (400), `ALREADY_PROCESSED`, `PRIOR_ATTEMPT_FAILED`, `DUPLICATE_REQUEST`,
  `PAYMENT_PROCESSING_UNAVAILABLE`, `IDEMPOTENCY_KEY_REUSED`. Storefront refusals arrive as an
  `*APIError` with `HTTPStatus` and `ErrorCode`.
- `ToMinorUnits` and `CurrencyExponent`: exact decimal string to minor units by the gateway's
  currency table. HUF is whole forints (0 decimals, unlike ISO 4217). ISK, KRW, OMR, JOD and TND
  are refused as not supported.
- `IsPaid` and `IsTerminal` status helpers.
- `CreateCheckoutSessionWithRetry` also retries the HTTP 200 refusal form of
  `PAYMENT_PROCESSING_UNAVAILABLE`, with the same key.
- Stored payment methods: `SaveCard`, `ChargePaymentMethod`, `RevokePaymentMethod`.

### Fixed

- Docs no longer claim a replayed key never hands the session back: the gateway returns the
  original open session.

Earlier releases: see the git tags.

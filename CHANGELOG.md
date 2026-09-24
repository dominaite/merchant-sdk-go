# Changelog

## 0.3.0 (unreleased)

### Breaking

- `IdempotencyKey` is required on `CreateCheckoutSession`, `CreateCheckoutSessionWithRetry` and
  `ChargePaymentMethod`. The SDK no longer generates a random key when it is empty; an empty or
  blank key is a `*ValidationError` and nothing is sent.

  Migration: set `IdempotencyKey` on every call, derived from the order with
  `dominaite.OrderIdempotencyKey("checkout", orderID, amountMinor, currency)` (for charges,
  derive it from the billing period). Never generate a fresh key per call: the same order at the
  same amount must replay the open session.

### Added

- `OrderIdempotencyKey` builds `{scope}-{orderId}-{amountMinor}-{CURRENCY}`.
- `ErrorCode*` constants: `STOREFRONT_NOT_WHITELISTED` (409), `STOREFRONT_INACTIVE` (409),
  `STOREFRONT_MISMATCH` (400), `ALREADY_PROCESSED`, `PRIOR_ATTEMPT_FAILED`, `DUPLICATE_REQUEST`,
  `PAYMENT_PROCESSING_UNAVAILABLE`, `IDEMPOTENCY_KEY_REUSED`. Storefront refusals arrive as an
  `*APIError` with `HTTPStatus` and `ErrorCode`.
- `ToMinorUnits` and `CurrencyExponent`: exact decimal string to minor units by ISO 4217
  exponent.
- `IsPaid` and `IsTerminal` status helpers.
- Stored payment methods: `SaveCard`, `ChargePaymentMethod`, `RevokePaymentMethod`.

### Fixed

- Docs no longer claim a replayed key never hands the session back: the gateway returns the
  original open session.

Earlier releases: see the git tags.

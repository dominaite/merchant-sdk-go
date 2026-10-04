# Changelog

## Unreleased

### Added

- Wallet reporting: `CheckoutStatus.PaymentMethod` (how the payer paid) and
  `CheckoutStatus.WalletType` (which wallet, when it was one). Empty while the payment is open
  and on older transactions; an unknown `WalletType` is a valid wallet, not an error. Reporting
  data only: a wallet payment refunds, captures and disputes like a card payment.
- `PaymentMethodCategories` (`card`, `wallet`, `bank_transfer`, `sepa`) and `WalletTypes`
  (`apple_pay`, `google_pay`, `samsung_pay`), with the `PaymentMethod*` and `WalletType*`
  constants.
- Contract fixtures: the `wallets` section and `paymentMethod`/`walletType` in the status read
  fields and examples.

## 0.4.0

### Added

- Card fields: `CreateCheckoutSessionParams.Integration` (`IntegrationWidget` or
  `IntegrationFields`, omitted when empty), and `CheckoutSession.Integration` and
  `CheckoutSession.ClientSecret` on the response. `ClientSecret` is set only for fields
  sessions. Card fields are enabled per merchant on request; see the README.
- Contract fixture: `integrationVocabulary`, the two new checkout fields and a fields session
  example.
- Processor reference: `CheckoutStatus.PSPReference` and `WebhookData.PSPReference` on `payment.*`
  events. The payment processor's reference for the transaction, empty until known and on a sale
  settled by reconciliation without a processor webhook. Refund and cancel events carry the
  original sale's reference. Not on `charge.*` events.
- Contract fixture: `pspReference` in the status read fields and examples.

## 0.3.1

### Added

- `WebhookEvent.APIVersion`: the dated payload version (`apiVersion`, currently `2026-09-25`)
  every webhook envelope now carries. Empty on deliveries from a gateway that predates it.
- `WebhookData.Sequence`: the per-object `data.sequence` on `agreement.*` and `charge.*` events.
  Keep the highest one processed per object and drop anything not higher; order by it, never by
  `createdAt`. Zero when absent. The README documents the object keys.
- Refunds: `CreateRefund` and `GetRefund` on `/merchant-api/payments/{transactionId}/refunds`.
  `CreateRefundParams.IdempotencyKey` is required and signed; a nil `Amount` sends no amount and
  refunds everything still refundable. `Refund`, the `RefundStatus*` constants with
  `RefundStatuses` and `IsRefundTerminal`, the `RefundFailure*` failure codes, and
  `*RefundError` with the `RefundError*` codes and `Retryable` (`DUPLICATE_REQUEST`,
  `REFUND_NOT_FOUND`). A failed refund is a `Refund` with `Status` `failed`, not an error, and
  fires no webhook.
- `WebhookData.StoredPaymentMethod`: the card a `SaveCard` session stored, on `payment.*`
  events, as the same `StoredPaymentMethod` type the status read returns. `nil` when no card was
  saved, and also on some sales where the card was stored after the approval was announced;
  `GetStatus` stays the source of truth.
- Contract fixture refreshed with the refund endpoints and vocabularies.

## 0.3.0

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
- Saved cards can be `retired`: the platform stopped the card on its own and it never becomes
  active again. `StoredPaymentMethodStatusRetired`, `StoredPaymentMethod.RetiredReason` (empty
  unless retired) and the `RetiredReason*` constants with `StoredPaymentMethodRetiredReasons`
  (`hard_decline`, `chargeback`, `source_sale_reversed`).
- `StorefrontErrorCodes` lists the three storefront codes in the contract's order.
- Contract fixtures refreshed from the gateway (contract version 2026-09-16).

### Fixed

- Docs no longer claim a replayed key never hands the session back: the gateway returns the
  original open session.

Earlier releases: see the git tags.

package dominaite

import (
	"strconv"
	"strings"
)

// OrderIdempotencyKey builds the idempotency key for one payment of one order:
// "{scope}-{orderID}-{amountMinor}-{CURRENCY}", for example
// OrderIdempotencyKey("checkout", "order-1042", 2500, "eur") is
// "checkout-order-1042-2500-EUR".
//
// Derive the key from the order, never per call. Your payment page will be
// re-entered: a reload, the Back button, a second tab, a retry after a timeout.
// The same order at the same amount produces the same key every time, so each
// of those replays the session already open (same TransactionID, same cashier
// handles) instead of opening a second payment. A changed amount or currency
// (the cart was edited) produces a new key and therefore a fresh session; the
// old key replayed with a new amount would be refused with
// IDEMPOTENCY_KEY_REUSED.
//
// scope is a fixed label for the call site ("checkout", "renewal"), so two
// different kinds of payment for the same order never share a key. SaveCard is
// part of a session's identity too: if the payer can flip it after a session
// was opened, use a different scope for each choice (for example
// "checkout-save"). Keep scope free of values that vary per request.
//
// amountMinor is in MINOR units and must be positive. currency is ISO 4217 and
// is uppercased here, so "eur" and "EUR" give the same key. Returns a
// *ValidationError when scope or orderID is blank, the amount is not
// positive, the currency is not three ASCII letters, or the key would be
// longer than the 100 characters the API accepts.
func OrderIdempotencyKey(scope, orderID string, amountMinor int64, currency string) (string, error) {
	if strings.TrimSpace(scope) == "" {
		return "", newValidationError("Missing required parameter: scope")
	}
	if strings.TrimSpace(orderID) == "" {
		return "", newValidationError("Missing required parameter: orderID")
	}
	if amountMinor <= 0 {
		return "", newValidationError("amountMinor must be a positive integer in MINOR units (e.g. 2500 for 25.00 EUR)")
	}
	code, err := normalizeCurrencyCode(currency)
	if err != nil {
		return "", err
	}

	key := scope + "-" + orderID + "-" + strconv.FormatInt(amountMinor, 10) + "-" + code
	if err := validateIdempotencyKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// normalizeCurrencyCode uppercases an ISO 4217 code and checks its shape:
// exactly three ASCII letters.
func normalizeCurrencyCode(currency string) (string, error) {
	// Checked before uppercasing: strings.ToUpper maps some non-ASCII letters
	// onto ASCII ones, which would let a lookalike through.
	code := strings.TrimSpace(currency)
	if len(code) != 3 {
		return "", newValidationError("currency must be a three-letter ISO 4217 code, e.g. EUR")
	}
	for i := 0; i < len(code); i++ {
		c := code[i] | 0x20 // ASCII lowercase
		if c < 'a' || c > 'z' {
			return "", newValidationError("currency must be a three-letter ISO 4217 code, e.g. EUR")
		}
	}
	return strings.ToUpper(code), nil
}

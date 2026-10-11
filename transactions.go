package dominaite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Transaction types on Transaction.Type, in the gateway's own order.
// TransactionTypePayment: the payer paid on the checkout.
// TransactionTypeCharge: a saved card was charged, by you or by the
// subscription scheduler. TransactionTypeVerification: a verify-only session
// saved a card. It is not a purchase, so leave it out of revenue.
const (
	TransactionTypePayment      = "payment"
	TransactionTypeCharge       = "charge"
	TransactionTypeVerification = "verification"
)

// TransactionTypes is the transaction type vocabulary, in the gateway's order.
var TransactionTypes = []string{TransactionTypePayment, TransactionTypeCharge, TransactionTypeVerification}

// ListTransactionsParams selects one page of ListTransactions.
type ListTransactionsParams struct {
	// From is the start of the window, inclusive: ISO 8601 UTC in the Z form,
	// e.g. "2026-10-01T00:00:00Z" (t.UTC().Format(time.RFC3339)), or a date,
	// which is midnight UTC. Required.
	From string
	// To is the end of the window, exclusive, in the same form. Required. It
	// must be later than From, at most 31 days after it.
	To string
	// Status keeps only payments now in this status: one of Statuses. Empty
	// lists every status. The SDK does not check it: the API refuses an
	// unknown status with a 400.
	Status string
	// Limit is the number of items per page, 1 to 100. 0 sends nothing, and
	// the API then uses 50.
	Limit int
	// Cursor is the NextCursor of the previous page, unchanged. Empty reads
	// the first page.
	Cursor string
}

// Transaction is one payment in ListTransactions. A refund is not a row of its
// own: it shows on its payment as RefundedAmount and Status.
type Transaction struct {
	// TransactionID is the payment's id, as the session or the charge
	// returned it.
	TransactionID string `json:"transactionId"`
	// OrderReference is your own order id from the session, empty when none
	// was sent.
	OrderReference string `json:"orderReference,omitempty"`
	// Type is one of the TransactionType* values.
	Type string `json:"type"`
	// Status is one of Statuses, the same value GetStatus answers.
	Status string `json:"status"`
	// Amount is in MINOR units of Currency.
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// CreatedAt is ISO 8601 UTC. The window and the order use it.
	CreatedAt string `json:"createdAt"`
	// CompletedAt is ISO 8601 UTC, when the payment first succeeded. Empty
	// when it never succeeded, and on older payments that succeeded before
	// this time was recorded.
	CompletedAt string `json:"completedAt,omitempty"`
	// StoredPaymentMethodID is the pm_ id of the card a charge was made on.
	// Empty on a checkout payment, and on an old charge that did not record
	// its card.
	StoredPaymentMethodID string `json:"storedPaymentMethodId,omitempty"`
	// RefundedAmount is what was refunded so far, in MINOR units. 0 when
	// nothing was refunded.
	RefundedAmount int64 `json:"refundedAmount,omitempty"`

	// Raw is the unparsed item, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// TransactionList is one page of ListTransactions.
type TransactionList struct {
	// Items are the payments of this page, oldest first. Empty when the
	// window has none.
	Items []Transaction `json:"items"`
	// NextCursor reads the next page: send it back as Cursor with the same
	// From, To and Status. Empty on the last page.
	NextCursor string `json:"nextCursor,omitempty"`

	// Raw is the unparsed page, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// ListTransactions reads one page of your card-not-present payments created
// in the window [From, To): checkout payments, saved-card charges,
// subscription charges and verify-only sessions. The rows come oldest first.
// Page with NextCursor until it is empty: paging never repeats or skips a row.
//
// A row has its status at the time of the read. For a statement, end the
// window a few minutes in the past, and read again later to pick up changes
// such as refunds. A key bound to a storefront lists that storefront only.
//
// The key needs the transactions:read scope. A key with the payments scope
// cannot hold it, so use a separate reporting key. The call is signed on the
// path only, with an empty idempotency key and an empty body: the query
// string is not part of the signature.
//
// Errors it returns:
//   - *ValidationError: an empty From or To; nothing was sent.
//   - *APIError: 400 VALIDATION_ERROR for a window, status, limit or cursor
//     the API refused. Raw carries error.validationErrors, one per field.
//     Do not retry it unchanged.
//   - *AuthError: also 403 INSUFFICIENT_SCOPE for a key without
//     transactions:read.
//   - *RateLimitError, *TransportError: as everywhere else.
func (c *Client) ListTransactions(ctx context.Context, params ListTransactionsParams) (*TransactionList, error) {
	if strings.TrimSpace(params.From) == "" || strings.TrimSpace(params.To) == "" {
		return nil, newValidationError("from and to are required: ISO 8601 UTC, e.g. 2026-10-01T00:00:00Z")
	}
	query := url.Values{}
	query.Set("from", params.From)
	query.Set("to", params.To)
	if params.Status != "" {
		query.Set("status", params.Status)
	}
	if params.Limit != 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Cursor != "" {
		query.Set("cursor", params.Cursor)
	}

	// GET signs an EMPTY idempotency key, an EMPTY body and the path without
	// its query string.
	reply, err := c.sendQuery(ctx, http.MethodGet, TransactionsPath, query.Encode(), "", "")
	if err != nil {
		return nil, err
	}
	if reply.status >= 400 {
		return nil, reply.rejection()
	}
	if !reply.parsed || !isJSONObject(reply.data) {
		return nil, newAPIError(reply.status, "The API answered the transaction list without a page body")
	}

	// A null nextCursor on the last page decodes as the empty string.
	var page struct {
		Items      json.RawMessage `json:"items"`
		NextCursor string          `json:"nextCursor"`
	}
	if json.Unmarshal(reply.data, &page) != nil {
		return nil, newAPIError(reply.status, "The API returned an unexpected transaction page")
	}
	items, err := decodeItems(page.Items, func(item *Transaction, raw json.RawMessage) { item.Raw = raw })
	if err != nil {
		return nil, newAPIError(reply.status, "The API returned an unexpected transaction in the page")
	}

	return &TransactionList{Items: items, NextCursor: page.NextCursor, Raw: reply.data}, nil
}

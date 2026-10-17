package dominaite

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// A plan or agreement id (plan_..., agr_...) is opaque and case-sensitive, so
// it is sent exactly as given. This only pins what keeps it one path segment
// of the signed canonical path, as for a payment method id.
var recurringIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// Plan status values, in the gateway's own order. Only an active plan accepts
// new agreements.
const (
	PlanStatusActive   = "active"
	PlanStatusArchived = "archived"
)

// PlanStatuses is the plan status vocabulary, in the gateway's order.
var PlanStatuses = []string{PlanStatusActive, PlanStatusArchived}

// Interval units of a plan's billing period. The interval is at most one year:
// 365 days, 52 weeks, 12 months or 1 year.
const (
	IntervalUnitDay   = "day"
	IntervalUnitWeek  = "week"
	IntervalUnitMonth = "month"
	IntervalUnitYear  = "year"
)

// IntervalUnits is the interval unit vocabulary, in the gateway's order.
var IntervalUnits = []string{IntervalUnitDay, IntervalUnitWeek, IntervalUnitMonth, IntervalUnitYear}

// CreatePlanParams is what CreatePlan sends. Amount, Currency and the
// interval never change once the plan exists: to change a price, create a new
// plan and archive the old one.
type CreatePlanParams struct {
	// Name is the display name, 1 to 100 characters. UpdatePlan can change it.
	Name string `json:"name"`
	// Amount is the charge per period, in MINOR units of Currency (2500 is
	// 25.00 EUR). Convert with ToMinorUnits.
	Amount int64 `json:"amount"`
	// Currency is the ISO 4217 code, three upper-case letters.
	Currency string `json:"currency"`
	// IntervalUnit is one of the IntervalUnit* values.
	IntervalUnit string `json:"intervalUnit"`
	// IntervalCount is the number of units per billing period: every 3 months
	// is IntervalUnitMonth with IntervalCount 3. 0 sends nothing, and the API
	// then uses 1.
	IntervalCount int `json:"intervalCount,omitempty"`
	// PeriodCount is the number of periods after which an agreement completes
	// on its own, at least 1. nil bills until the agreement is cancelled. It
	// is a pointer so that a count that computed to zero is refused instead
	// of turning into "bill forever".
	PeriodCount *int `json:"periodCount,omitempty"`
	// TrialDays is the number of days between activation and the first
	// charge, 0 to 365. 0 is no trial. UpdatePlan can change it for agreements
	// created later.
	TrialDays int `json:"trialDays,omitempty"`
	// CancelURL is your page where the customer cancels the subscription: an
	// absolute https URL. The platform prints it as the cancel link in every
	// receipt and decline notice it emails your customer. Empty sends nothing.
	CancelURL string `json:"cancelUrl,omitempty"`

	// IdempotencyKey is required. It is sent as the Idempotency-Key header and
	// signed. Derive it from your own plan record, never per attempt.
	IdempotencyKey string `json:"-"`
	// Extra holds request fields this struct does not model yet. They are
	// sent as given, and a key in Extra replaces the modelled field of the
	// same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON writes the modelled fields, then the Extra fields.
func (p CreatePlanParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params CreatePlanParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// UpdatePlanParams is what UpdatePlan sends. A nil field keeps its value.
// Amount, currency and interval cannot change.
type UpdatePlanParams struct {
	// Name is the new display name, 1 to 100 characters.
	Name *string `json:"name,omitempty"`
	// TrialDays is the new trial, 0 to 365 days, for agreements created from
	// now on. A pointer to 0 removes the trial.
	TrialDays *int `json:"trialDays,omitempty"`
	// CancelURL is the new cancel page, shown on the next email. A pointer to
	// "" removes it.
	CancelURL *string `json:"cancelUrl,omitempty"`

	// Extra holds request fields this struct does not model yet. They are
	// sent as given, and a key in Extra replaces the modelled field of the
	// same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON writes the modelled fields, then the Extra fields.
func (p UpdatePlanParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params UpdatePlanParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// Plan is a subscription plan: a price and a billing interval you sell. An
// agreement subscribes one customer to it.
type Plan struct {
	// ID is plan_ followed by 32 hex characters. It is case-sensitive: store
	// it and send it back exactly as it is.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Amount is the charge per period, in MINOR units of Currency.
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// IntervalUnit is one of the IntervalUnit* values.
	IntervalUnit  string `json:"intervalUnit"`
	IntervalCount int    `json:"intervalCount"`
	// PeriodCount is nil when agreements bill until they are cancelled.
	PeriodCount *int `json:"periodCount,omitempty"`
	// TrialDays applies to agreements created from now on. 0 is no trial.
	TrialDays int `json:"trialDays"`
	// Status is one of the PlanStatus* values.
	Status string `json:"status"`
	// CancelURL is empty when the plan has no cancel page.
	CancelURL string `json:"cancelUrl,omitempty"`
	// CreatedAt is ISO 8601 UTC.
	CreatedAt string `json:"createdAt"`
	// ArchivedAt is ISO 8601 UTC, empty while the plan is active.
	ArchivedAt string `json:"archivedAt,omitempty"`

	// RequestID is the API's id of the call that returned this plan. Quote it
	// to support. Empty when the API sent none.
	RequestID string `json:"-"`

	// Raw is the unparsed plan object, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// CreatePlan creates a subscription plan. Nothing is charged: an agreement
// subscribes a customer to the plan, and the hosted subscription session
// activates the agreement.
//
// params.IdempotencyKey is required and signed. The same key with the same
// body answers the plan it created, with HTTP 200 instead of 201, so a create
// whose answer you lost is safe to repeat with the same key. The same key
// with a different body is refused with IDEMPOTENCY_KEY_REUSED. Keys are not
// case-sensitive: two keys that differ only in letter case are one key.
//
// Errors it returns:
//   - *ValidationError: a missing name, currency or interval unit, a
//     non-positive amount, or a bad IdempotencyKey; nothing was sent.
//   - *APIError: a 4xx with ErrorCode. 400 is a field the API refused (Raw
//     carries error.validationErrors); 422 is IDEMPOTENCY_KEY_REUSED.
//   - *AuthError, *RateLimitError: as everywhere else.
//   - *TransportError: network failure or 5xx. Nothing was created on a 503;
//     retry WITH the same key.
func (c *Client) CreatePlan(ctx context.Context, params CreatePlanParams) (*Plan, error) {
	if strings.TrimSpace(params.Name) == "" {
		return nil, newValidationError("Missing required parameter: name")
	}
	if params.Amount <= 0 {
		return nil, newValidationError("amount must be a positive integer in MINOR units (e.g. 2500 for 25.00 EUR)")
	}
	if strings.TrimSpace(params.Currency) == "" {
		return nil, newValidationError("Missing required parameter: currency")
	}
	if strings.TrimSpace(params.IntervalUnit) == "" {
		return nil, newValidationError("Missing required parameter: intervalUnit")
	}
	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	body, err := marshalNoEscape(params)
	if err != nil {
		return nil, newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	return retry(ctx, c, c.retryPolicy("CreatePlan"), func() (*Plan, error) {
		reply, err := c.send(ctx, http.MethodPost, PlansPath, string(body), idempotencyKey)
		if err != nil {
			return nil, err
		}
		return reply.plan()
	})
}

// ListPlans lists your plans: at most 200, active first, newest first within
// a status. The list has no pages; read a plan by id with GetPlan when you
// have more.
//
// Errors it returns: *AuthError, *RateLimitError, *TransportError, and an
// *APIError for any other rejection.
func (c *Client) ListPlans(ctx context.Context) ([]Plan, error) {
	return retry(ctx, c, c.retryPolicy("ListPlans"), func() ([]Plan, error) {
		// GET signs an EMPTY idempotency key and an EMPTY body, and sends no
		// Idempotency-Key header.
		reply, err := c.send(ctx, http.MethodGet, PlansPath, "", "")
		if err != nil {
			return nil, err
		}
		if reply.status >= 400 {
			return nil, reply.rejection()
		}
		if !reply.parsed {
			return nil, reply.apiError("The API returned a non-JSON response")
		}
		plans, err := decodeItems(reply.data, func(plan *Plan, raw json.RawMessage) {
			plan.Raw = raw
			plan.RequestID = reply.requestID
		})
		if err != nil {
			return nil, reply.apiError("The API returned an unexpected plan list")
		}
		return plans, nil
	})
}

// GetPlan reads one of your plans.
//
// Errors it returns: an *APIError with HTTPStatus 404 and ErrorCode
// PLAN_NOT_FOUND for an id that is not yours, or 400 INVALID_FORMAT for an id
// that is not a plan id; *ValidationError for an id that cannot be a path
// segment; the generic errors otherwise.
func (c *Client) GetPlan(ctx context.Context, planID string) (*Plan, error) {
	id, err := normalizeRecurringID(planID, "planId must be the plan id CreatePlan returned")
	if err != nil {
		return nil, err
	}

	return retry(ctx, c, c.retryPolicy("GetPlan"), func() (*Plan, error) {
		reply, err := c.send(ctx, http.MethodGet, PlansPath+"/"+id, "", "")
		if err != nil {
			return nil, err
		}
		return reply.plan()
	})
}

// UpdatePlan changes a plan's name, its trial for agreements created from now
// on, or its cancel page, and returns the plan. A nil field keeps its value.
// Amount, currency and interval never change.
//
// The route takes no idempotency key, and the SDK sends none: the same update
// twice leaves the same plan.
//
// Errors it returns: as GetPlan. A field the API refused is an *APIError with
// HTTPStatus 400.
func (c *Client) UpdatePlan(ctx context.Context, planID string, params UpdatePlanParams) (*Plan, error) {
	id, err := normalizeRecurringID(planID, "planId must be the plan id CreatePlan returned")
	if err != nil {
		return nil, err
	}
	body, err := marshalNoEscape(params)
	if err != nil {
		return nil, newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	// PATCH is signed with an EMPTY idempotency key: the route does not use one.
	reply, err := c.send(ctx, http.MethodPatch, PlansPath+"/"+id, string(body), "")
	if err != nil {
		return nil, err
	}
	return reply.plan()
}

// DeletePlan archives a plan: it accepts no new agreements, and the agreements
// it already has continue unchanged. Deleting an archived plan succeeds again,
// so a retry after a timeout is safe. An archived plan stays readable.
//
// Errors it returns: as GetPlan.
func (c *Client) DeletePlan(ctx context.Context, planID string) error {
	id, err := normalizeRecurringID(planID, "planId must be the plan id CreatePlan returned")
	if err != nil {
		return err
	}

	// DELETE signs an EMPTY idempotency key and an EMPTY body, like GET.
	reply, err := c.send(ctx, http.MethodDelete, PlansPath+"/"+id, "", "")
	if err != nil {
		return err
	}
	if reply.status >= 400 {
		return reply.rejection()
	}
	return nil
}

// plan reads a plan route's reply: the plan on a 2xx, the generic errors
// otherwise (a coded 4xx is an *APIError, a 5xx a *TransportError).
func (r *response) plan() (*Plan, error) {
	if r.status >= 400 {
		return nil, r.rejection()
	}
	plan := &Plan{Raw: r.data}
	if !r.parsed || !isJSONObject(r.data) || json.Unmarshal(r.data, plan) != nil || plan.ID == "" {
		return nil, r.apiError("The API returned an unexpected plan object")
	}
	plan.RequestID = r.requestID
	return plan, nil
}

// normalizeRecurringID trims a plan or agreement id and refuses anything that
// would not survive as one path segment of the signed canonical path. The
// case is kept: these ids are case-sensitive.
func normalizeRecurringID(id, message string) (string, error) {
	normalized := strings.TrimSpace(id)
	if !recurringIDPattern.MatchString(normalized) {
		return "", newValidationError(message)
	}
	return normalized, nil
}

package dominaite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Webhook endpoint status values, in the gateway's own order. inactive means
// deliveries are paused: the endpoint was switched off in the dashboard, or
// after repeated failed deliveries. An inactive endpoint does not count toward
// the limit of active endpoints.
const (
	WebhookEndpointStatusActive   = "active"
	WebhookEndpointStatusInactive = "inactive"
)

// WebhookEndpointStatuses is the endpoint status vocabulary, in the gateway's
// order.
var WebhookEndpointStatuses = []string{WebhookEndpointStatusActive, WebhookEndpointStatusInactive}

// CreateWebhookEndpointParams is what CreateWebhookEndpoint sends.
type CreateWebhookEndpointParams struct {
	// URL receives the deliveries: https on a public host, at most 500
	// characters.
	URL string `json:"url"`
	// Events are the event types to deliver, 1 to 50 entries: the Event*
	// constants. Duplicates count once.
	Events []string `json:"events"`

	// IdempotencyKey is required. It is sent as the Idempotency-Key header and
	// signed. Use a fresh key for each new endpoint.
	IdempotencyKey string `json:"-"`
	// Extra holds request fields this struct does not model yet. They are
	// sent as given, and a key in Extra replaces the modelled field of the
	// same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON writes the modelled fields, then the Extra fields.
func (p CreateWebhookEndpointParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params CreateWebhookEndpointParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// WebhookEndpoint is one registered webhook endpoint.
type WebhookEndpoint struct {
	// ID is the endpoint's UUID, lowercase.
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	// Status is one of the WebhookEndpointStatus* values.
	Status string `json:"status"`
	// CreatedAt is ISO 8601 UTC.
	CreatedAt string `json:"createdAt"`
	// Secret is the endpoint's signing secret (whsec_...), for VerifyWebhook.
	// Only the first answer to CreateWebhookEndpoint carries it. A replay of
	// the same key and ListWebhookEndpoints leave it empty, so store it when
	// you receive it. json:"-" keeps it out of structured JSON loggers, and
	// String keeps it out of %v.
	Secret string `json:"-"`

	// Raw is the unparsed endpoint object, for fields this struct does not
	// model yet. On the first answer to CreateWebhookEndpoint it holds the
	// secret too: never log it.
	Raw json.RawMessage `json:"-"`
}

// String keeps the secret out of your logs: printing a WebhookEndpoint with
// %v, %+v or %#v (value or pointer) shows every field except the secret and
// Raw.
func (e WebhookEndpoint) String() string {
	return fmt.Sprintf(
		"dominaite.WebhookEndpoint{ID: %q, URL: %q, Events: %q, Status: %q, CreatedAt: %q, Secret: %s}",
		e.ID, e.URL, e.Events, e.Status, e.CreatedAt, redactSecret(e.Secret),
	)
}

// GoString covers %#v, which does not use String.
func (e WebhookEndpoint) GoString() string { return e.String() }

// CreateWebhookEndpoint registers an endpoint that receives the events you
// name. The answer carries the endpoint's Secret, once: store it, and pass it
// to VerifyWebhook for every delivery. Lost the secret? Delete the endpoint
// and create it again.
//
// The key needs the webhooks:manage scope. A key with the payments scope
// cannot hold it, so use a separate key for webhook endpoints.
//
// params.IdempotencyKey is required and signed. The same key with the same
// URL and events answers the same endpoint without its secret, and registers
// nothing new. After no answer, a timeout or a 503, retry with the SAME key:
// the retry replaces an endpoint the lost attempt registered, and answers a
// new id and a new secret. A retry with a new key cannot do that.
//
// Errors it returns:
//   - *ValidationError: an empty URL, no events, or a bad IdempotencyKey;
//     nothing was sent.
//   - *APIError: a 4xx with ErrorCode, e.g. 400 WEBHOOK_URL_REJECTED, 409
//     WEBHOOK_LIMIT_REACHED, 409 WEBHOOK_ALREADY_REGISTERED (Raw carries the
//     existing endpoint's id at error.params.webhookId), 422
//     IDEMPOTENCY_KEY_REUSED, 409 DUPLICATE_REQUEST (retry with the same key
//     after a second). Nothing was registered.
//   - *AuthError: also 403 INSUFFICIENT_SCOPE for a key without
//     webhooks:manage.
//   - *RateLimitError: as everywhere else.
//   - *TransportError: network failure or 5xx. Retry WITH the same key.
func (c *Client) CreateWebhookEndpoint(ctx context.Context, params CreateWebhookEndpointParams) (*WebhookEndpoint, error) {
	if strings.TrimSpace(params.URL) == "" {
		return nil, newValidationError("Missing required parameter: url")
	}
	if len(params.Events) == 0 {
		return nil, newValidationError("events must name at least one event type")
	}
	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	body, err := marshalNoEscape(params)
	if err != nil {
		return nil, newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	reply, err := c.send(ctx, http.MethodPost, WebhooksPath, string(body), idempotencyKey)
	if err != nil {
		return nil, err
	}
	if reply.status >= 400 {
		return nil, reply.rejection()
	}
	endpoint, ok := decodeWebhookEndpoint(reply.data)
	if !reply.parsed || !ok {
		return nil, newAPIError(reply.status, "The API returned an unexpected webhook endpoint object")
	}
	return endpoint, nil
}

// ListWebhookEndpoints lists your webhook endpoints, newest first: every
// active endpoint and your newest 1000 inactive ones. The list has no pages
// and never carries a secret. The key needs the webhooks:manage scope.
//
// Errors it returns: as CreateWebhookEndpoint, without the validation and
// idempotency rules.
func (c *Client) ListWebhookEndpoints(ctx context.Context) ([]WebhookEndpoint, error) {
	// GET signs an EMPTY idempotency key and an EMPTY body.
	reply, err := c.send(ctx, http.MethodGet, WebhooksPath, "", "")
	if err != nil {
		return nil, err
	}
	if reply.status >= 400 {
		return nil, reply.rejection()
	}
	if !reply.parsed {
		return nil, newAPIError(reply.status, "The API returned a non-JSON response")
	}
	endpoints, err := decodeItems(reply.data, func(endpoint *WebhookEndpoint, raw json.RawMessage) { endpoint.Raw = raw })
	if err != nil {
		return nil, newAPIError(reply.status, "The API returned an unexpected webhook endpoint list")
	}
	return endpoints, nil
}

// DeleteWebhookEndpoint deletes one of your webhook endpoints. It stops
// receiving deliveries at once. The key needs the webhooks:manage scope.
//
// An id that is unknown, not yours or already deleted answers an *APIError
// with HTTPStatus 404 and ErrorCode WEBHOOK_NOT_FOUND. A retry after a lost
// answer gets that 404 too: treat it as done.
//
// Errors it returns: as ListWebhookEndpoints, and a *ValidationError for an
// id that is not a UUID.
func (c *Client) DeleteWebhookEndpoint(ctx context.Context, webhookID string) error {
	id := strings.ToLower(strings.TrimSpace(webhookID))
	if !uuidPattern.MatchString(id) {
		return newValidationError("webhookId must be the endpoint ID that CreateWebhookEndpoint or ListWebhookEndpoints returned")
	}

	// DELETE signs an EMPTY idempotency key and an EMPTY body. The API signs
	// the lowercase hyphenated id, which is what normalization produced.
	reply, err := c.send(ctx, http.MethodDelete, WebhooksPath+"/"+id, "", "")
	if err != nil {
		return err
	}
	if reply.status >= 400 {
		return reply.rejection()
	}
	return nil
}

// decodeWebhookEndpoint reads one endpoint object, its secret included.
func decodeWebhookEndpoint(raw json.RawMessage) (*WebhookEndpoint, bool) {
	if !isJSONObject(raw) {
		return nil, false
	}
	endpoint := &WebhookEndpoint{Raw: raw}
	// Secret is json:"-" so loggers skip it; read it on its own.
	var secret struct {
		Secret string `json:"secret"`
	}
	if json.Unmarshal(raw, endpoint) != nil || json.Unmarshal(raw, &secret) != nil || endpoint.ID == "" {
		return nil, false
	}
	endpoint.Secret = secret.Secret
	return endpoint, true
}

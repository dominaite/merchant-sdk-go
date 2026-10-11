package dominaite

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const (
	testAgreementID  = "agr_0123456789abcdef0123456789ABCDEF"
	testAgreementKey = "subscription-8817"
)

func testAgreement() map[string]any {
	return map[string]any{
		"id":                    testAgreementID,
		"planId":                testPlanID,
		"customerReference":     "customer-8817",
		"storedPaymentMethodId": "pm_0123456789abcdef0123456789abcdef",
		"status":                "past_due",
		"amount":                2500,
		"maxAmountPerPeriod":    5000,
		"currency":              "EUR",
		"intervalUnit":          "month",
		"intervalCount":         1,
		"trialDays":             0,
		"nextChargeAt":          "2026-11-01T09:30:00Z",
		"pastDueReason":         "card_expired",
		"pastDueAt":             "2026-10-03T06:00:00Z",
		"consent": map[string]any{
			"textVersion":    "terms-2026-09",
			"acceptedAt":     "2026-10-01T09:29:00Z",
			"ip":             "203.0.113.7",
			"receivedAt":     "2026-10-01T09:30:00Z",
			"receivedFromIp": "198.51.100.4",
		},
		"version":     1,
		"sequence":    3,
		"createdAt":   "2026-10-01T09:30:00Z",
		"activatedAt": "2026-10-01T09:31:00Z",
	}
}

func testAgreementParams() CreateAgreementParams {
	return CreateAgreementParams{
		PlanID:                testPlanID,
		CustomerReference:     "customer-8817",
		StoredPaymentMethodID: "pm_0123456789abcdef0123456789abcdef",
		Consent: &Consent{
			TextVersion: "terms-2026-09",
			AcceptedAt:  "2026-10-01T09:29:00Z",
			IP:          "203.0.113.7",
		},
		MaxAmountPerPeriod: 5000,
		IdempotencyKey:     testAgreementKey,
	}
}

func TestCreateAgreementSendsTheFieldsAndSignsTheKey(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testAgreement()))
	client := newTestClient(t, server.URL)

	agreement, err := client.CreateAgreement(context.Background(), testAgreementParams())
	if err != nil {
		t.Fatalf("CreateAgreement: %v", err)
	}

	call := calls()[0]
	want := `{"planId":"` + testPlanID + `","customerReference":"customer-8817",` +
		`"storedPaymentMethodId":"pm_0123456789abcdef0123456789abcdef",` +
		`"consent":{"textVersion":"terms-2026-09","acceptedAt":"2026-10-01T09:29:00Z","ip":"203.0.113.7"},` +
		`"maxAmountPerPeriod":5000}`
	if call.Method != http.MethodPost || call.Path != AgreementsPath || call.Body != want {
		t.Fatalf("got %s %s %s\nwant POST %s %s", call.Method, call.Path, call.Body, AgreementsPath, want)
	}
	if call.Header.Get("Idempotency-Key") != testAgreementKey {
		t.Errorf("Idempotency-Key = %q", call.Header.Get("Idempotency-Key"))
	}
	assertSignedWith(t, call, http.MethodPost, AgreementsPath, testAgreementKey)

	if agreement.ID != testAgreementID || agreement.PlanID != testPlanID || agreement.CustomerReference != "customer-8817" {
		t.Errorf("agreement = %+v", agreement)
	}
	if agreement.Status != AgreementStatusPastDue || agreement.PastDueReason != PastDueReasonCardExpired || agreement.PastDueAt != "2026-10-03T06:00:00Z" {
		t.Errorf("past due = %s %s %s", agreement.Status, agreement.PastDueReason, agreement.PastDueAt)
	}
	if agreement.Amount != 2500 || agreement.MaxAmountPerPeriod == nil || *agreement.MaxAmountPerPeriod != 5000 || agreement.Currency != "EUR" {
		t.Errorf("amounts = %d %v %s", agreement.Amount, agreement.MaxAmountPerPeriod, agreement.Currency)
	}
	if agreement.IntervalUnit != IntervalUnitMonth || agreement.IntervalCount != 1 || agreement.PeriodCount != nil || agreement.TrialDays != 0 {
		t.Errorf("interval = %+v", agreement)
	}
	consent := agreement.Consent
	if consent == nil || consent.TextVersion != "terms-2026-09" || consent.IP != "203.0.113.7" || consent.ReceivedFromIP != "198.51.100.4" || consent.ReceivedAt == "" {
		t.Errorf("consent = %+v", consent)
	}
	if agreement.Version != 1 || agreement.Sequence != 3 || agreement.NextChargeAt == "" || agreement.ActivatedAt == "" || agreement.CancelledAt != "" {
		t.Errorf("agreement = %+v", agreement)
	}
	if len(agreement.Raw) == 0 {
		t.Error("Raw must keep the agreement object")
	}
}

// Without a stored card the agreement needs neither consent nor a maximum:
// the subscription session captures both the card and the consent.
func TestCreateAgreementLeavesTheOptionalFieldsOut(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testAgreement()))
	client := newTestClient(t, server.URL)

	_, err := client.CreateAgreement(context.Background(), CreateAgreementParams{
		PlanID:            testPlanID,
		CustomerReference: "customer-8817",
		IdempotencyKey:    testAgreementKey,
		Extra:             map[string]any{"futureField": "a & b"},
	})
	if err != nil {
		t.Fatalf("CreateAgreement: %v", err)
	}
	want := `{"customerReference":"customer-8817","futureField":"a & b","planId":"` + testPlanID + `"}`
	if calls()[0].Body != want {
		t.Fatalf("body = %s\nwant   %s", calls()[0].Body, want)
	}
	assertSignedWith(t, calls()[0], http.MethodPost, AgreementsPath, testAgreementKey)
}

func TestCreateAgreementRefusesBadParamsBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testAgreement()))
	client := newTestClient(t, server.URL)

	noPlan, noCustomer, noKey := testAgreementParams(), testAgreementParams(), testAgreementParams()
	noPlan.PlanID = ""
	noCustomer.CustomerReference = " "
	noKey.IdempotencyKey = ""

	for name, params := range map[string]CreateAgreementParams{
		"missing plan id":            noPlan,
		"missing customer reference": noCustomer,
		"missing idempotency key":    noKey,
	} {
		_, err := client.CreateAgreement(context.Background(), params)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused agreement must never reach the network")
	}
}

// The agreement id is case-sensitive, so it goes into the path and the
// signature exactly as given.
func TestGetAgreementSendsTheIDVerbatim(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testAgreement()))
	client := newTestClient(t, server.URL)

	agreement, err := client.GetAgreement(context.Background(), testAgreementID)
	if err != nil {
		t.Fatalf("GetAgreement: %v", err)
	}

	call := calls()[0]
	path := AgreementsPath + "/" + testAgreementID
	if call.Method != http.MethodGet || call.Path != path || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	assertSignedWith(t, call, http.MethodGet, path, "")
	if agreement.ID != testAgreementID {
		t.Errorf("ID = %q", agreement.ID)
	}
}

func TestCancelAgreementSendsADelete(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	if err := client.CancelAgreement(context.Background(), testAgreementID); err != nil {
		t.Fatalf("CancelAgreement: %v", err)
	}

	call := calls()[0]
	path := AgreementsPath + "/" + testAgreementID
	if call.Method != http.MethodDelete || call.Path != path || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	assertSignedWith(t, call, http.MethodDelete, path, "")
}

func TestAgreementRejectionsAreAPIErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		call   func(*Client) error
	}{
		{"archived plan", http.StatusConflict, ErrorCodePlanNotActive, func(c *Client) error {
			_, err := c.CreateAgreement(context.Background(), testAgreementParams())
			return err
		}},
		{"card in another currency", http.StatusUnprocessableEntity, ErrorCodePaymentMethodCurrencyMismatch, func(c *Client) error {
			_, err := c.CreateAgreement(context.Background(), testAgreementParams())
			return err
		}},
		{"unknown agreement", http.StatusNotFound, ErrorCodeAgreementNotFound, func(c *Client) error {
			_, err := c.GetAgreement(context.Background(), testAgreementID)
			return err
		}},
		{"ended agreement", http.StatusConflict, ErrorCodeAgreementEnded, func(c *Client) error {
			return c.CancelAgreement(context.Background(), testAgreementID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, gatewayError(tc.status, tc.code, "refused", nil))
			err := tc.call(newTestClient(t, server.URL))

			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.HTTPStatus != tc.status || apiErr.ErrorCode != tc.code || len(apiErr.Raw) == 0 {
				t.Fatalf("got %v, want *APIError %d %s with Raw", err, tc.status, tc.code)
			}
		})
	}
}

func TestAgreementIDsMustBeOnePathSegment(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	for _, id := range []string{"", "agr_1/2", "agr_1#x", "agr 1"} {
		err := client.CancelAgreement(context.Background(), id)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%q: got %v, want *ValidationError", id, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused id must never reach the network")
	}
}

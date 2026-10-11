package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

const (
	testPlanID  = "plan_0A1b2C3d4E5f60718293a4b5c6d7e8f9"
	testPlanKey = "plan-gold-quarterly"
)

func testPlan() map[string]any {
	return map[string]any{
		"id":            testPlanID,
		"name":          "Gold",
		"amount":        2500,
		"currency":      "EUR",
		"intervalUnit":  "month",
		"intervalCount": 3,
		"periodCount":   12,
		"trialDays":     14,
		"status":        "active",
		"cancelUrl":     "https://shop.example/account/cancel",
		"createdAt":     "2026-10-01T09:30:00Z",
	}
}

func dataReply(status int, data any) reply {
	return reply{Status: status, Body: map[string]any{"success": true, "data": data}}
}

func intPtr(value int) *int { return &value }

func stringPtr(value string) *string { return &value }

func TestCreatePlanSendsTheFieldsAndSignsTheKey(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testPlan()))
	client := newTestClient(t, server.URL)

	plan, err := client.CreatePlan(context.Background(), CreatePlanParams{
		Name:           "Gold",
		Amount:         2500,
		Currency:       "EUR",
		IntervalUnit:   IntervalUnitMonth,
		IntervalCount:  3,
		PeriodCount:    intPtr(12),
		TrialDays:      14,
		CancelURL:      "https://shop.example/account/cancel",
		IdempotencyKey: testPlanKey,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	call := calls()[0]
	want := `{"name":"Gold","amount":2500,"currency":"EUR","intervalUnit":"month","intervalCount":3,` +
		`"periodCount":12,"trialDays":14,"cancelUrl":"https://shop.example/account/cancel"}`
	if call.Method != http.MethodPost || call.Path != PlansPath || call.Body != want {
		t.Fatalf("got %s %s %s\nwant POST %s %s", call.Method, call.Path, call.Body, PlansPath, want)
	}
	if call.Header.Get("Idempotency-Key") != testPlanKey {
		t.Errorf("Idempotency-Key = %q", call.Header.Get("Idempotency-Key"))
	}
	assertSignedWith(t, call, http.MethodPost, PlansPath, testPlanKey)

	if plan.ID != testPlanID || plan.Name != "Gold" || plan.Amount != 2500 || plan.Currency != "EUR" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.IntervalUnit != IntervalUnitMonth || plan.IntervalCount != 3 || plan.PeriodCount == nil || *plan.PeriodCount != 12 {
		t.Errorf("interval = %s x%d, periods %v", plan.IntervalUnit, plan.IntervalCount, plan.PeriodCount)
	}
	if plan.TrialDays != 14 || plan.Status != PlanStatusActive || plan.CancelURL != "https://shop.example/account/cancel" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.CreatedAt != "2026-10-01T09:30:00Z" || plan.ArchivedAt != "" || len(plan.Raw) == 0 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestCreatePlanLeavesTheOptionalFieldsOut(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testPlan()))
	client := newTestClient(t, server.URL)

	_, err := client.CreatePlan(context.Background(), CreatePlanParams{
		Name:           "Basic",
		Amount:         990,
		Currency:       "EUR",
		IntervalUnit:   IntervalUnitMonth,
		IdempotencyKey: testPlanKey,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if want := `{"name":"Basic","amount":990,"currency":"EUR","intervalUnit":"month"}`; calls()[0].Body != want {
		t.Fatalf("body = %s\nwant   %s", calls()[0].Body, want)
	}
}

func TestCreatePlanExtraFieldsAreSentAndSigned(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testPlan()))
	client := newTestClient(t, server.URL)

	_, err := client.CreatePlan(context.Background(), CreatePlanParams{
		Name:           "Gold",
		Amount:         2500,
		Currency:       "EUR",
		IntervalUnit:   IntervalUnitMonth,
		IdempotencyKey: testPlanKey,
		Extra:          map[string]any{"futureField": "a & b", "name": "from Extra"},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	call := calls()[0]
	body := sentBody(t, call)
	if body["futureField"] != "a & b" || body["name"] != "from Extra" || body["amount"] != float64(2500) {
		t.Errorf("body = %s", call.Body)
	}
	assertSignedWith(t, call, http.MethodPost, PlansPath, testPlanKey)
}

// A replay of the same key answers the plan it created, with 200.
func TestCreatePlanReplayIsThePlan(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusOK, testPlan()))
	client := newTestClient(t, server.URL)

	plan, err := client.CreatePlan(context.Background(), CreatePlanParams{
		Name: "Gold", Amount: 2500, Currency: "EUR", IntervalUnit: IntervalUnitMonth, IdempotencyKey: testPlanKey,
	})
	if err != nil || plan.ID != testPlanID {
		t.Fatalf("got %+v, %v", plan, err)
	}
}

func TestCreatePlanRefusesBadParamsBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testPlan()))
	client := newTestClient(t, server.URL)

	valid := CreatePlanParams{Name: "Gold", Amount: 2500, Currency: "EUR", IntervalUnit: IntervalUnitMonth, IdempotencyKey: testPlanKey}
	noName, zeroAmount, noCurrency, noUnit, noKey := valid, valid, valid, valid, valid
	noName.Name = " "
	zeroAmount.Amount = 0
	noCurrency.Currency = ""
	noUnit.IntervalUnit = ""
	noKey.IdempotencyKey = ""

	for name, params := range map[string]CreatePlanParams{
		"missing name":            noName,
		"zero amount":             zeroAmount,
		"missing currency":        noCurrency,
		"missing interval unit":   noUnit,
		"missing idempotency key": noKey,
	} {
		_, err := client.CreatePlan(context.Background(), params)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused plan must never reach the network")
	}
}

func TestListPlansReadsEveryPlan(t *testing.T) {
	archived := testPlan()
	archived["id"] = "plan_ffffffffffffffffffffffffffffffff"
	archived["status"] = "archived"
	archived["archivedAt"] = "2026-10-05T08:00:00Z"
	delete(archived, "periodCount")
	server, calls := newTestServer(t, dataReply(http.StatusOK, []any{testPlan(), archived}))
	client := newTestClient(t, server.URL)

	plans, err := client.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}

	call := calls()[0]
	if call.Method != http.MethodGet || call.Path != PlansPath || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q and key %q", call.Method, call.Path, call.Body, call.Header.Get("Idempotency-Key"))
	}
	assertSignedWith(t, call, http.MethodGet, PlansPath, "")

	if len(plans) != 2 || plans[0].ID != testPlanID || plans[1].Status != PlanStatusArchived {
		t.Fatalf("plans = %+v", plans)
	}
	if plans[1].PeriodCount != nil || plans[1].ArchivedAt != "2026-10-05T08:00:00Z" {
		t.Errorf("archived plan = %+v", plans[1])
	}
	var raw map[string]any
	if err := json.Unmarshal(plans[1].Raw, &raw); err != nil || raw["id"] != archived["id"] {
		t.Errorf("each plan keeps its own Raw, got %s", plans[1].Raw)
	}
}

func TestListPlansWithNoPlansIsEmpty(t *testing.T) {
	for name, data := range map[string]any{"empty array": []any{}, "null": nil} {
		server, _ := newTestServer(t, dataReply(http.StatusOK, data))
		plans, err := newTestClient(t, server.URL).ListPlans(context.Background())
		if err != nil || len(plans) != 0 {
			t.Errorf("%s: got %v, %v", name, plans, err)
		}
	}
}

// The plan id is case-sensitive, so it goes into the path and the signature
// exactly as given.
func TestGetPlanSendsTheIDVerbatim(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testPlan()))
	client := newTestClient(t, server.URL)

	plan, err := client.GetPlan(context.Background(), " "+testPlanID+" ")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}

	call := calls()[0]
	path := PlansPath + "/" + testPlanID
	if call.Method != http.MethodGet || call.Path != path || call.Body != "" {
		t.Fatalf("got %s %s with body %q, want GET %s", call.Method, call.Path, call.Body, path)
	}
	assertSignedWith(t, call, http.MethodGet, path, "")
	if plan.ID != testPlanID {
		t.Errorf("ID = %q", plan.ID)
	}
}

func TestUpdatePlanSendsOnlyTheGivenFieldsWithoutAKey(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testPlan()))
	client := newTestClient(t, server.URL)

	_, err := client.UpdatePlan(context.Background(), testPlanID, UpdatePlanParams{
		Name:      stringPtr("Gold & more"),
		CancelURL: stringPtr(""),
	})
	if err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	call := calls()[0]
	path := PlansPath + "/" + testPlanID
	if want := `{"name":"Gold & more","cancelUrl":""}`; call.Method != http.MethodPatch || call.Path != path || call.Body != want {
		t.Fatalf("got %s %s %s\nwant PATCH %s %s", call.Method, call.Path, call.Body, path, want)
	}
	if call.Header.Get("Idempotency-Key") != "" {
		t.Error("UpdatePlan sends no Idempotency-Key")
	}
	assertSignedWith(t, call, http.MethodPatch, path, "")
}

func TestUpdatePlanTrialZeroIsSent(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testPlan()))
	client := newTestClient(t, server.URL)

	_, err := client.UpdatePlan(context.Background(), testPlanID, UpdatePlanParams{
		TrialDays: intPtr(0),
		Extra:     map[string]any{"futureField": 1},
	})
	if err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	if want := `{"futureField":1,"trialDays":0}`; calls()[0].Body != want {
		t.Fatalf("body = %s\nwant   %s", calls()[0].Body, want)
	}
}

func TestDeletePlanArchivesIt(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	if err := client.DeletePlan(context.Background(), testPlanID); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}

	call := calls()[0]
	path := PlansPath + "/" + testPlanID
	if call.Method != http.MethodDelete || call.Path != path || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	assertSignedWith(t, call, http.MethodDelete, path, "")
}

func TestPlanRejectionsAreAPIErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		call   func(*Client) error
	}{
		{"unknown plan", http.StatusNotFound, ErrorCodePlanNotFound, func(c *Client) error {
			_, err := c.GetPlan(context.Background(), testPlanID)
			return err
		}},
		{"not a plan id", http.StatusBadRequest, "INVALID_FORMAT", func(c *Client) error {
			return c.DeletePlan(context.Background(), "agr_0123")
		}},
		{"key reused", http.StatusUnprocessableEntity, ErrorCodeIdempotencyKeyReused, func(c *Client) error {
			_, err := c.CreatePlan(context.Background(), CreatePlanParams{
				Name: "Gold", Amount: 2500, Currency: "EUR", IntervalUnit: IntervalUnitMonth, IdempotencyKey: testPlanKey,
			})
			return err
		}},
		{"unknown plan on update", http.StatusNotFound, ErrorCodePlanNotFound, func(c *Client) error {
			_, err := c.UpdatePlan(context.Background(), testPlanID, UpdatePlanParams{Name: stringPtr("Gold")})
			return err
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

func TestPlanUnavailableIsATransportError(t *testing.T) {
	server, _ := newTestServer(t, gatewayError(http.StatusServiceUnavailable, "MERCHANT_API_UNAVAILABLE", "Try again.", nil))
	client := newTestClient(t, server.URL)

	_, err := client.CreatePlan(context.Background(), CreatePlanParams{
		Name: "Gold", Amount: 2500, Currency: "EUR", IntervalUnit: IntervalUnitMonth, IdempotencyKey: testPlanKey,
	})
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
}

func TestPlanIDsMustBeOnePathSegment(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testPlan()))
	client := newTestClient(t, server.URL)

	for _, id := range []string{"", "plan_1/../2", "plan_1?x=1", "plan 1", "plan_1%2F"} {
		_, err := client.GetPlan(context.Background(), id)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%q: got %v, want *ValidationError", id, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused id must never reach the network")
	}
}

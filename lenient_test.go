package dominaite

import (
	"context"
	"net/http"
	"testing"
)

// Every status, retired reason, decline class and event type is a plain
// string. A value this SDK has never seen reaches the caller as it is, and is
// never an error: the API can add a value without a new SDK release.
func TestValuesTheSDKHasNeverSeenAreNotErrors(t *testing.T) {
	t.Run("status read", func(t *testing.T) {
		server, _ := newTestServer(t, dataReply(http.StatusOK, map[string]any{
			"transactionId": testCheckout["transactionId"],
			"status":        "held_for_review",
			"amount":        2500,
			"currency":      "EUR",
			"paymentMethod": "crypto",
			"walletType":    "future_pay",
			"declineClass":  "soft_issuer_offline",
			"storedPaymentMethod": map[string]any{
				"id":            testPaymentMethodID,
				"status":        "suspended",
				"retiredReason": "issuer_recall",
			},
		}))
		status, err := newTestClient(t, server.URL).GetStatus(context.Background(), testCheckout["transactionId"].(string))
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status.Status != "held_for_review" || status.PaymentMethod != "crypto" || status.WalletType != "future_pay" ||
			status.DeclineClass != "soft_issuer_offline" {
			t.Errorf("status = %+v", status)
		}
		card := status.StoredPaymentMethod
		if card == nil || card.Status != "suspended" || card.RetiredReason != "issuer_recall" {
			t.Errorf("storedPaymentMethod = %+v", card)
		}
		if IsPaid(status.Status) || IsTerminal(status.Status) {
			t.Error("an unknown status must read as still open")
		}
	})

	t.Run("charge decline", func(t *testing.T) {
		declined := map[string]any{
			"chargeId":      "ch_33333333333343338333333333333335",
			"status":        "failed",
			"declineClass":  "soft_issuer_offline",
			"transactionId": "33333333-3333-4333-8333-333333333335",
		}
		server, _ := newTestServer(t, chargeEnvelope(http.StatusPaymentRequired, "CHARGE_DECLINED", "Declined.", declined))
		charge, err := newTestClient(t, server.URL).ChargePaymentMethod(context.Background(), testPaymentMethodID, testChargeParams())
		if err != nil || charge.DeclineClass != "soft_issuer_offline" {
			t.Fatalf("got %+v, %v", charge, err)
		}
	})

	t.Run("plan and agreement", func(t *testing.T) {
		plan := testPlan()
		plan["status"] = "paused"
		plan["intervalUnit"] = "fortnight"
		agreement := testAgreement()
		agreement["status"] = "on_hold"
		agreement["pastDueReason"] = "account_frozen"
		server, _ := newTestServer(t, dataReply(http.StatusOK, plan), dataReply(http.StatusOK, agreement))
		client := newTestClient(t, server.URL)

		gotPlan, err := client.GetPlan(context.Background(), testPlanID)
		if err != nil || gotPlan.Status != "paused" || gotPlan.IntervalUnit != "fortnight" {
			t.Fatalf("plan: got %+v, %v", gotPlan, err)
		}
		gotAgreement, err := client.GetAgreement(context.Background(), testAgreementID)
		if err != nil || gotAgreement.Status != "on_hold" || gotAgreement.PastDueReason != "account_frozen" {
			t.Fatalf("agreement: got %+v, %v", gotAgreement, err)
		}
	})

	t.Run("transaction and webhook endpoint", func(t *testing.T) {
		page := testTransactionPage(nil)
		item := page["items"].([]any)[0].(map[string]any)
		item["type"] = "payout"
		item["status"] = "held_for_review"
		endpoint := testWebhookEndpoint(false)
		endpoint["status"] = "suspended"
		server, _ := newTestServer(t, dataReply(http.StatusOK, page), dataReply(http.StatusOK, []any{endpoint}))
		client := newTestClient(t, server.URL)

		list, err := client.ListTransactions(context.Background(), ListTransactionsParams{From: "2026-10-01", To: "2026-10-08"})
		if err != nil || list.Items[0].Type != "payout" || list.Items[0].Status != "held_for_review" {
			t.Fatalf("transactions: got %+v, %v", list, err)
		}
		endpoints, err := client.ListWebhookEndpoints(context.Background())
		if err != nil || endpoints[0].Status != "suspended" {
			t.Fatalf("endpoints: got %v, %v", endpoints, err)
		}
	})

	t.Run("webhook event", func(t *testing.T) {
		event, err := verifySigned(t, `{"id":"evt-1","type":"payout.sent","createdAt":"2026-10-11T09:00:00Z",`+
			`"data":{"transactionId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","status":"settled_elsewhere","declineClass":"soft_issuer_offline"}}`)
		if err != nil {
			t.Fatalf("VerifyWebhook: %v", err)
		}
		if event.Type != "payout.sent" || event.Data.Status != "settled_elsewhere" || event.Data.DeclineClass != "soft_issuer_offline" {
			t.Errorf("event = %+v", event)
		}
	})
}

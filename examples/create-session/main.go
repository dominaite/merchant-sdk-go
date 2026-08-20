// Command create-session mints a Dominaite checkout session and reads its status
// back. It is the runnable version of the README quickstart.
//
//	export DOMINAITE_KEY_ID=dmk_...
//	export DOMINAITE_SECRET=dms_...
//	export DOMINAITE_BASE_URL=https://func-dom-gw-payments-dev-gwc-01.azurewebsites.net/api
//	go run ./examples/create-session
//
// Leave DOMINAITE_BASE_URL unset to hit production.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	dominaite "github.com/dominaite/merchant-sdk-go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := dominaite.New(
		os.Getenv("DOMINAITE_KEY_ID"),
		os.Getenv("DOMINAITE_SECRET"),
		dominaite.WithBaseURL(os.Getenv("DOMINAITE_BASE_URL")), // ignored when empty
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := client.CreateCheckoutSession(ctx, dominaite.CreateCheckoutSessionParams{
		Amount:         2500, // minor units: 2500 = 25.00 EUR
		Currency:       "EUR",
		OrderReference: fmt.Sprintf("sdk-go-smoke-%d", time.Now().Unix()),
		Customer: &dominaite.Customer{
			FirstName: "Ana",
			LastName:  "Kirova",
			Email:     "ana@example.com",
		},
		Language: "bg",
		Theme:    "dark",
	})
	if err != nil {
		var refusal *dominaite.RefusalError
		if errors.As(err, &refusal) {
			return fmt.Errorf("payment unavailable: %s", refusal.ErrorCode)
		}
		return err
	}

	fmt.Printf("transactionId: %s\n", session.TransactionID)
	fmt.Printf("orderId:       %s\n", session.OrderID)
	fmt.Printf("cashierKey:    %s\n", session.CashierKey)
	fmt.Printf("cashierToken:  %s\n", session.CashierToken)
	fmt.Printf("expiresAt:     %s\n", session.ExpiresAt)

	status, err := client.GetStatus(ctx, session.TransactionID)
	if err != nil {
		return err
	}
	fmt.Printf("status:        %s\n", status.Status)

	return nil
}

package dominaite

import "testing"

func TestIsPaidOnlyForSucceeded(t *testing.T) {
	for _, status := range append(append([]string{}, Statuses...), "", "SUCCEEDED", "paid", "captured") {
		if got, want := IsPaid(status), status == StatusSucceeded; got != want {
			t.Errorf("IsPaid(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	want := map[string]bool{
		StatusSucceeded:         true,
		StatusFailed:            true,
		StatusCancelled:         true,
		StatusAbandoned:         true,
		StatusRefunded:          true,
		StatusPartiallyRefunded: true,
		StatusPending:           false,
		StatusProcessing:        false,
		StatusRequiresCapture:   false,
		StatusDisputed:          false,
	}
	if len(want) != len(Statuses) {
		t.Fatalf("the table covers %d statuses, the vocabulary has %d: classify the new one", len(want), len(Statuses))
	}
	for _, status := range Statuses {
		if got := IsTerminal(status); got != want[status] {
			t.Errorf("IsTerminal(%q) = %v, want %v", status, got, want[status])
		}
	}
	for _, unknown := range []string{"", "settled", "SUCCEEDED", "chargeback"} {
		if IsTerminal(unknown) {
			t.Errorf("IsTerminal(%q) = true; an unknown status must stay open", unknown)
		}
	}
}

package dominaite

import (
	"errors"
	"strings"
	"testing"
)

func TestToMinorUnitsConvertsByExponent(t *testing.T) {
	cases := []struct {
		amount   string
		currency string
		want     int64
	}{
		{"0.30", "EUR", 30},
		{"0.3", "EUR", 30},
		{"25", "EUR", 2500},
		{"25.00", "eur", 2500},
		{"19.99", "USD", 1999},
		{"0.01", "GBP", 1},
		{"1234.56", "BGN", 123456},
		{"007.50", "RON", 750},
		{" 12.5 ", "CHF", 1250},
		{"0", "PLN", 0},
		{"12.34", "CAD", 1234},
		{"12.34", "AUD", 1234},
		{"1500", "JPY", 1500},
		{"1500", "HUF", 1500},
		{"1.250", "KWD", 1250},
		{"1.25", "BHD", 1250},
		{"0.001", "KWD", 1},
		{"92233720368547758.07", "EUR", 9223372036854775807},
	}
	for _, tc := range cases {
		got, err := ToMinorUnits(tc.amount, tc.currency)
		if err != nil {
			t.Errorf("ToMinorUnits(%q, %q): %v", tc.amount, tc.currency, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ToMinorUnits(%q, %q) = %d, want %d", tc.amount, tc.currency, got, tc.want)
		}
	}
}

func TestCurrencyExponentCoversTheListedCurrencies(t *testing.T) {
	want := map[int][]string{
		2: {"EUR", "USD", "GBP", "CAD", "AUD", "CHF", "BGN", "RON", "PLN", "CZK", "SEK", "DKK", "NOK"},
		0: {"JPY", "HUF"},
		3: {"BHD", "KWD"},
	}
	for exponent, codes := range want {
		for _, code := range codes {
			got, err := CurrencyExponent(code)
			if err != nil || got != exponent {
				t.Errorf("CurrencyExponent(%s) = %d, %v; want %d", code, got, err, exponent)
			}
		}
	}
}

// HUF follows the gateway (whole forints), not ISO 4217's two decimals: the
// ISO reading would send 100 times the price.
func TestHUFIsWholeForintsLikeTheGateway(t *testing.T) {
	if exponent, err := CurrencyExponent("HUF"); err != nil || exponent != 0 {
		t.Fatalf("CurrencyExponent(HUF) = %d, %v; want 0", exponent, err)
	}
	if _, err := ToMinorUnits("1500.00", "HUF"); err == nil {
		t.Fatal("HUF with decimals must be refused, not read as ISO 4217 fillér")
	}
}

// Where ISO 4217 and the gateway disagree the helper refuses outright instead
// of picking one reading and being off by 10x or 100x.
func TestCurrenciesWithADisputedExponentAreNotSupported(t *testing.T) {
	for _, code := range []string{"ISK", "KRW", "OMR", "JOD", "TND", "isk"} {
		if exponent, err := CurrencyExponent(code); err == nil {
			t.Errorf("CurrencyExponent(%s) = %d, want an error", code, exponent)
		}
		minor, err := ToMinorUnits("10", code)
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("ToMinorUnits(10, %s) = %d, %v; want *ValidationError", code, minor, err)
		} else if !strings.Contains(validation.Message, "not supported") {
			t.Errorf("ToMinorUnits(10, %s) message %q does not say not supported", code, validation.Message)
		}
	}
}

func TestToMinorUnitsRefusesWhatItCannotConvertExactly(t *testing.T) {
	cases := map[string][2]string{
		"too many EUR decimals": {"0.305", "EUR"},
		"extra zero decimals":   {"25.000", "EUR"},
		"any JPY decimals":      {"100.0", "JPY"},
		"any HUF decimals":      {"100.00", "HUF"},
		"too many KWD decimals": {"1.2345", "KWD"},
		"unknown currency":      {"10.00", "XYZ"},
		"malformed currency":    {"10.00", "EURO"},
		"negative":              {"-1.00", "EUR"},
		"plus sign":             {"+1.00", "EUR"},
		"comma decimal":         {"1,00", "EUR"},
		"thousands separator":   {"1,000.00", "EUR"},
		"trailing point":        {"10.", "EUR"},
		"leading point":         {".50", "EUR"},
		"exponent":              {"1e3", "EUR"},
		"empty":                 {"", "EUR"},
		"overflows int64":       {"92233720368547758.08", "EUR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ToMinorUnits(tc[0], tc[1])
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("ToMinorUnits(%q, %q) = %d, %v; want *ValidationError", tc[0], tc[1], got, err)
			}
		})
	}
}

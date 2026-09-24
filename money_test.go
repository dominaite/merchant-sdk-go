package dominaite

import (
	"errors"
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
		{"1500", "JPY", 1500},
		{"1500", "KRW", 1500},
		{"990", "ISK", 990},
		{"1.250", "KWD", 1250},
		{"1.25", "BHD", 1250},
		{"0.001", "OMR", 1},
		{"10", "JOD", 10000},
		{"3.5", "TND", 3500},
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
		2: {"EUR", "USD", "GBP", "BGN", "RON", "CHF", "PLN", "CZK", "HUF", "SEK", "DKK", "NOK"},
		0: {"JPY", "KRW", "ISK"},
		3: {"BHD", "KWD", "OMR", "JOD", "TND"},
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

func TestToMinorUnitsRefusesWhatItCannotConvertExactly(t *testing.T) {
	cases := map[string][2]string{
		"too many EUR decimals": {"0.305", "EUR"},
		"any JPY decimals":      {"100.0", "JPY"},
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

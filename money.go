package dominaite

import (
	"regexp"
	"strconv"
	"strings"
)

// currencyExponents is the minor-unit exponent the Dominaite GATEWAY uses for
// each currency ToMinorUnits knows. It follows the gateway's currency registry,
// not ISO 4217, because the gateway is what reads Amount: HUF is whole forints
// there (0), although ISO 4217 says 2. A currency missing here is an error,
// never a guessed 2: guessing wrong charges 100 times too much or too little.
var currencyExponents = map[string]int{
	"EUR": 2, "USD": 2, "GBP": 2, "CAD": 2, "AUD": 2, "CHF": 2, "BGN": 2,
	"RON": 2, "PLN": 2, "CZK": 2, "SEK": 2, "DKK": 2, "NOK": 2,
	"JPY": 0, "HUF": 0,
	"BHD": 3, "KWD": 3,
}

// unsupportedCurrencies are refused by name: ISO 4217 and the gateway disagree
// on their exponent, so any conversion here would be off by 10x or 100x for
// one of them.
var unsupportedCurrencies = map[string]bool{
	"ISK": true, "KRW": true, "OMR": true, "JOD": true, "TND": true,
}

var decimalAmountPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// CurrencyExponent returns the number of minor-unit digits the gateway uses
// for a currency: 2 for EUR (1 EUR is 100 cents), 0 for JPY and HUF, 3 for KWD.
// HUF differs from ISO 4217 on purpose; see ToMinorUnits. The code is
// case-insensitive. ISK, KRW, OMR, JOD and TND are refused as not supported,
// and an unknown currency is refused too, both with a *ValidationError.
func CurrencyExponent(currency string) (int, error) {
	code, err := normalizeCurrencyCode(currency)
	if err != nil {
		return 0, err
	}
	if unsupportedCurrencies[code] {
		return 0, newValidationError("currency " + code + " is not supported by ToMinorUnits: ISO 4217 and the gateway disagree on its minor unit, convert the amount yourself")
	}
	exponent, ok := currencyExponents[code]
	if !ok {
		return 0, newValidationError("unknown currency " + code + ": no minor-unit exponent on record, convert the amount yourself")
	}
	return exponent, nil
}

// ToMinorUnits converts a decimal amount string to the integer minor units
// Amount takes, by the exponent the gateway uses for the currency:
//
//	ToMinorUnits("0.30", "EUR")  // 30
//	ToMinorUnits("25", "EUR")    // 2500
//	ToMinorUnits("1500", "JPY")  // 1500
//	ToMinorUnits("1500", "HUF")  // 1500: whole forints, NOT ISO 4217's 150000
//	ToMinorUnits("1.250", "KWD") // 1250
//
// Known currencies: EUR, USD, GBP, CAD, AUD, CHF, BGN, RON, PLN, CZK, SEK,
// DKK, NOK (2 decimals), JPY, HUF (0) and BHD, KWD (3).
//
// The conversion is exact, done on the digits, never through a float (19.99
// as a float64 times 100 is 1998.9999999999998, which truncates to 1998). The
// amount is plain digits with an optional point: no sign, no thousands
// separators, no exponent. More fractional digits than the currency has is
// refused, zeros included ("0.305" and "25.000" EUR, "100.0" JPY): nothing is
// rounded or trimmed, because rounding a price is a business decision, not
// the SDK's. Surrounding whitespace is ignored.
//
// Returns a *ValidationError for a malformed amount, an unknown or
// unsupported currency, or an amount too large for int64.
func ToMinorUnits(amount, currency string) (int64, error) {
	exponent, err := CurrencyExponent(currency)
	if err != nil {
		return 0, err
	}

	value := strings.TrimSpace(amount)
	if !decimalAmountPattern.MatchString(value) {
		return 0, newValidationError("amount must be a plain decimal like 25.00, got " + strconv.Quote(amount))
	}

	whole, fraction, _ := strings.Cut(value, ".")
	if len(fraction) > exponent {
		return 0, newValidationError(
			"amount " + strconv.Quote(amount) + " has more decimal places than " +
				strings.ToUpper(strings.TrimSpace(currency)) + " allows (" + strconv.Itoa(exponent) + ")",
		)
	}

	digits := whole + fraction + strings.Repeat("0", exponent-len(fraction))
	minor, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, newValidationError("amount " + strconv.Quote(amount) + " is too large")
	}
	return minor, nil
}

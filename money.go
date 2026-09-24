package dominaite

import (
	"regexp"
	"strconv"
	"strings"
)

// currencyExponents is the ISO 4217 minor-unit exponent of each currency
// ToMinorUnits knows. A currency missing here is an error, never a guessed 2:
// guessing wrong charges 100 times too much or too little.
var currencyExponents = map[string]int{
	"EUR": 2, "USD": 2, "GBP": 2, "BGN": 2, "RON": 2, "CHF": 2,
	"PLN": 2, "CZK": 2, "HUF": 2, "SEK": 2, "DKK": 2, "NOK": 2,
	"JPY": 0, "KRW": 0, "ISK": 0,
	"BHD": 3, "KWD": 3, "OMR": 3, "JOD": 3, "TND": 3,
}

var decimalAmountPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// CurrencyExponent returns the number of minor-unit digits of an ISO 4217
// currency: 2 for EUR (1 EUR is 100 cents), 0 for JPY, 3 for KWD. The code is
// case-insensitive. An unknown currency is a *ValidationError.
func CurrencyExponent(currency string) (int, error) {
	code, err := normalizeCurrencyCode(currency)
	if err != nil {
		return 0, err
	}
	exponent, ok := currencyExponents[code]
	if !ok {
		return 0, newValidationError("unknown currency " + code + ": no minor-unit exponent on record, convert the amount yourself")
	}
	return exponent, nil
}

// ToMinorUnits converts a decimal amount string to the integer minor units
// Amount takes, by the currency's ISO 4217 exponent:
//
//	ToMinorUnits("0.30", "EUR")  // 30
//	ToMinorUnits("25", "EUR")    // 2500
//	ToMinorUnits("1500", "JPY")  // 1500
//	ToMinorUnits("1.250", "KWD") // 1250
//
// The conversion is exact, done on the digits, never through a float (19.99
// as a float64 times 100 is 1998.9999999999998, which truncates to 1998). The
// amount is plain digits with an optional point: no sign, no thousands
// separators, no exponent. More fractional digits than the currency has
// ("0.305" EUR, "100.0" JPY) is refused rather than rounded, because rounding
// a price is a business decision, not the SDK's. Surrounding whitespace is
// ignored.
//
// Returns a *ValidationError for a malformed amount, an unknown currency, or
// an amount too large for int64.
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

package model

import (
	"encoding/json"
	"math"
	"strconv"
)

// Amount is a quantity of loyalty points stored in hundredths (like kopecks)
// to avoid floating point errors. In JSON it is represented as a decimal number.
type Amount int64

// AmountFromFloat converts a decimal number of points to an Amount,
// rounding to the nearest hundredth.
func AmountFromFloat(f float64) Amount {
	return Amount(math.Round(f * 100))
}

// Float returns the amount as a decimal number of points.
func (a Amount) Float() float64 {
	return float64(a) / 100
}

// MarshalJSON encodes the amount as a decimal number.
func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(a.Float(), 'f', -1, 64)), nil
}

// UnmarshalJSON decodes the amount from a decimal number.
func (a *Amount) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*a = AmountFromFloat(f)
	return nil
}

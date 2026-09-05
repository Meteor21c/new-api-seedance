package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAmountDiscountUsesMinimumAmountTiers(t *testing.T) {
	originalDiscounts := make(map[int]float64, len(paymentSetting.AmountDiscount))
	for threshold, rate := range paymentSetting.AmountDiscount {
		originalDiscounts[threshold] = rate
	}
	t.Cleanup(func() { paymentSetting.AmountDiscount = originalDiscounts })

	paymentSetting.AmountDiscount = map[int]float64{
		10:  1.01,
		50:  1,
		100: 0.98,
		500: 0.9,
	}

	testCases := []struct {
		name     string
		amount   float64
		expected float64
	}{
		{name: "below first tier", amount: 9.99, expected: 1},
		{name: "fee tier", amount: 10, expected: 1.01},
		{name: "fee tier upper bound", amount: 49.99, expected: 1.01},
		{name: "no fee tier", amount: 50, expected: 1},
		{name: "no fee tier upper bound", amount: 99.99, expected: 1},
		{name: "discount tier", amount: 100, expected: 0.98},
		{name: "discount tier upper bound", amount: 499, expected: 0.98},
		{name: "highest tier", amount: 500, expected: 0.9},
		{name: "highest tier above threshold", amount: 10000, expected: 0.9},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, tc.expected, GetAmountDiscount(tc.amount), 0.000001)
		})
	}
}

func TestGetAmountDiscountIgnoresInvalidRates(t *testing.T) {
	originalDiscounts := make(map[int]float64, len(paymentSetting.AmountDiscount))
	for threshold, rate := range paymentSetting.AmountDiscount {
		originalDiscounts[threshold] = rate
	}
	t.Cleanup(func() { paymentSetting.AmountDiscount = originalDiscounts })

	paymentSetting.AmountDiscount = map[int]float64{
		10:  0.9,
		50:  0,
		100: -1,
	}

	require.InDelta(t, 0.9, GetAmountDiscount(100), 0.000001)
	require.InDelta(t, 1, GetAmountDiscount(5), 0.000001)
}

package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPT6AstraBuiltinBilling(t *testing.T) {
	require.Equal(t, BillingModeTieredExpr, GetBillingMode("gpt-6-astra"))
	expression, ok := GetBillingExpr("gpt-6-astra")
	require.True(t, ok)

	standard, trace, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{P: 1000, C: 100, Len: 1000})
	require.NoError(t, err)
	assert.Equal(t, "standard", trace.MatchedTier)
	assert.Equal(t, float64(15000), standard)

	longContext, trace, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{P: 272001, C: 100, Len: 272001})
	require.NoError(t, err)
	assert.Equal(t, "long_context", trace.MatchedTier)
	assert.Equal(t, float64(5447520), longContext)

	modes := GetBillingModeCopy()
	exprs := GetBillingExprCopy()
	assert.Equal(t, BillingModeTieredExpr, modes["gpt-6-astra"])
	assert.Equal(t, expression, exprs["gpt-6-astra"])
}

func TestGPT6AstraExplicitModeOverridesBuiltin(t *testing.T) {
	originalMode, hadMode := billingSetting.BillingMode["gpt-6-astra"]
	originalExpr, hadExpr := billingSetting.BillingExpr["gpt-6-astra"]
	t.Cleanup(func() {
		if hadMode {
			billingSetting.BillingMode["gpt-6-astra"] = originalMode
		} else {
			delete(billingSetting.BillingMode, "gpt-6-astra")
		}
		if hadExpr {
			billingSetting.BillingExpr["gpt-6-astra"] = originalExpr
		} else {
			delete(billingSetting.BillingExpr, "gpt-6-astra")
		}
	})

	billingSetting.BillingMode["gpt-6-astra"] = BillingModeRatio
	delete(billingSetting.BillingExpr, "gpt-6-astra")
	assert.Equal(t, BillingModeRatio, GetBillingMode("gpt-6-astra"))
	_, ok := GetBillingExpr("gpt-6-astra")
	assert.False(t, ok)

	billingSetting.BillingMode["gpt-6-astra"] = BillingModeTieredExpr
	billingSetting.BillingExpr["gpt-6-astra"] = `tier("custom", p * 7)`
	expression, ok := GetBillingExpr("gpt-6-astra")
	require.True(t, ok)
	assert.Equal(t, `tier("custom", p * 7)`, expression)
}

func TestBuiltinBillingDoesNotChangeUnrelatedModels(t *testing.T) {
	assert.Equal(t, BillingModeRatio, GetBillingMode("gpt-5.6-sol"))
	_, ok := GetBillingExpr("gpt-5.6-sol")
	assert.False(t, ok)
}

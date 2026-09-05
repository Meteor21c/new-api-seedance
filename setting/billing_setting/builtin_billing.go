package billing_setting

// Built-in token prices are expressed as USD per million tokens. They are
// fallback defaults only: administrator-configured model prices, ratios, or
// billing expressions always take precedence.
var builtinBillingExpr = map[string]string{
	// GPT-6 Astra standard pricing; the long-context rate applies to the whole
	// request once the context exceeds the boundary.
	"gpt-6-astra": `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25)`,
}

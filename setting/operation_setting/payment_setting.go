package operation_setting

import (
	"math"

	"github.com/QuantumNous/new-api/setting/config"
)

type PaymentSetting struct {
	AmountOptions  []int           `json:"amount_options"`
	// AmountDiscount maps the minimum recharge amount for a tier to its price
	// multiplier. For example, {"10": 1.01, "50": 1, "100": 0.98}
	// charges a 1% fee from 10, no fee from 50, and gives 2% off from 100.
	AmountDiscount map[int]float64 `json:"amount_discount"`

	ComplianceConfirmed    bool   `json:"compliance_confirmed"`
	ComplianceTermsVersion string `json:"compliance_terms_version"`
	ComplianceConfirmedAt  int64  `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy  int    `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP  string `json:"compliance_confirmed_ip"`
}

const CurrentComplianceTermsVersion = "v1"

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:  []int{10, 20, 50, 100, 200, 500},
	AmountDiscount: map[int]float64{
		10:  1.01,
		50:  1,
		100: 0.98,
		500: 0.9,
	},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

// GetAmountDiscount returns the multiplier for the highest configured tier
// whose minimum amount is no greater than amount. A missing or invalid tier
// falls back to 1 (no adjustment).
func GetAmountDiscount(amount float64) float64 {
	if !math.IsNaN(amount) && !math.IsInf(amount, 0) {
		matchedThreshold := math.Inf(-1)
		matchedRate := 1.0
		for threshold, rate := range paymentSetting.AmountDiscount {
			if float64(threshold) > amount || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
				continue
			}
			if float64(threshold) >= matchedThreshold {
				matchedThreshold = float64(threshold)
				matchedRate = rate
			}
		}
		return matchedRate
	}
	return 1.0
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}

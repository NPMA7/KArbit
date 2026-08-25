package risk

// FeeModel manages transaction fee calculations for maker and taker orders.
type FeeModel struct {
	TakerFeeRate   float64
	MakerFeeRate   float64
	UseBNBDiscount bool
}

// NewFeeModel initializes fee configuration.
func NewFeeModel(feeRate float64, useBNBDiscount bool) *FeeModel {
	if feeRate <= 0 {
		if useBNBDiscount {
			feeRate = 0.00075 // 0.075% BNB discount
		} else {
			feeRate = 0.001 // 0.1% standard Binance taker fee
		}
	}

	return &FeeModel{
		TakerFeeRate:   feeRate,
		MakerFeeRate:   feeRate,
		UseBNBDiscount: useBNBDiscount,
	}
}

// CalculateFee returns the deduction amount on a trade volume.
func (f *FeeModel) CalculateFee(amount float64) float64 {
	return amount * f.TakerFeeRate
}

// CalculateNetAfterFee returns the remaining volume after 1 trade leg.
func (f *FeeModel) CalculateNetAfterFee(amount float64) float64 {
	return amount * (1.0 - f.TakerFeeRate)
}

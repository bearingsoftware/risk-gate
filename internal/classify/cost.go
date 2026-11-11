package classify

import (
	"math"

	"github.com/bearingsoftware/risk-gate/internal/decision"
)

type price struct{ in, out, cacheRead, cacheWrite float64 }

// Dollars per million tokens, keyed by the model the response reports.
var prices = map[string]price{
	"claude-sonnet-5-5": {in: 2, out: 10, cacheRead: 0.10, cacheWrite: 2.50},
}

type Usage = decision.AIUsage

// Cost is nil for a model without a price.
func Cost(model string, u Usage) *float64 {
	p, ok := prices[model]
	if !ok {
		return nil
	}
	usd := (float64(u.InputTokens)*p.in + float64(u.OutputTokens)*p.out +
		float64(u.CacheReadInputTokens)*p.cacheRead + float64(u.CacheCreationInputTokens)*p.cacheWrite) / 1e6
	usd = math.Round(usd*1e6) / 1e6
	return &usd
}

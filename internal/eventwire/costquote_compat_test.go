package eventwire

import (
	"encoding/json"
	"testing"
	"time"

	"deepzora/internal/billing"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

// Usage exposes the current quote and occurrence-time schedule metadata.
func TestToWireUsageCostQuoteScheduleMetadata(t *testing.T) {
	e := event.Event{
		Kind:     event.Usage,
		ModelRef: "deepseek-flash/deepseek-v4-flash",
		Usage:    &provider.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000},
		Pricing:  &provider.Pricing{CacheHit: 0.10, Input: 3, Output: 9, Currency: "¥"},
		CostQuote: func() *billing.CostQuote {
			q := billing.BuildQuote(billing.QuoteInput{
				Usage:        billing.UsageTokens{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
				Rates:        billing.RateCard{CacheHit: 0.10, Input: 3, Output: 9, Currency: "CNY"},
				OccurredAt:   time.Date(2026, 8, 17, 0, 30, 0, 0, time.UTC),
				Display:      billing.DisplayRequest{Currency: "USD", Source: billing.DisplaySourceExplicit},
				ProviderKind: "deepseek",
				ModelID:      "deepseek-v4-flash",
				ScheduleID:   billing.ScheduleDeepSeekV4August2026,
			})
			return &q
		}(),
	}
	w := ToWire(e)
	if w.Usage == nil || w.Usage.CostQuote == nil {
		t.Fatal("missing costQuote")
	}
	if w.Usage.CostQuote.Valuations["USD"].Basis != billing.BasisOfficialTable {
		t.Fatalf("USD basis = %q, want official_table", w.Usage.CostQuote.Valuations["USD"].Basis)
	}
	if w.Usage.Cost <= 0 {
		t.Fatalf("selected cost = %v", w.Usage.Cost)
	}
	raw, err := json.Marshal(w.Usage)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("usage json invalid")
	}
	// Old clients ignore unknown fields; new field present.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["costQuote"]; !ok {
		t.Fatalf("costQuote missing from JSON: %s", raw)
	}
	if _, ok := m["cost"]; !ok {
		t.Fatalf("legacy cost missing: %s", raw)
	}
	quoteJSON, _ := m["costQuote"].(map[string]any)
	if quoteJSON["rateBand"] != billing.RateBandOffPeak || quoteJSON["ratedAt"] != "2026-08-17T00:30:00Z" {
		t.Fatalf("scheduled quote metadata missing: %s", raw)
	}
}

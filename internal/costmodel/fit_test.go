package costmodel

import "testing"

func TestFitPrefill(t *testing.T) {
	fit := FitPrefill([]CalibrationSample{
		{Kind: "prefill", InputTokens: 100, TTFTMS: 60, Valid: true},
		{Kind: "prefill", InputTokens: 200, TTFTMS: 110, Valid: true},
		{Kind: "decode", InputTokens: 200, TTFTMS: 999, Valid: true},
	})
	if fit.SampleCount != 2 || fit.Slope != 0.5 || fit.Intercept != 10 {
		t.Fatalf("fit=%+v", fit)
	}
}

func TestFitDecodeUsesActualCompletionTokens(t *testing.T) {
	fit := FitDecode([]CalibrationSample{
		{Kind: "decode", CompletionTokens: 10, TTFTMS: 20, LatencyMS: 50, Valid: true},
		{Kind: "decode", CompletionTokens: 20, TTFTMS: 20, LatencyMS: 80, Valid: true},
		{Kind: "decode", CompletionTokens: 0, TTFTMS: 20, LatencyMS: 80, Valid: true},
	})
	if fit.SampleCount != 2 || fit.Slope != 3 || fit.Intercept != 0 {
		t.Fatalf("fit=%+v", fit)
	}
}

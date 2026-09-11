package costmodel

import "math"

type CalibrationSample struct {
	WorkerID         string  `json:"worker_id,omitempty"`
	Kind             string  `json:"kind"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TTFTMS           float64 `json:"ttft_ms,omitempty"`
	LatencyMS        float64 `json:"latency_ms,omitempty"`
	Valid            bool    `json:"valid"`
}

type LinearFit struct {
	Slope       float64 `json:"slope"`
	Intercept   float64 `json:"intercept"`
	RSquared    float64 `json:"r_squared"`
	SampleCount int     `json:"sample_count"`
}

func FitPrefill(samples []CalibrationSample) LinearFit {
	xs, ys := []float64{}, []float64{}
	for _, sample := range samples {
		if sample.Valid && sample.Kind == "prefill" && sample.InputTokens > 0 && sample.TTFTMS > 0 {
			xs = append(xs, float64(sample.InputTokens))
			ys = append(ys, sample.TTFTMS)
		}
	}
	return linear(xs, ys)
}

func FitDecode(samples []CalibrationSample) LinearFit {
	xs, ys := []float64{}, []float64{}
	for _, sample := range samples {
		if sample.Valid && sample.Kind == "decode" && sample.CompletionTokens > 0 && sample.LatencyMS > sample.TTFTMS && sample.TTFTMS > 0 {
			xs = append(xs, float64(sample.CompletionTokens))
			ys = append(ys, sample.LatencyMS-sample.TTFTMS)
		}
	}
	return linear(xs, ys)
}

func ValidPrefillTokenRange(samples []CalibrationSample) [2]int {
	var result [2]int
	for _, sample := range samples {
		if !sample.Valid || sample.Kind != "prefill" || sample.InputTokens <= 0 || sample.TTFTMS <= 0 {
			continue
		}
		if result[0] == 0 || sample.InputTokens < result[0] {
			result[0] = sample.InputTokens
		}
		if sample.InputTokens > result[1] {
			result[1] = sample.InputTokens
		}
	}
	return result
}

func linear(xs, ys []float64) LinearFit {
	if len(xs) == 0 || len(xs) != len(ys) {
		return LinearFit{}
	}
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX, meanY := sumX/float64(len(xs)), sumY/float64(len(ys))
	var ssXX, ssXY, ssTot float64
	for i := range xs {
		dx := xs[i] - meanX
		dy := ys[i] - meanY
		ssXX += dx * dx
		ssXY += dx * dy
		ssTot += dy * dy
	}
	slope := 0.0
	if ssXX > 0 {
		slope = ssXY / ssXX
	}
	intercept := meanY - slope*meanX
	var ssRes float64
	for i := range xs {
		residual := ys[i] - (intercept + slope*xs[i])
		ssRes += residual * residual
	}
	r2 := 1.0
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	if math.IsNaN(r2) || math.IsInf(r2, 0) {
		r2 = 0
	}
	return LinearFit{Slope: slope, Intercept: intercept, RSquared: r2, SampleCount: len(xs)}
}

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"distserve/internal/costmodel"
)

func main() {
	input := flag.String("input", "", "JSONL calibration samples")
	output := flag.String("output", "", "cost profile output JSON")
	modelIdentityHash := flag.String("model-identity-hash", "", "expected cache identity hash")
	hardwareClass := flag.String("hardware-class", "unknown", "worker hardware class")
	shadowConfidence := flag.Float64("shadow-confidence", 0.5, "confidence for shadow cache affinity")
	allowEmptyIdentity := flag.Bool("allow-empty-identity", false, "allow an empty model identity hash for tests only")
	minimumRSquared := flag.Float64("minimum-r-squared", 0.9, "warn when a fitted model is below this R-squared")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "-input and -output are required")
		os.Exit(2)
	}
	if *modelIdentityHash == "" && !*allowEmptyIdentity {
		fmt.Fprintln(os.Stderr, "-model-identity-hash is required (use -allow-empty-identity only for tests)")
		os.Exit(2)
	}
	samples, err := readSamples(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	prefill := costmodel.FitPrefill(samples)
	decode := costmodel.FitDecode(samples)
	if prefill.SampleCount == 0 {
		fmt.Fprintln(os.Stderr, "no valid prefill calibration samples")
		os.Exit(1)
	}
	if decode.SampleCount == 0 {
		fmt.Fprintln(os.Stderr, "no valid decode calibration samples")
		os.Exit(1)
	}
	if prefill.RSquared < *minimumRSquared {
		fmt.Fprintf(os.Stderr, "warning: prefill R-squared %.4f is below %.4f\n", prefill.RSquared, *minimumRSquared)
	}
	if decode.RSquared < *minimumRSquared {
		fmt.Fprintf(os.Stderr, "warning: decode R-squared %.4f is below %.4f\n", decode.RSquared, *minimumRSquared)
	}
	profile := costmodel.DefaultProfile()
	profile.ModelIdentityHash = *modelIdentityHash
	profile.HardwareClass = *hardwareClass
	profile.PrefillFixedMS = maxZero(prefill.Intercept)
	profile.PrefillMSPerToken = maxZero(prefill.Slope)
	profile.DecodeFixedMS = maxZero(decode.Intercept)
	profile.DecodeMSPerToken = maxZero(decode.Slope)
	profile.ShadowConfidence = *shadowConfidence
	profile.SampleCount = prefill.SampleCount + decode.SampleCount
	profile.PrefillSampleCount = prefill.SampleCount
	profile.DecodeSampleCount = decode.SampleCount
	profile.PrefillRSquared = prefill.RSquared
	profile.DecodeRSquared = decode.RSquared
	profile.PrefillInterceptClipped = prefill.Intercept < 0
	profile.DecodeInterceptClipped = decode.Intercept < 0
	profile.PrefillSource = costmodel.SourceCalibrated
	profile.DecodeSource = costmodel.SourceCalibrated
	profile.QueueSource = costmodel.SourceDefault
	profile.CalibratedAt = time.Now().UTC()
	profile.ValidTokenRange = costmodel.ValidPrefillTokenRange(samples)
	profile.Source = costmodel.SourceCalibrated
	if err := profile.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, err := json.MarshalIndent(struct {
		costmodel.Profile
		PrefillFit costmodel.LinearFit `json:"prefill_fit"`
		DecodeFit  costmodel.LinearFit `json:"decode_fit"`
	}{Profile: profile, PrefillFit: prefill, DecodeFit: decode}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, append(raw, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readSamples(path string) ([]costmodel.CalibrationSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var samples []costmodel.CalibrationSample
	for scanner.Scan() {
		var sample costmodel.CalibrationSample
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, scanner.Err()
}

func maxZero(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

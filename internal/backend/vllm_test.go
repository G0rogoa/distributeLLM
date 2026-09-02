package backend_test

import (
	"strings"
	"testing"

	"distserve/internal/backend"
)

func TestParseVLLMMetricsUsesOptionalValues(t *testing.T) {
	load := backend.ParseVLLMMetrics(strings.NewReader(`
# HELP vllm:num_requests_running Running requests.
vllm:num_requests_running 2
vllm:num_requests_waiting 3
vllm:gpu_cache_usage_perc 0.42
vllm:prefix_cache_hits_total 11
`))
	if !load.RunningRequests.Valid || load.RunningRequests.Value != 2 {
		t.Fatalf("running=%+v", load.RunningRequests)
	}
	if !load.WaitingRequests.Valid || load.WaitingRequests.Value != 3 {
		t.Fatalf("waiting=%+v", load.WaitingRequests)
	}
	if !load.GPUKVCacheUsageRatio.Valid || load.GPUKVCacheUsageRatio.Value != 0.42 {
		t.Fatalf("gpu cache=%+v", load.GPUKVCacheUsageRatio)
	}
	if !load.PrefixCacheHitsTotal.Valid || load.PrefixCacheHitsTotal.Value != 11 {
		t.Fatalf("hits=%+v", load.PrefixCacheHitsTotal)
	}
	if load.PrefixCacheMissesTotal.Valid {
		t.Fatalf("missing metric should be unknown: %+v", load.PrefixCacheMissesTotal)
	}
}

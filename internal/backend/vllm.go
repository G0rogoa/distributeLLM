package backend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"distserve/internal/api"
	"distserve/internal/registry"
)

type VLLM struct {
	OpenAIHTTP
}

func (b VLLM) Metrics(ctx context.Context, worker registry.WorkerSnapshot) (api.WorkerLoadSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(worker.Address, "/")+"/metrics", nil)
	if err != nil {
		return api.WorkerLoadSnapshot{}, err
	}
	resp, err := httpClient(b.Client).Do(request)
	if err != nil {
		return api.WorkerLoadSnapshot{}, classifyDoError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.WorkerLoadSnapshot{}, fmt.Errorf("metrics status: %s", resp.Status)
	}
	load := ParseVLLMMetrics(resp.Body)
	load.Healthy = true
	return load, nil
}

func ParseVLLMMetrics(reader io.Reader) api.WorkerLoadSnapshot {
	var load api.WorkerLoadSnapshot
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		name, value, ok := parsePrometheusSample(scanner.Text())
		if !ok {
			continue
		}
		switch name {
		case "vllm:num_requests_running", "vllm_num_requests_running":
			load.RunningRequests = api.OptionalInt64{Value: int64(value), Valid: true}
		case "vllm:num_requests_waiting", "vllm_num_requests_waiting":
			load.WaitingRequests = api.OptionalInt64{Value: int64(value), Valid: true}
		case "vllm:gpu_cache_usage_perc", "vllm_gpu_cache_usage_perc":
			load.GPUKVCacheUsageRatio = api.OptionalFloat64{Value: value, Valid: true}
		case "vllm:prefix_cache_hits", "vllm_prefix_cache_hits_total", "vllm:prefix_cache_hits_total":
			load.PrefixCacheHitsTotal = api.OptionalInt64{Value: int64(value), Valid: true}
		case "vllm:prefix_cache_misses", "vllm_prefix_cache_misses_total", "vllm:prefix_cache_misses_total":
			load.PrefixCacheMissesTotal = api.OptionalInt64{Value: int64(value), Valid: true}
		}
	}
	return load
}

func parsePrometheusSample(line string) (string, float64, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", 0, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, false
	}
	name := fields[0]
	if brace := strings.IndexByte(name, '{'); brace >= 0 {
		name = name[:brace]
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return "", 0, false
	}
	return name, value, true
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"distserve/internal/api"
)

type result struct {
	Status              int
	Latency, TTFT, TPOT time.Duration
	Err                 string
}
type summary struct {
	Requests    int         `json:"requests"`
	SuccessRate float64     `json:"success_rate"`
	Throughput  float64     `json:"throughput_rps"`
	MeanMS      float64     `json:"mean_latency_ms"`
	P50MS       float64     `json:"p50_latency_ms"`
	P95MS       float64     `json:"p95_latency_ms"`
	P99MS       float64     `json:"p99_latency_ms"`
	TTFTP50MS   float64     `json:"ttft_p50_ms"`
	TTFTP95MS   float64     `json:"ttft_p95_ms"`
	TTFTP99MS   float64     `json:"ttft_p99_ms"`
	TPOTP50MS   float64     `json:"tpot_p50_ms"`
	TPOTP95MS   float64     `json:"tpot_p95_ms"`
	TPOTP99MS   float64     `json:"tpot_p99_ms"`
	Status      map[int]int `json:"http_status"`
	Errors      int         `json:"errors"`
	Rejections  int         `json:"rejections"`
}
type job struct{ InputTokens, OutputTokens int }

func main() {
	target := flag.String("target", "http://127.0.0.1:8080", "controller base URL")
	concurrency := flag.Int("concurrency", 8, "concurrent clients")
	requests := flag.Int("requests", 100, "request count; ignored when duration is set")
	duration := flag.Duration("duration", 0, "generation duration")
	arrival := flag.String("arrival", "fixed-concurrency", "fixed-concurrency, fixed-rate, or burst")
	rate := flag.Float64("rate", 10, "requests per second for fixed-rate")
	burst := flag.Int("burst-size", 20, "requests per burst")
	stream := flag.Bool("stream", true, "request streaming responses")
	inputMin := flag.Int("input-min", 16, "minimum approximate input tokens")
	inputMax := flag.Int("input-max", 64, "maximum approximate input tokens")
	outputMin := flag.Int("output-min", 16, "minimum output tokens")
	outputMax := flag.Int("output-max", 64, "maximum output tokens")
	seed := flag.Int64("seed", 1, "random seed")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	format := flag.String("format", "json", "json or csv")
	flag.Parse()
	if *concurrency < 1 || *rate <= 0 || *inputMin < 1 || *inputMax < *inputMin || *outputMin < 1 || *outputMax < *outputMin {
		fmt.Fprintln(os.Stderr, "invalid load parameters")
		os.Exit(2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan job, *concurrency*4)
	results := make(chan result)
	client := &http.Client{}
	var workers sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				results <- run(ctx, client, *target, *stream, *timeout, item)
			}
		}()
	}
	started := time.Now()
	go produce(ctx, jobs, *arrival, *requests, *duration, *rate, *burst, *inputMin, *inputMax, *outputMin, *outputMax, *seed)
	go func() { workers.Wait(); close(results) }()
	all := []result{}
	for item := range results {
		all = append(all, item)
	}
	elapsed := time.Since(started)
	value := summarize(all, elapsed)
	if *format == "csv" {
		writeCSV(value)
	} else {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
	}
}

func produce(ctx context.Context, jobs chan<- job, arrival string, count int, duration time.Duration, rate float64, burst, inputMin, inputMax, outputMin, outputMax int, seed int64) {
	defer close(jobs)
	random := rand.New(rand.NewSource(seed))
	deadline := time.Time{}
	if duration > 0 {
		deadline = time.Now().Add(duration)
	}
	sent := 0
	for (duration > 0 && time.Now().Before(deadline)) || (duration <= 0 && sent < count) {
		batch := 1
		if arrival == "burst" {
			batch = burst
		}
		for i := 0; i < batch && ((duration > 0 && time.Now().Before(deadline)) || (duration <= 0 && sent < count)); i++ {
			item := job{inputMin + random.Intn(inputMax-inputMin+1), outputMin + random.Intn(outputMax-outputMin+1)}
			select {
			case jobs <- item:
				sent++
			case <-ctx.Done():
				return
			}
		}
		var wait time.Duration
		switch arrival {
		case "fixed-rate":
			wait = time.Duration(float64(time.Second) / rate)
		case "burst":
			wait = time.Second
		case "fixed-concurrency":
			continue
		default:
			return
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func run(parent context.Context, client *http.Client, target string, stream bool, timeout time.Duration, item job) result {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	input := api.ChatCompletionRequest{Model: "mock-llm", Messages: []api.Message{{Role: "user", Content: strings.Repeat("word ", item.InputTokens)}}, MaxTokens: item.OutputTokens, Stream: stream}
	body, _ := json.Marshal(input)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+"/v1/chat/completions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return result{Err: err.Error(), Latency: time.Since(started)}
	}
	defer response.Body.Close()
	value := result{Status: response.StatusCode}
	if stream && response.StatusCode == http.StatusOK {
		reader := bufio.NewReader(response.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if value.TTFT == 0 && strings.HasPrefix(line, "data:") && !strings.Contains(line, "[DONE]") {
				value.TTFT = time.Since(started)
			}
			if readErr != nil {
				if readErr != io.EOF {
					value.Err = readErr.Error()
				}
				break
			}
		}
	} else {
		_, err = io.Copy(io.Discard, response.Body)
		if err != nil {
			value.Err = err.Error()
		}
	}
	value.Latency = time.Since(started)
	if item.OutputTokens > 1 && value.TTFT > 0 {
		value.TPOT = (value.Latency - value.TTFT) / time.Duration(item.OutputTokens-1)
	}
	return value
}

func summarize(results []result, elapsed time.Duration) summary {
	value := summary{Requests: len(results), Status: map[int]int{}}
	latencies, ttfts, tpots := []time.Duration{}, []time.Duration{}, []time.Duration{}
	var total time.Duration
	success := 0
	for _, item := range results {
		value.Status[item.Status]++
		if item.Err != "" {
			value.Errors++
		}
		if item.Status == 429 || item.Status == 503 {
			value.Rejections++
		}
		if item.Status >= 200 && item.Status < 300 && item.Err == "" {
			success++
		}
		latencies = append(latencies, item.Latency)
		total += item.Latency
		if item.TTFT > 0 {
			ttfts = append(ttfts, item.TTFT)
		}
		if item.TPOT > 0 {
			tpots = append(tpots, item.TPOT)
		}
	}
	if len(results) > 0 {
		value.SuccessRate = float64(success) / float64(len(results))
		value.MeanMS = float64(total) / float64(len(results)) / float64(time.Millisecond)
	}
	if elapsed > 0 {
		value.Throughput = float64(success) / elapsed.Seconds()
	}
	value.P50MS, value.P95MS, value.P99MS = pct(latencies, .5), pct(latencies, .95), pct(latencies, .99)
	value.TTFTP50MS, value.TTFTP95MS, value.TTFTP99MS = pct(ttfts, .5), pct(ttfts, .95), pct(ttfts, .99)
	value.TPOTP50MS, value.TPOTP95MS, value.TPOTP99MS = pct(tpots, .5), pct(tpots, .95), pct(tpots, .99)
	return value
}
func pct(values []time.Duration, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1) * p)
	return float64(values[index]) / float64(time.Millisecond)
}
func writeCSV(value summary) {
	writer := csv.NewWriter(os.Stdout)
	_ = writer.Write([]string{"requests", "success_rate", "throughput_rps", "mean_ms", "p50_ms", "p95_ms", "p99_ms", "errors", "rejections"})
	_ = writer.Write([]string{strconv.Itoa(value.Requests), fmt.Sprint(value.SuccessRate), fmt.Sprint(value.Throughput), fmt.Sprint(value.MeanMS), fmt.Sprint(value.P50MS), fmt.Sprint(value.P95MS), fmt.Sprint(value.P99MS), strconv.Itoa(value.Errors), strconv.Itoa(value.Rejections)})
	writer.Flush()
}

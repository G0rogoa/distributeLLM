package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadWorkloadJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workload.jsonl")
	data := `{"id":"a","prompt":"hello","output_tokens":2}` + "\n" + `{"input_tokens":4,"output_tokens":1}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs, err := readWorkload(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[1].ID != "line-2" || jobs[1].InputTokens != 4 {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestWriteResultsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	results := []result{{JobID: "a", Group: "hot", RequestID: "req-a", Status: 200, Latency: 10 * time.Millisecond, TTFT: time.Millisecond, SelectedWorker: "worker-1", SelectedInstance: "instance-1", BackendType: "vllm", RequestedInput: 16, RequestedOutput: 4, PromptTokens: 20, CompletionTokens: 4, TotalTokens: 24}}
	if err := writeResultsJSONL(path, results); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record resultRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	if record.JobID != "a" || record.Group != "hot" || record.SelectedWorkerID != "worker-1" || record.LatencyMS != 10 || record.PromptTokens != 20 || record.RequestedOutput != 4 {
		t.Fatalf("record=%+v", record)
	}
}

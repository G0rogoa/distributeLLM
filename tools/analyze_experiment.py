#!/usr/bin/env python3
import argparse
import csv
import json
import math
import pathlib
import random
import statistics
from collections import Counter, defaultdict


def read_jsonl(path):
    rows = []
    with open(path, "r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def percentile(values, p):
    values = sorted(values)
    if not values:
        return None
    index = int((len(values) - 1) * p)
    return values[index]


def mean(values):
    return statistics.mean(values) if values else None


def stdev(values):
    return statistics.stdev(values) if len(values) > 1 else 0.0


def bootstrap_ci(values, rounds=1000, seed=1):
    if len(values) < 20:
        return None
    rng = random.Random(seed)
    samples = []
    for _ in range(rounds):
        draw = [values[rng.randrange(len(values))] for _ in values]
        samples.append(statistics.mean(draw))
    return [percentile(samples, 0.025), percentile(samples, 0.975)]


def jain(counts):
    values = list(counts.values())
    if not values:
        return None
    numerator = sum(values) ** 2
    denominator = len(values) * sum(v * v for v in values)
    if denominator == 0:
        return None
    return numerator / denominator


def summarize_requests(rows):
    successes = [row for row in rows if 200 <= row.get("status", 0) < 300 and not row.get("error")]
    latencies = [row["latency_ms"] for row in successes if row.get("latency_ms", 0) > 0]
    ttfts = [row["ttft_ms"] for row in successes if row.get("ttft_ms", 0) > 0]
    tpots = [row["tpot_ms"] for row in successes if row.get("tpot_ms", 0) > 0]
    workers = Counter(row.get("selected_worker_id", "unknown") for row in successes)
    by_group = defaultdict(Counter)
    for row in successes:
        by_group[row.get("group", "unknown")][row.get("selected_worker_id", "unknown")] += 1
    usage_valid = sum(1 for row in successes if row.get("usage_valid"))
    tokenizer_fallback = sum(1 for row in successes if row.get("usage_source") == "tokenizer")
    return {
        "requests": len(rows),
        "success": len(successes),
        "success_rate": len(successes) / len(rows) if rows else 0,
        "latency_ms": stats(latencies),
        "ttft_ms": stats(ttfts),
        "tpot_ms": stats(tpots),
        "workers": dict(workers),
        "worker_jain_index": jain(workers),
        "groups": {group: dict(counts) for group, counts in by_group.items()},
        "usage_valid_rate": usage_valid / len(successes) if successes else 0,
        "tokenizer_fallback_count": tokenizer_fallback,
    }


def stats(values):
    return {
        "mean": mean(values),
        "stdev": stdev(values),
        "p50": percentile(values, 0.50),
        "p90": percentile(values, 0.90),
        "p95": percentile(values, 0.95),
        "p99": percentile(values, 0.99),
        "mean_bootstrap_95ci": bootstrap_ci(values),
    }


def parse_metric_value(text, name):
    total = 0.0
    found = False
    for line in text.splitlines():
        if line.startswith("#") or not line.strip():
            continue
        fields = line.split()
        metric = fields[0].split("{", 1)[0]
        if metric == name and len(fields) >= 2:
            try:
                total += float(fields[1])
                found = True
            except ValueError:
                pass
    return total if found else None


def summarize_artifact(root):
    root = pathlib.Path(root)
    result = {"experiment_id": root.name, "runs": {}}
    manifest = root / "manifest.json"
    if manifest.exists():
        result["manifest"] = json.loads(manifest.read_text(encoding="utf-8"))
    for path in sorted(root.glob("**/*requests.jsonl")) + sorted(root.glob("**/*run*.jsonl")):
        if path.name.endswith("warm.jsonl"):
            continue
        rows = read_jsonl(path)
        result["runs"][str(path.relative_to(root))] = summarize_requests(rows)
    before = root / "controller-metrics-before.txt"
    after = root / "controller-metrics-after.txt"
    if before.exists() and after.exists():
        before_text = before.read_text(encoding="utf-8")
        after_text = after.read_text(encoding="utf-8")
        result["controller_metric_delta"] = {}
        for name in [
            "distserve_tokenizer_fallbacks_total",
            "distserve_shadow_affinity_matches_total",
            "distserve_scheduler_failures_total",
            "distserve_retries_total",
            "distserve_admission_rejections_total",
        ]:
            start = parse_metric_value(before_text, name)
            end = parse_metric_value(after_text, name)
            if start is not None and end is not None:
                result["controller_metric_delta"][name] = end - start
    return result


MANIFEST_FIELDS = {
    "git_commit", "model_id", "model_revision", "tokenizer_id", "tokenizer_revision",
    "chat_template_version", "vllm_version", "torch_version", "cuda_build_version",
    "driver_version", "gpu_indices", "scheduler", "cost_profile", "workload",
    "prefix_tokens", "output_tokens", "requests", "concurrency", "warmup_requests",
    "seed", "repetition", "controller_flags", "worker_flags", "shadow_confidence",
    "online_learning",
}


def load_decisions(path):
    if not path.exists():
        return []
    value = json.loads(path.read_text(encoding="utf-8"))
    return value if isinstance(value, list) else value.get("decisions", [])


def data_quality(root, manifest, rows):
    decisions = load_decisions(root / "decisions.json")
    by_request = {row.get("request_id"): row for row in decisions}
    missing_manifest = sorted(name for name in MANIFEST_FIELDS if manifest.get(name) in (None, "", []))
    request_ids = [row.get("request_id") for row in rows if row.get("request_id")]
    mismatches = [request_id for request_id in request_ids if request_id in by_request and by_request[request_id].get("worker_id") != next(row.get("selected_worker_id") for row in rows if row.get("request_id") == request_id)]
    worker_samples = root / "worker-samples.jsonl"
    sample_count = len(read_jsonl(worker_samples)) if worker_samples.exists() and worker_samples.stat().st_size else 0
    profile_identity_match = None
    tokens_in_range = None
    profile_value = manifest.get("cost_profile", "")
    profile_path = pathlib.Path(profile_value) if profile_value else None
    if profile_path is not None and not profile_path.is_absolute():
        candidates = [root / profile_path, root.parent / profile_path]
        profile_path = next((path for path in candidates if path.exists()), profile_path)
    if profile_path is not None and profile_path.is_file():
        profile = json.loads(profile_path.read_text(encoding="utf-8"))
        expected = manifest.get("model_identity_hash")
        profile_identity_match = bool(expected and profile.get("model_identity_hash") == expected)
        token_range = profile.get("valid_token_range")
        prompt_tokens = [row.get("prompt_tokens") for row in rows if row.get("prompt_tokens")]
        if token_range and prompt_tokens:
            tokens_in_range = all(token_range[0] <= value <= token_range[1] for value in prompt_tokens)
    failures = sum(1 for row in rows if not 200 <= row.get("status", 0) < 300 or row.get("error"))
    return {
        "valid": not (manifest.get("dirty_worktree") or missing_manifest or sample_count == 0 or failures or len(by_request) < len(request_ids) or mismatches),
        "dirty_worktree": bool(manifest.get("dirty_worktree")),
        "missing_manifest_fields": missing_manifest,
        "worker_sample_count": sample_count,
        "all_requests_have_decisions": len(by_request) >= len(request_ids),
        "decision_worker_mismatches": mismatches,
        "tokenizer_fallback_count": sum(1 for row in rows if row.get("usage_source") == "tokenizer"),
        "failed_requests": failures,
        "cost_profile_identity_match": profile_identity_match,
        "tokens_in_calibrated_range": tokens_in_range,
        "repetition_sufficient": manifest.get("repetition", 0) >= 3,
    }


def matrix_summary(matrix_root):
    matrix_root = pathlib.Path(matrix_root)
    runs = []
    quality = {}
    commits = set()
    for manifest_path in sorted(matrix_root.glob("**/manifest.json")):
        root = manifest_path.parent
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        request_path = root / "requests.jsonl"
        rows = read_jsonl(request_path) if request_path.exists() else []
        summary = summarize_requests(rows)
        loadgen_summary = root / "summary.json"
        loadgen = json.loads(loadgen_summary.read_text(encoding="utf-8")) if loadgen_summary.exists() else {}
        summary["throughput_requests_per_second"] = loadgen.get("throughput_rps")
        decisions = load_decisions(root / "decisions.json")
        chosen = []
        for decision in decisions:
            chosen.extend(candidate for candidate in decision.get("candidates", []) if candidate.get("worker_id") == decision.get("worker_id") and candidate.get("instance_id") == decision.get("instance_id"))
        shadow = [candidate for candidate in chosen if candidate.get("cache_match", {}).get("evidence") == "ShadowEstimated"]
        summary["shadow_match_rate"] = len(shadow) / len(chosen) if chosen else 0
        ratios = [candidate.get("cache_match", {}).get("match_ratio", 0) for candidate in shadow]
        summary["matched_token_ratio"] = mean(ratios) or 0
        summary["manifest"] = manifest
        summary["path"] = str(root.relative_to(matrix_root))
        runs.append(summary)
        quality[summary["path"]] = data_quality(root, manifest, rows)
        commits.add(manifest.get("git_commit"))
    grouped = defaultdict(list)
    for run in runs:
        manifest = run["manifest"]
        key = (manifest.get("scheduler"), manifest.get("workload"), manifest.get("prefix_tokens"), manifest.get("concurrency"), manifest.get("congestion_level", 0))
        grouped[key].append(run)
    groups = []
    for key, members in sorted(grouped.items(), key=lambda item: str(item[0])):
        metric = lambda path: [member[path[0]][path[1]] for member in members if member.get(path[0], {}).get(path[1]) is not None]
        group = {
            "scheduler": key[0], "workload": key[1], "prefix_tokens": key[2],
            "concurrency": key[3], "congestion_level": key[4], "repetitions": len(members),
            "request_samples": sum(member["requests"] for member in members),
            "success_rate": stats([member["success_rate"] for member in members]),
            "throughput": stats([member["throughput_requests_per_second"] for member in members if member["throughput_requests_per_second"] is not None]),
            "ttft_p50": stats(metric(("ttft_ms", "p50"))), "ttft_p95": stats(metric(("ttft_ms", "p95"))),
            "ttft_p99": stats(metric(("ttft_ms", "p99"))), "latency_p50": stats(metric(("latency_ms", "p50"))),
            "latency_p95": stats(metric(("latency_ms", "p95"))), "latency_p99": stats(metric(("latency_ms", "p99"))),
            "tpot_p50": stats(metric(("tpot_ms", "p50"))), "worker_jain_index": stats([member["worker_jain_index"] for member in members if member["worker_jain_index"] is not None]),
            "shadow_match_rate": stats([member["shadow_match_rate"] for member in members]),
            "matched_token_ratio": stats([member["matched_token_ratio"] for member in members]),
            "tokenizer_fallback": sum(member["tokenizer_fallback_count"] for member in members),
        }
        groups.append(group)
    quality["matrix"] = {"commit_consistent": len(commits) == 1, "commits": sorted(value for value in commits if value), "run_count": len(runs)}
    return {"groups": groups, "runs": runs}, quality


def write_matrix(root, output):
    output.mkdir(parents=True, exist_ok=True)
    matrix, quality = matrix_summary(root)
    (output / "matrix.json").write_text(json.dumps(matrix, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    (output / "data-quality.json").write_text(json.dumps(quality, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    comparisons = {"note": "Pairwise scheduler deltas require matching workload, prefix, concurrency and congestion groups.", "groups": matrix["groups"]}
    (output / "comparisons.json").write_text(json.dumps(comparisons, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    columns = ["scheduler", "workload", "prefix_tokens", "concurrency", "congestion_level", "repetitions", "request_samples", "success_rate_mean", "throughput_mean", "ttft_p95_mean", "latency_p95_mean"]
    with (output / "table.csv").open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=columns)
        writer.writeheader()
        for group in matrix["groups"]:
            writer.writerow({**{name: group[name] for name in columns[:7]}, "success_rate_mean": group["success_rate"]["mean"], "throughput_mean": group["throughput"]["mean"], "ttft_p95_mean": group["ttft_p95"]["mean"], "latency_p95_mean": group["latency_p95"]["mean"]})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("artifact", nargs="?", help="artifact directory")
    parser.add_argument("--matrix", help="matrix artifact directory")
    parser.add_argument("-o", "--output", help="summary JSON path or matrix output directory")
    args = parser.parse_args()
    if args.matrix:
        if not args.output:
            parser.error("--output is required with --matrix")
        write_matrix(pathlib.Path(args.matrix), pathlib.Path(args.output))
        return
    if not args.artifact:
        parser.error("artifact is required unless --matrix is used")
    summary = summarize_artifact(args.artifact)
    raw = json.dumps(summary, indent=2, sort_keys=True)
    if args.output:
        pathlib.Path(args.output).write_text(raw + "\n", encoding="utf-8")
    else:
        print(raw)


if __name__ == "__main__":
    main()

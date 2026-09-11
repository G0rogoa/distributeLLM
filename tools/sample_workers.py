#!/usr/bin/env python3
import argparse
import datetime
import json
import signal
import time
import urllib.request


stopping = False


def stop(_signum, _frame):
    global stopping
    stopping = True


def optional_value(load, name):
    value = load.get(name, {})
    return value.get("value") if value.get("valid") else None


def selected_counts(url):
    with urllib.request.urlopen(url, timeout=2) as response:
        text = response.read().decode("utf-8")
    counts = {}
    for line in text.splitlines():
        if not line.startswith("distserve_worker_selected_total{"):
            continue
        metric, value = line.rsplit(None, 1)
        worker_id = metric.split('worker_id="', 1)[1].split('"', 1)[0]
        counts[worker_id] = int(float(value))
    return counts


def collect(url, metrics_url):
    with urllib.request.urlopen(url, timeout=2) as response:
        workers = json.load(response)
    now = datetime.datetime.now(datetime.timezone.utc)
    selected = selected_counts(metrics_url)
    rows = []
    for worker in workers:
        load = worker.get("load") or {}
        heartbeat = datetime.datetime.fromisoformat(worker["last_heartbeat"].replace("Z", "+00:00"))
        rows.append({
            "timestamp": now.isoformat().replace("+00:00", "Z"),
            "worker_id": worker["id"],
            "instance_id": worker["instance_id"],
            "status": worker["status"],
            "reported_running": worker["reported_running"],
            "reported_waiting": worker["reported_queued"],
            "local_reservations": worker["local_reservations"],
            "heartbeat_age_ms": max(0, (now - heartbeat).total_seconds() * 1000),
            "gpu_kv_cache_usage_ratio": optional_value(load, "gpu_kv_cache_usage_ratio"),
            "prefix_cache_hits_total": optional_value(load, "prefix_cache_hits_total"),
            "prefix_cache_misses_total": optional_value(load, "prefix_cache_misses_total"),
            "selected_requests_total": selected.get(worker["id"], 0),
        })
    return rows


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--controller-url", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--interval", type=float, default=1.0)
    args = parser.parse_args()
    if args.interval <= 0:
        parser.error("--interval must be positive")
    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)
    workers_url = args.controller_url.rstrip("/") + "/internal/workers"
    metrics_url = args.controller_url.rstrip("/") + "/metrics"
    with open(args.output, "a", encoding="utf-8", buffering=1) as output:
        while True:
            try:
                for row in collect(workers_url, metrics_url):
                    output.write(json.dumps(row, separators=(",", ":")) + "\n")
            except (OSError, ValueError, KeyError) as error:
                print(f"worker sample failed: {error}", flush=True)
            if stopping:
                break
            time.sleep(args.interval)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
import argparse
import json
from pathlib import Path


REQUIRED_TEXT = (
    "experiment_id", "git_commit", "model_id", "model_identity_hash", "model_revision", "tokenizer_id",
    "tokenizer_revision", "chat_template_version", "vllm_version", "torch_version",
    "cuda_build_version", "driver_version", "scheduler", "cost_profile", "workload",
)


def validate(manifest):
    errors = [name for name in REQUIRED_TEXT if not manifest.get(name)]
    for name in ("gpu_indices", "controller_flags", "worker_flags"):
        if not manifest.get(name):
            errors.append(name)
    for name in ("prefix_tokens", "output_tokens", "requests", "concurrency", "warmup_requests", "seed", "repetition"):
        if not isinstance(manifest.get(name), int) or manifest[name] < (0 if name == "warmup_requests" else 1):
            errors.append(name)
    if manifest.get("dirty_worktree"):
        errors.append("dirty_worktree")
    return sorted(set(errors))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    for name in REQUIRED_TEXT:
        parser.add_argument("--" + name.replace("_", "-"), required=True)
    parser.add_argument("--start-time", required=True)
    parser.add_argument("--end-time", required=True)
    parser.add_argument("--gpu-index", action="append", type=int, required=True)
    parser.add_argument("--controller-flag", action="append", required=True)
    parser.add_argument("--worker-flag", action="append", required=True)
    parser.add_argument("--prefix-tokens", type=int, required=True)
    parser.add_argument("--output-tokens", type=int, required=True)
    parser.add_argument("--requests", type=int, required=True)
    parser.add_argument("--concurrency", type=int, required=True)
    parser.add_argument("--warmup-requests", type=int, required=True)
    parser.add_argument("--seed", type=int, required=True)
    parser.add_argument("--repetition", type=int, required=True)
    parser.add_argument("--shadow-confidence", type=float, required=True)
    parser.add_argument("--congestion-level", type=int, default=0)
    parser.add_argument("--online-learning", action="store_true")
    parser.add_argument("--dirty-worktree", action="store_true")
    args = parser.parse_args()
    manifest = vars(args)
    manifest["gpu_indices"] = manifest.pop("gpu_index")
    manifest["controller_flags"] = manifest.pop("controller_flag")
    manifest["worker_flags"] = manifest.pop("worker_flag")
    output = manifest.pop("output")
    manifest["request_rate"] = None
    errors = validate(manifest)
    if errors:
        parser.error("invalid manifest fields: " + ", ".join(errors))
    Path(output).write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()

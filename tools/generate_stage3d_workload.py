#!/usr/bin/env python3
import argparse
import json
import random


WORDS = [
    "cache", "scheduler", "worker", "prefix", "decode", "prefill", "latency", "queue",
    "token", "block", "identity", "routing", "shadow", "profile", "calibration", "throughput",
]


def make_text(target_tokens, rng, marker):
    words = [marker]
    while len(words) < target_tokens:
        words.append(rng.choice(WORDS))
    return " ".join(words)


def unique(prefix_tokens, count, output_tokens, seed):
    rng = random.Random(seed)
    for index in range(count):
        prompt = make_text(prefix_tokens, rng, f"unique-{prefix_tokens}-{index}") + f" question-{index}"
        yield {"id": f"unique-{prefix_tokens}-{index:04d}", "group": f"unique-{prefix_tokens}", "prompt": prompt, "output_tokens": output_tokens}


def shared(prefix_tokens, count, output_tokens, seed):
    rng = random.Random(seed)
    prefix = make_text(prefix_tokens, rng, f"shared-document-{prefix_tokens}")
    for index in range(count):
        suffix = f" Question {index}: compare routing tradeoff {rng.randrange(1_000_000)}."
        yield {"id": f"shared-{prefix_tokens}-{index:04d}", "group": f"shared-{prefix_tokens}", "prompt": prefix + suffix, "output_tokens": output_tokens}


def mixed(prefix_tokens, count, output_tokens, seed):
    rng = random.Random(seed)
    rows = []
    shared_count = int(count * 0.4)
    unique_count = int(count * 0.4)
    other_count = count - shared_count - unique_count
    rows.extend(shared(prefix_tokens, shared_count, output_tokens, seed + 11))
    rows.extend(unique(prefix_tokens, unique_count, output_tokens, seed + 23))
    for group in range(max(1, other_count // 20)):
        group_prefix = make_text(max(16, prefix_tokens // 2), rng, f"other-group-{group}")
        for index in range(other_count // max(1, other_count // 20)):
            rows.append({"id": f"other-{group}-{index:04d}", "group": f"other-{group}", "prompt": group_prefix + f" Variant suffix {index} seed {rng.randrange(1_000_000)}.", "output_tokens": output_tokens})
    rng.shuffle(rows)
    return rows[:count]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--kind", choices=["unique", "shared", "mixed"], required=True)
    parser.add_argument("--prefix-tokens", type=int, required=True)
    parser.add_argument("--requests", type=int, default=100)
    parser.add_argument("--output-tokens", type=int, default=64)
    parser.add_argument("--seed", type=int, default=20260903)
    parser.add_argument("-o", "--output", required=True)
    args = parser.parse_args()
    if args.kind == "unique":
        rows = list(unique(args.prefix_tokens, args.requests, args.output_tokens, args.seed))
    elif args.kind == "shared":
        rows = list(shared(args.prefix_tokens, args.requests, args.output_tokens, args.seed))
    else:
        rows = list(mixed(args.prefix_tokens, args.requests, args.output_tokens, args.seed))
    with open(args.output, "w", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row, ensure_ascii=False) + "\n")


if __name__ == "__main__":
    main()

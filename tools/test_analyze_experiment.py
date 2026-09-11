import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import analyze_experiment
import write_manifest


class AnalyzeExperimentTest(unittest.TestCase):
    def test_manifest_validation_rejects_dirty_and_missing_fields(self):
        manifest = {name: "value" for name in write_manifest.REQUIRED_TEXT}
        manifest.update({
            "dirty_worktree": True, "gpu_indices": [0], "controller_flags": ["-scheduler=ect"],
            "worker_flags": ["worker_id=w"], "prefix_tokens": 4096, "output_tokens": 64,
            "requests": 30, "concurrency": 1, "warmup_requests": 0, "seed": 1, "repetition": 1,
        })
        self.assertIn("dirty_worktree", write_manifest.validate(manifest))
        del manifest["model_revision"]
        self.assertIn("model_revision", write_manifest.validate(manifest))

    def test_summarizes_requests_and_usage_validity(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            rows = [
                {"status": 200, "latency_ms": 10, "ttft_ms": 2, "tpot_ms": 1, "selected_worker_id": "a", "group": "shared", "usage_valid": True},
                {"status": 200, "latency_ms": 20, "ttft_ms": 3, "tpot_ms": 1.5, "selected_worker_id": "b", "group": "shared", "usage_valid": False},
                {"status": 503, "latency_ms": 1, "selected_worker_id": "", "group": "shared"},
            ]
            with (root / "requests.jsonl").open("w", encoding="utf-8") as handle:
                for row in rows:
                    handle.write(json.dumps(row) + "\n")
            summary = analyze_experiment.summarize_artifact(root)
            run = summary["runs"]["requests.jsonl"]
            self.assertEqual(run["requests"], 3)
            self.assertEqual(run["success"], 2)
            self.assertEqual(run["workers"], {"a": 1, "b": 1})
            self.assertEqual(run["usage_valid_rate"], 0.5)

    def test_matrix_reports_empty_worker_samples_and_writes_outputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            run = root / "run-1"
            run.mkdir()
            manifest = {
                "git_commit": "abc", "dirty_worktree": True, "model_id": "m",
                "scheduler": "ect", "workload": "shared", "prefix_tokens": 4096,
                "concurrency": 4, "repetition": 1,
            }
            (run / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
            (run / "requests.jsonl").write_text(json.dumps({"request_id": "r1", "status": 200, "latency_ms": 10, "ttft_ms": 2, "selected_worker_id": "w"}) + "\n", encoding="utf-8")
            (run / "worker-samples.jsonl").write_text("", encoding="utf-8")
            output = root / "summary"
            analyze_experiment.write_matrix(root, output)
            quality = json.loads((output / "data-quality.json").read_text(encoding="utf-8"))
            self.assertEqual(quality["run-1"]["worker_sample_count"], 0)
            self.assertFalse(quality["run-1"]["valid"])
            for name in ("matrix.json", "table.csv", "comparisons.json", "data-quality.json"):
                self.assertTrue((output / name).exists())


if __name__ == "__main__":
    unittest.main()

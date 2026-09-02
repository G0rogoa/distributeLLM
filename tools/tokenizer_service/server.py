#!/usr/bin/env python3
import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from transformers import AutoTokenizer


def error_response(handler, status, code, message):
    body = json.dumps(
        {"error": {"message": message, "type": "tokenizer_error", "code": code}},
        separators=(",", ":"),
    ).encode("utf-8")
    handler.send_response(status)
    handler.send_header("Content-Type", "application/json")
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


class TokenizerHandler(BaseHTTPRequestHandler):
    tokenizer = None
    identity = None
    max_bytes = 1 << 20
    max_tokens = 262144

    def log_message(self, fmt, *args):
        print("%s - %s" % (self.address_string(), fmt % args), flush=True)

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
            return
        if self.path == "/v1/identity":
            self._json(200, self.identity)
            return
        error_response(self, 404, "not_found", "not found")

    def do_POST(self):
        if self.path != "/v1/tokenize":
            error_response(self, 404, "not_found", "not found")
            return
        length = self.headers.get("Content-Length")
        if length is None:
            error_response(self, 411, "length_required", "content length required")
            return
        try:
            size = int(length)
        except ValueError:
            error_response(self, 400, "invalid_content_length", "invalid content length")
            return
        if size < 0 or size > self.max_bytes:
            error_response(self, 413, "request_too_large", "request too large")
            return
        raw = self.rfile.read(size)
        try:
            request = json.loads(raw)
        except json.JSONDecodeError:
            error_response(self, 400, "invalid_json", "invalid JSON")
            return
        text = request.get("text")
        expected = request.get("expected_identity")
        if not isinstance(text, str) or not isinstance(expected, dict):
            error_response(self, 400, "invalid_request", "text and expected_identity are required")
            return
        if expected != self.identity:
            error_response(self, 409, "identity_mismatch", "expected identity does not match tokenizer")
            return
        token_ids = self.tokenizer.encode(text, add_special_tokens=False)
        if len(token_ids) > self.max_tokens:
            error_response(self, 413, "too_many_tokens", "token count exceeds limit")
            return
        self._json(200, {"token_ids": token_ids, "token_count": len(token_ids), "identity": self.identity})

    def _json(self, status, value):
        body = json.dumps(value, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    parser = argparse.ArgumentParser(description="DistServe tokenizer sidecar")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8091)
    parser.add_argument("--tokenizer-path", required=True)
    parser.add_argument("--model-id", required=True)
    parser.add_argument("--model-revision", required=True)
    parser.add_argument("--tokenizer-id", required=True)
    parser.add_argument("--tokenizer-revision", required=True)
    parser.add_argument("--chat-template-version", required=True)
    parser.add_argument("--max-bytes", type=int, default=1 << 20)
    parser.add_argument("--max-tokens", type=int, default=262144)
    parser.add_argument("--trust-remote-code", action="store_true", default=False)
    args = parser.parse_args()

    tokenizer = AutoTokenizer.from_pretrained(
        args.tokenizer_path,
        local_files_only=True,
        trust_remote_code=args.trust_remote_code,
    )
    TokenizerHandler.tokenizer = tokenizer
    TokenizerHandler.identity = {
        "model_id": args.model_id,
        "model_revision": args.model_revision,
        "tokenizer_id": args.tokenizer_id,
        "tokenizer_revision": args.tokenizer_revision,
        "chat_template_version": args.chat_template_version,
        "adapter_id": "",
    }
    TokenizerHandler.max_bytes = args.max_bytes
    TokenizerHandler.max_tokens = args.max_tokens

    server = ThreadingHTTPServer((args.host, args.port), TokenizerHandler)
    print(f"tokenizer service listening on {args.host}:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()

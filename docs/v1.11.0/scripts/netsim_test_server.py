import argparse
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

class Handler(BaseHTTPRequestHandler):
    server_version = "NetSimLab/1.1"

    def _json(self, code: int, payload: dict):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path
        qs = parse_qs(parsed.query)

        if path == "/ping":
            sleep_ms = int(qs.get("sleep_ms", ["0"])[0])
            if sleep_ms > 0:
                time.sleep(sleep_ms / 1000.0)
            self._json(200, {"ok": True, "ts": time.time()})
            return

        if path == "/blob":
            kb = int(qs.get("kb", ["1024"])[0])
            kb = max(1, min(kb, 16384))
            payload = os.urandom(kb * 1024)
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return

        if path == "/healthz":
            self._json(200, {"status": "up"})
            return

        self._json(404, {"error": "not_found", "path": path})

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path != "/upload":
            self._json(404, {"error": "not_found", "path": parsed.path})
            return

        length = int(self.headers.get("Content-Length", "0"))
        body = b""
        remain = length
        while remain > 0:
            chunk = self.rfile.read(min(65536, remain))
            if not chunk:
                break
            body += chunk
            remain -= len(chunk)

        self._json(200, {"received_bytes": len(body)})

    def log_message(self, fmt, *args):
        return


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="0.0.0.0")
    ap.add_argument("--port", type=int, default=18080)
    args = ap.parse_args()

    httpd = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"serving on {args.host}:{args.port}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()

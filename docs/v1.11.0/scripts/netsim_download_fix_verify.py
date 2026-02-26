import json
import statistics
import subprocess
import time
import urllib.request

API = "http://127.0.0.1:19090"
PROXY = "http://192.168.8.100:7890"
URL = "http://192.168.8.101:18080/blob?kb=1024"


def api(method, path, data=None):
    body = None
    headers = {}
    if data is not None:
        body = json.dumps(data, separators=(",", ":")).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(API + path, data=body, method=method, headers=headers)
    with urllib.request.urlopen(req, timeout=20) as r:
        text = r.read().decode()
        return json.loads(text) if text else {}


def timed_download(timeout):
    cp = subprocess.run([
        "curl.exe", "-s", "-o", "NUL", "-w", "%{time_total} %{http_code}",
        "--max-time", str(timeout), "--proxy", PROXY, URL,
    ], capture_output=True, text=True)
    out = cp.stdout.strip()
    parts = out.split()
    if cp.returncode != 0 or len(parts) != 2:
        return {"ok": False, "raw": out, "rc": cp.returncode}
    return {"ok": True, "time_s": float(parts[0]), "code": int(parts[1]), "raw": out}


def run_case(name, cfg, runs, timeout):
    set_resp = api("PUT", "/net-sim", cfg)
    effective = api("GET", "/net-sim")
    api("DELETE", "/net-sim/stats")

    rows = []
    for i in range(runs):
        row = timed_download(timeout)
        row["run"] = i + 1
        rows.append(row)
        time.sleep(0.15)

    stats = api("GET", "/net-sim/stats")
    times = [x["time_s"] for x in rows if x.get("ok") and x.get("code") == 200]
    summary = {}
    if times:
        summary = {
            "count": len(times),
            "avg_s": round(statistics.mean(times), 4),
            "min_s": round(min(times), 4),
            "max_s": round(max(times), 4),
            "samples_s": [round(t, 4) for t in times],
        }

    return {
        "case": name,
        "requested": cfg,
        "set_response": set_resp,
        "effective": effective,
        "runs": rows,
        "summary": summary,
        "stats": stats,
    }


report = {
    "timestamp": time.strftime("%Y-%m-%d %H:%M:%S"),
    "target": URL,
    "proxy": PROXY,
    "results": []
}

report["results"].append(run_case("before_disabled", {"enabled": False}, runs=5, timeout=30))
report["results"].append(run_case("after_download_bw_0.5", {"enabled": True, "download-bandwidth": 0.5}, runs=5, timeout=90))

api("PUT", "/net-sim", {"enabled": True, "latency": 200})

print(json.dumps(report, ensure_ascii=False, indent=2))

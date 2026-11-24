#!/usr/bin/env python3
"""
net-sim end-to-end check: "configured value == measured value".

Starts local TCP echo / HTTP blob / UDP echo servers, (optionally) launches a
mihomo binary, then drives traffic through mihomo's SOCKS5 inbound (TCP CONNECT
and UDP ASSOCIATE, which exercise the exact tunnel paths used by TUN/Android)
while switching net-sim settings via the REST API. Every case prints the
configured value next to the measured one.

Python 3.8+, stdlib only.

  # launch mihomo automatically (Linux / WSL)
  python3 netsim_e2e.py --mihomo ./mihomo-linux-amd64

  # against an already running core (e.g. Android via adb forward)
  python3 netsim_e2e.py --api http://127.0.0.1:19090 --socks 127.0.0.1:7890 \
      --target-host <ip of this machine reachable from the core>
"""
import argparse
import hashlib
import json
import os
import socket
import socketserver
import statistics
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# ----------------------------------------------------------------- servers --

BLOB = os.urandom(8 << 20)
BLOB_SHA = {}


class TCPEcho(socketserver.BaseRequestHandler):
    def handle(self):
        s = self.request
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        while True:
            d = s.recv(65536)
            if not d:
                return
            s.sendall(d)


class HTTPBlob(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"

    def do_GET(self):
        n = int(self.path.split("=", 1)[1]) if "=" in self.path else 1 << 20
        self.send_response(200)
        self.send_header("Content-Length", str(n))
        self.end_headers()
        self.wfile.write(BLOB[:n])

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        got = 0
        while got < n:
            d = self.rfile.read(min(65536, n - got))
            if not d:
                break
            got += len(d)
        body = json.dumps({"received": got, "t": time.time()}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


class TServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def udp_echo_server(sock):
    while True:
        try:
            d, a = sock.recvfrom(65535)
        except OSError:
            return
        sock.sendto(d, a)


# ------------------------------------------------------------------ socks5 --

def socks_tcp(socks, host, port, timeout=10):
    s = socket.create_connection(socks, timeout=timeout)
    s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    s.sendall(b"\x05\x01\x00")
    assert s.recv(2) == b"\x05\x00"
    s.sendall(b"\x05\x01\x00\x01" + socket.inet_aton(host) + struct.pack(">H", port))
    r = recv_exact(s, 10)
    assert r[1] == 0, "socks connect failed %r" % r
    return s


def recv_exact(s, n):
    b = b""
    while len(b) < n:
        d = s.recv(n - len(b))
        if not d:
            raise EOFError
        b += d
    return b


class SocksUDP:
    def __init__(self, socks, host, port):
        self.ctrl = socket.create_connection(socks, timeout=10)
        self.ctrl.sendall(b"\x05\x01\x00")
        assert self.ctrl.recv(2) == b"\x05\x00"
        self.ctrl.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00")
        r = recv_exact(self.ctrl, 10)
        assert r[1] == 0, "udp associate failed"
        relay_ip = socket.inet_ntoa(r[4:8])
        if relay_ip == "0.0.0.0":
            relay_ip = socks[0]
        self.relay = (relay_ip, struct.unpack(">H", r[8:10])[0])
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4 << 20)
        self.hdr = b"\x00\x00\x00\x01" + socket.inet_aton(host) + struct.pack(">H", port)

    def send(self, payload):
        self.sock.sendto(self.hdr + payload, self.relay)

    def recv(self, timeout):
        self.sock.settimeout(timeout)
        d, _ = self.sock.recvfrom(65535)
        return d[10:]

    def close(self):
        self.sock.close()
        self.ctrl.close()


# ---------------------------------------------------------------- measures --

def tcp_rtt(a, n=10):
    s = socks_tcp(a.socks, a.target, a.echo_port)
    s.sendall(b"x")
    recv_exact(s, 1)
    out = []
    for _ in range(n):
        t = time.perf_counter()
        s.sendall(b"ping")
        recv_exact(s, 4)
        out.append((time.perf_counter() - t) * 1000)
    s.close()
    return out


def http_get(a, size):
    s = socks_tcp(a.socks, a.target, a.http_port, timeout=120)
    t = time.perf_counter()
    s.sendall(b"GET /?n=%d HTTP/1.0\r\nHost: x\r\n\r\n" % size)
    buf = b""
    while b"\r\n\r\n" not in buf:
        buf += s.recv(65536)
    body = buf.split(b"\r\n\r\n", 1)[1]
    t0 = time.perf_counter()
    h = hashlib.sha256()
    h.update(body)
    got = len(body)
    while True:
        d = s.recv(1 << 20)
        if not d:
            break
        h.update(d)
        got += len(d)
    el = time.perf_counter() - t0
    s.close()
    ok = h.hexdigest() == hashlib.sha256(BLOB[:size]).hexdigest()
    return got, el, ok, time.perf_counter() - t


def http_post(a, size):
    s = socks_tcp(a.socks, a.target, a.http_port, timeout=120)
    s.sendall(b"POST / HTTP/1.0\r\nHost: x\r\nContent-Length: %d\r\n\r\n" % size)
    t = time.time()
    s.sendall(BLOB[:size])
    buf = b""
    while True:
        d = s.recv(65536)
        if not d:
            break
        buf += d
    s.close()
    j = json.loads(buf.split(b"\r\n\r\n", 1)[1])
    return j["received"], j["t"] - t


def udp_rtt(a, n=20, size=32):
    u = SocksUDP(a.socks, a.target, a.udp_port)
    out = []
    for i in range(n):
        p = struct.pack(">I", i) + b"\0" * (size - 4)
        t = time.perf_counter()
        u.send(p)
        try:
            while True:
                d = u.recv(3)
                if d[:4] == p[:4]:
                    break
            out.append((time.perf_counter() - t) * 1000)
        except socket.timeout:
            pass
    u.close()
    return out


def udp_burst(a, n, gap, size=64, wait=1.5):
    """send n datagrams, return list of received payloads in arrival order"""
    u = SocksUDP(a.socks, a.target, a.udp_port)
    marker = bytes(range(256)) * (size // 256 + 1)
    got = []
    stop = threading.Event()

    def rx():
        while not stop.is_set():
            try:
                got.append(u.recv(0.2))
            except socket.timeout:
                continue
            except OSError:
                return

    th = threading.Thread(target=rx, daemon=True)
    th.start()
    u.send(b"warm" + marker[: size - 4])
    time.sleep(0.3)
    got.clear()
    for i in range(n):
        u.send(struct.pack(">I", i) + marker[: size - 4])
        if gap:
            time.sleep(gap)
    time.sleep(wait)
    stop.set()
    th.join()
    u.close()
    return [g for g in got if not g.startswith(b"warm")], marker[: size - 4]


# --------------------------------------------------------------------- api --

def api(a, method, path="", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(a.api + "/net-sim" + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    if a.secret:
        req.add_header("Authorization", "Bearer " + a.secret)
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def setsim(a, **cfg):
    cfg["enabled"] = True
    api(a, "PUT", body=cfg)
    api(a, "DELETE", "/stats")
    time.sleep(0.2)


# ------------------------------------------------------------------- cases --

RESULTS = []


def report(name, config, measured, ok, note=""):
    RESULTS.append({"case": name, "config": config, "measured": measured, "pass": ok, "note": note})
    print("%-4s %-28s config=%-22s measured=%s %s" % ("PASS" if ok else "FAIL", name, config, measured, note), flush=True)


def run(a):
    api(a, "DELETE")
    base_tcp = statistics.median(tcp_rtt(a))
    base_udp = statistics.median(udp_rtt(a))
    report("baseline", "off", "tcp %.1fms udp %.1fms" % (base_tcp, base_udp), True)

    setsim(a, latency=200)
    r = statistics.median(tcp_rtt(a)) - base_tcp
    report("tcp latency (rtt)", "200ms", "%.1fms" % r, abs(r - 200) <= 15)
    r = statistics.median(udp_rtt(a)) - base_udp
    report("udp latency (rtt)", "200ms", "%.1fms" % r, abs(r - 200) <= 15)

    setsim(a, latency=100, jitter=40)
    rs = [x - base_udp for x in udp_rtt(a, n=60)]
    ok = 55 <= min(rs) and max(rs) <= 150 and abs(statistics.mean(rs) - 100) <= 12
    report("udp jitter", "100±40ms", "avg %.1f min %.1f max %.1f" % (statistics.mean(rs), min(rs), max(rs)), ok)

    setsim(a, latency=200)
    got, el, ok, _ = http_get(a, 32 << 20)
    mbps = got * 8 / el / 1e6
    report("latency != bw cap", "200ms, no bw", "%.0f Mbps (32MB)" % mbps, ok and mbps > 50,
           "old code: ~1 chunk per latency")

    for bw in (2, 8, 20):
        setsim(a, **{"download-bandwidth": bw})
        size = int(bw * 1e6 / 8 * 3)  # ~3s
        got, el, ok, _ = http_get(a, size)
        m = got * 8 / el / 1e6
        report("download-bandwidth", "%g Mbps" % bw, "%.2f Mbps" % m, ok and abs(m - bw) / bw <= 0.06)

    setsim(a, bandwidth=30, **{"upload-bandwidth": 4})
    n, el = http_post(a, 1_500_000)
    m = n * 8 / el / 1e6
    report("upload-bandwidth", "4 Mbps", "%.2f Mbps" % m, abs(m - 4) / 4 <= 0.06)

    N = 3000
    setsim(a, loss=10)
    got, _ = udp_burst(a, N, 0.0015)
    seqs = {struct.unpack(">I", g[:4])[0] for g in got}
    loss = 100 * (N - len(seqs)) / N
    report("udp loss (round trip)", "10%", "%.2f%%" % loss, abs(loss - 10) <= 1.8)

    setsim(a, loss=6, **{"burst-loss": 4})
    NB = 10000  # bursts of 4 need more samples for a stable average
    got, _ = udp_burst(a, NB, 0.0008)
    seqs = sorted({struct.unpack(">I", g[:4])[0] for g in got})
    loss = 100 * (NB - len(seqs)) / NB
    report("udp burst loss avg", "6%, runs of 4", "%.2f%%" % loss, abs(loss - 6) <= 1.6)

    setsim(a, corruption=5)
    got, marker = udp_burst(a, N, 0.0015, size=256)
    bad = sum(1 for g in got if g[4:] != marker or struct.unpack(">I", g[:4])[0] >= N)
    pct = 100 * bad / max(1, len(got))
    # a flip inside the 4 seq bytes is only caught if it leaves [0,N): 252/256 caught at least
    report("udp corruption", "5%", "%.2f%%" % pct, abs(pct - 5) <= 1.4)

    setsim(a, duplication=5)
    got, _ = udp_burst(a, N, 0.0015)
    cnt = {}
    for g in got:
        k = struct.unpack(">I", g[:4])[0]
        cnt[k] = cnt.get(k, 0) + 1
    pct = 100 * sum(1 for v in cnt.values() if v > 1) / N
    report("udp duplication", "5%", "%.2f%%" % pct, abs(pct - 5) <= 1.4)

    setsim(a, reordering=10)
    got, _ = udp_burst(a, N, 0.0015)
    late, mx = 0, -1
    for g in got:
        k = struct.unpack(">I", g[:4])[0]
        if k < mx:
            late += 1
        else:
            mx = k
    pct = 100 * late / N
    report("udp reordering", "10%", "%.2f%%" % pct, abs(pct - 10) <= 2)

    setsim(a, **{"packet-size": 500})
    small, _ = udp_burst(a, 50, 0.002, size=400, wait=0.5)
    big, _ = udp_burst(a, 50, 0.002, size=600, wait=0.5)
    report("udp packet-size", "500B", "400B:%d/50 600B:%d/50" % (len(small), len(big)),
           len(small) == 50 and len(big) == 0)

    setsim(a, loss=2, latency=40)
    got, el, ok, _ = http_get(a, 4 << 20)
    st = api(a, "GET", "/stats")
    report("tcp loss integrity", "2%, 40ms", "sha %s, %.2f Mbps, retransmits=%d" %
           ("ok" if ok else "BAD", got * 8 / el / 1e6, st.get("tcp-retransmits", 0)),
           ok and st.get("tcp-retransmits", 0) > 0)

    api(a, "PUT", body={"enabled": True, "latency": 120})
    api(a, "PATCH", body={"jitter": 30})
    cfg = api(a, "GET")
    report("api patch", "latency kept", "latency=%s jitter=%s" % (cfg["latency"], cfg["jitter"]),
           cfg["latency"] == 120 and cfg["jitter"] == 30)
    try:
        api(a, "PUT", body={"enabled": True, "loss": 150})
        bad = False
    except urllib.error.HTTPError as e:
        bad = e.code == 400
    report("api validation", "loss=150 rejected", "400" if bad else "accepted", bad)
    api(a, "DELETE")
    report("api delete", "disabled", "enabled=%s" % api(a, "GET")["enabled"], api(a, "GET")["enabled"] is False)


CONFIG = """
mixed-port: {mixed}
allow-lan: false
mode: rule
log-level: warning
ipv6: false
external-controller: 127.0.0.1:{api}
rules:
  - MATCH,DIRECT
net-sim:
  enabled: false
"""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--mihomo", help="mihomo binary to launch (omit to use a running core)")
    ap.add_argument("--api", default=None)
    ap.add_argument("--secret", default="")
    ap.add_argument("--socks", default="127.0.0.1:17890")
    ap.add_argument("--target-host", dest="target", default="127.0.0.1",
                    help="address of THIS machine as seen by the core")
    ap.add_argument("--bind", default="0.0.0.0")
    ap.add_argument("--out", default="netsim_e2e_result.json")
    a = ap.parse_args()

    tcp = TServer((a.bind, 0), TCPEcho)
    http = ThreadingHTTPServer((a.bind, 0), HTTPBlob)
    udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    udp.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4 << 20)
    udp.bind((a.bind, 0))
    for f in (tcp.serve_forever, http.serve_forever, lambda: udp_echo_server(udp)):
        threading.Thread(target=f, daemon=True).start()
    a.echo_port, a.http_port, a.udp_port = tcp.server_address[1], http.server_address[1], udp.getsockname()[1]

    proc = None
    if a.mihomo:
        work = tempfile.mkdtemp(prefix="netsim-e2e-")
        mixed, apiport = 17890, 19090
        with open(os.path.join(work, "config.yaml"), "w") as f:
            f.write(CONFIG.format(mixed=mixed, api=apiport))
        proc = subprocess.Popen([a.mihomo, "-d", work], stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
        a.socks, a.api = "127.0.0.1:%d" % mixed, a.api or "http://127.0.0.1:%d" % apiport
        for _ in range(100):
            try:
                urllib.request.urlopen(a.api + "/version", timeout=1)
                break
            except Exception:
                time.sleep(0.1)
    a.api = a.api or "http://127.0.0.1:9090"
    host, port = a.socks.rsplit(":", 1)
    a.socks = (host, int(port))
    try:
        run(a)
    finally:
        try:
            api(a, "DELETE")
        except Exception:
            pass
        if proc:
            proc.terminate()
    with open(a.out, "w") as f:
        json.dump(RESULTS, f, indent=2, ensure_ascii=False)
    failed = [r for r in RESULTS if not r["pass"]]
    print("\n%d/%d passed, results -> %s" % (len(RESULTS) - len(failed), len(RESULTS), a.out))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()

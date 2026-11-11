"""Two instances on one PostgreSQL + Redis must behave as one deployment. CI runs this after the smoke tests.

Env: VB_A / VB_B (default http://localhost:18080 and :18081), VB_EXPECT_VERSION (optional).
Checks:
  - both report healthy on PostgreSQL with the Redis bus, and the expected version;
  - an event raised on A (a job being queued) reaches a browser connected to B;
  - rate limits count requests made to either instance together;
  - a setting changed through A is not served stale from B's cache.
"""
import json, os, sys, threading, time, urllib.error, urllib.request, uuid

A = os.environ.get("VB_A", "http://localhost:18080")
B = os.environ.get("VB_B", "http://localhost:18081")
WANT = os.environ.get("VB_EXPECT_VERSION", "")
DB = os.environ.get("VB_EXPECT_DB", "postgres")
FAILS = 0


def check(name, ok, detail=""):
    global FAILS
    print(("[OK ] " if ok else "[ERR] ") + name + ("" if ok else "  " + str(detail)[:400]), flush=True)
    FAILS += 0 if ok else 1


def call(base, method, path, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw, code = r.read(), r.status
    except urllib.error.HTTPError as e:
        raw, code = e.read(), e.code
    try:
        js = json.loads(raw) if raw else None
    except ValueError:
        js = None
    if isinstance(js, dict) and "data" in js and len(js) <= 2:
        js = js["data"]
    return code, js


for name, base in (("A", A), ("B", B)):
    code, h = call(base, "GET", "/healthz")
    check(f"instance {name} healthy on {DB} + Redis", code == 200 and h.get("ok") and h.get("db") == DB and h.get("bus") == "redis", h)
    if WANT:
        check(f"instance {name} runs version {WANT}", h.get("version") == WANT, h)

email = f"cluster-{uuid.uuid4().hex[:8]}@example.com"
code, r = call(A, "POST", "/api/v1/auth/register", {"email": email, "password": "Cluster#2026", "nickname": "集群测试", "agree": True})
token = (r or {}).get("accessToken")
check("register on A", code == 200 and token, r)
code, me = call(A, "GET", "/api/v1/me", token=token)
sid = me["studios"][0]["id"]

# a browser connected to B
events, ready = [], threading.Event()


def listen():
    req = urllib.request.Request(B + "/api/v1/events?access_token=" + token)
    with urllib.request.urlopen(req, timeout=60) as resp:
        ready.set()
        for line in resp:
            line = line.decode().strip()
            if line.startswith("data:"):
                try:
                    events.append(json.loads(line[5:]))
                except ValueError:
                    pass


threading.Thread(target=listen, daemon=True).start()
ready.wait(10)
time.sleep(0.5)
code, job = call(A, "POST", f"/api/v1/studios/{sid}/generate", {"kind": "sfx", "prompt": "窗外的雨声", "count": 1}, token)
check("queue a job on A", code == 200 and job and job.get("id"), job)
jid = (job or {}).get("id")
seen = set()
for _ in range(60):
    seen = {e.get("data", {}).get("status") for e in list(events) if e.get("type") == "job" and e.get("data", {}).get("id") == jid}
    if "queued" in seen and ("succeeded" in seen or "failed" in seen):
        break
    time.sleep(0.5)
check("B's browser hears the job queued on A (via Redis)", "queued" in seen, seen)
check("and hears it finish, whichever instance ran it", "succeeded" in seen, seen)

limited = 0
for i in range(12):
    code, _ = call(A if i % 2 == 0 else B, "POST", "/api/v1/redeem", {"code": "NOPE-0000-0000"}, token)
    limited += code == 429
check("rate limit counts both instances together (10 per minute)", limited >= 1, f"{limited} of 12 refused")

code, adm = call(A, "POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})
admin = (adm or {}).get("accessToken")
code, site = call(B, "GET", "/api/v1/site")  # B caches the site settings now
_, current = call(A, "GET", "/api/admin/v1/settings/site", token=admin)
value = (current or {}).get("value", current) or {}
new_name = "集群检查 " + uuid.uuid4().hex[:4]
value["name"] = new_name
code, r = call(A, "PUT", "/api/admin/v1/settings/site", value, admin)
check("change the site name through A", code == 200, r)
time.sleep(0.5)
code, site = call(B, "GET", "/api/v1/site")
check("B serves the new name at once (cache dropped via Redis)", (site or {}).get("name") == new_name, site)

print("FAILS:", FAILS)
sys.exit(1 if FAILS else 0)

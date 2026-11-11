import os, json, time, urllib.request, urllib.error, urllib.parse, sys, uuid

BASE = os.environ.get("VB_BASE", "http://localhost:18080")  # CI points this at its own server
TOKEN = None


def call(method, path, body=None, expect=200, raw=False):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if TOKEN:
        headers["Authorization"] = "Bearer " + TOKEN
    req = urllib.request.Request(BASE + urllib.parse.quote(path, safe="/?=&:%"), data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            out = r.read()
            code = r.status
    except urllib.error.HTTPError as e:
        out = e.read()
        code = e.code
    try:
        js = json.loads(out) if out else None
    except Exception:
        js = out[:200]
    ok = code == expect if isinstance(expect, int) else code in expect
    flag = "OK " if ok else "ERR"
    print(f"[{flag}] {method} {path} -> {code} {str(js)[:220] if not ok else ''}")
    if not ok:
        global FAILS
        FAILS += 1
    return js


FAILS = 0


def check(name, cond, detail=""):
    global FAILS
    print(f"[{'OK ' if cond else 'ERR'}] {name}" + ("" if cond else f" {detail}"))
    if not cond:
        FAILS += 1


def wait_job(job_id, limit=180):
    for _ in range(limit):
        j = call("GET", f"/api/v1/jobs/{job_id}")
        if j and j.get("status") in ("succeeded", "failed", "canceled"):
            print("     job", j["type"], j["status"], j.get("progressText"), j.get("error", ""))
            return j
        time.sleep(1)
    print("     job timeout")
    return None


# Never bill a configured real LLM: a fake model answers for this run (top priority, removed on exit).
import atexit
FAKE = "http://127.0.0.1:18999"
ADM = call("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]


def admin_call(method, path, body=None):
    global TOKEN
    saved, TOKEN = TOKEN, ADM
    try:
        return call(method, path, body)
    finally:
        TOKEN = saved


def real_calls():
    return {p["name"]: p.get("calls", 0) for p in admin_call("GET", "/api/admin/v1/providers") or []
            if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE)}


for p in admin_call("GET", "/api/admin/v1/providers?kind=llm") or []:
    if p.get("baseUrl", "").startswith(FAKE) and p.get("priority") == 1000:
        admin_call("DELETE", f"/api/admin/v1/providers/{p['id']}")
_risky_llm = [p["name"] for p in admin_call("GET", "/api/admin/v1/providers?kind=llm") or [] if p.get("enabled") and not p.get("baseUrl", "").startswith(FAKE) and p.get("priority", 0) >= 1000]
if _risky_llm:
    sys.exit(f"abort: real LLM accounts rank as high as the test account: {_risky_llm}")
REAL_BEFORE = real_calls()
_llm = admin_call("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "测试模型（冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake-chat", "config": {"jsonMode": "json_object"}, "priority": 1000})
atexit.register(lambda: admin_call("DELETE", f"/api/admin/v1/providers/{_llm['id']}"))
# voice design picks real speech platforms before the demo engine: keep this run on a fake Qianwen account
for p in admin_call("GET", "/api/admin/v1/providers?kind=tts") or []:
    if p.get("baseUrl", "").startswith(FAKE) and p.get("priority") == 1000:
        admin_call("DELETE", f"/api/admin/v1/providers/{p['id']}")
_risky = [p["name"] for p in admin_call("GET", "/api/admin/v1/providers") or [] if p["kind"] == "tts" and p.get("enabled")
          and p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE) and p.get("priority", 0) >= 1000]
if _risky:
    sys.exit(f"abort: real speech accounts rank as high as the test accounts: {_risky}")
_tts = admin_call("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "qwen", "name": "千问测试（冒烟）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "priority": 1000})
atexit.register(lambda: admin_call("DELETE", f"/api/admin/v1/providers/{_tts['id']}"))
# covers and portraits: a fake GPT Image account, so a real image account is never billed
for p in admin_call("GET", "/api/admin/v1/providers?kind=image") or []:
    if p.get("baseUrl", "").startswith(FAKE) and p.get("priority") == 1000:
        admin_call("DELETE", f"/api/admin/v1/providers/{p['id']}")
_risky = [p["name"] for p in admin_call("GET", "/api/admin/v1/providers") or [] if p["kind"] == "image" and p.get("enabled")
          and p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE) and p.get("priority", 0) >= 1000]
if _risky:
    sys.exit(f"abort: real image accounts rank as high as the test account: {_risky}")
_img = admin_call("POST", "/api/admin/v1/providers", {"kind": "image", "provider": "openai_image", "name": "GPT Image 测试（冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "sk-img-test", "model": "gpt-image-2.6-flare", "priority": 1000})
atexit.register(lambda: admin_call("DELETE", f"/api/admin/v1/providers/{_img['id']}"))
_t = admin_call("POST", f"/api/admin/v1/providers/{_img['id']}/test")
check("image account test checks the key without drawing", "不产生费用" in (_t or {}).get("message", ""), _t)

email = f"smoke{uuid.uuid4().hex[:6]}@example.com"
# agreements: public, and signing up without accepting them is refused
legal = call("GET", "/api/v1/legal")
check("legal documents published", {d["slug"] for d in legal} >= {"terms", "privacy", "copyright", "ai"}, [d["slug"] for d in legal])
check("copyright statement disclaims liability", "不承担" in call("GET", "/api/v1/legal/copyright")["body"])
call("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "冒烟测试"}, expect=400)
reg = call("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "冒烟测试", "agree": True})
TOKEN = reg["accessToken"]
check("consent recorded at sign-up", reg["user"].get("legalVersion") == call("GET", "/api/v1/site")["legalVersion"] != "", reg["user"].get("legalVersion"))
# the full flow (stems export, collaboration) needs a paid plan; the free plan's limits are covered by membership_smoke.py
_plans = {p["code"]: p["id"] for p in admin_call("GET", "/api/admin/v1/plans?pageSize=100")["items"]}
admin_call("POST", f"/api/admin/v1/users/{reg['user']['id']}/membership", {"planId": _plans["team"], "days": 30, "note": "冒烟测试"})
me = call("GET", "/api/v1/me")
sid = me["studios"][0]["id"]
call("GET", f"/api/v1/studios/{sid}")
# product review round 2: same voice on several models listed once, credits explained, operator checklist
_flat = call("GET", f"/api/v1/studios/{sid}/voices?tab=system&pageSize=200")
_grouped = call("GET", f"/api/v1/studios/{sid}/voices?tab=system&pageSize=200&group=1")
check("grouped voice list never repeats a name", len({(v["provider"], v["name"]) for v in _grouped["items"]}) == len(_grouped["items"]) and _grouped["total"] <= _flat["total"], (_grouped["total"], _flat["total"]))
_guide = call("GET", "/api/v1/credits/guide")
check("credit guide per tier", 0 < _guide["per10kChars"]["economy"] < _guide["per10kChars"]["standard"] < _guide["per10kChars"]["premium"], _guide)
_launch = {i["key"]: i for i in admin_call("GET", "/api/admin/v1/launch-check")}
check("launch checklist covers the essentials", {"admin_password", "dev_mode", "llm", "tts", "image", "operator", "legal"} <= set(_launch), list(_launch))
text = open(sys.argv[1], encoding="utf-8").read()
prev = call("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "红楼片段"})
print("     detection:", prev["detection"], "chapters:", len(prev["chapters"]))
book = call("POST", f"/api/v1/studios/{sid}/books", {"importId": prev["id"], "title": "红楼片段", "author": "曹雪芹", "mode": "multi"})
bid = book["id"]
call("GET", f"/api/v1/books/{bid}")
chs = call("GET", f"/api/v1/books/{bid}/chapters")
cid = chs[0]["id"]
check("chapter list carries open review tickets", "openIssues" in chs[0] and "verifyingIssues" in chs[0], list(chs[0]))
# LLM is not configured: expect a clear 503
call("POST", f"/api/v1/books/{bid}/characters/analyze", {})  # answered by the fake LLM configured above
# add characters manually
for name, g in [("林黛玉", "female"), ("贾宝玉", "male")]:
    call("POST", f"/api/v1/books/{bid}/characters", {"name": name, "gender": g, "kind": "lead", "aliases": [name[1:]]})
# rule-based script
jobs = call("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [cid], "mode": "rules"})
wait_job(jobs[0]["id"])
sc = call("GET", f"/api/v1/chapters/{cid}/script")
segs = sc["segments"]
print("     segments:", len(segs))
chars = call("GET", f"/api/v1/books/{bid}/characters")
lin = [c for c in chars if c["name"] == "林黛玉"][0]
need = [s for s in segs if s["needsReview"]]
if need:
    call("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [need[0]["id"]], "patch": {"characterId": lin["id"], "emotion": "sad"}})
    rest = [s["id"] for s in need[1:]]
    if rest:
        call("PATCH", f"/api/v1/books/{bid}/segments", {"ids": rest, "patch": {"characterId": [c for c in chars if c["name"] == "贾宝玉"][0]["id"]}})
# casting (rule fallback since no LLM)
cast = call("POST", f"/api/v1/books/{bid}/casting", {"apply": True})
wait_job(cast["job"]["id"])
chars = call("GET", f"/api/v1/books/{bid}/characters")
print("     voices:", [(c["name"], (c.get("voice") or {}).get("name")) for c in chars])
# keep all synthesis on the built-in demo engine, even when a real TTS account is configured
demo = [v for v in call("GET", f"/api/v1/studios/{sid}/voices?provider=placeholder&pageSize=50")["items"] if v.get("priceTier") != "premium"]  # premium voices need a paid plan
assert demo, "no demo-engine voices"
for i, c in enumerate(chars):
    if c.get("mode") != "human" and (c.get("voice") or {}).get("provider") != "placeholder":
        call("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": demo[i % len(demo)]["id"]})
chars = call("GET", f"/api/v1/books/{bid}/characters")
assert all((c.get("voice") or {}).get("provider") == "placeholder" for c in chars if c.get("mode") != "human"), "cast onto demo engine"
narr = [c for c in chars if c["kind"] == "narrator"][0]
call("GET", f"/api/v1/characters/{narr['id']}/audition-lines")
au = call("POST", f"/api/v1/characters/{narr['id']}/audition", {"voiceId": narr["voiceId"]})
est = call("POST", f"/api/v1/books/{bid}/generate/estimate", {"chapterIds": [cid], "scope": "missing"})
print("     estimate:", est)
gen = call("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "missing"})
wait_job(gen[0]["id"])
sc = call("GET", f"/api/v1/chapters/{cid}/script")
print("     audio statuses:", {s["audioStatus"] for s in sc["segments"]})
s0 = sc["segments"][1]
call("GET", f"/api/v1/segments/{s0['id']}/takes")
regen = call("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [s0["id"]], "regenerate": True})
wait_job(regen[0]["id"])
# split/merge
sp = call("POST", f"/api/v1/segments/{s0['id']}/split", {"at": 2})
tl = call("GET", f"/api/v1/chapters/{cid}/timeline")
print("     timeline ms:", tl["durationMs"], "missing:", tl["missing"])
gen2 = call("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "missing"})
wait_job(gen2[0]["id"])
# add BGM to mix
assets = call("GET", f"/api/v1/studios/{sid}/assets?kind=bgm&scope=official")
bgm = assets["items"][0]
tl = call("GET", f"/api/v1/chapters/{cid}/timeline")
tracks = [t for t in tl["tracks"]]
for t in tracks:
    t.pop("resolved", None)
    if t["kind"] == "bgm":
        t["clips"] = [{"assetId": bgm["id"], "name": bgm["name"], "fill": True, "loop": True, "volume": 0, "fadeInMs": 1000, "fadeOutMs": 2000, "startMs": 0}]
call("PUT", f"/api/v1/chapters/{cid}/mix", {"tracks": tracks, "duckingDb": -12, "intro": False, "outro": False, "version": tl["mixVersion"]})
rj = call("POST", f"/api/v1/chapters/{cid}/render", {"preset": "generic"})
wait_job(rj["id"])
renders = call("GET", f"/api/v1/chapters/{cid}/renders")
print("     render report passed:", renders[0]["report"].get("passed"), [ (c["name"], c["value"], c["ok"]) for c in renders[0]["report"]["checks"]])
rv = call("POST", f"/api/v1/chapters/{cid}/review", {"autoIssue": True}, expect=(200, 403))
if rv and rv.get("id"):
    wait_job(rv["id"])
ex = call("POST", f"/api/v1/books/{bid}/exports", {"chapterIds": [cid], "format": "mp3", "subtitles": ["srt", "lrc"], "script": True, "lines": True, "stems": True})
jid = ex.get("jobId")
time.sleep(1)
exs = call("GET", f"/api/v1/books/{bid}/exports")
wait_job(exs[0]["export"]["jobId"])
exs = call("GET", f"/api/v1/books/{bid}/exports")
print("     export:", exs[0]["export"]["status"], exs[0]["export"].get("fileName"), exs[0]["export"].get("error"))
m4b = call("POST", f"/api/v1/books/{bid}/exports", {"chapterIds": [cid], "format": "m4b"})
time.sleep(1)
exs = call("GET", f"/api/v1/books/{bid}/exports")
wait_job(exs[0]["export"]["jobId"])
exs = call("GET", f"/api/v1/books/{bid}/exports")
print("     m4b:", exs[0]["export"]["status"], exs[0]["export"].get("error"))
# voice design & sfx & music & cover
d = call("POST", f"/api/v1/studios/{sid}/voices/design", {"description": "二十岁左右的女生，声音清亮温柔，语速偏慢"})
wait_job(d["job"]["id"])
dv = call("GET", f"/api/v1/voice-designs/{d['design']['id']}")
print("     design candidates:", len(dv["candidates"]))
v = call("POST", f"/api/v1/studios/{sid}/voice-designs/{d['design']['id']}/save", {"index": 0, "name": "清亮少女"})
for kind, prompt in [("sfx", "雨夜的雷声"), ("music", "悬疑紧张的配乐"), ("image", "雨夜古宅")]:
    j = call("POST", f"/api/v1/studios/{sid}/generate", {"kind": kind, "prompt": prompt, "durationMs": 3000 if kind == "sfx" else 20000, "count": 1})
    wait_job(j["id"])
_cov = call("GET", f"/api/v1/books/{bid}/covers")
check("cover engine can letter titles", _cov["engine"]["lettering"] and _cov["engine"]["name"].startswith("GPT Image 测试"), _cov["engine"])
j = call("POST", f"/api/v1/studios/{sid}/generate", {"kind": "image", "bookId": bid, "prompt": "雨夜古宅", "count": 2, "setCover": True})
_cj = wait_job(j["id"])
_ir = [r for r in json.loads(urllib.request.urlopen(FAKE + "/img-requests").read()) if r["path"].endswith("/images/generations")]
check("AI cover drawn by GPT Image with the title on it", _cj and _cj["status"] == "succeeded" and _ir and _ir[-1]["body"]["n"] == 2 and "「" in _ir[-1]["body"]["prompt"], (_cj or {}).get("error"))
_cov = call("GET", f"/api/v1/books/{bid}/covers")
check("cover candidates listed, one set as cover", len(_cov["items"]) >= 2 and any(c["current"] for c in _cov["items"]), _cov["items"])
call("PATCH", f"/api/v1/books/{bid}", {"coverAssetId": "00000000-0000-0000-0000-000000000000"}, expect=400)
q = call("POST", f"/api/v1/studios/{sid}/quick", {"title": "测试", "lines": [{"speaker": "旁白", "voiceId": narr["voiceId"], "text": "这是一个快速配音测试。"}, {"speaker": "黛玉", "voiceId": v["id"], "text": "你来做什么？", "emotion": "sad"}]})
time.sleep(4)
call("GET", f"/api/v1/quick/{q['id']}")
# share link
sh = call("POST", f"/api/v1/books/{bid}/shares", {"mode": "review", "allowComment": True})
tok = sh["link"]["token"]
saved = TOKEN
TOKEN = None
call("GET", f"/api/v1/share/{tok}")
call("POST", f"/api/v1/share/{tok}/comments", {"chapterId": cid, "author": "作者", "body": "第一句可以再慢一点"})
TOKEN = saved
# workflow requires team plan -> expect 403 on free
call("POST", f"/api/v1/books/{bid}/workflow", {"templateId": "x"}, expect=(403, 404))
call("GET", f"/api/v1/studios/{sid}/stats")
call("GET", f"/api/v1/studios/{sid}/search?q=红楼")
call("GET", f"/api/v1/studios/{sid}/voices?tab=official&gender=female")
call("GET", f"/api/v1/notifications")
call("GET", f"/api/v1/books/{bid}/progress")
call("GET", f"/api/v1/chapters/{cid}/integrity")
run = call("POST", f"/api/v1/books/{bid}/pipeline", {"tier": "standard"})  # answered by the fake LLM configured above
# wait for the run to end: the fake accounts are removed on exit, and a run still going would then fall
# back to the real LLM account
for _ in range(240):
    v = call("GET", f"/api/v1/pipelines/{run['id']}") if run and run.get("id") else None
    if not v or v.get("status") in ("completed", "failed", "canceled"):
        break
    time.sleep(1)
else:  # still running (or waiting for a person): stop it before the fake accounts go away
    call("POST", f"/api/v1/pipelines/{run['id']}/action", {"action": "cancel"})
print("     pipeline:", v and v.get("status"))
call("GET", "/api/v1/articles")
_real_after = real_calls()
if _real_after != REAL_BEFORE:
    FAILS += 1
    print("[ERR] real provider accounts were called:", REAL_BEFORE, "->", _real_after)
else:
    print("[OK ] real provider accounts untouched", _real_after)
print("FAILS:", FAILS)
sys.exit(1 if FAILS else 0)  # CI fails the run on any failed check

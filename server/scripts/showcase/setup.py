"""Builds the showcase studio and the book 《雾港来信》 that the landing-page screenshots are taken from.

Everything runs through the product's real workflow (import, character analysis, AI script, lexicon,
casting, synthesis, final mix, review), but the AI calls go to local stand-ins only:
  - language model: scripts/showcase/showcase_llm.py on 127.0.0.1:18997 (priority 1000, removed on exit)
  - speech: scripts/fake_llm.py on 127.0.0.1:18999 as a Qianwen account (priority 1000, removed on exit)
Real provider accounts are never called; the script asserts their call counts are unchanged.

Usage: python3 scripts/showcase/setup.py   (backend on :18080, both stand-ins running)
Prints the showcase login and the book id.
"""
import atexit, json, os, sys, time, urllib.error, urllib.parse, urllib.request

BASE = os.environ.get("VB_BASE", "http://localhost:18080")
LLM = "http://127.0.0.1:18997"
FAKE = "http://127.0.0.1:18999"
EMAIL, PASSWORD = "showcase@example.com", "Showcase#2026"
HERE = os.path.dirname(os.path.abspath(__file__))
TOKEN = None


def call(method, path, body=None, token=None, ok=(200,)):
    tok = token if token is not None else TOKEN
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None, method=method,
                                 headers={**({"Content-Type": "application/json"} if body is not None else {}), **({"Authorization": "Bearer " + tok} if tok else {})})
    try:
        with urllib.request.urlopen(req, timeout=180) as r:
            code, out = r.status, r.read()
    except urllib.error.HTTPError as e:
        code, out = e.code, e.read()
    js = json.loads(out) if out else None
    if code not in ok:
        raise SystemExit(f"{method} {path} -> {code} {str(js)[:300]}")
    return js


ADM = call("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": os.environ.get("VB_ADMIN_PASSWORD", "Admin@123456")}, token="")["accessToken"]
adm = lambda m, p, b=None: call(m, p, b, token=ADM)


def real_calls():
    return {p["name"]: p.get("calls", 0) for p in adm("GET", "/api/admin/v1/providers")
            if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(("http://127.0.0.1", "http://localhost"))}


for p in adm("GET", "/api/admin/v1/providers"):
    local = p.get("baseUrl", "").startswith(("http://127.0.0.1", "http://localhost"))
    if p["kind"] in ("llm", "tts") and p.get("enabled") and not local and p.get("provider") != "placeholder" and p.get("priority", 0) >= 1000:
        sys.exit(f"abort: real account「{p['name']}」ranks as high as the stand-ins")
BEFORE = real_calls()
temp = []
llm = adm("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "演示用模型（展示页）", "baseUrl": LLM + "/v1", "apiKey": "k", "model": "showcase-chat", "config": {"jsonMode": "json_object"}, "priority": 1000})
temp.append(llm["id"])
tts = adm("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "qwen", "name": "演示用语音（展示页）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "priority": 1000})
temp.append(tts["id"])
atexit.register(lambda: [adm("DELETE", f"/api/admin/v1/providers/{i}") for i in temp])


def wait(job_id, limit=600):
    for _ in range(limit):
        j = call("GET", f"/api/v1/jobs/{job_id}")
        if j["status"] in ("succeeded", "failed", "canceled"):
            if j["status"] != "succeeded":
                raise SystemExit(f"job {j['type']} {j['status']}: {j.get('error')}")
            return j
        time.sleep(0.5)
    raise SystemExit("job timeout")


# ---- the showcase account, on the team plan ----
r = call("POST", "/api/v1/auth/login", {"email": EMAIL, "password": PASSWORD}, token="", ok=(200, 400, 401, 404))
if not r or "accessToken" not in r:
    r = call("POST", "/api/v1/auth/register", {"email": EMAIL, "password": PASSWORD, "nickname": "沈知秋", "agree": True}, token="")
TOKEN = r["accessToken"]
me = call("GET", "/api/v1/me")
uid, sid = me["user"]["id"], me["studios"][0]["id"]
plans = {p["code"]: p["id"] for p in adm("GET", "/api/admin/v1/plans?pageSize=100")["items"]}
adm("POST", f"/api/admin/v1/users/{uid}/membership", {"planId": plans["team"], "days": 365, "note": "展示页演示账号"})
call("PATCH", f"/api/v1/studios/{sid}", {"name": "雾港有声工作室"})
for b in call("GET", f"/api/v1/studios/{sid}/books?pageSize=50")["items"]:
    if b["title"] == "雾港来信":
        call("DELETE", f"/api/v1/books/{b['id']}")

# ---- the book ----
text = open(os.path.join(HERE, "story.txt"), encoding="utf-8").read()
imp = call("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "雾港来信"})
book = call("POST", f"/api/v1/studios/{sid}/books", {"importId": imp["id"], "title": "雾港来信", "author": "沈知秋", "mode": "multi"})
bid = book["id"]
call("PATCH", f"/api/v1/books/{bid}", {"narrator": "雾港有声工作室", "genre": "悬疑"})
# the stand-in speech service has no real recogniser behind it, so automatic proofreading would only report noise
_bk = call("GET", f"/api/v1/books/{bid}")
_settings = (_bk.get("book") or _bk).get("settings") or {}
_settings["autoQc"] = False
call("PATCH", f"/api/v1/books/{bid}", {"settings": _settings, "copyright": {"type": "original", "licensor": "沈知秋", "statement": "原创作品，作者授权雾港有声工作室制作有声版"}})
chapters = call("GET", f"/api/v1/books/{bid}/chapters")
print("chapters:", [c["title"] for c in chapters])

j = call("POST", f"/api/v1/books/{bid}/characters/analyze", {})
if isinstance(j, dict) and j.get("id"):
    wait(j["id"])
elif isinstance(j, dict) and j.get("job"):
    wait(j["job"]["id"])
for jb in call("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [c["id"] for c in chapters], "mode": "ai"}):
    wait(jb["id"])
terms = call("POST", f"/api/v1/books/{bid}/lexicon/extract")
ids = [t["id"] for t in (terms if isinstance(terms, list) else terms.get("items", []))]
if ids:
    call("POST", f"/api/v1/books/{bid}/lexicon/activate", {"ids": ids})
intro = call("POST", f"/api/v1/books/{bid}/intro")
call("PATCH", f"/api/v1/books/{bid}", {"description": intro["description"], "tags": intro.get("tags", [])})

# ---- casting: platform voices picked for each part ----
CAST = {"旁白": ("谢舒柔", "qwen-audio-3.1-tts-flash"), "林晚": ("叶清禾", "qwen-audio-3.1-tts-flash"),
        "陆沉舟": ("龙岚珺蕊", "qwen-audio-3.0-tts-plus"), "周伯": ("龙岭芷杏", "qwen-audio-3.0-tts-plus"), "阿满": ("叶知晴", "qwen-audio-3.1-tts-flash")}
chars = call("GET", f"/api/v1/books/{bid}/characters")
for c in chars:
    name = "旁白" if c["kind"] == "narrator" else c["name"]
    if name not in CAST:
        continue
    vname, vmodel = CAST[name]
    found = call("GET", f"/api/v1/studios/{sid}/voices?tab=system&q={urllib.parse.quote(vname)}&pageSize=50")["items"]
    pick = next((v for v in found if v["name"] == vname and v.get("model") == vmodel), None) or next((v for v in found if v["name"] == vname), None)
    if not pick:
        raise SystemExit(f"voice {vname} not found")
    call("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": pick["id"]})
chars = call("GET", f"/api/v1/books/{bid}/characters")
print("cast:", [(c["name"], (c.get("voice") or {}).get("name")) for c in chars])

# ---- audio, mix, review ----
for jb in call("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [c["id"] for c in chapters], "scope": "missing"}):
    wait(jb["id"])
for c in chapters[:2]:
    rj = call("POST", f"/api/v1/chapters/{c['id']}/render", {"allowMissing": True})
    wait((rj.get("job") or rj)["id"])
call("POST", f"/api/v1/chapters/{chapters[0]['id']}/conclude", {"pass": True, "reason": ""})
seg2 = call("GET", f"/api/v1/chapters/{chapters[1]['id']}/script")["segments"]
pick = [s for s in seg2 if "母亲的字" in s["text"]] + [s for s in seg2 if "他是谁呀" in s["text"]]
if len(pick) >= 2:
    call("POST", f"/api/v1/books/{bid}/issues", {"chapterId": chapters[1]["id"], "segmentId": pick[0]["id"], "type": "performance", "title": "情绪再压一点", "description": "“这是我母亲的字”要更克制，停顿再长半拍"})
    call("POST", f"/api/v1/books/{bid}/issues", {"chapterId": chapters[1]["id"], "segmentId": pick[1]["id"], "type": "noise", "title": "耳语音量偏小", "description": "阿满这句耳语比前后低了约 4 dB"})

cover = os.path.join(HERE, "cover.png")
if os.path.exists(cover):
    import subprocess
    out = subprocess.run(["curl", "-s", "-H", "Authorization: Bearer " + TOKEN, "-F", f"file=@{cover}", f"{BASE}/api/v1/books/{bid}/cover"], capture_output=True, text=True)
    print("cover:", out.stdout[:120])

call("POST", "/api/v1/notifications/read", {"all": True})
for i in temp:
    adm("DELETE", f"/api/admin/v1/providers/{i}")
temp.clear()
AFTER = real_calls()
if AFTER != BEFORE:
    sys.exit(f"REAL ACCOUNTS WERE CALLED: {BEFORE} -> {AFTER}")
print("real accounts untouched:", AFTER)
print(json.dumps({"email": EMAIL, "password": PASSWORD, "studio": sid, "book": bid, "chapters": [c["id"] for c in chapters]}, ensure_ascii=False))

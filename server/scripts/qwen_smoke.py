"""End-to-end check of the Qianwen TTS integration against the fake DashScope server in fake_llm.py.

Run with the backend on :18080 and `python3 scripts/fake_llm.py` on :18999.
"""
import json, sys, time, urllib.request, urllib.error

BASE = "http://localhost:18080"
FAKE = "http://127.0.0.1:18999"


def req(method, path, body=None, token=None, base=BASE):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(base + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=60) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{method} {path} -> {e.code}: {e.read().decode()}")


def check(cond, msg):
    print(("  ok  " if cond else "  FAIL") + " " + msg)
    if not cond:
        sys.exit(1)


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]
me = req("GET", "/api/v1/me", token=req("POST", "/api/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"])
tok = req("POST", "/api/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]
sid = me["user"]["personalStudioId"]

# clean slate: remove only accounts left by earlier test runs (never a real account)
real_qwen = False
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p["provider"] in ("qwen", "aliyun"):
        if p.get("baseUrl") == FAKE:
            req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
        elif p.get("enabled"):
            real_qwen = True
# AI casting needs an LLM: the fake answers for this run only (top priority, removed on exit)
import atexit
for p in req("GET", "/api/admin/v1/providers?kind=llm", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
_llm = req("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "测试模型（千问）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake-chat", "config": {"jsonMode": "json_object"}, "priority": 1000}, token=adm)
atexit.register(lambda: req("DELETE", f"/api/admin/v1/providers/{_llm['id']}", token=adm))
time.sleep(16)  # provider cache TTL

# remove books left by earlier runs (free plan allows 3)
for b in req("GET", f"/api/v1/studios/{sid}/books?pageSize=100", token=tok)["items"]:
    if b["title"] == "千问测试":
        req("DELETE", f"/api/v1/books/{b['id']}", token=tok)
for b in req("GET", f"/api/v1/studios/{sid}/trash", token=tok) or []:
    if b["title"] == "千问测试":
        req("DELETE", f"/api/v1/studios/{sid}/trash/{b['id']}", token=tok)

print("1. built-in voices stay hidden without an account")
if real_qwen:
    print("  skip a real Qianwen account is configured, so built-in voices are visible")
else:
    hidden = req("GET", f"/api/v1/studios/{sid}/voices?model=cosyvoice-v3-flash", token=tok)
    check(hidden["total"] == 0, f"cosyvoice voices hidden (total={hidden['total']})")

print("2. add a Qianwen account")
# top priority so every request in this test goes to the fake server, not a real account
acc = req("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "qwen", "name": "千问测试", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "priority": 1000}, token=adm)
t = req("POST", f"/api/admin/v1/providers/{acc['id']}/test", token=adm)
check(t["ok"], "connection test: " + t["message"])

print("3. voices per model become visible with official previews")
cosy = req("GET", f"/api/v1/studios/{sid}/voices?model=cosyvoice-v3-flash&pageSize=100", token=tok)
check(cosy["total"] == 88, f"88 cosyvoice-v3-flash voices (got {cosy['total']})")
v31 = req("GET", f"/api/v1/studios/{sid}/voices?model=qwen-audio-3.1-tts-flash&q=%E9%BE%99%E5%AE%89%E6%AC%A2", token=tok)["items"][0]
check(v31["sampleUrl"].startswith("https://") and v31["modelLabel"] == "Qwen-Audio 3.1 Flash", "sample url + model label")
check(v31["capabilities"]["inlineTags"] and v31["capabilities"]["instruction"], "qwen-audio voice supports tags & instruction")
cv = next(v for v in cosy["items"] if v["providerVoiceId"] == "longxiu_v3")
check(not cv["capabilities"]["instruction"] and not cv["capabilities"]["emotion"], "cosyvoice system voice: no instruction/emotion")
base = req("GET", f"/api/v1/studios/{sid}/voices?series=base&model=qwen-audio-3.0-tts-flash", token=tok)
check(base["total"] >= 590, f"base voices listed ({base['total']})")

print("4. preview uses the voice's own model")
pv = req("POST", f"/api/v1/studios/{sid}/voices/{v31['id']}/preview", {"text": "今天的天气真不错" + str(int(time.time())), "emotion": "sad"}, token=tok)
last = req("GET", "/tts-requests", base=FAKE)[-1]
check(last["model"] == "qwen-audio-3.1-tts-flash" and last["input"]["voice"] == "longanhuan_v3.1", f"model/voice {last['model']} {last['input']['voice']}")
check(last["input"]["text"].startswith("[sad]"), "emotion as inline tag: " + last["input"]["text"])
check(pv["url"].startswith("/files/"), "preview stored")
pv2 = req("POST", f"/api/v1/studios/{sid}/voices/{cv['id']}/preview", {"text": "银行" + str(int(time.time())), "emotion": "happy"}, token=tok)
last = req("GET", "/tts-requests", base=FAKE)[-1]
check(last["model"] == "cosyvoice-v3-flash" and "[" not in last["input"]["text"] and not last["input"].get("instruction"), "cosyvoice request has no tags/instruction")

print("5. book synthesis: tags, instruction and pronunciation hot_fix")
bk = req("POST", f"/api/v1/studios/{sid}/imports", {"text": "第一章 测试\n他走进银行。\n“我来取钱。”他叹气道。", "title": "千问测试"}, token=tok)
book = req("POST", f"/api/v1/studios/{sid}/books", {"importId": bk["id"], "title": "千问测试", "mode": "multi", "autoAnalyze": False,
                                                   "chapters": [{"sources": [i], "title": c["title"], "included": c["included"]} for i, c in enumerate(bk["chapters"])]}, token=tok)
chs = req("GET", f"/api/v1/books/{book['id']}/chapters", token=tok)
cid = chs[0]["id"]
req("POST", f"/api/v1/books/{book['id']}/scripts/parse", {"chapterIds": [cid], "mode": "rules"}, token=tok)
for _ in range(60):
    sc = req("GET", f"/api/v1/chapters/{cid}/script", token=tok)
    if sc.get("segments"):
        break
    time.sleep(1)
segs = sc["segments"]
check(len(segs) > 0, f"{len(segs)} segments")
narr = next(c for c in req("GET", f"/api/v1/books/{book['id']}/characters", token=tok) if c["kind"] == "narrator")
req("PUT", f"/api/v1/characters/{narr['id']}/voice", {"voiceId": v31["id"]}, token=tok)
seg = next(s for s in segs if "银行" in s["text"])
off = seg["text"].index("行")
req("PATCH", f"/api/v1/books/{book['id']}/segments", {"ids": [seg["id"]], "patch": {"characterId": narr["id"], "emotion": "tired", "tags": ["叹气"], "instruction": "放慢语速",
                                                                                   "prons": [{"offset": off, "char": "行", "pinyin": "háng"}]}}, token=tok)
n_before = len(req("GET", "/tts-requests", base=FAKE))
req("POST", f"/api/v1/books/{book['id']}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [seg["id"]], "regenerate": True}, token=tok)
for _ in range(60):
    s = next(x for x in req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"] if x["id"] == seg["id"])
    if s["audioStatus"] in ("ready", "failed"):
        break
    time.sleep(1)
check(s["audioStatus"] == "ready", "segment audio ready: " + (s.get("lastError") or "ok"))
reqs = req("GET", "/tts-requests", base=FAKE)[n_before:]
r = reqs[-1]["input"]
check(r["text"].startswith("[tired][sighing]"), "tags: " + r["text"])
check(r.get("instruction") == "放慢语速", "instruction: " + str(r.get("instruction")))
check(r.get("hot_fix", {}).get("pronunciation") == [{"行": "hang2"}], "hot_fix: " + json.dumps(r.get("hot_fix"), ensure_ascii=False))
check(r.get("seed", 0) > 0, "re-generation sends a fresh seed")

print("6. AI casting draws from the Qianwen library")
res = req("POST", f"/api/v1/books/{book['id']}/casting", {"apply": False}, token=tok)
if "job" in res:
    for _ in range(90):
        if req("GET", f"/api/v1/jobs/{res['job']['id']}", token=tok)["status"] in ("succeeded", "failed"):
            break
        time.sleep(1)
cands = [c for ch in req("GET", f"/api/v1/books/{book['id']}/characters", token=tok) for c in (ch.get("casting") or [])]
check(len(cands) > 0, f"{len(cands)} casting candidates")
ids = {c["voiceId"] for c in cands}
models = set()
for vid in list(ids)[:10]:
    models.add(req("GET", f"/api/v1/voices/{vid}", token=tok).get("provider"))
check("qwen" in models, f"casting candidates include qwen voices ({models})")

print("7. wrong key gives a friendly message")
req("PATCH", f"/api/admin/v1/providers/{acc['id']}", {"apiKey": "sk-wrong"}, token=adm)
t = req("POST", f"/api/admin/v1/providers/{acc['id']}/test", token=adm)
check(not t["ok"] and "API Key" in t["message"], t["message"])
req("PATCH", f"/api/admin/v1/providers/{acc['id']}", {"apiKey": "sk-qwen-test"}, token=adm)
req("DELETE", f"/api/v1/books/{book['id']}", token=tok)
req("DELETE", f"/api/v1/studios/{sid}/trash/{book['id']}", token=tok)
req("DELETE", f"/api/admin/v1/providers/{acc['id']}", token=adm)
print("all good")

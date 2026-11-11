"""End-to-end check of intro/outro templates: compose vs fixed audio, every chapter, caching.

Requires the server on :18080 and fake_llm.py on :18999. All synthesis runs on the built-in demo
engine (placeholder voices); real provider accounts must not be called, which is asserted at the end.
"""
import atexit, json, os, subprocess, sys, tempfile, time, urllib.error, urllib.parse, urllib.request, uuid

BASE = "http://localhost:18080"
FAKE = "http://127.0.0.1:18999"
FAILS = 0
TMP = tempfile.mkdtemp(prefix="tplsmoke")


def req(method, path, body=None, token=None, expect=200, files=None, raw=False):
    h, data = {}, None
    if files:
        boundary = uuid.uuid4().hex
        parts = [f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode() for k, v in (body or {}).items()]
        for k, (name, content) in files.items():
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode() + content + b"\r\n")
        parts.append(f"--{boundary}--\r\n".encode())
        data, h["Content-Type"] = b"".join(parts), "multipart/form-data; boundary=" + boundary
    elif body is not None:
        data, h["Content-Type"] = json.dumps(body).encode(), "application/json"
    if token:
        h["Authorization"] = "Bearer " + token
    url = path if path.startswith("http") else BASE + urllib.parse.quote(path, safe="/?=&:%")
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method, headers=h), timeout=180) as resp:
            code, out = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        code, out = e.code, e.read()
    if raw:
        return code, out
    js = json.loads(out) if out else None
    if code != expect:
        raise SystemExit(f"{method} {path} -> {code}, expected {expect}: {str(js)[:300]}")
    return js


def check(cond, msg):
    global FAILS
    print(("  ok  " if cond else "  FAIL") + " " + msg)
    FAILS += 0 if cond else 1


def err(out):
    return ((out or {}).get("error") or {}).get("message", "")


def wait(job_id, token):
    for _ in range(240):
        j = req("GET", f"/api/v1/jobs/{job_id}", token=token)
        if j["status"] in ("succeeded", "failed", "canceled"):
            return j
        time.sleep(0.5)
    raise SystemExit("job timeout")


def tone(seconds, name):
    path = os.path.join(TMP, name)
    subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=frequency=330:duration={seconds}", "-ac", "2", "-ar", "44100", path], check=True)
    return open(path, "rb").read()


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]

# ---- safety: never call or modify real provider accounts ----
_cleanup = []
atexit.register(lambda: [f() for f in reversed(_cleanup)])
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p["kind"] in ("llm", "tts") and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
for body in ({"kind": "llm", "provider": "openai_chat", "name": "测试模型（片头冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake"},
             {"kind": "tts", "provider": "qwen", "name": "千问测试（片头冒烟）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash"}):
    acc = req("POST", "/api/admin/v1/providers", {**body, "priority": 1000}, token=adm)
    _cleanup.append(lambda i=acc["id"]: req("DELETE", f"/api/admin/v1/providers/{i}", token=adm))
real_calls = lambda: {p["name"]: p.get("calls", 0) for p in req("GET", "/api/admin/v1/providers", token=adm) if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE)}  # the built-in demo engine is local
calls_before = real_calls()

# ---- a member with a book whose chapters are voiced by the demo engine ----
email = f"tpl{uuid.uuid4().hex[:6]}@example.com"
tok = req("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "片头测试", "agree": True})["accessToken"]
sid = req("GET", "/api/v1/me", token=tok)["user"]["personalStudioId"]
req("POST", f"/api/admin/v1/studios/{sid}/credits", {"amount": 20000, "reason": "片头冒烟"}, token=adm)
text = open(os.path.join(os.path.dirname(__file__), "sample.txt"), encoding="utf-8").read()
imp = req("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "片头测试书"}, token=tok)
bid = req("POST", f"/api/v1/studios/{sid}/books", {"importId": imp["id"], "title": "片头测试书", "author": "测试"}, token=tok)["id"]
chs = req("GET", f"/api/v1/books/{bid}/chapters", token=tok)
for j in req("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [c["id"] for c in chs], "mode": "rules"}, token=tok):
    wait(j["id"], tok)
demo = [v for v in req("GET", f"/api/v1/studios/{sid}/voices?provider=placeholder&pageSize=50", token=tok)["items"] if v.get("priceTier") != "premium"]
for i, c in enumerate(req("GET", f"/api/v1/books/{bid}/characters", token=tok)):
    req("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": demo[i % len(demo)]["id"]}, token=tok)
if not all((c.get("voice") or {}).get("provider") == "placeholder" for c in req("GET", f"/api/v1/books/{bid}/characters", token=tok)):
    sys.exit("abort: a character is not on the demo engine")
for g in req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [c["id"] for c in chs], "scope": "missing"}, token=tok):
    wait(g["id"], tok)
cid, cid2 = chs[0]["id"], chs[1]["id"]
voice = demo[0]["id"]


def render(chapter):
    j = wait(req("POST", f"/api/v1/chapters/{chapter}/render", {"allowMissing": True}, token=tok)["id"], tok)  # rule-based parse leaves a few lines without a speaker
    r = req("GET", f"/api/v1/chapters/{chapter}/renders", token=tok)[0]
    return j, r


print("1. baseline render without templates (creates the chapter mixes)")
_, r0 = render(cid)
_, r0b = render(cid2)
check(r0["status"] == "ready" and r0b["status"] == "ready", f"rendered {r0['durationMs']} / {r0b['durationMs']} ms")

print("2. compose template: draft preview, placeholders")
tpls = req("GET", f"/api/v1/studios/{sid}/templates", token=tok)
check(all("mode" in v["template"] for v in tpls) and any(v["template"]["scope"] == "system" for v in tpls), "list has official templates with a mode")
pv = req("POST", f"/api/v1/studios/{sid}/templates/preview", {"kind": "intro", "text": "{书名}，{章节名}。", "voiceId": voice}, token=tok)
code, audio = req("GET", pv["url"], raw=True)
check(code == 200 and len(audio) > 10000, "unsaved draft can be previewed")
empty = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "intro", "mode": "compose", "text": ""}, token=tok, expect=400)
check("播报文字" in err(empty), "empty compose template rejected")
intro = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "intro", "name": "测试片头", "text": "{书名}，{章节名}。", "voiceId": voice}, token=tok)["template"]
check(intro["mode"] == "compose", "compose intro created")

print("3. fixed audio: generate once, upload, limits")
bad = req("POST", f"/api/v1/studios/{sid}/templates/generate", {"kind": "outro", "text": "感谢收听{书名}", "voiceId": voice}, token=tok, expect=400)
check("占位符" in err(bad), "placeholders refused for fixed audio: " + err(bad))
gen = req("POST", f"/api/v1/studios/{sid}/templates/generate", {"kind": "outro", "name": "测试片尾", "text": "本集到这里就结束了，感谢收听。", "voiceId": voice}, token=tok)
check(gen["kind"] == "jingle" and gen["durationMs"] > 500, f"generated fixed audio ({gen['durationMs']} ms)")
up = req("POST", f"/api/v1/studios/{sid}/assets", {"kind": "jingle", "name": "我的片尾音乐"}, token=tok, files={"file": ("outro.wav", tone(3, "outro.wav"))})
check(up["kind"] == "jingle" and 2900 <= up["durationMs"] <= 3100, "uploaded jingle")
lib = req("GET", f"/api/v1/studios/{sid}/assets", token=tok)
check(all(a["kind"] != "jingle" for a in lib["items"]), "jingles stay out of the default asset library list")
noaudio = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "outro", "mode": "audio"}, token=tok, expect=400)
check("固定音频" in err(noaudio), "audio mode needs an audio")
long = req("POST", f"/api/v1/studios/{sid}/assets", {"kind": "jingle", "name": "太长"}, token=tok, files={"file": ("long.wav", tone(190, "long.wav"))})
toolong = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "outro", "mode": "audio", "audioAssetId": long["id"]}, token=tok, expect=400)
check("3 分钟" in err(toolong), "audio over 3 minutes rejected")
outro_v = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "outro", "mode": "audio", "name": "固定片尾", "audioAssetId": up["id"]}, token=tok)
outro = outro_v["template"]
check(outro_v["audio"]["id"] == up["id"] and outro["mode"] == "audio", "fixed outro created with audio view")
pv2 = req("POST", f"/api/v1/studios/{sid}/templates/{outro['id']}/preview", token=tok)
check(pv2["url"] == outro_v["audio"]["url"], "fixed template preview is the audio itself (no synthesis)")

print("4. choose them in the book settings -> every existing chapter gets them")
book = req("GET", f"/api/v1/books/{bid}", token=tok)
settings = {**book["settings"], "introTemplateId": intro["id"], "outroTemplateId": outro["id"]}
req("PATCH", f"/api/v1/books/{bid}", {"settings": settings}, token=tok)
tl1, tl2 = req("GET", f"/api/v1/chapters/{cid}/timeline", token=tok), req("GET", f"/api/v1/chapters/{cid2}/timeline", token=tok)
check(tl1["intro"] and tl1["outro"] and tl2["intro"] and tl2["outro"], "mixes created before the template now include intro and outro")
check(req("GET", f"/api/v1/books/{bid}/chapters", token=tok)[0].get("renderStale") in (True, None), "renders marked stale")
usage = {v["template"]["id"]: v for v in req("GET", f"/api/v1/studios/{sid}/templates", token=tok)}
check(usage[intro["id"]]["books"] == 1 and usage[outro["id"]]["books"] == 1, "template usage counted")

print("5. render: intro + fixed outro in every chapter, announcements cached")
bal0 = req("GET", f"/api/v1/studios/{sid}/credits", token=tok)["balance"]["available"]
_, r1 = render(cid)
_, r1b = render(cid2)
bal1 = req("GET", f"/api/v1/studios/{sid}/credits", token=tok)["balance"]["available"]
grow, grow2 = r1["durationMs"] - r0["durationMs"], r1b["durationMs"] - r0b["durationMs"]
check(r1["status"] == "ready" and grow >= 3500 and grow2 >= 3500, f"both chapters grew by intro + 3 s outro ({grow} / {grow2} ms)")
_, r2 = render(cid)
bal2 = req("GET", f"/api/v1/studios/{sid}/credits", token=tok)["balance"]["available"]
check(abs(r2["durationMs"] - r1["durationMs"]) <= 50, "re-render gives the same length")
check(bal2 == bal1, f"re-render does not synthesise or charge the intro again ({bal0} -> {bal1} -> {bal2})")

print("6. per-chapter opt-out, template edits, deletion")
mix = req("GET", f"/api/v1/chapters/{cid2}/timeline", token=tok)
req("PUT", f"/api/v1/chapters/{cid2}/mix", {"tracks": [{k: v for k, v in t.items() if k != "resolved"} for t in mix["tracks"]], "duckingDb": mix["duckingDb"], "intro": False, "outro": True, "version": mix["mixVersion"]}, token=tok)
_, r3 = render(cid2)
check(r3["durationMs"] < r1b["durationMs"] - 500, "chapter with the intro switched off renders without it")
req("PUT", f"/api/v1/studios/{sid}/templates/{intro['id']}", {**intro, "text": "{书名}。"}, token=tok)
chap = next(c for c in req("GET", f"/api/v1/books/{bid}/chapters", token=tok) if c["id"] == cid)
check(chap.get("renderStale") is True, "editing a template marks its books' renders stale")
# switching the intro to another template keeps the chapter that opted out
other = req("POST", f"/api/v1/studios/{sid}/templates", {"kind": "intro", "name": "另一个片头", "mode": "audio", "audioAssetId": gen["id"]}, token=tok)["template"]
req("PATCH", f"/api/v1/books/{bid}", {"settings": {**settings, "introTemplateId": other["id"]}}, token=tok)
check(not req("GET", f"/api/v1/chapters/{cid2}/timeline", token=tok)["intro"], "switching templates keeps a chapter's opt-out")
req("DELETE", f"/api/v1/studios/{sid}/templates/{outro['id']}", token=tok)
_, r4 = render(cid)
check(r4["status"] == "ready", "deleted template is skipped instead of failing the render")

calls_after = real_calls()
check(calls_after == calls_before, f"real provider accounts were not called {calls_after}")
print("FAILS:", FAILS)
sys.exit(1 if FAILS else 0)

"""End-to-end check of the MiniMax speech adapter through the product: account, voice library, previews,
chapter generation (sync), performance tags and readings, throttling retry, async long-text mode.

Requires the server on :18080 and fake_llm.py on :18999 (which also speaks the MiniMax API). Only temporary
priority-1000 accounts pointing at the fake server are used; real provider accounts must not be called,
which is asserted at the end. Catalog voices touched by the test (preview samples, synced voices) are restored.
"""
import atexit, json, os, subprocess, sys, tempfile, time, urllib.error, urllib.parse, urllib.request, uuid

BASE = "http://localhost:18080"
FAKE = "http://127.0.0.1:18999"
FAILS = 0
RUN = uuid.uuid4().hex[:6]  # keeps texts unique so caches and the fake server's one-off failures apply every run


def req(method, path, body=None, token=None, expect=200, raw=False, files=None):
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


def wait(job_id, token):
    for _ in range(400):
        j = req("GET", f"/api/v1/jobs/{job_id}", token=token)
        if j["status"] in ("succeeded", "failed", "canceled"):
            return j
        time.sleep(0.5)
    raise SystemExit("job timeout")


def mm_reqs():
    return req("GET", FAKE + "/mm-requests")


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]

# ---- safety: only temporary fake accounts; restore anything touched in the catalog ----
_cleanup = []
atexit.register(lambda: [f() for f in reversed(_cleanup)])
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p["kind"] in ("llm", "tts") and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
real_calls = lambda: {p["name"]: p.get("calls", 0) for p in req("GET", "/api/admin/v1/providers", token=adm) if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE)}
calls_before = real_calls()
llm = req("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "测试模型（MiniMax 冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake", "priority": 1000}, token=adm)
_cleanup.append(lambda: req("DELETE", f"/api/admin/v1/providers/{llm['id']}", token=adm))

def require_fakes_first(adm_token, fake):
    """Cloning picks the highest-priority platform: refuse to run unless every real speech account ranks below the fakes."""
    risky = [p["name"] for p in req("GET", "/api/admin/v1/providers", token=adm_token) if p["kind"] == "tts" and p.get("enabled")
             and p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(fake) and p.get("priority", 0) >= 1000]
    if risky:
        sys.exit(f"abort: real speech accounts rank as high as the test accounts: {risky}")


require_fakes_first(adm, FAKE)

print("1. preset and account")
pre = next(p for p in req("GET", "/api/admin/v1/providers/presets", token=adm) if p["key"] == "minimax_tts")
check(pre["implemented"] and pre["rpm"] == 20 and "speech-2.8-hd" in pre["models"], "MiniMax preset is implemented with the official rate limit")
acc = req("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "minimax", "name": "MiniMax 测试（冒烟）", "baseUrl": FAKE, "apiKey": "mm-test", "model": "speech-2.8-hd",
                                              "priority": 1000, "rpm": 600, "concurrency": 3, "config": {}}, token=adm)
_cleanup.append(lambda: req("DELETE", f"/api/admin/v1/providers/{acc['id']}", token=adm))
t = req("POST", f"/api/admin/v1/providers/{acc['id']}/test", token=adm)
check(t.get("ok") is not False, f"connection test: {t.get('message')}")
bad = req("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "minimax", "name": "MiniMax 错误密钥（冒烟）", "baseUrl": FAKE, "apiKey": "wrong", "priority": 1, "enabled": False}, token=adm)
_cleanup.append(lambda: req("DELETE", f"/api/admin/v1/providers/{bad['id']}", token=adm))
t = req("POST", f"/api/admin/v1/providers/{bad['id']}/test", token=adm)
check(t.get("ok") is False and "API Key" in (t.get("message") or ""), f"wrong key is explained: {t.get('message')}")

print("2. voice library")
email = f"mm{uuid.uuid4().hex[:6]}@example.com"
reg = req("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "MiniMax 测试", "agree": True})
tok, uid = reg["accessToken"], reg["user"]["id"]
sid = req("GET", "/api/v1/me", token=tok)["user"]["personalStudioId"]
req("POST", f"/api/admin/v1/studios/{sid}/credits", {"amount": 50000, "reason": "MiniMax 冒烟"}, token=adm)
lib = req("GET", f"/api/v1/studios/{sid}/voices?provider=minimax&pageSize=100", token=tok)
check(lib["total"] >= 600, f"{lib['total']} MiniMax voices visible once an account is enabled")
turbo = req("GET", f"/api/v1/studios/{sid}/voices?model=speech-2.8-turbo&pageSize=100", token=tok)["items"]
hd = req("GET", f"/api/v1/studios/{sid}/voices?model=speech-2.8-hd&pageSize=5", token=tok)["items"]
check(turbo and all(v["priceTier"] == "standard" for v in turbo) and hd and all(v["priceTier"] == "premium" for v in hd), "Turbo entries are standard tier, HD entries premium")
v0 = turbo[0]
caps = v0["capabilities"]
check(v0["modelLabel"] == "MiniMax 2.8 Turbo" and caps["emotion"] and caps["inlineTags"] and "laughing" in caps.get("tagKeys", []) and not caps["instruction"],
      "capabilities: emotion, interjection tags only, no instructions")
narr = req("GET", f"/api/v1/studios/{sid}/voices?provider=minimax&q=" + urllib.parse.quote("旁白") + "&pageSize=50", token=tok)["items"]
check(any("电台男主播" in v["name"] for v in narr), "narration voices are tagged for casting")

print("3. sync voices from the account")
FAKE_VOICE = "moss_audio_fake_new"  # only exists on the fake server
synced = lambda: [v for v in req("GET", "/api/admin/v1/voices?provider=minimax&q=" + urllib.parse.quote("新上线温柔女声") + "&pageSize=50", token=adm)["items"] if v["providerVoiceId"] == FAKE_VOICE]
for v in synced():  # left over from an interrupted run
    req("DELETE", f"/api/admin/v1/voices/{v['id']}", token=adm)
r = req("POST", f"/api/admin/v1/providers/{acc['id']}/sync-voices", token=adm)
fresh = synced()
_cleanup.append(lambda: [req("DELETE", f"/api/admin/v1/voices/{v['id']}", token=adm) for v in synced()])
check(r["imported"] == 2 and all(v["providerVoiceId"] == "moss_audio_fake_new" and v["gender"] == "female" for v in fresh), f"new platform voice imported for both models ({r['imported']})")

print("4. first preview of a voice without an official sample is free and shared")
target = next(v for v in turbo if not v.get("sampleUrl"))
_cleanup.append(lambda i=target["id"]: req("PUT", f"/api/admin/v1/voices/{i}", {"sampleAssetId": ""}, token=adm))
bal = lambda: req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"]
c0 = bal()
pv = req("POST", f"/api/v1/studios/{sid}/voices/{target['id']}/preview", {}, token=tok)
code, audio = req("GET", pv["url"], raw=True)
again = req("GET", f"/api/v1/voices/{target['id']}", token=tok)
check(code == 200 and audio[:4] == b"RIFF" and bal() == c0, "preview generated without charging credits")
check(bool(again.get("sampleUrl")), "the generated preview is now the voice's sample for everyone")
custom = req("POST", f"/api/v1/studios/{sid}/voices/{target['id']}/preview", {"text": f"我自己写的试听文本，编号{RUN}。"}, token=tok)
check(bool(custom.get("url")), "custom preview text still works")
last = mm_reqs()[-1]["body"]
check(last["model"] == "speech-2.8-turbo" and last["voice_setting"]["voice_id"] == target["providerVoiceId"] and last["audio_setting"]["format"] == "wav", "request uses the voice's model and id")

print("5. generate a chapter with MiniMax voices")
text = open(os.path.join(os.path.dirname(__file__), "sample.txt"), encoding="utf-8").read()
imp = req("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "MiniMax 测试书"}, token=tok)
bid = req("POST", f"/api/v1/studios/{sid}/books", {"importId": imp["id"], "title": "MiniMax 测试书", "author": "测试"}, token=tok)["id"]
book = req("GET", f"/api/v1/books/{bid}", token=tok)
req("PATCH", f"/api/v1/books/{bid}", {"settings": {**book["settings"], "autoQc": False}}, token=tok)  # fake ASR never matches fake audio
chs = req("GET", f"/api/v1/books/{bid}/chapters", token=tok)
cid = chs[0]["id"]
for j in req("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [cid], "mode": "rules"}, token=tok):
    wait(j["id"], tok)
zh = [v for v in turbo if v.get("language") == "zh-CN"]
for i, c in enumerate(req("GET", f"/api/v1/books/{bid}/characters", token=tok)):
    req("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": zh[i % len(zh)]["id"]}, token=tok)
chars = req("GET", f"/api/v1/books/{bid}/characters", token=tok)
if not all((c.get("voice") or {}).get("provider") == "minimax" for c in chars):
    sys.exit("abort: a character is not on the MiniMax test account")
n0 = len(mm_reqs())
for g in req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "missing"}, token=tok):
    j = wait(g["id"], tok)
segs = [s for s in req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"] if not s["removed"] and s["type"] != "sfx"]
voiced = [s for s in segs if s.get("characterId")]
ready = [s for s in voiced if s["audioStatus"] == "ready"]
check(j["status"] == "succeeded" and len(ready) == len(voiced) and voiced and len(mm_reqs()) - n0 == len(voiced), f"{len(ready)}/{len(voiced)} lines voiced with {len(mm_reqs()) - n0} MiniMax calls")

print("6. emotion, interjection tag and reading reach MiniMax")
seg = next(s for s in voiced if len(s["text"]) >= 6)
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [seg["id"]], "patch": {"emotion": "sad", "cues": [{"at": 2, "tag": "sighing"}], "prons": [{"offset": 0, "char": seg["text"][0], "pinyin": "hǎo", "homophone": ""}]}}, token=tok)
for g in req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [seg["id"]], "regenerate": True}, token=tok):
    wait(g["id"], tok)
body = mm_reqs()[-1]["body"]
check(body["voice_setting"].get("emotion") == "sad", "emotion sad is sent")
check("(sighs)" in body["text"] and body["text"].startswith("(hao3)"), f"text carries the interjection and the reading: {body['text'][:30]}")

print("7. throttling is retried automatically")
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [seg["id"]], "patch": {"text": f"限流测试{RUN}：这句话第一次会被限流。", "cues": [], "prons": []}}, token=tok)
t0, n0 = time.time(), len(mm_reqs())
for g in req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [seg["id"]], "regenerate": True}, token=tok):
    j = wait(g["id"], tok)
s1 = next(s for s in req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"] if s["id"] == seg["id"])
check(s1["audioStatus"] == "ready" and len(mm_reqs()) - n0 == 2, f"ready after a rate-limit reply and one retry ({time.time() - t0:.1f}s)")

print("8. async long-text mode")
req("PATCH", f"/api/admin/v1/providers/{acc['id']}", {"config": {"synthMode": "async"}}, token=adm)
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [seg["id"]], "patch": {"text": f"这一句走异步合成任务{RUN}。"}}, token=tok)
n0 = len(mm_reqs())
for g in req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [seg["id"]], "regenerate": True}, token=tok):
    j = wait(g["id"], tok)
s2 = next(s for s in req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"] if s["id"] == seg["id"])
new = mm_reqs()[n0:]
check(s2["audioStatus"] == "ready" and [x["api"] for x in new] == ["async"] and "audio_sample_rate" in new[0]["body"]["audio_setting"], "line generated through the async task API")
req("PATCH", f"/api/admin/v1/providers/{acc['id']}", {"config": {}}, token=adm)

print("9. render the chapter")
j = wait(req("POST", f"/api/v1/chapters/{cid}/render", {"allowMissing": True}, token=tok)["id"], tok)
rr = req("GET", f"/api/v1/chapters/{cid}/renders", token=tok)[0]
check(j["status"] == "succeeded" and rr["status"] == "ready" and rr["durationMs"] > 5000, f"rendered {rr['durationMs']} ms")

print("10. voice cloning")
TMP = tempfile.mkdtemp(prefix="mmclone")


def tone(seconds, name, ext="wav"):
    path = os.path.join(TMP, name + "." + ext)
    enc = ["-ar", "48000", "-c:a", "libopus"] if ext == "webm" else ["-ar", "44100"]  # browser recordings are webm/opus
    subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=frequency=210:duration={seconds}", "-ac", "1", *enc, path], check=True)
    return open(path, "rb").read()


plan = max(req("GET", "/api/admin/v1/plans?pageSize=100", token=adm)["items"], key=lambda p: p.get("cloneVoices") or 0)  # room for several clones
req("POST", f"/api/admin/v1/users/{uid}/membership", {"planId": plan["id"], "days": 30, "note": "MiniMax 冒烟"}, token=adm)
opts = req("GET", f"/api/v1/studios/{sid}/voices/clone-options", token=tok)
q = opts["quick"]
check(q["available"] and q["provider"] == "minimax" and not q["demo"] and q["minSeconds"] == 10 and q["maxSeconds"] == 300 and q["expiresUnused"] and q["expiryDays"] == 7,
      f"quick clone runs on {q['platform']} (10 s - 5 min, kept once used within 7 days)")
check(opts["pro"]["available"] and opts["pro"]["demo"], "professional clone is honestly reported as demo-only")
chal = req("GET", "/api/v1/voices/challenge", token=tok)["sentence"]
open("/tmp/asr_text.txt", "w").write(chal)  # the fake ASR "hears" the challenge sentence
ev = req("POST", f"/api/v1/studios/{sid}/uploads", {"kind": "voice_sample"}, token=tok, files={"file": ("c.wav", tone(4, "challenge"))})
short = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("short.wav", tone(4, "short"))})
out = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": "太短的样本", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [short["assetId"]], "denoise": True}, token=tok, expect=400)
check("至少需要 10 秒" in out["error"]["message"], f"a 4-second sample is refused up front: {out['error']['message']}")
s1 = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("a.webm", tone(8, "part1", "webm"))})
s2 = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("b.wav", tone(9, "part2"))})
c0 = bal()
n0 = len(mm_reqs())
v = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": "我的声音（MiniMax）", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [s1["assetId"], s2["assetId"]], "denoise": True, "gender": "male"}, token=tok)
for _ in range(120):
    vv = req("GET", f"/api/v1/voices/{v['id']}", token=tok)
    if vv["status"] in ("ready", "failed"):
        break
    time.sleep(0.5)
new = mm_reqs()[n0:]
up = [x["body"] for x in new if x["api"] == "upload"]
cl = [x["body"] for x in new if x["api"] == "clone"]
check(vv["status"] == "ready" and vv["provider"] == "minimax" and vv["providerVoiceId"].startswith("vb"), f"clone ready: {vv['providerVoiceId']} ({vv.get('statusNote')})")
check(len(up) == 1 and up[0]["purpose"] == "voice_clone" and up[0]["riff"] and up[0]["name"].endswith(".wav"), "webm + wav samples merged into one WAV and uploaded for cloning")
check(cl and cl[0]["need_noise_reduction"] is True and cl[0]["need_volume_normalization"] is True and cl[0]["model"] == "speech-2.8-hd" and cl[0]["text"], "clone request asks MiniMax to denoise and read a preview line")
check(not any(x["api"] in ("sync", "async") for x in new), "no synthesis right after cloning (MiniMax bills the first use)")
check(bool(vv.get("expiresAt")) and bool(vv.get("sampleUrl")) and bal() < c0, "expiry set, MiniMax preview stored as the sample, clone credits charged")
pv = req("POST", f"/api/v1/studios/{sid}/voices/{v['id']}/preview", {"text": f"第一次正式使用复刻音色{RUN}。"}, token=tok)
used = mm_reqs()[-1]["body"]
vv = req("GET", f"/api/v1/voices/{v['id']}", token=tok)
check(used["voice_setting"]["voice_id"] == vv["providerVoiceId"] and not vv.get("expiresAt") and "长期有效" in (vv.get("statusNote") or ""), "first use activates the clone: expiry cleared")
req("DELETE", f"/api/v1/studios/{sid}/voices/{v['id']}", token=tok)
clones = req("GET", FAKE + "/mm-clones")
check(clones.get(vv["providerVoiceId"], {}).get("deleted") is True, "deleting the voice also deletes it on MiniMax")

calls_after = real_calls()
check(calls_after == calls_before, f"real provider accounts untouched {calls_after}")
print("\nFAILS", FAILS)
sys.exit(1 if FAILS else 0)

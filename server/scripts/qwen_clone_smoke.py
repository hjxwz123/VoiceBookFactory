"""End-to-end check of Qianwen voice cloning (Qwen-Audio-TTS / CosyVoice voice-enrollment) through the product.

Requires the server on :18080 and fake_llm.py on :18999. Only temporary priority-1000 accounts pointing at the
fake server are used; the run aborts if a real speech account ranks as high, and asserts at the end that real
provider accounts were not called.
"""
import atexit, json, os, subprocess, sys, tempfile, time, urllib.error, urllib.parse, urllib.request, uuid

BASE = "http://localhost:18080"
FAKE = "http://127.0.0.1:18999"
FAILS = 0
RUN = uuid.uuid4().hex[:6]
TMP = tempfile.mkdtemp(prefix="qwclone")


def req(method, path, body=None, token=None, expect=200, files=None):
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
    js = json.loads(out) if out else None
    if code != expect:
        raise SystemExit(f"{method} {path} -> {code}, expected {expect}: {str(js)[:300]}")
    return js


def check(cond, msg):
    global FAILS
    print(("  ok  " if cond else "  FAIL") + " " + msg)
    FAILS += 0 if cond else 1


def tone(seconds, name, ext="wav"):
    path = os.path.join(TMP, name + "." + ext)
    enc = ["-ar", "48000", "-c:a", "libopus"] if ext == "webm" else ["-ar", "44100"]  # browser recordings are webm/opus
    subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=frequency=230:duration={seconds}", "-ac", "1", *enc, path], check=True)
    return open(path, "rb").read()


def wait_voice(vid, token):
    for _ in range(120):
        v = req("GET", f"/api/v1/voices/{vid}", token=token)
        if v["status"] in ("ready", "failed"):
            return v
        time.sleep(0.5)
    raise SystemExit("clone timeout")


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]

# ---- safety: temporary fake accounts only, ranked above every real one ----
_cleanup = []
atexit.register(lambda: [f() for f in reversed(_cleanup)])
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p["kind"] in ("llm", "tts") and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
risky = [p["name"] for p in req("GET", "/api/admin/v1/providers", token=adm) if p["kind"] == "tts" and p.get("enabled")
         and p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE) and p.get("priority", 0) >= 1000]
if risky:
    sys.exit(f"abort: real speech accounts rank as high as the test accounts: {risky}")
real_calls = lambda: {p["name"]: p.get("calls", 0) for p in req("GET", "/api/admin/v1/providers", token=adm) if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE)}
calls_before = real_calls()
for body in ({"kind": "llm", "provider": "openai_chat", "name": "测试模型（千问复刻冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake"},
             {"kind": "tts", "provider": "qwen", "name": "千问测试（复刻冒烟）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "config": {}}):
    acc = req("POST", "/api/admin/v1/providers", {**body, "priority": 1000}, token=adm)
    _cleanup.append(lambda i=acc["id"]: req("DELETE", f"/api/admin/v1/providers/{i}", token=adm))
qwen_acc = acc["id"]

reg = req("POST", "/api/v1/auth/register", {"email": f"qwc{RUN}@example.com", "password": "Passw0rd!", "nickname": "千问复刻测试", "agree": True})
tok, uid = reg["accessToken"], reg["user"]["id"]
sid = req("GET", "/api/v1/me", token=tok)["user"]["personalStudioId"]
plan = max(req("GET", "/api/admin/v1/plans?pageSize=100", token=adm)["items"], key=lambda p: p.get("cloneVoices") or 0)  # room for several clones
req("POST", f"/api/admin/v1/users/{uid}/membership", {"planId": plan["id"], "days": 30, "note": "千问复刻冒烟"}, token=adm)
req("POST", f"/api/admin/v1/studios/{sid}/credits", {"amount": 20000, "reason": "千问复刻冒烟"}, token=adm)

print("1. clone options")
q = req("GET", f"/api/v1/studios/{sid}/voices/clone-options", token=tok)["quick"]
check(q["available"] and q["provider"] == "qwen" and q["platform"] == "千问" and q["modelLabel"] == "Qwen-Audio 3.1 Plus", f"quick clone on {q['platform']} ({q.get('modelLabel')})")
check(q["minSeconds"] == 10 and q["maxSeconds"] == 60 and q["recommendSeconds"] == 20 and not q["expiresUnused"], "sample 10 s - 1 min, 20 s recommended, kept until deleted")
if q["provider"] != "qwen":
    sys.exit("abort: cloning is not routed to the fake Qianwen account")

print("2. sample checks and consent")
chal = req("GET", "/api/v1/voices/challenge", token=tok)["sentence"]
open("/tmp/asr_text.txt", "w").write(chal)  # the fake ASR "hears" the challenge sentence
ev = req("POST", f"/api/v1/studios/{sid}/uploads", {"kind": "voice_sample"}, token=tok, files={"file": ("c.wav", tone(4, "challenge"))})
short = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("short.wav", tone(4, "short"))})
out = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": "太短", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [short["assetId"]]}, token=tok, expect=400)
check("千问至少需要 10 秒" in out["error"]["message"], f"too-short sample refused: {out['error']['message']}")

print("3. clone with webm + wav samples")
s1 = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("a.webm", tone(8, "p1", "webm"))})
s2 = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=tok, files={"file": ("b.wav", tone(9, "p2"))})
c0 = req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"]
v = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": f"我的声音（千问）{RUN}", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [s1["assetId"], s2["assetId"]], "denoise": True, "gender": "female"}, token=tok)
vv = wait_voice(v["id"], tok)
check(vv["status"] == "ready" and vv["provider"] == "qwen" and vv["providerVoiceId"].startswith("qwen-audio-3.1-tts-plus-vb"), f"clone ready: {vv['providerVoiceId']}")
check(vv["model"] == "qwen-audio-3.1-tts-plus" and vv["modelLabel"] == "Qwen-Audio 3.1 Plus" and vv["priceTier"] == "premium" and not vv.get("expiresAt"), "voice bound to Qwen-Audio 3.1 Plus, premium tier, no expiry")
qv = req("GET", FAKE + "/qw-voices")[vv["providerVoiceId"]]
inp = qv["input"]
check(inp["target_model"] == "qwen-audio-3.1-tts-plus" and inp["enable_preprocess"] is True and inp["enable_volume_normalization"] == "true" and inp["language_hints"] == ["zh"], "create_voice: target model, server-side denoise, volume normalization, language hint")
check(10 <= inp["max_prompt_audio_length"] <= 20 and qv["bytes"] > 400000, f"merged sample sent as Base64 WAV ({qv['bytes']} bytes, uses {inp['max_prompt_audio_length']} s)")
synth = [r for r in req("GET", FAKE + "/tts-requests") if r.get("input", {}).get("voice") == vv["providerVoiceId"]]
check(synth and synth[-1]["model"] == "qwen-audio-3.1-tts-plus" and bool(vv.get("sampleUrl")), "sample synthesized with the clone on its bound model")
check(req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"] < c0, "clone credits charged")
pv = req("POST", f"/api/v1/studios/{sid}/voices/{v['id']}/preview", {"text": f"用复刻的声音读一句话{RUN}。"}, token=tok)
check(bool(pv.get("url")), "the clone works in previews (the bound model is used automatically)")

print("4. another clone model chosen by the admin")
req("PATCH", f"/api/admin/v1/providers/{qwen_acc}", {"config": {"cloneModel": "cosyvoice-v3.5-flash"}}, token=adm)
q = req("GET", f"/api/v1/studios/{sid}/voices/clone-options", token=tok)["quick"]
check(q["modelLabel"] == "CosyVoice v3.5 Flash", "clone options follow the account's clone model")
v2 = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": f"CosyVoice 复刻 {RUN}", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [s2["assetId"], s1["assetId"]], "denoise": False}, token=tok)
vv2 = wait_voice(v2["id"], tok)
inp2 = req("GET", FAKE + "/qw-voices")[vv2["providerVoiceId"]]["input"]
check(vv2["status"] == "ready" and vv2["model"] == "cosyvoice-v3.5-flash" and vv2["priceTier"] == "economy" and inp2["enable_preprocess"] is False, "CosyVoice v3.5 Flash clone, economy tier, local denoise off")

print("5. delete")
for x in (vv, vv2):
    req("DELETE", f"/api/v1/studios/{sid}/voices/{x['id']}", token=tok)
qws = req("GET", FAKE + "/qw-voices")
check(qws[vv["providerVoiceId"]]["deleted"] and qws[vv2["providerVoiceId"]]["deleted"], "deleting the voices also deletes them on Qianwen")

calls_after = real_calls()
check(calls_after == calls_before, f"real provider accounts untouched {calls_after}")
print("\nFAILS", FAILS)
sys.exit(1 if FAILS else 0)

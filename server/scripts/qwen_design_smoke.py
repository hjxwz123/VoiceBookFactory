"""End-to-end check of Qianwen voice design (voice-enrollment with voice_prompt) through the product.

Requires the server on :18080 and fake_llm.py on :18999. Only temporary priority-1000 accounts pointing at the
fake server are used; the run aborts if a real speech account ranks as high, and asserts at the end that real
provider accounts were not called.
"""
import atexit, json, sys, time, urllib.error, urllib.parse, urllib.request, uuid

BASE = "http://localhost:18080"
FAKE = "http://127.0.0.1:18999"
FAILS = 0
RUN = uuid.uuid4().hex[:6]


def req(method, path, body=None, token=None, expect=200):
    h, data = {}, None
    if body is not None:
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


def design(token, sid, description, preview=""):
    d = req("POST", f"/api/v1/studios/{sid}/voices/design", {"description": description, "previewText": preview}, token=token)["design"]
    for _ in range(120):
        d = req("GET", f"/api/v1/voice-designs/{d['id']}", token=token)
        if d["status"] in ("ready", "failed"):
            return d
        time.sleep(0.5)
    raise SystemExit("design timeout")


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
for body in ({"kind": "llm", "provider": "openai_chat", "name": "测试模型（千问设计冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake"},
             {"kind": "tts", "provider": "qwen", "name": "千问测试（设计冒烟）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "config": {}}):
    acc = req("POST", "/api/admin/v1/providers", {**body, "priority": 1000}, token=adm)
    _cleanup.append(lambda i=acc["id"]: req("DELETE", f"/api/admin/v1/providers/{i}", token=adm))
qwen_acc = acc["id"]

reg = req("POST", "/api/v1/auth/register", {"email": f"qwd{RUN}@example.com", "password": "Passw0rd!", "nickname": "千问设计测试", "agree": True})
tok = reg["accessToken"]
sid = req("GET", "/api/v1/me", token=tok)["user"]["personalStudioId"]
req("POST", f"/api/admin/v1/studios/{sid}/credits", {"amount": 20000, "reason": "千问设计冒烟"}, token=adm)

print("1. design options")
o = req("GET", f"/api/v1/studios/{sid}/voices/design-options", token=tok)
check(o["available"] and o["provider"] == "qwen" and not o["demo"] and o["modelLabel"] == "Qwen-Audio 3.1 Plus", f"designed by {o['platform']} ({o.get('modelLabel')})")
check(o["promptMax"] == 500 and o["keepHours"] == 24, "description up to 500 characters, unsaved candidates kept 24 h")
if o["provider"] != "qwen":
    sys.exit("abort: voice design is not routed to the fake Qianwen account")

print("2. design three candidates")
long_desc = "温柔知性的女性，三十岁左右，语调平和，吐字清晰，语速偏慢，声音圆润有磁性，适合有声书朗读。" * 12  # ~540 characters
c0 = req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"]
d = design(tok, sid, long_desc, "こんにちは、今日はいい天気ですね。")
check(d["status"] == "ready" and len(d["candidates"]) == 3 and all(c["previewUrl"] for c in d["candidates"]), f"3 candidates with previews ({d.get('error') or 'ok'})")
qv = req("GET", FAKE + "/qw-voices")
mine = [qv[c["providerVoiceId"]] for c in d["candidates"]]
inp = mine[0]["input"]
check(all(v.get("design") for v in mine) and len(inp["voice_prompt"]) <= 500 and inp["voice_prompt"].endswith("。"), f"description cut to {len(inp['voice_prompt'])} characters at a sentence end")
check(inp["language_hints"] == ["zh"] and 15 <= len(inp["preview_text"]) <= 200 and "こんにちは" not in inp["preview_text"], "a Japanese preview line is replaced by the default Chinese one")
check(len({v["input"]["prefix"] for v in mine}) == 3 and mine[0]["parameters"]["sample_rate"] == 48000, "each candidate has its own prefix; 48 kHz preview")
check(req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"] < c0, "design credits charged")

print("3. save a candidate")
v = req("POST", f"/api/v1/studios/{sid}/voice-designs/{d['id']}/save", {"index": 1, "name": f"知性女声 {RUN}"}, token=tok)
check(v["origin"] == "designed" and v["provider"] == "qwen" and v["providerVoiceId"] == d["candidates"][1]["providerVoiceId"], "saved as a designed Qianwen voice")
check(v["model"] == "qwen-audio-3.1-tts-plus" and v["modelLabel"] == "Qwen-Audio 3.1 Plus" and v["priceTier"] == "premium" and bool(v.get("sampleUrl")), "bound to Qwen-Audio 3.1 Plus, premium tier, preview kept as the sample")
dup = req("POST", f"/api/v1/studios/{sid}/voice-designs/{d['id']}/save", {"index": 1, "name": "重复"}, token=tok, expect=400)
check("已经保存过了" in dup["error"]["message"], "the same candidate cannot be saved twice")
pv = req("POST", f"/api/v1/studios/{sid}/voices/{v['id']}/preview", {"text": f"用设计出来的声音读一句话{RUN}。"}, token=tok)
synth = [r for r in req("GET", FAKE + "/tts-requests") if r.get("input", {}).get("voice") == v["providerVoiceId"]]
check(bool(pv.get("url")) and synth and synth[-1]["model"] == "qwen-audio-3.1-tts-plus", "the designed voice synthesizes on its bound model")

print("4. admin picks another design model; English preview")
req("PATCH", f"/api/admin/v1/providers/{qwen_acc}", {"config": {"designModel": "cosyvoice-v3.5-flash"}}, token=adm)
o = req("GET", f"/api/v1/studios/{sid}/voices/design-options", token=tok)
check(o["modelLabel"] == "CosyVoice v3.5 Flash", "design options follow the account's design model")
d2 = design(tok, sid, "活泼的少年，十五岁左右，声音清亮，语速偏快，适合动画角色", "Good evening, and welcome to tonight's story.")
inp2 = req("GET", FAKE + "/qw-voices")[d2["candidates"][0]["providerVoiceId"]]["input"]
v2 = req("POST", f"/api/v1/studios/{sid}/voice-designs/{d2['id']}/save", {"index": 0, "name": f"少年 {RUN}"}, token=tok)
check(inp2["language_hints"] == ["en"] and inp2["target_model"] == "cosyvoice-v3.5-flash" and v2["model"] == "cosyvoice-v3.5-flash" and v2["priceTier"] == "economy",
      "English preview hinted as en; CosyVoice v3.5 Flash voice, economy tier")

print("5. content review and deletion")
c1 = req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"]
bad = design(tok, sid, f"带有违禁内容的声音描述{RUN}")
check(bad["status"] == "failed" and "内容审核" in (bad.get("error") or ""), f"rejected description explained: {bad.get('error')}")
check(req("GET", f"/api/v1/studios/{sid}", token=tok)["credits"]["total"] == c1, "a rejected description costs no credits")
for x in (v, v2):
    req("DELETE", f"/api/v1/studios/{sid}/voices/{x['id']}", token=tok)
qv = req("GET", FAKE + "/qw-voices")
check(qv[v["providerVoiceId"]]["deleted"] and qv[v2["providerVoiceId"]]["deleted"], "deleting designed voices also deletes them on Qianwen")
check(not qv[d["candidates"][0]["providerVoiceId"]]["deleted"], "unsaved candidates stay until the 24-hour cleanup (covered by go test)")

calls_after = real_calls()
check(calls_after == calls_before, f"real provider accounts untouched {calls_after}")
print("\nFAILS", FAILS)
sys.exit(1 if FAILS else 0)

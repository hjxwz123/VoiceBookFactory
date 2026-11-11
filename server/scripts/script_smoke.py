"""End-to-end check: empty book -> import chapters -> AI script with positional cues -> edit/delete/undo -> synthesis.

Needs the backend on :18080 and scripts/fake_llm.py on :18999 (fake LLM + fake DashScope TTS).
"""
import json, sys, time, urllib.request, urllib.error

BASE, FAKE = "http://localhost:18080", "http://127.0.0.1:18999"


def req(method, path, body=None, token=None, base=BASE):
    r = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, method=method)
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


def wait(fn, timeout=90):
    for _ in range(timeout):
        v = fn()
        if v:
            return v
        time.sleep(1)
    return None


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]

# By default the fake LLM answers (top priority for this run, removed on exit). Pass --real-llm to let the
# configured model annotate the script instead: it checks prompt quality and costs a few real tokens.
import atexit
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
if "--real-llm" not in sys.argv:
    _llm = req("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "测试模型（画本）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake-chat", "config": {"jsonMode": "json_object"}, "priority": 1000}, token=adm)
    atexit.register(lambda: req("DELETE", f"/api/admin/v1/providers/{_llm['id']}", token=adm))
email = f"script{int(time.time())}@example.com"
tok = req("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "画本测试", "agree": True})["accessToken"]
sid = req("GET", "/api/v1/me", token=tok)["user"]["personalStudioId"]

print("1. create a book without a manuscript")
book = req("POST", f"/api/v1/studios/{sid}/books", {"title": "空白起步", "mode": "multi", "autoAnalyze": True}, token=tok)
bid = book["id"]
check(req("GET", f"/api/v1/books/{bid}/chapters", token=tok) in (None, []), "book has no chapters yet")
check(req("GET", f"/api/v1/books/{bid}", token=tok)["progress"]["next"] == "import", "next step is importing the manuscript")

print("2. import a manuscript into the existing book")
text = "第一章 初见\n夜色渐深，她推开窗，叹了口气。\n“你来做什么？”林黛玉冷冷地问。\n宝玉笑道：“我来看看妹妹。”\n\n第二章 雨夜\n雨下了一整夜。"
pv = req("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "空白起步"}, token=tok)
check(len(pv["chapters"]) == 2, f"preview found {len(pv['chapters'])} chapters")
r = req("POST", f"/api/v1/books/{bid}/chapters/import", {"importId": pv["id"], "chapters": [{"sources": [0], "title": "第一章 初见", "included": True}, {"sources": [1], "title": "第二章 雨夜", "included": True}], "autoAnalyze": True}, token=tok)
check(r["added"] == 2, "2 chapters appended")
chs = req("GET", f"/api/v1/books/{bid}/chapters", token=tok)
check([c["index"] for c in chs] == [1, 2], "chapter order")
cast = wait(lambda: len([c for c in req("GET", f"/api/v1/books/{bid}/characters", token=tok) if c["kind"] != "narrator"]) >= 2)
check(cast, "character analysis ran after import")

print("3. AI script carries positional cues and performance direction (text untouched)")
cid = chs[0]["id"]
req("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [cid]}, token=tok)
segs = wait(lambda: req("GET", f"/api/v1/chapters/{cid}/script", token=tok).get("segments"))
check(segs, f"{len(segs or [])} segments")
# works against the fake LLM or a real one: check the rules, not exact model choices
tags = {t["key"]: t for t in req("GET", "/api/v1/performance-tags", token=tok)}
check(len(tags) == 30 and sum(t["kind"] == "sound" for t in tags.values()) == 7, "30 performance tags (7 sounds)")
narr = next(s for s in segs if s["text"].startswith("夜色渐深"))
check(narr["text"] == "夜色渐深，她推开窗，叹了口气。", "original text preserved")
for s in segs:
    print(f"       {s['type']:9} {s['text']:<18} cues={[(c['at'], c['tag']) for c in s['cues']]} 指导={s['instruction']!r}")
check(all(c["tag"] in tags and 0 <= c["at"] <= len(s["text"]) for s in segs for c in s["cues"]), "every cue is a known tag inside its line")
check(all(tags[c["tag"]]["kind"] == "style" for s in segs if s["type"] == "narration" for c in s["cues"]), "narration carries no sound cues")
meta = ("台词", "旁白", "叙述", "提示语", "广播剧", "删除")
check(all(len(s["instruction"]) <= 24 and not any(w in s["instruction"] for w in meta) for s in segs), "directions are short and speakable")
check(any(s["cues"] or s["instruction"] for s in segs if s["type"] == "dialogue"), "dialogue got cues or a direction")
lin = next(s for s in segs if "你来做什么" in s["text"])

print("4. cues follow text edits")
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [narr["id"]], "patch": {"cues": [{"at": 10, "tag": "sighing"}]}}, token=tok)
upd = req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [narr["id"]], "patch": {"text": "夜色渐深了，她轻轻推开窗，叹了口气。"}}, token=tok)
check(upd[0]["cues"] == [{"at": 13, "tag": "sighing"}], f"remapped {upd[0]['cues']}")
upd = req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [narr["id"]], "patch": {"cues": [{"at": 99, "tag": "tired"}, {"at": 0, "tag": "叹气"}, {"at": 2, "tag": "nope"}], "tags": []}}, token=tok)
check(upd[0]["cues"] == [{"at": 0, "tag": "sighing"}, {"at": 18, "tag": "tired"}], f"normalized {upd[0]['cues']}")

print("5. delete a single line and undo")
n0 = len(req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"])
d = req("POST", f"/api/v1/books/{bid}/segments/delete", {"ids": [lin["id"]]}, token=tok)
check(len(d["deleted"]) == 1 and d["locked"] == 0, "one line deleted")
segs2 = req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"]
check(len(segs2) == n0 - 1 and all(s["id"] != lin["id"] for s in segs2), "line gone from script")
res = req("POST", f"/api/v1/books/{bid}/segments/restore", {"segments": d["deleted"]}, token=tok)
segs3 = req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"]
back = next((s for s in segs3 if s["id"] == lin["id"]), None)
check(res["restored"] == 1 and back and back["cues"] == lin["cues"] and [s["id"] for s in segs3] == [s["id"] for s in segs], "undo restores the same line in place")
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [lin["id"]], "patch": {"locked": True}}, token=tok)
try:
    req("POST", f"/api/v1/books/{bid}/segments/delete", {"ids": [lin["id"]]}, token=tok)
    check(False, "locked line must not be deleted")
except SystemExit as e:
    check("锁定" in str(e), "locked line is protected")

print("6. cues reach the Qwen-Audio request at the right place")
# top priority so synthesis goes to the fake server, never to a real Qianwen account
acc = req("POST", "/api/admin/v1/providers", {"kind": "tts", "provider": "qwen", "name": "千问测试", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash", "priority": 1000}, token=adm)
try:
    time.sleep(16)
    v = req("GET", f"/api/v1/studios/{sid}/voices?model=qwen-audio-3.1-tts-flash&pageSize=1", token=tok)["items"][0]
    narrator = next(c for c in req("GET", f"/api/v1/books/{bid}/characters", token=tok) if c["kind"] == "narrator")
    req("PUT", f"/api/v1/characters/{narrator['id']}/voice", {"voiceId": v["id"]}, token=tok)
    line = f"第{int(time.time()) % 100000}夜，她轻轻推开窗，叹了口气。"  # unique text: bypass the synthesis cache
    sigh = line.index("叹")
    req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [narr["id"]], "patch": {"characterId": narrator["id"], "emotion": "neutral", "text": line, "cues": [{"at": sigh, "tag": "sighing"}, {"at": 0, "tag": "tired"}]}}, token=tok)
    n_before = len(req("GET", "/tts-requests", base=FAKE))
    req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "segments", "segmentIds": [narr["id"]], "regenerate": True}, token=tok)
    s = wait(lambda: next((x for x in req("GET", f"/api/v1/chapters/{cid}/script", token=tok)["segments"] if x["id"] == narr["id"] and x["audioStatus"] in ("ready", "failed")), None))
    check(s and s["audioStatus"] == "ready", "line synthesized: " + str(s and s.get("lastError")))
    sent = req("GET", "/tts-requests", base=FAKE)[n_before:][-1]["input"]["text"]
    check(sent == f"[tired]{line[:sigh]}[sighing]{line[sigh:]}", "sent: " + sent)
finally:
    req("DELETE", f"/api/admin/v1/providers/{acc['id']}", token=adm)
print("all good" + (" (real LLM)" if "--real-llm" in sys.argv else ""))

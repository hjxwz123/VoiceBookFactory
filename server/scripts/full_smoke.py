"""Covers features not exercised by smoke.py / ai_smoke.py. Requires the server on :18080 and fake_llm.py on :18999."""
import json, time, urllib.request, urllib.error, urllib.parse, sys, uuid, subprocess, os

BASE = os.environ.get("VB_BASE", "http://localhost:18080")  # CI points this at its own server
FAILS = 0


def req(method, path, body=None, token=None, expect=200, files=None, headers=None, base=BASE):
    global FAILS
    h = dict(headers or {})
    data = None
    if files:
        boundary = uuid.uuid4().hex
        parts = []
        for k, v in (body or {}).items():
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
        for k, (name, content) in files.items():
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode() + content + b"\r\n")
        parts.append(f"--{boundary}--\r\n".encode())
        data = b"".join(parts)
        h["Content-Type"] = "multipart/form-data; boundary=" + boundary
    elif body is not None:
        data = json.dumps(body).encode()
        h["Content-Type"] = "application/json"
    if token:
        h["Authorization"] = "Bearer " + token
    r = urllib.request.Request(base + urllib.parse.quote(path, safe="/?=&:%"), data=data, method=method, headers=h)
    try:
        with urllib.request.urlopen(r, timeout=180) as resp:
            code, out = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        code, out = e.code, e.read()
    try:
        js = json.loads(out) if out else None
    except Exception:
        js = out[:100]
    ok = code == expect if isinstance(expect, int) else code in expect
    if not ok:
        FAILS += 1
        print(f"[ERR] {method} {path} -> {code} {str(js)[:300]}")
    else:
        print(f"[OK ] {method} {path} -> {code}")
    return js


def wait(job_id, token, admin=False):
    p = "/api/admin/v1/jobs/" if admin else "/api/v1/jobs/"
    for _ in range(240):
        r = urllib.request.Request(BASE + p + job_id, headers={"Authorization": "Bearer " + token})
        j = json.loads(urllib.request.urlopen(r).read())
        if j["status"] in ("succeeded", "failed", "canceled", "paused"):
            print("     job", j["type"], j["status"], "|", j.get("progressText"), "|", j.get("error", ""))
            return j
        time.sleep(1)


def wav(text_seconds, path):
    subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=frequency=220:duration={text_seconds}", "-ac", "1", "-ar", "44100", path], check=True)
    return open(path, "rb").read()


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]

# Never touch accounts an operator configured. Everything this run needs points at the local fake
# server with top priority, so real LLM / TTS accounts are neither used (billed) nor modified, and the
# temporary accounts are removed on exit. Leftovers from a crashed run are cleaned up first.
import atexit
FAKE = "http://127.0.0.1:18999"
_cleanup = []
atexit.register(lambda: [f() for f in reversed(_cleanup)])
for p in req("GET", "/api/admin/v1/providers", token=adm):
    if p.get("baseUrl", "").startswith(FAKE) and p["kind"] in ("llm", "tts") and p.get("priority") == 1000:
        req("DELETE", f"/api/admin/v1/providers/{p['id']}", token=adm)
def temp_provider(body):
    acc = req("POST", "/api/admin/v1/providers", {**body, "priority": 1000}, token=adm)
    _cleanup.append(lambda: req("DELETE", f"/api/admin/v1/providers/{acc['id']}", token=adm))
    return acc
_fake_llm = temp_provider({"kind": "llm", "provider": "openai_chat", "name": "测试模型（冒烟）", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "fake"})
temp_provider({"kind": "tts", "provider": "qwen", "name": "千问测试（冒烟）", "baseUrl": FAKE, "apiKey": "sk-qwen-test", "model": "qwen-audio-3.1-tts-flash"})
# voice cloning prefers real platforms over the demo engine: keep it on a fake MiniMax account too
temp_provider({"kind": "tts", "provider": "minimax", "name": "MiniMax 测试（冒烟）", "baseUrl": FAKE, "apiKey": "mm-test", "model": "speech-2.8-turbo", "rpm": 600})
if not any(p["kind"] == "asr" for p in req("GET", "/api/admin/v1/providers", token=adm)):
    req("POST", "/api/admin/v1/providers", {"kind": "asr", "provider": "openai_compat", "name": "测试识别", "baseUrl": FAKE + "/v1", "apiKey": "k", "model": "whisper-1"}, token=adm)

def require_fakes_first(adm_token, fake):
    """Cloning picks the highest-priority platform: refuse to run unless every real speech account ranks below the fakes."""
    risky = [p["name"] for p in req("GET", "/api/admin/v1/providers", token=adm_token) if p["kind"] == "tts" and p.get("enabled")
             and p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(fake) and p.get("priority", 0) >= 1000]
    if risky:
        sys.exit(f"abort: real speech accounts rank as high as the test accounts: {risky}")


require_fakes_first(adm, FAKE)
_risky_llm = [p["name"] for p in req("GET", "/api/admin/v1/providers", token=adm) if p["kind"] == "llm" and p.get("enabled")
              and not p.get("baseUrl", "").startswith(FAKE) and p.get("priority", 0) >= 1000]
if _risky_llm:
    sys.exit(f"abort: real LLM accounts rank as high as the test accounts: {_risky_llm}")
real_calls = lambda: {p["name"]: p.get("calls", 0) for p in req("GET", "/api/admin/v1/providers", token=adm) if p.get("provider") != "placeholder" and not p.get("baseUrl", "").startswith(FAKE)}
REAL_BEFORE = real_calls()

# users
def register(nick):
    e = f"{nick}{uuid.uuid4().hex[:6]}@example.com"
    r = req("POST", "/api/v1/auth/register", {"email": e, "password": "Passw0rd!", "nickname": nick, "agree": True})
    return r["accessToken"], r["user"]

owner, ou = register("owner")
cv, cvu = register("cv")
# team studio: the free plan only includes the personal studio, so the owner first gets a team membership
plan_ids = {p["code"]: p["id"] for p in req("GET", "/api/admin/v1/plans?pageSize=100", token=adm)["items"]}
req("POST", f"/api/admin/v1/users/{ou['id']}/membership", {"planId": plan_ids["team"], "days": 30, "note": "冒烟测试"}, token=adm)
st = req("POST", "/api/v1/studios", {"name": "测试工作室", "description": "团队"}, token=owner)
sid = st["id"]
plans = req("GET", "/api/v1/plans")
team = [p for p in plans if p["code"] == "team"][0]
ent = [p for p in plans if p["code"] == "enterprise"][0]
req("PATCH", f"/api/admin/v1/studios/{sid}", {"planId": ent["id"], "reason": "测试"}, token=adm)
req("POST", f"/api/admin/v1/studios/{sid}/credits", {"amount": 50000, "reason": "测试发放"}, token=adm)
# invite cv
inv = req("POST", f"/api/v1/studios/{sid}/invitations", {"role": "member"}, token=owner)
tokn = inv["invitation"]["token"]
req("GET", f"/api/v1/invitations/{tokn}")
req("POST", f"/api/v1/invitations/{tokn}/accept", {}, token=cv)
req("GET", f"/api/v1/studios/{sid}/members", token=owner)
req("PATCH", f"/api/v1/studios/{sid}/members/{cvu['id']}", {"creditCap": 100000}, token=owner)

text = open(os.path.join(os.path.dirname(__file__), "sample.txt"), encoding="utf-8").read()
prev = req("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "协作测试"}, token=owner)
bid = req("POST", f"/api/v1/studios/{sid}/books", {"importId": prev["id"], "title": "协作测试", "author": "测试", "mode": "multi"}, token=owner)["id"]
chs = req("GET", f"/api/v1/books/{bid}/chapters", token=owner)
cid = chs[0]["id"]
j = req("POST", f"/api/v1/books/{bid}/characters/analyze", {}, token=owner)
wait(j["id"], owner)
chars = req("GET", f"/api/v1/books/{bid}/characters", token=owner)
lin = [c for c in chars if c["name"] == "林黛玉"][0]
# crew + human casting
req("POST", f"/api/v1/books/{bid}/crew", {"userId": cvu["id"], "positions": ["voice"]}, token=owner)
req("PATCH", f"/api/v1/characters/{lin['id']}", {"mode": "human", "assigneeId": cvu["id"]}, token=owner)
# workflow
wfs = req("GET", f"/api/v1/studios/{sid}/workflows", token=owner)
mixed = [w for w in wfs if w["name"] == "人机混编"][0]
req("POST", f"/api/v1/books/{bid}/workflow", {"templateId": mixed["id"]}, token=owner)
jobs = req("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [c["id"] for c in chs], "mode": "ai"}, token=owner)
for x in jobs:
    wait(x["id"], owner)
tasks = req("GET", f"/api/v1/books/{bid}/tasks", token=owner)
print("     tasks:", [(t["task"]["stageName"], t["characterName"], t["assignee"]) for t in tasks if t["task"]["chapterId"] == cid])
c1 = [t for t in tasks if t["task"]["chapterId"] == cid]
script_task = [t for t in c1 if t["task"]["stageKind"] == "script"][0]
req("POST", f"/api/v1/tasks/{script_task['task']['id']}/action", {"action": "start"}, token=owner)
req("POST", f"/api/v1/tasks/{script_task['task']['id']}/action", {"action": "submit"}, token=owner)
cast = req("POST", f"/api/v1/books/{bid}/casting", {"apply": True}, token=owner)
wait(cast["job"]["id"], owner)
# keep all synthesis on the built-in demo engine, even when a real TTS account is configured
demo = [v for v in req("GET", f"/api/v1/studios/{sid}/voices?provider=placeholder&pageSize=50", token=owner)["items"] if v.get("priceTier") != "premium"]  # premium voices need a paid plan
for i, c in enumerate(req("GET", f"/api/v1/books/{bid}/characters", token=owner)):
    if c.get("mode") != "human" and (c.get("voice") or {}).get("provider") != "placeholder":
        req("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": demo[i % len(demo)]["id"]}, token=owner)
if not all((c.get("voice") or {}).get("provider") == "placeholder" for c in req("GET", f"/api/v1/books/{bid}/characters", token=owner) if c.get("mode") != "human"):
    sys.exit("abort: some characters are not on the demo engine; refusing to synthesize with a real TTS account")
gen = req("POST", f"/api/v1/books/{bid}/generate", {"chapterIds": [cid], "scope": "missing"}, token=owner)
wait(gen[0]["id"], owner)
# CV lines + recording
lines = req("GET", "/api/v1/me/lines", token=cv)
print("     cv lines:", len(lines["lines"]))
my = [l for l in lines["lines"] if l["chapterId"] == cid]
open("/tmp/asr_text.txt", "w").write(my[0]["text"])
rec = req("POST", f"/api/v1/segments/{my[0]['id']}/record", {"origin": "record"}, token=cv, files={"file": ("a.wav", wav(2, "/tmp/rec1.wav"))})
print("     record hints:", rec and rec.get("hints"))
# cv cannot edit script
req("PATCH", f"/api/v1/books/{bid}/segments", {"ids": [my[0]["id"]], "patch": {"text": "改"}}, token=cv, expect=403)
# alignment: one long take for both lines of 林黛玉
open("/tmp/asr_text.txt", "w").write("".join(l["text"] for l in my) + "嗯重来一下" + my[-1]["text"])
up = req("POST", f"/api/v1/studios/{sid}/uploads", {"kind": "recording"}, token=cv, files={"file": ("long.wav", wav(8, "/tmp/long.wav"))})
recd = req("POST", f"/api/v1/chapters/{cid}/recordings", {"assetIds": [up["id"]], "mode": "character", "characterId": lin["id"]}, token=cv)
time.sleep(1)
recs = req("GET", f"/api/v1/chapters/{cid}/recordings", token=cv)
wait(recs[0]["jobId"], cv)
rr = req("GET", f"/api/v1/recordings/{recd['id']}", token=cv)
res = rr["recording"]["result"]
print("     align:", res["stats"], "alts:", [len(m["alternatives"]) for m in res["matches"]])
if res["matches"]:
    m = res["matches"][0]
    req("POST", f"/api/v1/recordings/{recd['id']}/adjust", {"segmentId": m["segmentId"], "startMs": m["startMs"], "endMs": m["endMs"] + 100}, token=cv)
voice_task = [t for t in req("GET", f"/api/v1/books/{bid}/tasks", token=owner) if t["task"]["chapterId"] == cid and t["task"]["stageKind"] == "record"]
for t in voice_task:
    req("POST", f"/api/v1/tasks/{t['task']['id']}/action", {"action": "submit"}, token=owner, expect=(200, 400))
    req("POST", f"/api/v1/tasks/{t['task']['id']}/action", {"action": "approve"}, token=owner, expect=(200, 400))
rj = req("POST", f"/api/v1/chapters/{cid}/render", {}, token=owner)
wait(rj["id"], owner)
# review with ASR
sc = req("GET", f"/api/v1/chapters/{cid}/script", token=owner)
open("/tmp/asr_text.txt", "w").write("".join(s["text"] for s in sc["segments"] if not s["removed"]).replace("竹影", ""))
rv = req("POST", f"/api/v1/chapters/{cid}/review", {"autoIssue": True}, token=owner)
wait(rv["id"], owner)
runs = req("GET", f"/api/v1/chapters/{cid}/reviews", token=owner)
print("     review:", runs[0]["summary"])
fs = runs[0]["findings"] or []
if fs:
    req("POST", f"/api/v1/reviews/{runs[0]['id']}/findings/{fs[0]['id']}", {"action": "ignore"}, token=owner)
# issues
iss = req("GET", f"/api/v1/books/{bid}/issues?status=active&pageSize=200", token=owner)
print("     issues:", iss["total"])
seg = sc["segments"][0]
ni = req("POST", f"/api/v1/books/{bid}/issues", {"chapterId": cid, "segmentId": seg["id"], "type": "pronunciation", "title": "读音不对", "assigneeId": cvu["id"]}, token=owner)
iid = ni["issue"]["id"]
req("POST", f"/api/v1/issues/{iid}/action", {"action": "start"}, token=cv)
req("POST", f"/api/v1/issues/{iid}/action", {"action": "submit", "note": "已重录"}, token=cv)
req("POST", f"/api/v1/issues/{iid}/action", {"action": "close"}, token=owner)
for i in (iss["items"] or []):
    req("POST", f"/api/v1/issues/{i['issue']['id']}/action", {"action": "close"}, token=owner)
# comments
cm = req("POST", f"/api/v1/books/{bid}/comments", {"chapterId": cid, "segmentId": seg["id"], "body": "@cv 这句再温柔点", "mentions": [cvu["id"]]}, token=owner)
req("POST", f"/api/v1/books/{bid}/comments", {"chapterId": cid, "segmentId": seg["id"], "body": "好的", "parentId": cm["id"]}, token=cv)
req("PATCH", f"/api/v1/comments/{cm['id']}", {"resolved": True}, token=owner)
req("GET", f"/api/v1/chapters/{cid}/comments", token=owner)
req("POST", f"/api/v1/chapters/{cid}/conclude", {"pass": True}, token=owner)
# payroll
req("POST", f"/api/v1/studios/{sid}/pay-rates", {"stageKey": "script", "stageKind": "script", "unit": "per_1k_words", "priceCent": 2000}, token=owner)
req("POST", f"/api/v1/studios/{sid}/pay-rates", {"stageKey": "voice", "stageKind": "record", "unit": "per_1k_words", "priceCent": 8000}, token=owner)
pr = req("GET", f"/api/v1/studios/{sid}/payroll", token=owner)
print("     payroll rows:", len(pr["rows"] or []), pr["totals"])
req("GET", f"/api/v1/studios/{sid}/payroll/export", token=owner)
if pr["rows"]:
    req("POST", f"/api/v1/studios/{sid}/payroll/settle", {"taskIds": [r["taskId"] for r in pr["rows"]]}, token=owner)
st2 = req("GET", f"/api/v1/studios/{sid}/stats", token=owner)
print("     stats:", st2["tasksDone"], st2["words"], st2["firstPassRate"])
req("GET", f"/api/v1/studios/{sid}/tasks", token=owner)
req("GET", f"/api/v1/studios/{sid}/issues", token=owner)
req("GET", "/api/v1/me/tasks", token=cv)
# open API + webhooks
key = req("POST", f"/api/v1/studios/{sid}/api-keys", {"name": "测试"}, token=owner)
secret = key["secret"]
wh = req("POST", f"/api/v1/studios/{sid}/webhooks", {"url": "http://127.0.0.1:18999/hook", "events": ["pipeline.completed", "export.completed", "chapter.rendered"]}, token=owner)
req("POST", f"/api/v1/studios/{sid}/webhooks/{wh['webhook']['id']}/test", {}, token=owner)
ak = {"X-API-Key": secret}
req("GET", "/openapi/v1/voices", headers=ak)
ob = req("POST", "/openapi/v1/books", {"title": "开放平台书", "text": text, "autoProduce": True, "config": {"tier": "economy"}}, headers=ak)
for _ in range(120):
    p = req("GET", f"/openapi/v1/pipelines/{ob['pipelineId']}", headers=ak)
    if p["status"] in ("completed", "failed"):
        break
    time.sleep(2)
print("     open pipeline:", p["status"], p.get("error"))
req("GET", f"/openapi/v1/books/{ob['bookId']}", headers=ak)
outs = req("GET", f"/openapi/v1/books/{ob['bookId']}/outputs", headers=ak)
print("     outputs:", len(outs["outputs"]))
ovs = req("GET", "/openapi/v1/voices", headers=ak)["voices"]
vid = next((v["id"] for v in ovs if v.get("provider") == "placeholder"), demo[0]["id"])
req("POST", "/openapi/v1/tts", {"text": "开放平台合成测试", "voiceId": vid}, headers=ak)
req("GET", "/openapi/v1/voices", headers={"X-API-Key": "bad"}, expect=401)
time.sleep(3)
hooks = json.loads(urllib.request.urlopen("http://127.0.0.1:18999/").read())
print("     webhooks received:", [h["body"].get("event", "publish") for h in hooks])
req("GET", f"/api/v1/studios/{sid}/webhooks/{wh['webhook']['id']}/deliveries", token=owner)
# distribution
chn = req("POST", f"/api/v1/studios/{sid}/channels", {"kind": "rss", "name": "播客"}, token=owner)
whc = req("POST", f"/api/v1/studios/{sid}/channels", {"kind": "webhook", "name": "推送", "config": {"url": "http://127.0.0.1:18999/hook/pub"}}, token=owner)
xm = req("POST", f"/api/v1/studios/{sid}/channels", {"kind": "ximalaya", "name": "喜马拉雅"}, token=owner)
req("POST", f"/api/v1/books/{bid}/publish/check", {"channelId": xm["id"]}, token=owner)
for c in (chn, whc, xm):
    req("POST", f"/api/v1/books/{bid}/publications", {"channelId": c["id"], "chapterIds": [cid]}, token=owner)
print("     waiting publish cron (~65s)")
time.sleep(66)
pubs = req("GET", f"/api/v1/books/{bid}/publications", token=owner)
print("     pubs:", [(p["channel"]["kind"], p["publication"]["status"], [e["status"] for e in p["episodes"]]) for p in pubs])
feed = [p for p in pubs if p["channel"]["kind"] == "rss"][0].get("feedUrl")
x = urllib.request.urlopen(feed).read().decode()
print("     feed has item:", "<item>" in x)
# voice clone (self) + market
chal = req("GET", "/api/v1/voices/challenge", token=owner)["sentence"]
sample = req("POST", f"/api/v1/studios/{sid}/voices/sample-check", {"tier": "quick"}, token=owner, files={"file": ("s.wav", wav(15, "/tmp/s.wav"))})
print("     sample check:", sample["check"]["score"], sample["check"]["tips"])
open("/tmp/asr_text.txt", "w").write(chal)
ev = req("POST", f"/api/v1/studios/{sid}/uploads", {"kind": "voice_sample"}, token=owner, files={"file": ("c.wav", wav(4, "/tmp/c.wav"))})
v = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": "我的声音", "consentType": "self", "challenge": chal, "evidenceId": ev["id"], "sampleIds": [sample["assetId"]], "gender": "male"}, token=owner)
time.sleep(4)
vv = req("GET", f"/api/v1/voices/{v['id']}", token=owner)
print("     clone status:", vv["status"], vv.get("consent"))
req("POST", "/api/v1/me/realname", {"name": "张三", "idNumber": "110101199001011234"}, token=owner)
req("POST", f"/api/admin/v1/users/{ou['id']}/realname", {"approve": True}, token=adm)
li = req("POST", f"/api/v1/studios/{sid}/voices/{v['id']}/listing", {"pricePer1k": 5, "commercial": True, "intro": "温和男声"}, token=owner)
lists = req("GET", "/api/admin/v1/listings?status=pending", token=adm)
req("POST", f"/api/admin/v1/listings/{li['id']}", {"approve": True}, token=adm)
req("GET", f"/api/v1/studios/{sid}/voices?tab=market", token=cv)
req("GET", "/api/v1/me/earnings", token=owner)
# clone other -> admin review
doc = req("POST", f"/api/v1/studios/{sid}/uploads", {"kind": "document"}, token=owner, files={"file": ("auth.pdf", b"%PDF-1.4 test")})
v2 = req("POST", f"/api/v1/studios/{sid}/voices/clone", {"name": "他人声音", "consentType": "other", "speakerName": "李四", "documentId": doc["id"], "sampleIds": [sample["assetId"]]}, token=owner)
cons = req("GET", "/api/admin/v1/consents?status=pending", token=adm)
req("POST", f"/api/admin/v1/consents/{cons['items'][0]['consent']['id']}", {"approve": True, "note": "材料齐全"}, token=adm)
# moderation
req("PUT", "/api/admin/v1/settings/moderation", {"enabled": True, "mode": "pre", "useLlm": True}, token=adm)
req("POST", "/api/admin/v1/sensitive-words/import", {"words": "违禁测试词", "level": "block"}, token=adm)
p2 = req("POST", f"/api/v1/studios/{sid}/imports", {"text": "第一章 测试\n这里有一个违禁测试词。" * 3}, token=owner)
b2 = req("POST", f"/api/v1/studios/{sid}/books", {"importId": p2["id"], "title": "审核测试"}, token=owner)
time.sleep(2)
mods = req("GET", "/api/admin/v1/moderation?status=pending", token=adm)
print("     moderation pending:", mods["total"])
if mods["items"]:
    req("POST", f"/api/admin/v1/moderation/{mods['items'][0]['record']['id']}", {"approve": True}, token=adm)
req("PUT", "/api/admin/v1/settings/moderation", {"enabled": True, "mode": "post", "useLlm": False}, token=adm)
# reports
req("POST", "/api/v1/reports", {"targetType": "voice", "targetId": v["id"], "reason": "impersonation", "detail": "测试"}, token=cv)
reps = req("GET", "/api/admin/v1/reports?status=open", token=adm)
req("POST", f"/api/admin/v1/reports/{reps['items'][0]['report']['id']}", {"status": "rejected", "resolution": "核实无问题"}, token=adm)
# admin sweep
for path in ["/dashboard", "/users", "/studios", "/voices", "/assets", "/templates", "/providers", "/routing", "/pricing", "/prompts", "/evals", "/lexicon", "/plans", "/redeem-batches", "/membership/overview", "/ledger", "/finance", "/withdrawals", "/listings", "/moderation", "/consents", "/reports", "/sensitive-words", "/jobs", "/announcements", "/articles", "/feedback", "/audit-logs", "/system", "/settings/site", "/settings/smtp", "/settings/ai_label", "/settings/billing", "/settings/export_presets", "/settings/casting_policy", f"/users/{ou['id']}", f"/studios/{sid}"]:
    req("GET", "/api/admin/v1" + path, token=adm)
promo = "SMOKE" + uuid.uuid4().hex[:8].upper()  # a fresh shared code each run (codes are unique)
req("POST", "/api/admin/v1/redeem-batches", {"name": "冒烟通用码", "kind": "credits", "credits": 1000, "mode": "shared", "code": promo}, token=adm)
req("POST", f"/api/v1/studios/{sid}/promo", {"code": promo.lower()}, token=owner)
req("POST", f"/api/v1/studios/{sid}/promo", {"code": promo}, token=owner, expect=400)
req("POST", "/api/admin/v1/announcements", {"title": "测试公告", "body": "hello", "published": True}, token=adm)
req("GET", "/api/v1/announcements", token=cv)
req("POST", "/api/admin/v1/messages", {"title": "群发测试", "body": "hi"}, token=adm)
req("POST", "/api/v1/feedback", {"content": "很好用"}, token=cv)
_rule = req("POST", "/api/admin/v1/routing", {"feature": "script", "tier": "*", "primaryId": _fake_llm["id"], "enabled": True}, token=adm)
if _rule and _rule.get("id"):
    req("DELETE", f"/api/admin/v1/routing/{_rule['id']}", token=adm)
req("PUT", "/api/admin/v1/settings/site", {"name": "声书工坊", "registrationOpen": True, "signupCredits": 20000, "maxUploadMb": 200, "freePreviewsDaily": 30, "speechRate": 220}, token=adm)
req("POST", "/api/admin/v1/jobs/retry-failed", {}, token=adm)
# templates preview + intro/outro render
tpls = req("GET", f"/api/v1/studios/{sid}/templates", token=owner)
req("POST", f"/api/v1/studios/{sid}/templates/{tpls[0]['template']['id']}/preview", {}, token=owner)
# transfer + leave
# the receiver's membership must allow one more studio: refused on the free plan, fine after upgrading
req("POST", f"/api/v1/studios/{sid}/transfer", {"userId": cvu["id"]}, token=owner, expect=400)
req("POST", f"/api/admin/v1/users/{cvu['id']}/membership", {"planId": plan_ids["team"], "days": 30, "note": "冒烟测试"}, token=adm)
req("POST", f"/api/v1/studios/{sid}/transfer", {"userId": cvu["id"]}, token=owner)
req("GET", f"/api/v1/studios/{sid}", token=cv)
print("FAILS:", FAILS)
# ---- new features ----
pc = req("POST", "/api/v1/auth/phone/code", {"phone": "13800001234"})
ph = req("POST", "/api/v1/auth/login/code", {"target": "13800001234", "code": pc["devCode"]})
req("GET", "/api/v1/site")
req("POST", f"/api/v1/chapters/{cid}/presence", {"segmentId": seg["id"]}, token=cv)
pres = req("POST", f"/api/v1/chapters/{cid}/presence", {"segmentId": seg["id"]}, token=owner)
print("     presence:", [p["nickname"] for p in pres])
aj = req("POST", f"/api/v1/characters/{lin['id']}/avatar/generate", {}, token=cv, expect=(200, 403))
ex = req("POST", f"/api/v1/books/{bid}/exports", {"chapterIds": [cid], "format": "wav", "timeline": True}, token=owner)
time.sleep(1)
wait(ex["jobId"], owner)
prompts = req("GET", "/api/admin/v1/prompts", token=adm)
np_ = req("POST", "/api/admin/v1/prompts", {"key": "script_annotation", "content": prompts[0]["default"] if prompts[0]["key"] == "script_annotation" else [p for p in prompts if p["key"] == "script_annotation"][0]["default"], "note": "灰度"}, token=adm)
req("POST", f"/api/admin/v1/prompts/{np_['id']}/activate", {"rollout": 20}, token=adm)
req("PUT", "/api/admin/v1/settings/oauth", {"google": {"enabled": False, "clientId": ""}, "wechat": {"enabled": False}}, token=adm)
req("GET", "/api/admin/v1/settings/oauth", token=adm)
req("GET", "/api/v1/auth/oauth/google/start", expect=(200, 302))
time.sleep(3)  # let background jobs started above settle before checking (fakes are removed on exit)
if real_calls() != REAL_BEFORE:
    FAILS += 1
    print("[ERR] real provider accounts were called:", REAL_BEFORE, "->", real_calls())
else:
    print("[OK ] real provider accounts untouched", REAL_BEFORE)
print("FAILS:", FAILS)
sys.exit(1 if FAILS else 0)  # CI fails the run on any failed check

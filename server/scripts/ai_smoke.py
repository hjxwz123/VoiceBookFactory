import json, time, urllib.request, urllib.error, urllib.parse, sys, uuid

BASE = "http://localhost:18080"
FAILS = 0


def call(method, path, body=None, token=None, expect=200):
    global FAILS
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"} if body is not None else {}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + urllib.parse.quote(path, safe="/?=&:%"), data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=180) as r:
            code, out = r.status, r.read()
    except urllib.error.HTTPError as e:
        code, out = e.code, e.read()
    js = json.loads(out) if out else None
    ok = code == expect if isinstance(expect, int) else code in expect
    print(f"[{'OK ' if ok else 'ERR'}] {method} {path} -> {code} {'' if ok else str(js)[:300]}")
    if not ok:
        FAILS += 1
    return js


def wait(job_id, token, limit=300):
    for _ in range(limit):
        j = call("GET", f"/api/v1/jobs/{job_id}", token=token) if True else None
        if j and j.get("status") in ("succeeded", "failed", "canceled", "paused"):
            print("     job", j["type"], j["status"], "|", j.get("progressText"), "|", j.get("error", ""))
            return j
        time.sleep(1)


# admin configures an OpenAI Chat compatible model (fake server)
adm = call("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})
at = adm["accessToken"]
presets = call("GET", "/api/admin/v1/providers/presets", token=at)
print("     presets:", len(presets))
# Never touch an operator's real LLM accounts: the fake model gets top priority for this run only
# and is removed on exit; leftovers from a crashed run are cleaned up first.
import atexit
for p in call("GET", "/api/admin/v1/providers?kind=llm", token=at):
    if p.get("baseUrl", "").startswith("http://127.0.0.1:18999") and p.get("priority") == 1000:
        call("DELETE", f"/api/admin/v1/providers/{p['id']}", token=at)
acc = call("POST", "/api/admin/v1/providers", {"kind": "llm", "provider": "openai_chat", "name": "测试模型（冒烟）", "baseUrl": "http://127.0.0.1:18999/v1", "apiKey": "sk-fake", "model": "fake-chat", "config": {"jsonMode": "json_object"}, "priority": 1000}, token=at)
atexit.register(lambda: call("DELETE", f"/api/admin/v1/providers/{acc['id']}", token=at))
t = call("POST", f"/api/admin/v1/providers/{acc['id']}/test", {}, token=at)
print("     test:", t)
dash = call("GET", "/api/admin/v1/dashboard", token=at)
print("     dashboard alerts:", [a["title"] for a in dash["alerts"]])

email = f"ai{uuid.uuid4().hex[:6]}@example.com"
tok = call("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "AI测试", "agree": True})["accessToken"]
sid = call("GET", "/api/v1/me", token=tok)["studios"][0]["id"]
text = open(sys.argv[1], encoding="utf-8").read()
prev = call("POST", f"/api/v1/studios/{sid}/imports", {"text": text, "title": "红楼AI"}, token=tok)
bid = call("POST", f"/api/v1/studios/{sid}/books", {"importId": prev["id"], "title": "红楼AI", "author": "曹雪芹", "mode": "multi", "autoAnalyze": True}, token=tok)["id"]
time.sleep(3)
jobs = call("GET", f"/api/v1/studios/{sid}/jobs", token=tok)["items"]
an = [j for j in jobs if j["type"] == "character_analysis"]
if an:
    wait(an[0]["id"], tok)
chars = call("GET", f"/api/v1/books/{bid}/characters", token=tok)
print("     characters:", [(c["name"], c["kind"], c["gender"]) for c in chars])
intro = call("POST", f"/api/v1/books/{bid}/intro", {}, token=tok)
print("     intro:", intro)
lex = call("POST", f"/api/v1/books/{bid}/lexicon/extract", {}, token=tok)
print("     lexicon candidates:", [e["term"] for e in lex] if lex else lex)
chs = call("GET", f"/api/v1/books/{bid}/chapters", token=tok)
cid = chs[0]["id"]
j = call("POST", f"/api/v1/books/{bid}/scripts/parse", {"chapterIds": [c["id"] for c in chs], "mode": "ai"}, token=tok)
for x in j:
    wait(x["id"], tok)
sc = call("GET", f"/api/v1/chapters/{cid}/script", token=tok)
names = {c["id"]: c["name"] for c in call("GET", f"/api/v1/books/{bid}/characters", token=tok)}
for s in sc["segments"]:
    print("       ", names.get(s["characterId"], "?"), "|", s["type"], s["emotion"], "|", s["text"], "| prons:", s["prons"])
cast = call("POST", f"/api/v1/books/{bid}/casting", {"apply": True}, token=tok)
wait(cast["job"]["id"], tok)
chars = call("GET", f"/api/v1/books/{bid}/characters", token=tok)
print("     cast:", [(c["name"], (c.get("voice") or {}).get("name"), (c.get("casting") or [{}])[0].get("reason")) for c in chars])
# keep the pipeline's synthesis on the built-in demo engine, even when a real TTS account is configured
demo = [v for v in call("GET", f"/api/v1/studios/{sid}/voices?provider=placeholder&pageSize=50", token=tok)["items"] if v.get("priceTier") != "premium"]  # premium voices need a paid plan
for i, c in enumerate(chars):
    if c.get("mode") != "human" and (c.get("voice") or {}).get("provider") != "placeholder":
        call("PUT", f"/api/v1/characters/{c['id']}/voice", {"voiceId": demo[i % len(demo)]["id"]}, token=tok)
chars = call("GET", f"/api/v1/books/{bid}/characters", token=tok)
if not all((c.get("voice") or {}).get("provider") == "placeholder" for c in chars if c.get("mode") != "human"):
    sys.exit("abort: some characters are not on the demo engine; refusing to synthesize with a real TTS account")
est = call("POST", f"/api/v1/books/{bid}/pipeline/estimate", {"tier": "standard"}, token=tok)
print("     pipeline estimate:", est)
run = call("POST", f"/api/v1/books/{bid}/pipeline", {"tier": "standard", "bgm": True, "preset": "generic"}, token=tok)
for _ in range(240):
    v = call("GET", f"/api/v1/pipelines/{run['id']}", token=tok)
    if v["status"] in ("completed", "failed", "canceled"):
        break
    time.sleep(2)
print("     pipeline:", v["status"], v.get("error"), [(s["key"], s["status"]) for s in v["stages"]], "credits", v["usedCredits"])
prog = call("GET", f"/api/v1/books/{bid}/progress", token=tok)
print("     steps:", {k: v2["done"] for k, v2 in prog["steps"].items()}, "next:", prog["next"])
# prompt test bench + eval
pt = call("POST", "/api/admin/v1/prompts/test", {"key": "script_annotation", "content": "", "sample": "“你好。”他说。"}, token=at)
print("     prompt test units:", len(pt["units"]))
evals = call("GET", "/api/admin/v1/evals", token=at)["items"]
ej = call("POST", f"/api/admin/v1/evals/{evals[0]['id']}/run", {}, token=at)
for _ in range(60):
    jj = call("GET", f"/api/admin/v1/jobs/{ej['id']}", token=at)
    if jj["status"] in ("succeeded", "failed"):
        print("     eval:", jj["status"], jj.get("progressText"), jj.get("error"))
        break
    time.sleep(1)
print("FAILS:", FAILS)

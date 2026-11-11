"""End-to-end check of memberships and redeem codes (no AI providers involved).

Covers the admin side (plans, overview, batches, export, code lookup, granting/revoking, contract plans)
and the member side (limits, redeeming, history, credit studio, credit transfer).
"""
import json, sys, time, urllib.parse, urllib.request, urllib.error

BASE = "http://localhost:18080"
FAILS = 0


def req(method, path, body=None, token=None, raw=False, expect=200):
    r = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=60) as resp:
            data = resp.read()
            code = resp.status
            out = data if raw else (json.loads(data) if data else None)
    except urllib.error.HTTPError as e:
        code, data = e.code, e.read()
        out = json.loads(data) if data else None
    if code != expect:
        raise SystemExit(f"{method} {path} -> {code}, expected {expect}: {out}")
    return out


def check(cond, msg):
    global FAILS
    print(("  ok  " if cond else "  FAIL") + " " + msg)
    if not cond:
        FAILS += 1


def err(out):
    return (out or {}).get("error", {})


adm = req("POST", "/api/admin/v1/auth/login", {"email": "admin@example.com", "password": "Admin@123456"})["accessToken"]
stamp = int(time.time())

print("1. plans: list, null-safe fields, create/update, delete guard")
plans = req("GET", "/api/admin/v1/plans?pageSize=100", token=adm)["items"]
by = {p["code"]: p for p in plans}
check(all(isinstance(p[k], list) for p in plans for k in ("features", "benefits", "exportFormats")), "list fields are arrays, never null")
check(by["personal"]["level"] > by["free"]["level"], "levels ordered")
np = req("POST", "/api/admin/v1/plans", {"code": f"t{stamp}", "name": "测试会员", "level": 5, "monthlyCredits": 1000, "creditRate": 90, "maxStudios": 3, "listed": False}, token=adm)
import atexit
atexit.register(lambda: req("DELETE", f"/api/admin/v1/plans/{np['id']}", token=adm, expect=200))  # removed even if a check crashes
check(np["code"] == f"t{stamp}" and np["benefits"] == [], "plan created")
req("PUT", f"/api/admin/v1/plans/{np['id']}", {**np, "name": "测试会员2"}, token=adm)
bad = req("PUT", f"/api/admin/v1/plans/{np['id']}", {**np, "creditRate": 150}, token=adm, expect=400)
check("折扣" in err(bad).get("message", ""), "invalid discount rejected")
dup = req("POST", "/api/admin/v1/plans", {"code": "personal", "name": "重复"}, token=adm, expect=400)
check("已被" in err(dup).get("message", ""), "duplicate code rejected")
d = req("DELETE", f"/api/admin/v1/plans/{by['free']['id']}", token=adm, expect=400)
check("默认" in err(d).get("message", ""), "default plan cannot be deleted")
ov = req("GET", "/api/admin/v1/membership/overview", token=adm)
check(len(ov["byPlan"]) >= 4 and "paid" in ov, "overview")

print("2. member limits on the default plan")
email = f"member{stamp}@example.com"
tok = req("POST", "/api/v1/auth/register", {"email": email, "password": "Passw0rd!", "nickname": "会员测试", "agree": True})["accessToken"]
me = req("GET", "/api/v1/me", token=tok)
uid, personal = me["user"]["id"], me["user"]["personalStudioId"]
check(me["membership"]["isDefault"], "new user is on the default plan")
lim = req("POST", "/api/v1/studios", {"name": "团队"}, token=tok, expect=403)
check(err(lim).get("code") == "plan_limit" and err(lim).get("data", {}).get("kind") == "studios", "studio limit returns plan_limit with details")
mv = req("GET", f"/api/v1/membership?studioId={personal}", token=tok)
check(mv["usage"]["studios"] == [1, 1] and mv["plan"]["code"] == "free", "membership view + usage")

print("3. redeem codes: unique membership batch")
b = req("POST", "/api/admin/v1/redeem-batches", {"name": f"冒烟{stamp}", "kind": "membership", "planId": by["team"]["id"], "days": 30, "mode": "unique", "count": 3, "prefix": "smk", "channel": "测试"}, token=adm)
check(b["count"] == 3 and b["codes"]["unused"] == 3 and b["planName"] == by["team"]["name"], "batch created")
codes = req("GET", f"/api/admin/v1/redeem-batches/{b['id']}/codes", token=adm)["items"]
check(all(c["code"].startswith("SMK-") for c in codes), "prefix applied")
pv = req("POST", "/api/v1/redeem/preview", {"code": codes[0]["code"].lower().replace("-", " ")}, token=tok)
check(pv["kind"] == "membership" and pv["days"] == 30, "preview: " + pv["summary"])
r = req("POST", "/api/v1/redeem", {"code": codes[0]["code"]}, token=tok)
check(r["membership"]["plan"]["code"] == "team", "redeemed: " + r["summary"])
again = req("POST", "/api/v1/redeem", {"code": codes[0]["code"]}, token=tok, expect=400)
check(err(again).get("code") == "code_used", "used code rejected")
limit = req("POST", "/api/v1/redeem", {"code": codes[1]["code"]}, token=tok, expect=400)
check(err(limit).get("code") == "code_limit", "one code per user per batch")
me = req("GET", "/api/v1/me", token=tok)
check(me["membership"]["planCode"] == "team", "header badge data updated")
bal = req("GET", f"/api/v1/studios/{personal}/credits", token=tok)["balance"]["available"]
check(bal >= by["team"]["monthlyCredits"] + by["team"]["bonusCredits"], f"first month + bonus credits arrived ({bal})")
hist = req("GET", "/api/v1/redeem/history", token=tok)
check(len(hist) == 1 and hist[0]["kind"] == "membership", "redeem history")

print("4. team studio, credit studio, credit transfer")
st = req("POST", "/api/v1/studios", {"name": "冒烟团队"}, token=tok)
check(st["plan"]["code"] == "team", "new studio follows the membership")
mv = req("PATCH", "/api/v1/membership", {"creditStudioId": st["id"]}, token=tok)
check(mv["creditStudioId"] == st["id"], "credit studio chosen")
req("POST", f"/api/v1/studios/{personal}/credits/transfer", {"toStudioId": st["id"], "amount": 1234}, token=tok)
got = req("GET", f"/api/v1/studios/{st['id']}/credits", token=tok)["balance"]["available"]
check(got == 1234, "credits transferred")
over = req("POST", f"/api/v1/studios/{personal}/credits/transfer", {"toStudioId": st["id"], "amount": 10**12}, token=tok, expect=402)
check(err(over).get("code") == "insufficient_credits", "transfer beyond balance rejected")

print("5. shared credits code, export, disable, lookup")
code = f"SMOKE{stamp}"
sb = req("POST", "/api/admin/v1/redeem-batches", {"name": "通用积分", "kind": "credits", "credits": 777, "creditDays": 30, "mode": "shared", "code": code, "maxUses": 5}, token=adm)
check(sb["sharedCode"] == code, "shared code shown on the batch")
r = req("POST", "/api/v1/redeem", {"code": code.lower(), "studioId": st["id"]}, token=tok)
check(r["credits"] == 777 and r["studioId"] == st["id"], "credits redeemed into the chosen studio")
csv = req("GET", f"/api/admin/v1/redeem-batches/{b['id']}/export", token=adm, raw=True).decode("utf-8-sig")
check(csv.count("\n") == 4 and "已使用" in csv and "未使用" in csv and "会员测试" in csv, "CSV export with states and user")
req("POST", f"/api/admin/v1/redeem-codes/{codes[2]['id']}/status", {"status": "disabled"}, token=adm)
other = req("POST", "/api/v1/auth/register", {"email": f"other{stamp}@example.com", "password": "Passw0rd!", "nickname": "乙", "agree": True})["accessToken"]
dis = req("POST", "/api/v1/redeem", {"code": codes[2]["code"]}, token=other, expect=400)
check(err(dis).get("code") == "code_disabled", "disabled code rejected")
lk = req("GET", f"/api/admin/v1/redeem-codes/lookup?code={codes[0]['code']}", token=adm)
check(lk["code"]["status"] == "used" and lk["uses"][0]["user"] == "会员测试", "lookup shows who used it")
uses = req("GET", f"/api/admin/v1/redeem-batches/{b['id']}/uses", token=adm)
check(uses["total"] == 1, "batch uses listed")
req("PATCH", f"/api/admin/v1/redeem-batches/{sb['id']}", {"status": "disabled"}, token=adm)
off = req("POST", "/api/v1/redeem", {"code": code}, token=other, expect=400)
check(err(off).get("code") == "code_disabled", "disabled batch rejected")
lst = req("GET", "/api/admin/v1/redeem-batches?kind=credits&q=" + urllib.parse.quote("通用积分"), token=adm)
check(any(x["id"] == sb["id"] and x["state"] == "disabled" for x in lst["items"]), "batch list filters")

print("6. admin grant / revoke, contract plan on a studio")
g = req("POST", f"/api/admin/v1/users/{uid}/membership", {"planId": by["enterprise"]["id"], "days": 7, "note": "冒烟测试"}, token=adm)
check(g["plan"]["code"] == "enterprise", "admin grant: " + g["summary"])
um = req("GET", f"/api/admin/v1/users/{uid}/membership", token=adm)
ent_period = next(p for p in um["periods"] if p["planCode"] == "enterprise")
check(um["plan"]["code"] == "enterprise" and any(p["state"] == "paused" for p in um["periods"]), "team paused while enterprise runs")
req("DELETE", f"/api/admin/v1/membership-periods/{ent_period['id']}", token=adm)
check(req("GET", "/api/v1/me", token=tok)["membership"]["planCode"] == "team", "revoked: back to team")
in_use = req("DELETE", f"/api/admin/v1/plans/{by['team']['id']}", token=adm, expect=400)
check("使用中" in err(in_use).get("message", ""), "plan in use cannot be deleted")
before = req("GET", f"/api/v1/studios/{st['id']}/credits", token=tok)["balance"]["available"]
req("PATCH", f"/api/admin/v1/studios/{st['id']}", {"planId": by["enterprise"]["id"], "reason": "签约"}, token=adm)
after = req("GET", f"/api/v1/studios/{st['id']}/credits", token=tok)["balance"]["available"]
req("PATCH", f"/api/admin/v1/studios/{st['id']}", {"planId": by["enterprise"]["id"], "status": "active", "reason": "无关修改"}, token=adm)
again = req("GET", f"/api/v1/studios/{st['id']}/credits", token=tok)["balance"]["available"]
check(after - before == by["enterprise"]["monthlyCredits"] and again == after, "contract credits granted once, not on unrelated saves")
sv = req("GET", f"/api/v1/membership?studioId={st['id']}", token=tok)
check(sv["studio"]["contract"] and sv["studio"]["plan"]["code"] == "enterprise", "contract plan wins on that studio")
req("PATCH", f"/api/admin/v1/studios/{st['id']}", {"planId": "", "reason": "解约"}, token=adm)
check(req("GET", f"/api/v1/membership?studioId={st['id']}", token=tok)["studio"]["plan"]["code"] == "team", "cleared: follows the owner again")

print("FAILS:", FAILS)
sys.exit(1 if FAILS else 0)

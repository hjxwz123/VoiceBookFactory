#!/usr/bin/env python3
"""Local stand-ins for end-to-end checks of storage and sign-in, on 127.0.0.1:18996.

- Aliyun OSS, path style (http://127.0.0.1:18996/<bucket>/<key>): PUT, GET (with Range), HEAD, DELETE.
  Requests must carry an OSS V4 signature (header or presigned query); the signature itself is not
  recomputed. Admin form: region cn-hangzhou, endpoint http://127.0.0.1:18996, bucket vb-test,
  AccessKey any, secret any (use "deny" as the AccessKey ID to see the permission error).
- An OpenID Connect provider at http://127.0.0.1:18996/idp: discovery, a consent page, token endpoint
  (client_secret_basic, PKCE S256 checked), userinfo. ID tokens are HS256 with the client secret.
  Client ID vb-test-client, secret vb-test-secret.
- A Suno relay (New API task protocol) at the root: POST /suno/submit/music, GET /suno/fetch/<task>.
  Key vb-suno-key. Each task answers IN_PROGRESS twice, then SUCCESS with two clips whose mp3s are
  served from /suno-audio/<clip>.mp3 (an 8-second tone made with ffmpeg at start-up). A description
  containing "失败" makes the task fail. The last submitted body is at /_suno/last.
"""
import base64, hashlib, hmac, json, os, secrets, subprocess, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlparse

PORT = int(os.environ.get("FAKE_CLOUD_PORT", "18996"))
BASE = f"http://127.0.0.1:{PORT}"
ISSUER = BASE + "/idp"
CLIENT_ID, CLIENT_SECRET = "vb-test-client", "vb-test-secret"
USERS = {
    "linwan": {"sub": "idp-linwan", "name": "林晚", "email": "linwan@idp.example.com", "email_verified": True},
    "showcase": {"sub": "idp-showcase", "name": "沈知秋", "email": "showcase@example.com", "email_verified": True},
}
objects, codes, lock = {}, {}, threading.Lock()
stats = {"oss_put": 0, "oss_get": 0, "oss_delete": 0, "presigned_get": 0, "token": 0, "suno_submit": 0, "suno_fetch": 0, "suno_download": 0}
SUNO_KEY = "vb-suno-key"
suno_tasks, suno_last = {}, {}


def make_mp3():
    try:
        return subprocess.run(["ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=220:duration=8", "-f", "lavfi", "-i", "sine=frequency=330:duration=8",
                               "-filter_complex", "amix=inputs=2,volume=0.4", "-ac", "2", "-b:a", "96k", "-f", "mp3", "-"], capture_output=True, check=True).stdout
    except Exception as e:  # noqa: BLE001
        sys.stderr.write(f"fake-cloud: ffmpeg unavailable ({e}); serving a silent frame\n")
        return b"ID3\x04\x00\x00\x00\x00\x00\x00" + b"\xff\xfb\x90\x00" + bytes(413)


SUNO_MP3 = make_mp3()


def b64url(b):
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def id_token(claims):
    head = b64url(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    body = b64url(json.dumps(claims, ensure_ascii=False).encode())
    sig = hmac.new(CLIENT_SECRET.encode(), f"{head}.{body}".encode(), hashlib.sha256).digest()
    return f"{head}.{body}.{b64url(sig)}"


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("fake-cloud %s\n" % (fmt % args))

    def send(self, code, body=b"", ctype="application/json", headers=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body, ensure_ascii=False).encode()
        elif isinstance(body, str):
            body = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    # ------------------------------------------------------------ OIDC
    def idp(self, path, q):
        if path == "/idp/.well-known/openid-configuration":
            return self.send(200, {"issuer": ISSUER, "authorization_endpoint": ISSUER + "/authorize", "token_endpoint": ISSUER + "/token",
                                   "userinfo_endpoint": ISSUER + "/userinfo", "jwks_uri": ISSUER + "/jwks", "response_types_supported": ["code"],
                                   "token_endpoint_auth_methods_supported": ["client_secret_basic", "client_secret_post"], "code_challenge_methods_supported": ["S256"],
                                   "id_token_signing_alg_values_supported": ["HS256"], "subject_types_supported": ["public"]})
        if path == "/idp/jwks":
            return self.send(200, {"keys": []})
        if path == "/idp/authorize":
            p = {k: v[0] for k, v in q.items()}
            if p.get("client_id") != CLIENT_ID or p.get("response_type") != "code" or p.get("code_challenge_method") != "S256":
                return self.send(400, "bad authorization request", "text/plain; charset=utf-8")
            links = []
            for key, u in USERS.items():
                code = secrets.token_urlsafe(16)
                with lock:
                    codes[code] = {"user": key, "challenge": p["code_challenge"], "nonce": p.get("nonce", ""), "redirect": p["redirect_uri"], "at": time.time()}
                target = p["redirect_uri"] + "?" + urlencode({"code": code, "state": p["state"]})
                links.append(f'<a class="u" href="{target}"><b>{u["name"]}</b><span>{u["email"]}</span></a>')
            deny = p["redirect_uri"] + "?" + urlencode({"error": "access_denied", "state": p["state"]})
            html = f"""<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width"><title>模拟身份服务</title>
<style>body{{font:15px system-ui;background:#f2f3f5;display:grid;place-items:center;min-height:100vh;margin:0}}main{{background:#fff;border-radius:14px;padding:28px;width:340px;box-shadow:0 8px 30px #0001}}
h1{{font-size:18px;margin:0 0 6px}}p{{color:#666;margin:0 0 18px;font-size:13px}}.u{{display:flex;flex-direction:column;gap:2px;padding:12px 14px;border:1px solid #e3e5e8;border-radius:10px;margin-bottom:8px;color:#111;text-decoration:none}}
.u span{{color:#777;font-size:12px}}.u:hover{{border-color:#0f7c70}}.d{{display:block;text-align:center;color:#999;font-size:13px;margin-top:10px}}</style>
<main><h1>模拟身份服务（测试用）</h1><p>选择要授权给声书工坊的账号</p>{''.join(links)}<a class="d" href="{deny}">拒绝授权</a></main>"""
            return self.send(200, html, "text/html; charset=utf-8")
        if path == "/idp/token":
            stats["token"] += 1
            form = {k: v[0] for k, v in parse_qs(self.body().decode()).items()}
            cid, sec = form.get("client_id"), form.get("client_secret")
            auth = self.headers.get("Authorization", "")
            if auth.startswith("Basic "):
                cid, sec = base64.b64decode(auth[6:]).decode().split(":", 1)
            if cid != CLIENT_ID or sec != CLIENT_SECRET:
                return self.send(401, {"error": "invalid_client"})
            with lock:
                g = codes.pop(form.get("code", ""), None)
            verifier = form.get("code_verifier", "")
            if not g or b64url(hashlib.sha256(verifier.encode()).digest()) != g["challenge"] or form.get("redirect_uri") != g["redirect"]:
                return self.send(400, {"error": "invalid_grant", "error_description": "code, verifier or redirect_uri does not match"})
            u = USERS[g["user"]]
            now = int(time.time())
            tok = id_token({"iss": ISSUER, "sub": u["sub"], "aud": CLIENT_ID, "iat": now, "exp": now + 600, "nonce": g["nonce"], "email": u["email"], "email_verified": u["email_verified"], "name": u["name"]})
            return self.send(200, {"access_token": "at-" + u["sub"], "token_type": "Bearer", "expires_in": 600, "id_token": tok})
        if path == "/idp/userinfo":
            sub = self.headers.get("Authorization", "").removeprefix("Bearer at-")
            for u in USERS.values():
                if u["sub"] == sub:
                    return self.send(200, u)
            return self.send(401, {"error": "invalid_token"})
        return self.send(404, {"error": "not found"})

    # ------------------------------------------------------------ Suno (New API task protocol)
    def suno(self, path, q):
        if path.startswith("/suno-audio/"):
            stats["suno_download"] += 1
            return self.send(200, SUNO_MP3, "audio/mpeg")
        if self.headers.get("Authorization", "") != "Bearer " + SUNO_KEY:
            return self.send(401, {"error": {"message": "无效的令牌", "type": "new_api_error"}})
        if path == "/suno/submit/music" and self.command == "POST":
            stats["suno_submit"] += 1
            body = json.loads(self.body() or b"{}")
            suno_last.clear()
            suno_last.update(body)
            tid = "suno-" + secrets.token_hex(6)
            text = body.get("gpt_description_prompt") or body.get("tags") or ""
            with lock:
                suno_tasks[tid] = {"polls": 0, "fail": "失败" in text, "body": body}
            return self.send(200, {"code": "success", "message": "", "data": tid})
        if path.startswith("/suno/fetch/") and self.command == "GET":
            stats["suno_fetch"] += 1
            tid = path.rsplit("/", 1)[1]
            with lock:
                t = suno_tasks.get(tid)
                if t:
                    t["polls"] += 1
            if not t:
                return self.send(400, {"code": "task_not_exist", "message": "task_not_exist", "data": None})
            if t["polls"] <= 2:
                return self.send(200, {"code": "success", "data": {"task_id": tid, "action": "MUSIC", "status": "IN_PROGRESS", "progress": f"{t['polls'] * 40}%", "data": []}})
            if t["fail"]:
                return self.send(200, {"code": "success", "data": {"task_id": tid, "action": "MUSIC", "status": "FAILURE", "fail_reason": "模拟失败：内容未通过审核", "data": []}})
            b = t["body"]
            title = b.get("title") or "雾港晨曦"
            lyrics = "[Instrumental]" if b.get("make_instrumental") else (b.get("prompt") or "[Verse]\n雾港的清晨，一封信")
            clips = [{"id": f"{tid}-{i}", "title": title, "status": "complete", "audio_url": f"{BASE}/suno-audio/{tid}-{i}.mp3", "image_url": "",
                      "model_name": b.get("mv") or "chirp-v4", "metadata": {"tags": b.get("tags") or "piano, calm", "prompt": lyrics, "duration": 8.0, "error_message": None}} for i in (1, 2)]
            return self.send(200, {"code": "success", "data": {"task_id": tid, "action": "MUSIC", "status": "SUCCESS", "progress": "100%", "data": clips}})
        return self.send(404, b"404 page not found", "text/plain")

    # ------------------------------------------------------------ OSS
    def oss_error(self, code, status, msg):
        xml = f'<?xml version="1.0" encoding="UTF-8"?><Error><Code>{code}</Code><Message>{msg}</Message><RequestId>fake</RequestId></Error>'
        return self.send(status, xml, "application/xml")

    def oss(self, path, q):
        auth = self.headers.get("Authorization", "")
        presigned = q.get("x-oss-signature-version", [""])[0] == "OSS4-HMAC-SHA256"
        if not auth.startswith("OSS4-HMAC-SHA256 ") and not presigned:
            return self.oss_error("AccessDenied", 403, "You have no right to access this object because of bucket acl.")
        if "Credential=deny/" in auth or q.get("x-oss-credential", [""])[0].startswith("deny/"):
            return self.oss_error("InvalidAccessKeyId", 403, "The OSS Access Key Id you provided does not exist in our records.")
        key = path.lstrip("/")
        if self.command == "PUT":
            data = self.body()
            with lock:
                objects[key] = (data, self.headers.get("Content-Type", "application/octet-stream"))
            stats["oss_put"] += 1
            return self.send(200, b"", "text/plain", {"ETag": '"%s"' % hashlib.md5(data).hexdigest()})
        if self.command == "DELETE":
            with lock:
                objects.pop(key, None)
            stats["oss_delete"] += 1
            return self.send(204, b"")
        with lock:
            obj = objects.get(key)
        if not obj:
            return self.oss_error("NoSuchKey", 404, "The specified key does not exist.")
        data, ctype = obj
        headers = {"Accept-Ranges": "bytes", "ETag": '"%s"' % hashlib.md5(data).hexdigest(), "Last-Modified": "Sun, 08 Nov 2026 00:00:00 GMT"}
        cd = q.get("response-content-disposition", [""])[0]
        if cd:
            headers["Content-Disposition"] = cd
        if self.command == "GET":
            stats["presigned_get" if presigned else "oss_get"] += 1
        rng = self.headers.get("Range", "")
        if rng.startswith("bytes=") and self.command == "GET":
            a, _, b = rng[6:].partition("-")
            start = int(a) if a else max(0, len(data) - int(b))
            end = min(int(b), len(data) - 1) if a and b else len(data) - 1
            headers["Content-Range"] = f"bytes {start}-{end}/{len(data)}"
            return self.send(206, data[start:end + 1], ctype, headers)
        return self.send(200, data, ctype, headers)

    def route(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path == "/_stats":
            return self.send(200, {**stats, "objects": len(objects)})
        if u.path.startswith("/idp"):
            return self.idp(u.path, q)
        if u.path == "/_suno/last":
            return self.send(200, suno_last)
        if u.path.startswith("/suno"):
            return self.suno(u.path, q)
        return self.oss(u.path, q)

    do_GET = do_POST = do_PUT = do_DELETE = do_HEAD = route


if __name__ == "__main__":
    print(f"fake cloud on {BASE} (OSS bucket vb-test, OIDC issuer {ISSUER}, Suno key {SUNO_KEY})", flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()

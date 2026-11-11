"""A tiny OpenAI-compatible chat server used only to exercise the platform's LLM integration end to end."""
import json, re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def answer(system, user):
    if "资深的有声书导演" in system:
        return {"characters": [
            {"name": "林黛玉", "aliases": ["黛玉"], "kind": "lead", "gender": "female", "ageGroup": "teen", "identity": "贾府表小姐", "personality": "敏感聪慧", "voiceTraits": "清冷、轻柔、语速偏慢", "profile": "寄居贾府的少女，才情出众。", "lines": 2},
            {"name": "贾宝玉", "aliases": ["宝玉"], "kind": "lead", "gender": "male", "ageGroup": "teen", "identity": "贾府公子", "personality": "温柔多情", "voiceTraits": "清亮温和的少年音", "profile": "贾府的公子，与黛玉青梅竹马。", "lines": 2}]}
    if "专业的有声书画本师" in system:
        units = []
        last = "C2"
        for m in re.finditer(r"^\[(\d+)\] \((引号内|叙述)\) (.*)$", user.split("【需要标注的文本单元】")[-1], re.M):
            uid, kind, text = int(m.group(1)), m.group(2), m.group(3)
            if kind == "叙述":
                units.append({"id": uid, "type": "narration", "speaker": "NARRATOR", "emotion": "neutral", "intensity": 1, "tags": [], "instruction": "",
                              "cues": [],
                              "confidence": 0.95, "speechTag": text.endswith("说。") or text.endswith("道："), "prons": []})
            else:
                spk = "C2" if ("我来" in text or "外面" in text) else ("C1" if "你" in text or "与你" in text else ("C1" if last == "C2" else "C2"))
                last = spk
                cues = [{"tag": "giggles", "after": text[:3]}] if "我来" in text else ([{"tag": "scornful", "after": ""}] if spk == "C1" else [])
                units.append({"id": uid, "type": "dialogue", "speaker": spk, "emotion": "sad" if spk == "C1" else "gentle", "intensity": 2, "tags": [], "cues": cues,
                              "instruction": "冷淡，带点疑问" if spk == "C1" else "轻快温和", "confidence": 0.9, "speechTag": False,
                              "prons": [{"word": "了", "char": "了", "pinyin": "le", "homophone": "乐"}] if "了" in text else []})
        return {"units": units, "sfx": []}
    if "选角导演" in system:
        chars = re.findall(r"^(C\d+) ", user, re.M)
        voices = re.findall(r"^(V\d+) ", user, re.M)
        return {"castings": [{"characterId": c, "candidates": [{"voiceId": voices[(i + k) % len(voices)], "score": 0.9 - k * 0.1, "reason": "音色与角色气质契合"} for k in range(3)]} for i, c in enumerate(chars)]}
    if "播音审校专家" in system:
        return {"terms": [{"term": "黛玉", "pinyin": "dài yù", "note": "人名"}]}
    if "作品简介" in system or "吸引听众的简介" in system:
        return {"description": "一段关于少年少女心事的故事。", "tags": ["古风", "言情"], "genre": "言情"}
    if "章节标题" in system:
        return {"lines": [0]}
    if "连通性测试" in system:
        return {"reply": "ok"}
    if "声音描述" in system or "配音导演" in system:
        return {"description": "十六七岁的少女，声音清冷轻柔，语速偏慢，带一丝忧郁。"}
    if "结构化属性" in system:
        return {"gender": "female", "ageGroup": "teen", "tags": ["清冷", "温柔"], "name": "清冷少女"}
    if "封面" in system:
        return {"prompt": "雨夜的江南庭院，竹影摇曳，冷色调"}
    if "内容安全" in system:
        return {"verdict": "pass", "categories": [], "reason": "正常文学内容"}
    return {"reply": "ok"}


HOOKS = []
TTS_REQS = []
TTS_AUDIO = {}


def wav_for(text):
    """Generate a short 16-bit mono WAV whose length follows the text (stands in for DashScope audio)."""
    import math, struct, io, wave
    rate = 44100
    n = int(rate * max(0.4, len(text) * 0.18))
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1); w.setsampwidth(2); w.setframerate(rate)
        frames = bytearray()
        for i in range(n):
            env = 0.5 * (1 - math.cos(2 * math.pi * min(1, i / (rate * 0.15))))
            frames += struct.pack("<h", int(9000 * env * math.sin(2 * math.pi * (180 + 40 * math.sin(i / 3000)) * i / rate)))
        w.writeframes(bytes(frames))
    return buf.getvalue()


QW_VOICES = {}  # cloned voice id -> {"model", "status", "polls", "deleted", "input"}
QW_ENROLL = []  # enrollment calls (kept apart from synthesis requests)


def dashscope_enroll(handler, raw):
    """Qianwen voice-enrollment: create_voice / query_voice / delete_voice (Qwen-Audio-TTS, CosyVoice)."""
    import base64, re as _re
    if handler.headers.get("Authorization") != "Bearer sk-qwen-test":
        return 401, {"code": "InvalidApiKey", "message": "Invalid API-key provided.", "request_id": "r-auth"}
    body = json.loads(raw)
    inp = body.get("input", {})
    act = inp.get("action")
    QW_ENROLL.append({"enroll": act, "input": {k: (v[:40] + "…" if isinstance(v, str) and len(v) > 60 else v) for k, v in inp.items()}})
    if body.get("model") != "voice-enrollment":
        return 400, {"code": "InvalidParameter", "message": "model must be voice-enrollment", "request_id": "r-bad"}
    if act == "create_voice" and "voice_prompt" in inp:  # voice design
        design_models = {"cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash", "cosyvoice-v3-plus", "cosyvoice-v3-flash",
                         "qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash", "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash"}
        prompt, preview = inp.get("voice_prompt", ""), inp.get("preview_text", "")
        params = body.get("parameters") or {}
        if inp.get("target_model") not in design_models:
            return 400, {"code": "InvalidParameter", "message": "target_model does not support voice design", "request_id": "r-model"}
        if not prompt or len(prompt) > 500:
            return 400, {"code": "InvalidParameter", "message": "voice_prompt length must be in [1, 500]", "request_id": "r-prompt"}
        if not (15 <= len(preview) <= 200):
            return 400, {"code": "InvalidParameter", "message": "preview_text length must be in [15, 200]", "request_id": "r-preview"}
        if (inp.get("language_hints") or ["zh"])[0] not in ("zh", "en"):
            return 400, {"code": "InvalidParameter", "message": "language_hints only supports zh and en", "request_id": "r-lang"}
        if "违禁" in prompt:
            return 400, {"code": "DataInspectionFailed", "message": "Input data may contain inappropriate content.", "request_id": "r-sensitive"}
        prefix = inp.get("prefix", "")
        if not _re.fullmatch(r"[A-Za-z0-9]{1,10}", prefix):
            return 400, {"code": "InvalidParameter", "message": "prefix must be alphanumeric, at most 10 chars", "request_id": "r-prefix"}
        vid = "%s-vd-%s-%04d" % (inp["target_model"], prefix, len(QW_VOICES) + 1)
        QW_VOICES[vid] = {"model": inp["target_model"], "polls": 0, "deleted": False, "design": True, "input": inp, "parameters": params}
        wav = wav_for(preview)
        return 200, {"output": {"voice_id": vid, "target_model": inp["target_model"],
                                "preview_audio": {"data": base64.b64encode(wav).decode(), "sample_rate": params.get("sample_rate", 24000), "response_format": params.get("response_format", "wav")}},
                     "usage": {"count": 1}, "request_id": "r-design"}
    if act == "create_voice":
        audio = inp.get("audio", "")
        m = _re.match(r"^data:(audio/(?:wav|mpeg|mp4));base64,(.+)$", audio, _re.S)
        if not m:
            return 400, {"code": "BadRequest.UnsupportedFileFormat", "message": "File format unsupported.", "request_id": "r-fmt"}
        data = base64.b64decode(m.group(2))
        if m.group(1) == "audio/wav" and data[:4] != b"RIFF":
            return 400, {"code": "BadRequest.UnsupportedFileFormat", "message": "File format unsupported.", "request_id": "r-fmt"}
        prefix = inp.get("prefix", "")
        if not _re.fullmatch(r"[A-Za-z0-9]{1,10}", prefix):
            return 400, {"code": "InvalidParameter", "message": "prefix must be alphanumeric, at most 10 chars", "request_id": "r-prefix"}
        vid = "%s-%s-%04d" % (inp.get("target_model"), prefix, len(QW_VOICES) + 1)
        QW_VOICES[vid] = {"model": inp.get("target_model"), "polls": 0, "deleted": False, "bytes": len(data),
                          "input": {k: v for k, v in inp.items() if k != "audio"}}
        return 200, {"output": {"voice_id": vid}, "usage": {"count": 1}, "request_id": "r-create"}
    v = QW_VOICES.get(inp.get("voice_id", ""))
    if not v or v["deleted"]:
        return 400, {"code": "BadRequest.ResourceNotExist", "message": "The Required resource not exist.", "request_id": "r-404"}
    if act == "query_voice":
        v["polls"] += 1
        return 200, {"output": {"status": "OK" if v["polls"] >= 2 else "DEPLOYING", "target_model": v["model"], "gmt_create": "2026-11-06 10:00:00"}, "usage": {"count": 1}, "request_id": "r-q"}
    if act == "delete_voice":
        v["deleted"] = True
        return 200, {"output": {}, "usage": {"count": 1}, "request_id": "r-del"}
    return 400, {"code": "InvalidParameter", "message": "unknown action", "request_id": "r-act"}


def dashscope_tts(handler, raw):
    if handler.headers.get("Authorization") != "Bearer sk-qwen-test":
        return 401, {"code": "InvalidApiKey", "message": "Invalid API-key provided.", "request_id": "r-auth"}
    body = json.loads(raw)
    TTS_REQS.append(body)
    inp = body.get("input", {})
    if not inp.get("text") or not inp.get("voice"):
        return 400, {"code": "InvalidParameter", "message": "text and voice are required", "request_id": "r-bad"}
    cv = QW_VOICES.get(inp["voice"])
    if cv and (cv["deleted"] or cv["model"] != body.get("model")):  # clones only work with their target model
        return 400, {"code": "InvalidParameter", "message": "The requested voice '%s' is not supported." % inp["voice"], "request_id": "r-voice"}
    key = str(len(TTS_REQS))
    TTS_AUDIO[key] = wav_for(re.sub(r"<[^>]+>|\[[a-z ]+\]", "", inp["text"]))
    return 200, {"request_id": "r" + key, "output": {"finish_reason": "stop", "audio": {"data": "", "url": "http://127.0.0.1:18999/tts/" + key + ".wav", "id": "audio_" + key}}, "usage": {"characters": len(inp["text"])}}


MM_REQS = []
MM_TASKS = {}
MM_FAIL_ONCE = set()
MM_FILES = {}     # file_id -> {"purpose", "name", "size"}
MM_CLONES = {}    # voice_id -> {"file_id", "deleted"}
MM_DEMO = {}      # demo id -> wav bytes


FAKE_BASE = "http://127.0.0.1:18999"


def mm_reply(handler, obj, code=200, ctype="application/json"):
    out = obj if isinstance(obj, (bytes, bytearray)) else json.dumps(obj, ensure_ascii=False).encode()
    handler.send_response(code); handler.send_header("Content-Type", ctype); handler.send_header("Content-Length", str(len(out))); handler.end_headers(); handler.wfile.write(out)


def mm_ok(extra=None):
    d = {"base_resp": {"status_code": 0, "status_msg": "success"}}
    d.update(extra or {})
    return d


def mm_spoken(text):
    return re.sub(r"\([a-z0-9-]+\)", "x", text)  # interjections / readings take some time too


def minimax(handler, raw):
    """MiniMax T2A v2 (sync + async), file download and voice listing."""
    if handler.headers.get("Authorization") != "Bearer mm-test":
        return mm_reply(handler, {"base_resp": {"status_code": 1004, "status_msg": "login fail: Please carry the API secret key"}})
    path = handler.path.split("?")[0]
    if path == "/v1/t2a_v2":
        body = json.loads(raw)
        MM_REQS.append({"api": "sync", "body": body})
        text = body.get("text", "")
        if "限流测试" in text and text not in MM_FAIL_ONCE:
            MM_FAIL_ONCE.add(text)
            return mm_reply(handler, {"base_resp": {"status_code": 1002, "status_msg": "rate limit exceeded(RPM)"}})
        if body.get("voice_setting", {}).get("voice_id") == "not_a_voice":
            return mm_reply(handler, {"base_resp": {"status_code": 2054, "status_msg": "voice id not exist"}})
        wav = wav_for(mm_spoken(text))
        return mm_reply(handler, mm_ok({"data": {"audio": wav.hex(), "status": 2}, "trace_id": "t%d" % len(MM_REQS),
                                         "extra_info": {"audio_length": 1000, "audio_sample_rate": 44100, "audio_size": len(wav), "audio_format": "wav", "audio_channel": 1, "usage_characters": len(text)}}))
    if path == "/v1/t2a_async_v2":
        body = json.loads(raw)
        MM_REQS.append({"api": "async", "body": body})
        tid = 95157322514444 + len(MM_TASKS)
        MM_TASKS[str(tid)] = {"text": body.get("text", ""), "polls": 0, "file": tid + 1}
        return mm_reply(handler, mm_ok({"task_id": tid, "task_token": "tok", "file_id": tid + 1, "usage_characters": len(body.get("text", ""))}))
    if path == "/v1/query/t2a_async_query_v2":
        from urllib.parse import urlparse, parse_qs
        tid = parse_qs(urlparse(handler.path).query).get("task_id", [""])[0]
        t = MM_TASKS.get(tid)
        if not t:
            return mm_reply(handler, {"base_resp": {"status_code": 2013, "status_msg": "task not found"}})
        t["polls"] += 1
        return mm_reply(handler, mm_ok({"task_id": int(tid), "status": "Success" if t["polls"] >= 2 else "Processing", "file_id": t["file"]}))
    if path == "/v1/files/retrieve_content":
        import io, tarfile
        from urllib.parse import urlparse, parse_qs
        fid = parse_qs(urlparse(handler.path).query).get("file_id", [""])[0]
        t = next((x for x in MM_TASKS.values() if str(x["file"]) == fid), None)
        if not t:
            return mm_reply(handler, {"base_resp": {"status_code": 1000, "status_msg": "file not found"}})
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w") as tf:
            for name, data in ((fid + "/content.wav", wav_for(mm_spoken(t["text"]))), (fid + "/content.json", b"[]"), (fid + "/content_extra.json", b"{}")):
                info = tarfile.TarInfo(name); info.size = len(data); tf.addfile(info, io.BytesIO(data))
        return mm_reply(handler, buf.getvalue(), ctype="application/x-tar")
    if path == "/v1/files/upload":
        import cgi, io
        form = cgi.FieldStorage(fp=io.BytesIO(raw), environ={"REQUEST_METHOD": "POST", "CONTENT_TYPE": handler.headers["Content-Type"], "CONTENT_LENGTH": str(len(raw))})
        data = form["file"].file.read()
        fid = 300000000000 + len(MM_FILES) + 1
        MM_FILES[str(fid)] = {"purpose": form.getvalue("purpose"), "name": form["file"].filename, "size": len(data), "riff": data[:4] == b"RIFF"}
        MM_REQS.append({"api": "upload", "body": MM_FILES[str(fid)]})
        return mm_reply(handler, mm_ok({"file": {"file_id": fid, "bytes": len(data), "created_at": 1700000000, "filename": form["file"].filename, "purpose": form.getvalue("purpose")}}))
    if path == "/v1/voice_clone":
        body = json.loads(raw)
        MM_REQS.append({"api": "clone", "body": body})
        vid = body.get("voice_id", "")
        if str(body.get("file_id")) not in MM_FILES:
            return mm_reply(handler, {"base_resp": {"status_code": 2013, "status_msg": "invalid file_id"}})
        if vid in MM_CLONES and not MM_CLONES[vid]["deleted"]:
            return mm_reply(handler, {"base_resp": {"status_code": 2039, "status_msg": "voice clone voice id duplicate"}})
        MM_CLONES[vid] = {"file_id": body["file_id"], "deleted": False}
        out = {"input_sensitive": {"type": 0}, "demo_audio": ""}
        if body.get("text") and body.get("model"):
            key = str(len(MM_DEMO) + 1)
            MM_DEMO[key] = wav_for(body["text"])
            out["demo_audio"] = FAKE_BASE + "/mm-demo/" + key + ".wav"
        return mm_reply(handler, mm_ok(out))
    if path == "/v1/delete_voice":
        body = json.loads(raw)
        MM_REQS.append({"api": "delete", "body": body})
        c = MM_CLONES.get(body.get("voice_id", ""))
        if not c or c["deleted"]:
            return mm_reply(handler, {"base_resp": {"status_code": 2054, "status_msg": "voice id not exist"}})
        c["deleted"] = True
        return mm_reply(handler, mm_ok({"voice_id": body["voice_id"], "created_time": "2026-11-06"}))
    if path == "/v1/get_voice":
        return mm_reply(handler, mm_ok({"system_voice": [
            {"voice_id": "female-chengshu", "voice_name": "成熟女性音色", "description": []},
            {"voice_id": "Chinese (Mandarin)_Radio_Host", "voice_name": "电台男主播", "description": []},
            {"voice_id": "moss_audio_fake_new", "voice_name": "新上线温柔女声", "description": ["温柔", "知性"]}]}))
    return mm_reply(handler, {"base_resp": {"status_code": 2013, "status_msg": "unknown path"}}, 404)


def asr(raw, ctype):
    import cgi, io, subprocess, tempfile, os
    env = {"REQUEST_METHOD": "POST", "CONTENT_TYPE": ctype, "CONTENT_LENGTH": str(len(raw))}
    form = cgi.FieldStorage(fp=io.BytesIO(raw), environ=env)
    data = form["file"].file.read()
    with tempfile.NamedTemporaryFile(delete=False, suffix=".bin") as f:
        f.write(data)
    dur = float(subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", f.name], capture_output=True, text=True).stdout.strip() or 1)
    os.unlink(f.name)
    text = open("/tmp/asr_text.txt", encoding="utf-8").read().strip() if os.path.exists("/tmp/asr_text.txt") else "测试文本"
    chars = [c for c in text if c.strip()]
    step = dur / max(1, len(chars))
    words = [{"word": c, "start": i * step, "end": (i + 1) * step} for i, c in enumerate(chars)]
    return {"text": text, "language": "zh", "words": words, "segments": [{"text": text, "start": 0, "end": dur}]}


# ---- image generation (OpenAI GPT Image / Gemini Nano Banana) ----
IMG_REQS = []


def png_b64(w=64, h=64, seed=0):
    import zlib, struct, base64
    r, g, b = (seed * 53) % 256, (seed * 97 + 80) % 256, (seed * 31 + 160) % 256
    raw = b"".join(b"\x00" + bytes([r, g, b]) * w for _ in range(h))
    def chunk(t, d):
        c = struct.pack(">I", len(d)) + t + d
        return c + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
    data = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")
    return base64.b64encode(data).decode()


def image_api(handler, raw):
    path = handler.path.split("?")[0]
    body = json.loads(raw) if raw else {}
    IMG_REQS.append({"path": path, "body": body, "headers": {k: handler.headers.get(k) for k in ("Authorization", "x-goog-api-key", "Api-Revision")}})
    prompt = json.dumps(body, ensure_ascii=False)
    if "BLOCKME" in prompt:
        if path.endswith("/images/generations"):
            return mm_reply(handler, {"error": {"message": "Your request was rejected by the safety system.", "code": "moderation_blocked"}}, 400)
        return mm_reply(handler, {"error": {"code": 400, "message": "Request blocked by safety filters", "status": "INVALID_ARGUMENT"}}, 400)
    if path.endswith("/images/generations"):
        n = int(body.get("n", 1))
        return mm_reply(handler, {"created": 1, "output_format": "png", "size": body.get("size"), "data": [{"b64_json": png_b64(seed=len(IMG_REQS) + i)} for i in range(n)]})
    if path.endswith("/interactions"):
        return mm_reply(handler, {"id": "ix-%d" % len(IMG_REQS), "status": "completed", "steps": [
            {"type": "thought", "content": []},
            {"type": "model_output", "content": [{"type": "image", "mime_type": "image/jpeg", "data": png_b64(seed=len(IMG_REQS))}]}]})
    if path.endswith(":generateContent"):
        return mm_reply(handler, {"candidates": [{"finishReason": "STOP", "content": {"parts": [{"inlineData": {"mimeType": "image/png", "data": png_b64(seed=len(IMG_REQS))}}]}}]})
    return mm_reply(handler, {"error": {"message": "not found"}}, 404)


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/img-requests"):
            return mm_reply(self, IMG_REQS)
        if self.path.startswith("/v1beta/models/"):
            m = self.path.split("?")[0][len("/v1beta/models/"):]
            return mm_reply(self, {"name": "models/" + m, "displayName": "Fake " + m})
        if self.path.startswith("/v1/models/"):
            return mm_reply(self, {"id": self.path.split("?")[0][len("/v1/models/"):], "object": "model"})
        if self.path.startswith("/v1/query/") or self.path.startswith("/v1/files/"):
            return minimax(self, b"")
        if self.path.startswith("/mm-requests"):
            return mm_reply(self, MM_REQS)
        if self.path.startswith("/mm-clones"):
            return mm_reply(self, MM_CLONES)
        if self.path.startswith("/qw-voices"):
            return mm_reply(self, QW_VOICES)
        if self.path.startswith("/mm-demo/"):
            data = MM_DEMO.get(self.path[9:].split(".")[0], b"")
            return mm_reply(self, data, 200 if data else 404, "audio/wav")
        if self.path.startswith("/tts/"):
            data = TTS_AUDIO.get(self.path[5:].split(".")[0], b"")
            self.send_response(200 if data else 404)
            self.send_header("Content-Type", "audio/wav")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        data = json.dumps(TTS_REQS if self.path.startswith("/tts-requests") else HOOKS, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if self.path.startswith("/hook"):
            HOOKS.append({"path": self.path, "sig": self.headers.get("X-Voicebook-Signature"), "body": json.loads(raw)})
            self.send_response(200); self.end_headers(); return
        if self.path.split("?")[0] in ("/v1/t2a_v2", "/v1/t2a_async_v2", "/v1/get_voice", "/v1/files/upload", "/v1/voice_clone", "/v1/delete_voice"):
            return minimax(self, raw)
        if self.path.endswith("/services/audio/tts/customization"):
            code, obj = dashscope_enroll(self, raw)
            return mm_reply(self, obj, code)
        if self.path.endswith("/services/audio/tts/SpeechSynthesizer"):
            code, obj = dashscope_tts(self, raw)
            out = json.dumps(obj, ensure_ascii=False).encode()
            self.send_response(code); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out); return
        if self.path.split("?")[0].endswith(("/images/generations", "/interactions", ":generateContent")):
            return image_api(self, raw)
        if self.path.endswith("/audio/transcriptions"):
            out = json.dumps(asr(raw, self.headers["Content-Type"]), ensure_ascii=False).encode()
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out); return
        body = json.loads(raw)
        msgs = body.get("messages", [])
        system = next((m["content"] for m in msgs if m["role"] == "system"), "")
        user = "\n".join(m["content"] for m in msgs if m["role"] == "user")
        content = json.dumps(answer(system, user), ensure_ascii=False)
        out = {"id": "x", "model": body.get("model"), "choices": [{"message": {"role": "assistant", "content": content}, "finish_reason": "stop"}], "usage": {"prompt_tokens": len(system + user) // 2, "completion_tokens": len(content) // 2}}
        data = json.dumps(out, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


ThreadingHTTPServer(("127.0.0.1", 18999), H).serve_forever()

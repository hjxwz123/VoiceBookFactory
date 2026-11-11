"""A scripted OpenAI-compatible chat server that answers for the showcase book 《雾港来信》 (story.txt).

It stands in for the platform's language model while the landing-page screenshots are produced, so the
real LLM account is never billed and the annotations come out exactly as a careful editor would write them.
Run: python3 scripts/showcase/showcase_llm.py   (listens on 127.0.0.1:18997)
"""
import json, re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CHARACTERS = [
    {"name": "林晚", "aliases": ["晚姐姐"], "kind": "lead", "gender": "female", "ageGroup": "young", "identity": "雾港邮局的投递员",
     "personality": "安静、倔强、心细", "voiceTraits": "温柔清亮的青年女声，语速平稳，带一点倔强",
     "profile": "在雾港长大的投递员，每天骑旧自行车送信。收到一封寄往废弃灯塔的无名信后，决定亲自送到。", "lines": 9},
    {"name": "陆沉舟", "aliases": ["看守人", "男人"], "kind": "lead", "gender": "male", "ageGroup": "adult", "identity": "鹭屿灯塔的看守人",
     "personality": "寡言、克制、外冷内热", "voiceTraits": "低沉浑厚的成年男声，语速偏慢，情绪压在声音底下",
     "profile": "独自守着熄灭二十年的灯塔。母亲当年离开雾港，一封迟到的信让他重新点亮灯塔。", "lines": 9},
    {"name": "周伯", "aliases": [], "kind": "supporting", "gender": "male", "ageGroup": "senior", "identity": "雾港邮局的老局长",
     "personality": "慈祥、话少、心里藏着往事", "voiceTraits": "沙哑温厚的老年男声，说话慢，尾音下沉",
     "profile": "在邮局干了一辈子，知道灯塔的旧事却从不多说。是他把手电筒交给林晚。", "lines": 8},
    {"name": "阿满", "aliases": [], "kind": "supporting", "gender": "female", "ageGroup": "child", "identity": "渔家的小女儿，九岁",
     "personality": "活泼、胆大、爱跟着林晚", "voiceTraits": "清脆明亮的童声，语速快，情绪外放",
     "profile": "码头渔家的女儿，熟悉鹭屿的每一块礁石，坚持要带林晚去灯塔。", "lines": 5},
]

# dialogue lines: key text -> (speaker, emotion, intensity, instruction, cues)
LINES = [
    ("又起雾了", "周伯", "tired", 1, "随口一说，带点疲惫", []),
    ("今天的信，先送码头", "周伯", "neutral", 1, "平淡地交代", []),
    ("这封没有寄件人", "林晚", "nervous", 1, "疑惑，略带不安", [{"tag": "curious", "after": "周伯，"}]),
    ("没人收过信了", "周伯", "sad", 2, "压低声音，像在回忆", [{"tag": "sighing", "after": ""}]),
    ("你要是怕，就放回去", "周伯", "whisper", 1, "低声，带着试探", []),
    ("我不怕", "林晚", "neutral", 2, "干脆，不带犹豫", []),
    ("送信是我的工作", "林晚", "gentle", 1, "语气放软，但很笃定", []),
    ("晚姐姐！", "阿满", "happy", 3, "欢快地喊", [{"tag": "excited", "after": ""}]),
    ("我爹说今天有大雾", "阿满", "happy", 2, "雀跃，语速快", []),
    ("去灯塔可不是玩的", "林晚", "happy", 1, "笑着劝", [{"tag": "giggles", "after": ""}]),
    ("我认得路", "阿满", "happy", 2, "不服气，撒娇", [{"tag": "mischievously", "after": ""}]),
    ("我闭着眼睛都能走过去", "阿满", "happy", 2, "得意，尾音上扬", []),
    ("带上这个", "周伯", "gentle", 1, "叮嘱", []),
    ("天黑之前，一定要回来", "周伯", "gentle", 2, "郑重地叮嘱", [{"tag": "serious", "after": ""}]),
    ("你们找谁", "陆沉舟", "neutral", 2, "低沉，带着戒备", []),
    ("我是雾港邮局的投递员", "林晚", "nervous", 1, "礼貌，有点紧张", []),
    ("有一封寄到灯塔的信", "林晚", "nervous", 1, "鼓起勇气", []),
    ("这是我母亲的字", "陆沉舟", "sad", 2, "声音发涩，强忍情绪", [{"tag": "very slowly", "after": ""}]),
    ("她已经走了二十年", "陆沉舟", "sad", 3, "很慢，几乎是自语", []),
    ("他是谁呀", "阿满", "whisper", 1, "小声，有点害怕", [{"tag": "whispers", "after": ""}]),
    ("我叫陆沉舟", "陆沉舟", "gentle", 2, "放软声音，安抚孩子", [{"tag": "empathetic", "after": ""}]),
    ("别怕，小姑娘", "陆沉舟", "gentle", 1, "温和", []),
    ("谢谢你送来", "陆沉舟", "gentle", 1, "克制，客气", []),
    ("雾要散了", "陆沉舟", "neutral", 1, "平静地送客", []),
    ("我把信读完了", "陆沉舟", "gentle", 1, "平静，声音有些哑", []),
    ("她在信里写", "陆沉舟", "sad", 2, "温柔，带着释然", []),
    ("老灯塔的灯", "周伯", "sad", 2, "哽咽，回忆往事", [{"tag": "crying", "after": ""}]),
    ("您早就知道", "林晚", "surprised", 2, "意外，脱口而出", [{"tag": "gasp", "after": ""}]),
    ("我知道的事情不多", "周伯", "gentle", 1, "笑着，带点狡黠", [{"tag": "giggles", "after": ""}]),
    ("有些信，总要有人送到", "周伯", "gentle", 2, "慢慢地，意味深长", []),
    ("亮了！真的亮了", "阿满", "shout", 3, "大声欢呼", [{"tag": "shouting", "after": ""}]),
    ("欢迎回家", "林晚", "gentle", 1, "轻声，像自言自语", [{"tag": "asmr", "after": ""}]),
]

NARRATION_DIRECTION = {
    "男人没有接": "放慢，压低",
    "脸色一点一点变白": "放慢，留出停顿",
    "灯塔的铁门没有锁": "压低声音，制造悬念",
    "鹭屿的灯塔亮了": "舒展，带着暖意",
}

PRONS = [
    ("还给", "还", "huán", "环"), ("一行", "行", "háng", "杭"), ("长长", "长", "cháng", "常"),
    ("朝雾", "朝", "zhāo", "招"), ("重新", "重", "chóng", "虫"), ("扎着", "扎", "zā", "匝"),
]

LEXICON = [
    {"term": "陆沉舟", "pinyin": "lù chén zhōu", "note": "人名"},
    {"term": "鹭屿", "pinyin": "lù yǔ", "note": "地名，屿读 yǔ"},
    {"term": "雾港", "pinyin": "wù gǎng", "note": "地名"},
    {"term": "朝雾", "pinyin": "zhāo wù", "note": "朝读 zhāo，早晨的雾"},
    {"term": "一行脚印", "pinyin": "yì háng jiǎo yìn", "note": "行读 háng"},
    {"term": "还给", "pinyin": "huán gěi", "note": "还读 huán"},
    {"term": "扎着", "pinyin": "zā zhe", "note": "扎辫子读 zā"},
    {"term": "重新", "pinyin": "chóng xīn", "note": "重读 chóng"},
]


def char_codes(user):
    codes = {}
    for m in re.finditer(r"^(C\d+) (\S+?)(?:（|$)", user, re.M):
        codes[m.group(2)] = m.group(1)
    return codes


def annotate(user):
    codes = char_codes(user)
    units = []
    body = user.split("【需要标注的文本单元】")[-1]
    for m in re.finditer(r"^\[(\d+)\] \((引号内|叙述)\) (.*)$", body, re.M):
        uid, kind, text = int(m.group(1)), m.group(2), m.group(3)
        prons = [{"word": w, "char": c, "pinyin": p, "homophone": h} for w, c, p, h in PRONS if w in text]
        if kind == "引号内":
            hit = next((l for l in LINES if l[0] in text), None)
            if hit:
                _, who, emo, inten, ins, cues = hit
                units.append({"id": uid, "type": "dialogue", "speaker": codes.get(who, "NEW:" + who), "emotion": emo, "intensity": inten,
                              "tags": [], "cues": cues, "instruction": ins, "confidence": 0.96, "speechTag": False, "prons": prons})
                continue
        ins = next((v for k, v in NARRATION_DIRECTION.items() if k in text), "")
        tag = bool(re.fullmatch(r"[^，。]{1,6}(说|问|道)[。：]", text))
        units.append({"id": uid, "type": "narration", "speaker": "NARRATOR", "emotion": "neutral", "intensity": 1, "tags": [], "cues": [],
                      "instruction": ins, "confidence": 0.98, "speechTag": tag, "prons": prons})
    return {"units": units, "sfx": []}


def answer(system, user):
    if "资深的有声书导演" in system:
        return {"characters": CHARACTERS}
    if "专业的有声书画本师" in system:
        return annotate(user)
    if "播音审校专家" in system:
        return {"terms": LEXICON}
    if "作品简介" in system or "吸引听众的简介" in system:
        return {"description": "雾港邮局的投递员林晚，收到一封没有寄件人的信，收件地址是一座熄灭了二十年的灯塔。她带着渔家女孩阿满穿过礁石小路，把信交到沉默的看守人陆沉舟手里。一封迟到二十年的信，让一座灯塔重新亮起。",
                "tags": ["悬疑", "温情", "海岛"], "genre": "悬疑"}
    if "选角导演" in system:
        chars = re.findall(r"^(C\d+) ", user, re.M)
        voices = re.findall(r"^(V\d+) ", user, re.M)
        return {"castings": [{"characterId": c, "candidates": [{"voiceId": voices[(i + k) % len(voices)], "score": 0.9 - k * 0.1, "reason": "音色与角色气质契合"} for k in range(min(3, len(voices)))]} for i, c in enumerate(chars)]}
    if "章节标题" in system:
        return {"lines": [int(m.group(1)) for m in re.finditer(r"^(\d+)[：:.\s]+第.{1,3}章", user, re.M)]}
    if "声音描述" in system or "配音导演" in system:
        return {"description": "三十多岁的男声，低沉浑厚，语速偏慢，情绪压在声音底下，带一点海风般的沙哑。"}
    if "结构化属性" in system:
        return {"gender": "male", "ageGroup": "adult", "tags": ["低沉", "克制"], "name": "灯塔看守人"}
    if "封面" in system:
        return {"prompt": "浓雾中的海岛灯塔，一束暖黄的光穿过灰蓝色的海面，近处是湿漉漉的石板路和一辆旧自行车"}
    if "内容安全" in system:
        return {"verdict": "pass", "categories": [], "reason": "正常文学内容"}
    return {"reply": "ok"}


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        out = json.dumps({"data": [{"id": "showcase-chat"}]}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out)

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        body = json.loads(raw)
        msgs = body.get("messages", [])
        system = next((m["content"] for m in msgs if m["role"] == "system"), "")
        user = "\n".join(m["content"] for m in msgs if m["role"] == "user")
        content = json.dumps(answer(system, user), ensure_ascii=False)
        out = json.dumps({"id": "x", "model": body.get("model"), "choices": [{"message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
                          "usage": {"prompt_tokens": len(system + user) // 2, "completion_tokens": len(content) // 2}}, ensure_ascii=False).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 18997), H).serve_forever()

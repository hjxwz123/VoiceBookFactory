#!/usr/bin/env python3
"""Generate internal/ai/tts/qwen_voices_gen.go from the official Qianwen (DashScope) voice lists.

Sources (downloaded automatically, or pass a directory containing the cached files):
  - CosyVoice system voices          developer-guides/speech/voice-list/cosyvoice.md
  - Qwen-Audio-TTS system voices     developer-guides/speech/voice-list/qwen-audio-tts.md
  - Qwen-Audio-TTS 3.0 base voices   two Excel sheets (plus / flash)

Usage:  python3 scripts/gen_qwen_voices.py [cache_dir]
Re-run whenever the platform publishes new voices; the server upserts the catalog on start.
"""
import html
import os
import re
import sys
import tempfile
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

DOCS = "https://platform.qianwenai.com/docs/developer-guides/speech/voice-list/"
SOURCES = {
    "cosy_voices.md": DOCS + "cosyvoice.md",
    "qwen_voices.md": DOCS + "qwen-audio-tts.md",
    "base_plus.xlsx": "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20260723/ydwqqz/qwen-audio-3.0-tts-plus%E5%9F%BA%E7%A1%80%E9%9F%B3%E8%89%B2.xlsx",
    "base_flash.xlsx": "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20260723/thosjr/qwen-audio-3.0-tts-flash%E5%9F%BA%E7%A1%80%E9%9F%B3%E8%89%B2.xlsx",
}
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "ai", "tts", "qwen_voices_gen.go")

LANG_CODES = {
    "保加利亚语": "bg", "荷兰语": "nl", "匈牙利语": "hu", "罗马尼亚语": "ro", "丹麦语": "da", "克罗地亚语": "hr", "希腊语": "el", "捷克语": "cs",
    "挪威语": "nn", "瑞典语": "sv", "印地语": "hi", "斯洛伐克语": "sk", "芬兰语": "fi", "乌尔都语": "ur", "塞尔维亚语": "sr", "墨西哥西班牙语": "es-MX",
    "立陶宛语": "lt", "拉脱维亚语": "lv", "土耳其语": "tr", "波斯语": "fa", "希伯来语": "he", "蒙古语": "mn", "斯洛文尼亚语": "sl", "阿塞拜疆语": "az",
    "波兰语": "pl", "加泰罗尼亚语": "ca", "高棉语": "km", "巴西葡萄牙语": "pt-BR", "老挝语": "lo", "格鲁吉亚语": "ka", "南非荷兰语": "af", "尼泊尔语": "ne",
    "白俄罗斯语": "be", "马拉雅拉姆语": "ml", "卢森堡语": "lb", "阿尔巴尼亚语": "sq", "泰米尔语": "ta", "僧伽罗语": "si", "泰卢固语": "te", "爱沙尼亚语": "et",
    "宿务语": "ceb", "波斯尼亚语": "bs", "缅甸语": "my", "亚美尼亚语": "hy", "拉丁语": "la", "冰岛语": "is", "马耳他语": "mt", "斯瓦希里语": "sw",
    "哈萨克语": "kk", "塞舌尔克里奥尔语": "crs", "乌克兰语": "uk", "日语": "ja-JP", "韩语": "ko-KR", "印尼语": "id-ID",
}
DIALECTS = ["粤语", "广东话", "东北话", "河南话", "湖南话", "陕西话", "山东话", "四川话", "安徽话", "闽南话", "上海话", "重庆话", "云南话", "宁波话", "甘肃话"]


def fetch(cache):
    for name, url in SOURCES.items():
        path = os.path.join(cache, name)
        if not os.path.exists(path):
            print("downloading", url)
            urllib.request.urlretrieve(url, path)


def clean(s):
    s = re.sub(r"<[^>]+>", " ", s)
    s = s.replace("\\_", "_").replace("\\~", "~").replace("**", "")
    return re.sub(r"\s+", " ", html.unescape(s)).strip()


def age_group(text):
    t = text or ""
    if "60" in t and "以上" in t:
        return "senior"
    nums = [int(n) for n in re.findall(r"\d+", t)]
    if not nums:
        return ""
    lo, hi = min(nums), max(nums)
    mid = (lo + hi) / 2
    if hi <= 12:
        return "child"
    if mid < 17:
        return "teen" if lo >= 10 else "child"
    if mid < 30:
        return "young"
    if mid < 42:
        return "adult"
    if mid < 58:
        return "middle"
    return "senior"


def guess_gender(text):
    if re.search(r"女|萝莉|妃|阿姨|姨", text):
        return "female"
    if re.search(r"男|爷|叔|伯|哥|僧|诗仙|猴", text):
        return "male"
    return "unknown"


def language_of(langs, voice_lang=""):
    """Primary language code used by the library filter and casting."""
    t = langs or voice_lang
    if "普通话" in t or t.strip() in ("中文", "中文、英文"):
        return "zh-CN"
    if "粤语" in t or "广东话" in t:
        return "zh-HK"
    if any(d in t for d in DIALECTS):
        return "zh-dialect"
    if "英式" in t:
        return "en-GB"
    if "美式" in t or "英文" in t or "英语" in t:
        return "en-US"
    for k, v in LANG_CODES.items():
        if k in t:
            return v
    return "zh-CN"


def parse_cosy(path):
    out = []
    model = None
    header = None
    for line in open(path, encoding="utf-8"):
        m = re.match(r"^## (cosyvoice-[\w.-]+)", line)
        if m:
            model, header = m.group(1), None
            continue
        if not model or not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if "音色参数" in cells:
            header = cells
            continue
        if header is None or set(cells[0]) <= set("- "):
            continue
        row = dict(zip(header, cells))
        vid = clean(row.get("音色参数", "")).strip("`")
        if not vid:
            continue
        sample = re.search(r'src="([^"]+)"', row.get("试听", ""))
        langs = clean(row.get("语言", ""))
        traits = clean(row.get("特征", ""))
        age = clean(row.get("年龄", ""))
        out.append(dict(
            model=model, id=vid, name=clean(row.get("音色名称", "")), gender=guess_gender(traits), age=age_group(age) or age_group_from_traits(traits),
            language=language_of(langs), langs=langs, traits=traits, scene=clean(row.get("场景", "")), ssml=clean(row.get("SSML", "")) == "支持",
            instruct=clean(row.get("Instruct", "")) == "支持", sample=sample.group(1) if sample else "", preview="", base=False,
        ))
    return out


def age_group_from_traits(t):
    if re.search(r"童|萝莉|泡泡|稚气|懵懂|乖乖", t):
        return "child"
    if re.search(r"少年|学生", t):
        return "teen"
    if re.search(r"爷|老|岁月|阿姨", t):
        return "senior"
    if re.search(r"大叔|中年|沉稳|成熟|权威|稳重", t):
        return "adult"
    return "young"


def split_rows(block):
    return re.findall(r"<tr>(.*?)</tr>", block, re.S)


def cells_of(row):
    return re.findall(r"<td[^>]*>(.*?)</td>", row, re.S)


def parse_qwen(path):
    text = open(path, encoding="utf-8").read()
    text = text.split("## 基础音色")[0]
    out = []
    sections = re.split(r"^### ", text, flags=re.M)[1:]
    for sec in sections:
        model = sec.split()[0]
        # split into tabs (3.1 models) or treat the whole section as one table (3.0 models)
        tabs = re.split(r'<Tab title="(旗舰音色|多语种与方言音色|精品中文音色|精品英文音色|其他系统音色|小语种音色)">', sec)
        chunks = [("", tabs[0])] + [(tabs[i], tabs[i + 1]) for i in range(1, len(tabs) - 1, 2)]
        scene_carry = ""
        for tab, chunk in chunks:
            if "<tbody>" not in chunk:
                continue
            body = chunk.split("<tbody>", 1)[1]
            for row in split_rows(body):
                cells = cells_of(row)
                if not cells:
                    continue
                raw = row
                vid = re.search(r"voice\s*参数\**\s*[:：]\s*(?:</strong>\s*[:：]?)?\s*(?:<code>|`)?([\w.\-\\]+)", raw)
                if not vid:
                    vid = re.search(r"voice参数</strong>[:：]\s*([\w.\-\\]+)", raw)
                if not vid:
                    continue
                vid = vid.group(1).replace("\\_", "_").replace("\\", "")
                name = re.search(r"(?:\*\*(?:音色)?名称\*\*\s*[:：]|<strong>名称</strong>[:：])\s*([^\n<*]+)", raw)
                name = clean(name.group(1)) if name else vid
                name = re.sub(r"_v3\.\d$", "", name)
                feats = clean(" ".join(cells))
                ssml = "SSML: 支持" in feats
                instruct = "指令控制: 支持" in feats
                sample = re.search(r'src="([^"]+)"', raw)
                plain = [clean(c) for c in cells]
                gender, traits, scene, langs, age = "unknown", "", "", "", ""
                if model.startswith("qwen-audio-3.0"):
                    # rowSpan scene cell appears only on the first row of a group
                    if len(cells) == 4:
                        scene_carry = plain[0]
                        info = plain[1]
                    else:
                        info = plain[0]
                    scene = scene_carry
                    g = re.search(r"性别\s*[:：]\s*(\S+)", info)
                    gender = {"女": "female", "男": "male"}.get(g.group(1)[:1], "unknown") if g else "unknown"
                    t = re.search(r"特质\s*[:：]\s*(\S+)", info)
                    traits = t.group(1) if t else ""
                    a = re.search(r"年龄\s*[:：]\s*(\d+)", info)
                    age = age_group(a.group(1)) if a else age_group_from_traits(traits)
                    l = re.search(r"语言\s*[:：]\s*(.+?)$", info)
                    langs = l.group(1) if l else "中文（普通话）、英文"
                else:
                    gtext = next((p for p in plain[1:3] if p in ("女", "男", "儿童")), "")
                    gender = {"女": "female", "男": "male"}.get(gtext, "unknown")
                    if tab == "小语种音色":
                        langs = plain[1]
                        gender = {"女": "female", "男": "male"}.get(plain[2], "unknown")
                    elif tab == "精品英文音色":
                        langs = plain[2]  # accent
                        traits = plain[2]
                    elif len(plain) >= 6:
                        traits, scene = plain[2], plain[3]
                        langs = "中文（普通话）、英文" if "Bella" in name else "中文（普通话）"
                    elif tab == "多语种与方言音色":
                        langs = "中文（普通话）、英文、上海话、广东话、东北话、重庆话、陕西话、云南话、宁波话、甘肃话、日语、韩语、法语、德语、葡萄牙语、意大利语、越南语、印尼语"
                    else:
                        langs = "中文（普通话）、英文"
                    age = "child" if gtext == "儿童" or re.search(r"童", traits + scene) else age_group_from_traits(traits)
                    if gtext == "儿童":
                        gender = "female" if re.search(r"女", traits) else "unknown"
                out.append(dict(model=model, id=vid, name=name, gender=gender, age=age, language=language_of(langs), langs=langs, traits=traits,
                                scene=scene or tab.replace("音色", ""), ssml=ssml, instruct=instruct, sample=sample.group(1) if sample else "", preview="", base=False))
    return out


def xlsx_rows(path):
    ns = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
    z = zipfile.ZipFile(path)
    ss = ["".join(t.itertext()) for t in ET.fromstring(z.read("xl/sharedStrings.xml")).findall("m:si", ns)]
    rows = []
    for r in ET.fromstring(z.read("xl/worksheets/sheet1.xml")).findall(".//m:row", ns):
        vals = []
        for c in r.findall("m:c", ns):
            v = c.find("m:v", ns)
            val = v.text if v is not None else "".join(c.itertext())
            if c.get("t") == "s":
                val = ss[int(val)]
            vals.append(val or "")
        rows.append(vals)
    return rows


def parse_base(path, model):
    rows = xlsx_rows(path)
    head = rows[0]
    out = []
    for r in rows[1:]:
        row = dict(zip(head, r + [""] * (len(head) - len(r))))
        vid = row["voice参数"].strip()
        if not vid:
            continue
        lang = row.get("语种", "中文")
        out.append(dict(
            model=model, id=vid, name=row["名称"].strip(), gender={"女": "female", "男": "male"}.get(row["性别"].strip(), "unknown"),
            age=age_group(row["年龄"]) or "young", language="en-US" if lang == "英文" else "zh-CN", langs="英文" if lang == "英文" else "中文（普通话）、英文",
            traits=row["特质"].strip(), scene=row["适用场景"].strip(), ssml=True, instruct=True, sample="", preview=row.get("预览音频文件名", "").strip(), base=True,
        ))
    return out


def go_str(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main():
    cache = sys.argv[1] if len(sys.argv) > 1 else tempfile.mkdtemp(prefix="qwen-voices-")
    os.makedirs(cache, exist_ok=True)
    fetch(cache)
    voices = parse_cosy(os.path.join(cache, "cosy_voices.md")) + parse_qwen(os.path.join(cache, "qwen_voices.md"))
    voices += parse_base(os.path.join(cache, "base_plus.xlsx"), "qwen-audio-3.0-tts-plus")
    voices += parse_base(os.path.join(cache, "base_flash.xlsx"), "qwen-audio-3.0-tts-flash")
    seen = set()
    uniq = []
    for v in voices:
        k = (v["model"], v["id"])
        if k in seen:
            continue
        seen.add(k)
        uniq.append(v)
    lines = [
        "// Code generated by scripts/gen_qwen_voices.py from the official Qianwen voice lists. DO NOT EDIT.",
        "",
        "package tts",
        "",
        "// qwenVoices lists every built-in voice per model. Voices are bound to a model and cannot be mixed across models.",
        "var qwenVoices = []QwenVoice{",
    ]
    for v in uniq:
        lines.append("\t{" + ", ".join([
            go_str(v["model"]), go_str(v["id"]), go_str(v["name"]), go_str(v["gender"]), go_str(v["age"]), go_str(v["language"]), go_str(v["langs"]),
            go_str(v["traits"]), go_str(v["scene"]), "true" if v["ssml"] else "false", "true" if v["instruct"] else "false",
            go_str(v["sample"]), go_str(v["preview"]), "true" if v["base"] else "false",
        ]) + "},")
    lines.append("}")
    open(OUT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
    by = {}
    for v in uniq:
        by[v["model"]] = by.get(v["model"], 0) + 1
    print("wrote", OUT, len(uniq), "voices", by)


if __name__ == "__main__":
    main()

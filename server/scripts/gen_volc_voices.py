"""Generates internal/ai/tts/volc_voices_gen.go: the voices Volcengine's audio generation model (Seed Audio)
accepts as `speaker`, i.e. the "豆包语音合成模型2.0" voices.

Source: https://docs.volcengine.com/docs/zh/doubao-voice/tone-list-3.md (the first two tables: the 2.0 voices and
the 2.0 foreign-language voices). Refresh the saved table, then the Go file:

    python3 scripts/gen_volc_voices.py --from-md tone-list-3.md
    python3 scripts/gen_volc_voices.py

The table only names a voice and its scenes; gender comes from the voice id, the age group and language from the
name and the language column, so casting and filters work the same way as for other platforms.
"""
import os, re, sys

HERE = os.path.dirname(os.path.abspath(__file__))
TSV = os.path.join(HERE, "volc_seed_audio_voices.tsv")  # next to the script: data/ is not committed
OUT = os.path.join(HERE, "..", "internal", "ai", "tts", "volc_voices_gen.go")

# Voices that imitate a real person or a well-known character are left out of the library.
SKIP = {"en_male_brad_pitt_p1_uranus_bigtts", "ICL_uranus_en_male_kevin_mccallister_tob", "zh_female_peiqi_uranus_bigtts", "en_male_michael-mouse_uranus_bigtts"}

LANG = [  # language column prefix -> code
    ("美式英语", "en-US"), ("英式英语", "en-GB"), ("澳洲英语", "en-AU"), ("日语", "ja-JP"), ("韩语", "ko-KR"), ("印尼语", "id-ID"),
    ("巴西葡萄牙语", "pt-BR"), ("墨西哥西语", "es-MX"), ("西班牙语", "es-ES"), ("泰语", "th"), ("越南语", "vi"), ("俄语", "ru"),
    ("马来语", "ms"), ("法语", "fr"), ("意大利语", "it"), ("菲律宾语", "fil"), ("阿拉伯语", "ar"), ("德语", "de"),
]
AGE = [  # name keywords -> age group (first match wins)
    (r"童|宝宝|萌娃|奶娃|小孩|女孩|男孩|小学|儿子|女儿|绘本", "child"),
    (r"少年|少女|学妹|学弟|同桌|中二|萌妹|学生|学霸|元气", "teen"),
    (r"大爷|奶奶|爷爷|老太|老头|老奶|老爷|外婆|姥姥", "senior"),
    (r"大叔|叔叔|阿姨|妈妈|爸爸|大婶|婆婆|中年|老板|总裁|帮主|老师|校长|教授", "middle"),
    (r"御姐|成熟|知性|沉稳|磁性|儒雅|大壹|解说|译制|主播|播音|旁白", "adult"),
]


def from_md(path):
    rows = []
    lines = open(path, encoding="utf-8-sig").read().splitlines()
    stop = next((i for i, l in enumerate(lines) if l.startswith("## ") and "端到端" in l), len(lines))
    for l in lines[:stop]:
        if not l.startswith("|") or l.startswith("|---") or "**" in l:
            continue
        c = [x.strip().replace("\\-", "-").replace("\\_", "_") for x in l.strip().strip("|").split("|")]
        if len(c) < 4 or not re.fullmatch(r"[A-Za-z][A-Za-z0-9_\-]+", c[2]):
            continue
        tags = c[-1] if len(c) == 6 else ""  # 2.0 table: last column is "特殊标签"
        scenes = ",".join(s.strip() for s in c[0].split(",") if s.strip() and s.strip() != "外语音色" and "S2S" not in s)
        rows.append((c[2], c[1].replace(" 2.0", "").strip(), scenes, c[3].replace("<br>", " ").strip(), tags.strip()))
    seen, out = set(), []
    for r in rows:
        if r[0] not in seen:
            seen.add(r[0])
            out.append(r)
    with open(TSV, "w", encoding="utf-8") as f:
        f.write("# voice_id\tname\tscenes\tlanguage\ttags  (from tone-list-3.md, 豆包语音合成模型2.0)\n")
        for r in out:
            f.write("\t".join(r) + "\n")
    print(f"wrote {len(out)} voices to {TSV}")


def go_str(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main():
    if len(sys.argv) == 3 and sys.argv[1] == "--from-md":
        from_md(sys.argv[2])
    entries = []
    for line in open(TSV, encoding="utf-8"):
        if line.startswith("#") or not line.strip():
            continue
        vid, name, scenes, lang_col, tags = (line.rstrip("\n").split("\t") + [""] * 5)[:5]
        if vid in SKIP:
            continue
        low = vid.lower()
        gender = "female" if "female" in low else "male" if "male" in low else "unknown"
        lang = "zh-CN"
        for prefix, code in LANG:
            if lang_col.startswith(prefix):
                lang = code
                break
        if lang == "zh-CN" and re.match(r"(en|ja|ko|es|mx|pt|id|vi|th|ms|fr|it|ru|ar|de|tl)_", vid):
            lang = {"mx": "es-MX", "tl": "fil", "en": "en-US", "ja": "ja-JP", "ko": "ko-KR", "id": "id-ID"}.get(vid.split("_")[0], vid.split("_")[0])
        age = "young"
        for pat, a in AGE:
            if re.search(pat, name):
                age = a
                break
        scene_list = [s.strip() for s in scenes.split(",") if s.strip() and not re.search(r"英语|语$|西语", s.strip())]
        tag_list = scene_list + [t.strip() for t in re.split(r"[,，]", tags) if t.strip()]
        multi = "30+" in lang_col or "方言" in lang_col
        if multi:
            tag_list.append("多语种")
        if "方言" in lang_col:
            tag_list.append("方言")
        narration = any(s in ("有声阅读",) for s in scene_list)
        crowd = lang == "zh-CN" and "通用场景" in scene_list
        recommended = narration or vid in ("zh_female_vv_uranus_bigtts", "zh_female_xiaohe_uranus_bigtts", "zh_male_m191_uranus_bigtts", "zh_male_taocheng_uranus_bigtts")
        desc = "、".join(scene_list) + "音色" if scene_list else "豆包 2.0 音色"
        entries.append((vid, name, gender, age, lang, tag_list, desc, recommended, crowd))
    with open(OUT, "w", encoding="utf-8") as f:
        f.write("// Code generated by scripts/gen_volc_voices.py from the official Volcengine voice list. DO NOT EDIT.\n\npackage tts\n\n")
        f.write("// volcVoices are the 豆包语音合成模型2.0 voices that Volcengine audio generation accepts as `speaker`.\n")
        f.write("var volcVoices = []VolcVoice{\n")
        for vid, name, gender, age, lang, tags, desc, rec, crowd in entries:
            tags_go = "[]string{" + ", ".join(go_str(t) for t in dict.fromkeys(tags)) + "}"
            f.write(f"\t{{{go_str(vid)}, {go_str(name)}, {go_str(gender)}, {go_str(age)}, {go_str(lang)}, {tags_go}, {go_str(desc)}, {str(rec).lower()}, {str(crowd).lower()}}},\n")
        f.write("}\n")
    print(f"wrote {len(entries)} voices to {OUT}")


if __name__ == "__main__":
    main()

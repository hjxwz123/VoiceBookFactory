"""Build the official background-music catalog (internal/core/music_catalog.json).

Only recordings that can be redistributed and used commercially without conditions are accepted:
CC0 or public domain, as stated per file by the source. Sources:
  * Wikimedia Commons: Free Music Archive imports (grouped by genre) and Musopen classical recordings.
    Commons keeps a machine-readable license for every file, plus an MP3 transcode we can download.
  * OpenGameArt: a hand-picked list of CC0 oriental-style pieces (rare elsewhere), license checked per page.
Pixabay and similar "free" sites are deliberately excluded: their terms forbid redistributing audio
files on a standalone basis, which is exactly what a platform music library does.

The catalog stores metadata only (title, artist, source page, license, download URL, category);
the server downloads the audio when an administrator imports the library.

usage: python3 scripts/build_music_catalog.py [--cache DIR] [--refresh]
"""
import argparse, json, os, re, subprocess, sys, time, urllib.parse, urllib.request, html
from collections import Counter, defaultdict

UA = "VoicebookAssetBot/1.0 (audiobook platform music library; https://github.com/; admin@example.com)"
API = "https://commons.wikimedia.org/w/api.php"
OUT = os.path.join(os.path.dirname(__file__), "..", "internal", "core", "music_catalog.json")

FMA = " from Free Music Archive"
SOURCE_CATS = {  # Commons category -> default category key when no keyword matches
    "Soundtrack music" + FMA: "cinematic", "Ambient music" + FMA: "ambient", "New-age music" + FMA: "warm",
    "Piano music" + FMA: "piano", "Chill-out music" + FMA: "lofi", "Downtempo music" + FMA: "lofi", "Lo-fi music" + FMA: "lofi",
    "Trip hop music" + FMA: "lofi", "Jazz music" + FMA: "jazz", "Big band/swing music" + FMA: "jazz", "Easy listening music" + FMA: "jazz",
    "Lounge music" + FMA: "jazz", "Celtic music" + FMA: "folk", "Asian music" + FMA: "oriental", "Contemporary classical music" + FMA: "cinematic", "Drone music" + FMA: "suspense", "Minimal music" + FMA: "ambient",
    "Chiptune music" + FMA: "comedy", "Classical music" + FMA: "classical", "Instrumental music" + FMA: "cinematic",
    "Folk music" + FMA: "folk", "Electroacoustic music" + FMA: "ambient", "IDM music" + FMA: "scifi",
    "Classical Sampler by Kevin MacLeod (album)": "classical", "Jazz Sampler by Kevin MacLeod (album)": "jazz",
    "Musopen": "classical",
}

# category key -> (Chinese label, tags used by the platform's BGM matching)
CATEGORIES = {
    "famous": ("经典名曲", ["名曲", "经典", "古典"]),
    "suspense": ("悬疑推理", ["悬疑", "紧张", "推理"]),
    "horror": ("恐怖惊悚", ["恐怖", "惊悚", "悬疑"]),
    "epic": ("史诗热血", ["史诗", "热血", "激昂"]),
    "adventure": ("冒险奇幻", ["冒险", "奇幻", "神秘"]),
    "scifi": ("科幻未来", ["科幻", "未来", "神秘"]),
    "romance": ("浪漫言情", ["浪漫", "言情", "温柔"]),
    "warm": ("温馨治愈", ["温馨", "治愈", "舒缓"]),
    "sad": ("伤感催泪", ["伤感", "抒情"]),
    "cheerful": ("轻快日常", ["轻快", "日常"]),
    "comedy": ("诙谐喜剧", ["喜剧", "诙谐", "轻快"]),
    "children": ("童话儿童", ["儿童", "童话", "轻快"]),
    "ambient": ("氛围空灵", ["氛围", "空灵", "舒缓"]),
    "sleep": ("助眠冥想", ["助眠", "冥想", "舒缓", "睡前"]),
    "piano": ("钢琴独奏", ["钢琴", "抒情", "舒缓"]),
    "classical": ("古典音乐", ["古典", "管弦"]),
    "jazz": ("爵士复古", ["爵士", "复古", "都市"]),
    "folk": ("民谣田园", ["民谣", "田园", "乡村"]),
    "oriental": ("古风东方", ["古风", "东方", "异域", "神秘"]),
    "lofi": ("都市轻松", ["都市", "轻松", "Lo-fi"]),
    "cinematic": ("影视配乐", ["影视", "配乐"]),
    "stinger": ("片头片尾", ["片头", "片尾", "转场"]),
}

KEYWORDS = [  # first match wins; matched against title (and description) in lower case
    ("horror", r"horror|haunt|ghost|creep|nightmare|terror|scary|zombie|\bevil\b|demon|curse|dread|sinister|macabre|\bgrave|blood|\bfear|phobia|witch|spooky|halloween|monster"),
    ("children", r"lullab|\bchild|\bkids?\b|\btoy|music ?box|fairy|\bbaby|bedtime|nursery|\bcute\b|teddy"),
    ("comedy", r"funny|comic|comedy|silly|quirky|goofy|clown|circus|cartoon|wacky|monkey|banana|cheeky|sneaky|mischie"),
    ("suspense", r"myster|suspense|tension|\btense|secret|shadow|detective|investig|crime|noir|stealth|enigma|\bclue|conspir|thrill|\bchas(?:e|ing)|\bspy\b|riddle|unknown|uneasy|danger|anxi|nervous|obscur|\bdark|alley|midnight|swamp|lurk|footstep|\bfog\b|\btrap\b|escape|\btruth|interrogat|suspicio|serial killer|threat"),
    ("epic", r"\bepic|battle|\bwar\b|warrior|hero|legend|victor|triumph|glory|\bmarch|fight|conquer|empire|titan|brave|valor|army|\bsiege|crusade|rebel|uprising|anthem|overture|\baction|\bboss\b|ultimate|decisive|power|strike|showdown"),
    ("scifi", r"\bspace|galax|cosmic|\bstar|planet|orbit|nebula|future|robot|cyber|android|alien|sci.?fi|synthwave|\bneon|machine|digital|quantum|mars|moon|satellite|astro"),
    ("sad", r"\bsad|sorrow|tear|\bcry|lament|melanchol|grief|lonel|alone|\bloss\b|\blost\b|farewell|goodbye|memor|regret|\brain|elegy|mourn|broken|heartbreak|nostalg|requiem|funeral|lacrimosa|tragic|despair|solitude"),
    ("romance", r"\blove|romance|romantic|heart|kiss|tender|darling|valentine|embrace|beloved|liebes|amour|passion|serenade"),
    ("adventure", r"adventur|journey|quest|voyage|explor|discover|fantasy|magic|kingdom|dragon|castle|forest|\bmyth|wonder|travel|\bsail|island|treasure|wander|mountain|montagne|realm|enchant|horizon|pirate|river|desert|d[ée]sert|citadel|village|bridge|ponts"),
    ("warm", r"\bwarm|gentle|morning|sunrise|sunshine|\bhome|\bpeace|\bcalm|seren|\bhope|\bsoft|cozy|comfort|spring|breeze|smile|garden|tranquil|relax|healing|meditat|\bdawn|\btea\b|quiet|together|friend|delicate|tournesol|sunflower|lake|\blac\b"),
    ("cheerful", r"happy|cheer|bright|sunny|\bjoy|\bfun\b|upbeat|playful|bounc|\bdance|party|summer|\bwalk|picnic|holiday|carefree|sunday|saturday|stroll|beach|surf|skate|bicycle|ice cream|yippee|\bfair\b|alive|groovy|\bcool\b"),
    ("oriental", r"orient|asian|china|chinese|japan|koto|zen|bamboo|shanghai|tokyo|kyoto|eastern|pentaton|samurai|dynasty|lotus|jade"),
]
STINGER = r"\bintro\b|\boutro\b|sting|jingle|fanfare|transition|\blogo\b|bumper|interlude"
PIANO_CLASSICAL = r"piano|étude|etude|nocturne|prelude|prélude|mazurka|gymnop|ballade|impromptu|berceuse|bagatelle|arabesque|clair de lune|moonlight|für elise|fur elise|reverie|rêverie|consolation|waltz|valse|sonata"
# categories that mean sung or spoken content: always rejected
VOCAL_CATS = re.compile(r"vocal|singer|hip hop|rap music|choral|spoken|gospel|noise music|experimental pop|opera|songs|lieder|\baria|\bmass\b|cantata|oratorio|hymn|a cappella|chant|anthem", re.I)
# genres that often (not always) have vocals: kept only when the file is also filed under an instrumental genre
MAYBE_VOCAL = re.compile(r"rock music|pop music|punk|metal|synth-pop|singer", re.I)
INSTRUMENTAL = re.compile(r"instrumental|soundtrack|ambient|lo-fi|chill-out|downtempo|new-age|chiptune|piano|classical|jazz|celtic|minimal|drone", re.I)
PARTS = re.compile(r"^(?:bassoon|celli|cello|clarinet|contra ?bass?on|db|decca|violins?|violas?|flutes?|oboes?|horns?|trumpets?|timpani|harp|tuba|trombones?)\b[\w ]*\d|backing track|^(?:1st|2nd|3rd) (?:violin|viola|cello|flute|horn)|\b(?:violin|viola|cello|bass|flute|oboe|clarinet|horn) [a-h] \d|\bpart [a-h]\b|\bstem\b|\bclick track", re.I)
BLOCK = re.compile(r"acab|patriarch|tupac|sexy|fuck|shit|\bass\b|pas dans le cul|\bnazi|suicid|drug|cocaine|\bsex\b", re.I)
HISTORIC = re.compile(r"\((?:c\. ?)?1[89]\d\d\)|\(c\.? ?\d{4}\)")
VOCAL_TITLE = re.compile(r"\(vocal|feat\.|\bfeat |ft\.|vocals|\baria\b|ave maria|lied\b|lieder|song of|choir|chorus|cantata|kyrie|gloria|agnus|credo|sanctus|magnificat|stabat|requiem|mass in|\bmass no|opera|recitative|soprano|tenor|baritone", re.I)

OGA_PAGES = {  # hand-picked CC0 pieces on OpenGameArt (license re-checked, chiptune skipped when building)
    **{slug: "oriental" for slug in (
        "asianoriental1", "asianoriental2", "orient-peace-valley", "koto-booth", "lapsy-koto",
        "kingdom-of-a-million-elephants-under-a-white-parasol", "pentatonic-oscillation", "mysterious-sparrow-lake",
        "prepare-your-swords", "oriental", "japaori", "jaoan", "ch-ay-na", "sci-fi-adventure-eastern-quiet-piano-loop",
        "desert-calmness-and-fighting-orchestral-141", "frozen-desert-112", "kamogawa-nightbreak",
        "oriental-somber", "shangri-river", "orient-tune-again", "hot-springs-town", "koto-short", "zen-theme", "ninja-theme",
        "rpg-orient-17", "oriental8", "orien2", "the-last-samurai", "tyhosi-sparrow")},
    **{slug: "children" for slug in (
        "childrens-march-theme", "4-music-box-tracks", "happy-lullaby-song17", "lullaby-0", "story-time", "toy-soldiers",
        "puppy-playing-in-the-garden", "snow-globe", "carnival-rides", "ice-cream-truck-theme", "napping-on-a-cloud", "good-morning")},
    **{slug: "comedy" for slug in (
        "comedy-funny-background-song", "wacky-workings", "wacky-wobblings", "goofy-attitude", "stumble-around", "banana-track",
        "the-fridge-is-hungry", "bashing-the-bishop", "catsong", "the-war-over-a-melon-field-themes-and-jingles")},
    **{slug: "sleep" for slug in (
        "rizys-sleep-music", "sleep-hypnosis", "nap-time", "calm-ambient-1-synthwave-4k", "calm-ambient-2-synthwave-15k",
        "calm-ambient-3-lifewave-2k", "ambient-relaxing-loop", "first-light-particles-%E2%80%93-cc0-atmospheric-pianoambient-track", "the-budding-of-consciousness-%E2%80%93-cc0-ambient-minimalist-theme-yoiyami-blue-series-%E2%80%93-no4",
        "somnium", "restful-meadow", "among-mountains", "contemplation-0", "moonlit-lullaby-for-the-lost", "lullaby-stories",
        "november-snow", "calm-piano-1-vaporware")},
    **{slug: "piano" for slug in ("emotional-piano-loop", "sadness", "love-song-instrumental", "at-the-end-of-hope", "snowfall", "wintery-loop", "jrpg-piano")},
}

# Well-known public-domain pieces (modern recordings: Musopen, US military bands, CC0 performers), shown
# with their Chinese titles. (Commons title, Chinese title, artist, extra mood tags)
FAMOUS = [
    ("Canon (2004) - Strolling Strings - United States Air Force Band.mp3", "卡农（帕赫贝尔）", "美国空军乐团", ["温馨", "治愈", "婚礼"]),
    ("FurElise.ogg", "致爱丽丝（贝多芬）", "贝多芬", ["钢琴", "抒情"]),
    ("Ludwig van Beethoven - sonata no. 14 in c sharp minor 'moonlight', op. 27 no. 2 - i. adagio sostenuto.ogg", "月光奏鸣曲 第一乐章（贝多芬）", "Musopen", ["钢琴", "伤感", "夜晚"]),
    ("Clair de lune (Claude Debussy) Suite bergamasque.ogg", "月光（德彪西）", "德彪西", ["钢琴", "浪漫", "夜晚"]),
    ("Robert Schumann - scenes from childhood, op. 15 - vii. dreaming.ogg", "梦幻曲（舒曼）", "Musopen", ["钢琴", "温柔", "回忆"]),
    ("Air - Air Force Strings - United States Air Force Band.mp3", "G 弦上的咏叹调（巴赫）", "美国空军乐团", ["舒缓", "抒情"]),
    ("20091104 Alisa Weilerstein and Jason Yoder - Saint Saëns' The Swan.ogg", "天鹅（圣桑）", "Alisa Weilerstein", ["大提琴", "抒情", "浪漫"]),
    ("Chopin Nocturne No. 2 in E Flat Major, Op. 9.ogg", "降 E 大调夜曲（肖邦）", "肖邦", ["钢琴", "浪漫", "夜晚"]),
    ("Gymnopedie No. 1..ogg", "吉诺佩蒂 第一号（萨蒂）", "萨蒂", ["钢琴", "空灵", "情感电台"]),
    ("Gymnopédie no.3.ogg", "吉诺佩蒂 第三号（萨蒂）", "萨蒂", ["钢琴", "空灵", "情感电台"]),
    ("Hungarian Dance No. 4 - Strolling Strings - United States Air Force Band.mp3", "匈牙利舞曲 第四号（勃拉姆斯）", "美国空军乐团", ["激昂", "异域"]),
    ("Grieg - Peer Gynt Suite No. 1, Op. 46 - IV. In the Hall of the Mountain King (Musopen Symphony).flac", "山魔王的大厅（格里格）", "Musopen", ["悬疑", "紧张", "诡异"]),
    ("Grieg - Peer Gynt Suite No. 1, Op. 46 - I. Morning Mood (Musopen Symphony).flac", "晨景（格里格）", "Musopen", ["清晨", "温馨", "田园"]),
    ("Grieg - Peer Gynt Suite No. 1, Op. 46 - II. The Death of Aase (Musopen Symphony).flac", "奥丝之死（格里格）", "Musopen", ["伤感", "悲剧"]),
    ("Saint-Saëns - Danse macabre (Fourestier, 1953).flac", "骷髅之舞（圣桑）", "Fourestier", ["恐怖", "诡异", "万圣节"]),
    ("Rondo Alla Turka.ogg", "土耳其进行曲（莫扎特）", "莫扎特", ["轻快", "诙谐", "钢琴"]),
    ("Flight of the Bumblebee - Strolling Strings - United States Air Force Band.mp3", "野蜂飞舞（里姆斯基-科萨科夫）", "美国空军乐团", ["紧张", "诙谐", "忙乱"]),
    ("Free Tim - Vivaldos Spring from the Four Seasons-Allegro.ogg", "四季·春（维瓦尔第）", "Free Tim", ["轻快", "春天", "明媚"]),
    ('"An der schönen, blauen Donau", performed by the US Marine Band.mp3', "蓝色多瑙河（小约翰·施特劳斯）", "美国海军陆战队乐团", ["圆舞曲", "优雅", "舞会"]),
    ("Goin' Home - Concert Band - United States Air Force Band.mp3", "念故乡（德沃夏克）", "美国空军乐团", ["思乡", "抒情", "温馨"]),
    ("USMC Band - Radetzky March.mp3", "拉德茨基进行曲（老约翰·施特劳斯）", "美国海军陆战队乐团", ["欢快", "庆典", "进行曲"]),
    ("Toccata et Fugue BWV565.ogg", "d 小调托卡塔与赋格（巴赫）", "巴赫", ["恐怖", "管风琴", "古堡"]),
    ("Tchaikovsky - Swan Lake Op.20 - Act II Pt.1.ogg", "天鹅湖（柴可夫斯基）", "柴可夫斯基", ["浪漫", "伤感", "芭蕾"]),
    ("A Midsummer Night's Dream Op. 61 Wedding March (Mendelssohn) European Archive.ogg", "婚礼进行曲（门德尔松）", "European Archive", ["婚礼", "喜庆", "浪漫"]),
    ("Gioachino Rossini, William Tell Overture (military band version, 2000).ogg", "威廉·退尔序曲（罗西尼）", "军乐团", ["激昂", "冒险", "追逐"]),
    ("Richard Wagner - Ride of the Valkyries.ogg", "女武神的骑行（瓦格纳）", "瓦格纳", ["史诗", "战争", "激昂"]),
    ("Pyotr Ilyich Tchaikovsky Valzer dei fiori.ogg", "花之圆舞曲（柴可夫斯基）", "柴可夫斯基", ["童话", "优雅", "浪漫"]),
    ("Free Tim - Schuberts Ave Maria.ogg", "圣母颂（舒伯特）", "Free Tim", ["圣洁", "抒情", "舒缓"]),
    ("Menuet bwv anh 114 115 Anna Magdalena.wav", "G 大调小步舞曲（巴赫）", "巴赫", ["童年", "钢琴", "轻快"]),
    ("Fantaisie Impromptu Op. 66.mp3", "幻想即兴曲（肖邦）", "肖邦", ["钢琴", "激情", "紧张"]),
    ("Pomp and Circumstance, Op. 39 - No. 1 - United States Army Band.mp3", "威风凛凛进行曲（埃尔加）", "美国陆军乐团", ["庄严", "典礼", "毕业"]),
    ("Chopin - Waltz No. 6 in D-flat major, Op. 64, No. 1 'Minute' (Olga Gurevich).flac", "小狗圆舞曲（肖邦）", "Olga Gurevich", ["轻快", "俏皮", "钢琴"]),
]


def get(url, params=None, tries=4):
    if params:
        url += "?" + urllib.parse.urlencode(params)
    for i in range(tries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": UA})
            with urllib.request.urlopen(req, timeout=60) as r:
                return r.read()
        except Exception as e:
            if i == tries - 1:
                raise
            time.sleep(2 * (i + 1))


def strip(s):
    return html.unescape(re.sub(r"<[^>]+>", " ", s or "")).replace("\n", " ").strip()


def crawl_category(cat):
    files, cont = [], {}
    while True:
        params = {"action": "query", "format": "json", "maxlag": "5", "generator": "categorymembers", "gcmtitle": "Category:" + cat,
                  "gcmtype": "file", "gcmlimit": "50", "prop": "videoinfo|categories", "viprop": "url|size|mime|derivatives|extmetadata",
                  "viextmetadatafilter": "LicenseShortName|LicenseUrl|Artist|ImageDescription|ObjectName|UsageTerms", "clshow": "!hidden", "cllimit": "max", **cont}
        d = json.loads(get(API, params))
        for p in d.get("query", {}).get("pages", {}).values():
            v = (p.get("videoinfo") or [{}])[0]
            m = v.get("extmetadata", {})
            mp3 = next((x["src"] for x in v.get("derivatives", []) if x.get("transcodekey") == "mp3"), "")
            files.append({
                "pageid": p["pageid"], "title": p["title"], "duration": v.get("duration") or 0, "mime": v.get("mime", ""), "size": v.get("size", 0),
                "url": v.get("url", ""), "mp3": mp3, "page": v.get("descriptionurl", ""),
                "license": m.get("LicenseShortName", {}).get("value", ""), "licenseUrl": m.get("LicenseUrl", {}).get("value", ""),
                "artist": strip(m.get("Artist", {}).get("value", "")), "description": strip(m.get("ImageDescription", {}).get("value", ""))[:400],
                "name": strip(m.get("ObjectName", {}).get("value", "")), "categories": [c["title"][9:] for c in p.get("categories", [])], "source": cat,
            })
        if "continue" not in d:
            return files
        cont = d["continue"]
        time.sleep(0.4)


def free_license(lic):
    l = lic.lower()
    if any(x in l for x in ("by-sa", "cc by", "cc-by", "nc", "nd", "gfdl", "attribution")):
        return False
    return "cc0" in l or "public domain" in l or l.startswith("pd")


def clean_title(f):
    t = f["name"] or f["title"][5:]
    t = re.sub(r"\.(ogg|oga|opus|flac|wav|mp3|webm)$", "", t, flags=re.I)
    t = re.sub(r"\s*\((?:Musopen[^)]*)\)", "", t)
    t = re.sub(r"\s+-\s+\d{1,2}\s+-\s+", " - ", t)  # "Artist - 03 - Title" -> "Artist - Title"
    return re.sub(r"\s+", " ", t).strip(" -")


def artist_of(f, title):
    a = f["artist"]
    a = re.sub(r"\s*(Music by|Composer:|Performer:).*", "", a).strip() if len(a) > 60 else a
    if not a or len(a) > 60:
        a = title.split(" - ")[0] if " - " in title else ""
    return a[:60]


STYLE_CATS = {  # source categories that define the style regardless of the title
    "Chill-out music" + FMA: "lofi", "Downtempo music" + FMA: "lofi", "Lo-fi music" + FMA: "lofi", "Trip hop music" + FMA: "lofi",
    "Jazz music" + FMA: "jazz", "Big band/swing music" + FMA: "jazz", "Easy listening music" + FMA: "jazz", "Lounge music" + FMA: "jazz",
    "Jazz Sampler by Kevin MacLeod (album)": "jazz", "Celtic music" + FMA: "folk",
    "Asian music" + FMA: "oriental", "Asian music": "oriental", "Piano music" + FMA: "piano",
}


def mood_of(text):
    for key, rx in KEYWORDS:
        if re.search(rx, text):
            return key
    return ""


def classify(f, title):
    """Returns (primary category, extra mood category or "")."""
    if f.get("forced"):
        return f["forced"], ""
    text = (title + " " + f["description"][:160]).lower()
    cats = " | ".join(f["categories"]).lower()
    d = f["duration"] or 0
    if (d and d <= 40) or (re.search(STINGER, title.lower()) and d and d <= 75):
        return "stinger", mood_of(text)
    musopen = f["source"] == "Musopen" or "musopen" in cats or "musopen" in text
    if musopen or f["source"].startswith("Classical"):
        if re.search(r"funeral|lacrimosa|lament|elegy|élégie|adagio|largo|lento|grave\b|pathétique|pathetique|tragic|\bin [a-g](?:[ -](?:flat|sharp))? minor", text) and not re.search(r"allegro|presto|vivace", text):
            return "sad", ""
        if re.search(r"liebestraum|romance|romanze|amour|love|serenade|nocturne", text):
            return "romance", ""
        if re.search(r"march|marche|polonaise|overture|ouverture|heroic|héroïque|eroica|symphony no\. ?[59]|finale|allegro con fuoco", text):
            return "epic", ""
        if re.search(PIANO_CLASSICAL, text) or "piano" in cats:
            return "piano", ""
        return "classical", ""
    mood = mood_of(text)
    chiptune = "chiptune" in cats
    for c in f["categories"]:
        if c in STYLE_CATS:
            return STYLE_CATS[c], mood
    if chiptune:  # 8-bit timbres only suit light, playful scenes
        return ("children" if mood == "children" else "comedy"), ""
    if mood:
        return mood, ""
    for c in f["categories"]:
        if c in SOURCE_CATS:
            return SOURCE_CATS[c], ""
    return SOURCE_CATS.get(f["source"], "cinematic"), ""


def oga_entries(cache):
    path = os.path.join(cache, "oga.json")
    known = json.load(open(path)) if os.path.exists(path) else {}
    out = []
    for slug, cat in OGA_PAGES.items():
        if slug in known:  # pages already checked (None = rejected)
            if known[slug]:
                out.append({**known[slug], "forced": cat})
            continue
        known[slug] = None
        url = "https://opengameart.org/content/" + slug
        try:
            page = get(url).decode("utf-8", "replace")
        except Exception as e:
            print("  skip", slug, e)
            continue
        lic_block = re.search(r"License\(s\):(.*?)</div>\s*</div>", page, re.S)
        lic = strip(lic_block.group(1)) if lic_block else ""
        if "CC0" not in lic:
            print("  skip (not CC0):", slug, lic[:60])
            continue
        title = strip((re.search(r'<div class="field-item even" property="dc:title"><h2>(.*?)</h2>', page) or re.search(r"<title>(.*?)\s*\|", page)).group(1))
        author = strip((re.search(r'Author:.*?<a [^>]*>(.*?)</a>', page, re.S) or re.search(r'Submitted by\s*<[^>]+>(.*?)<', page, re.S) or re.search("x(x)", "xx")).group(1))
        files = re.findall(r'https://opengameart\.org/sites/default/files/[^"\s]+\.(?:mp3|ogg|wav)', page)
        files = sorted(set(files), key=lambda u: (not u.endswith(".mp3"), not u.endswith(".ogg"), u))
        if not files:
            continue
        chip = re.search(r"c64|chip|8.?bit|\bnes\b|\bsid\b|gameboy|retro", (title + " " + page[:20000]).lower())
        if chip:
            print("  skip (chiptune):", slug)
            continue
        f = files[0]
        dur = 0.0
        try:
            dur = float(subprocess.run(["ffprobe", "-v", "error", "-user_agent", UA, "-show_entries", "format=duration", "-of", "csv=p=0", f], capture_output=True, text=True, timeout=60).stdout.strip() or 0)
        except Exception:
            pass
        e = {"pageid": "oga:" + slug, "title": title, "duration": dur, "url": f, "mp3": f if f.endswith(".mp3") else "", "page": url,
             "license": "CC0", "licenseUrl": "https://creativecommons.org/publicdomain/zero/1.0/", "artist": author, "description": "",
             "name": title, "categories": [], "source": "OpenGameArt"}
        known[slug] = e
        out.append({**e, "forced": cat})
        time.sleep(0.5)
    json.dump(known, open(path, "w"), ensure_ascii=False)
    return out


def famous_entries():
    """Commons metadata for the hand-picked well-known pieces (titles shown in Chinese)."""
    out = []
    for i in range(0, len(FAMOUS), 20):
        batch = FAMOUS[i:i + 20]
        d = json.loads(get(API, {"action": "query", "format": "json", "prop": "videoinfo", "viprop": "url|size|mime|derivatives|extmetadata",
                                 "viextmetadatafilter": "LicenseShortName|LicenseUrl|Artist", "titles": "|".join("File:" + t for t, *_ in batch)}))
        pages = {p["title"]: p for p in d.get("query", {}).get("pages", {}).values()}
        norm = {n["from"]: n["to"] for n in d.get("query", {}).get("normalized", [])}
        for title, zh, artist, tags in batch:
            p = pages.get(norm.get("File:" + title, "File:" + title))
            if not p or "videoinfo" not in p:
                print("  missing famous piece:", title)
                continue
            v = p["videoinfo"][0]
            m = v.get("extmetadata", {})
            mp3 = next((x["src"] for x in v.get("derivatives", []) if x.get("transcodekey") == "mp3"), "")
            out.append({"pageid": p["pageid"], "title": p["title"], "duration": v.get("duration") or 0, "mime": v.get("mime", ""), "size": v.get("size", 0),
                        "url": v.get("url", ""), "mp3": mp3 or (v.get("url", "") if v.get("url", "").endswith(".mp3") else ""), "page": v.get("descriptionurl", ""),
                        "license": m.get("LicenseShortName", {}).get("value", ""), "licenseUrl": m.get("LicenseUrl", {}).get("value", ""),
                        "artist": artist, "description": "", "name": zh, "categories": [], "source": "Musopen" if "Musopen" in title else "Commons",
                        "forced": "famous", "display": zh, "extraTags": tags})
        time.sleep(0.5)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cache", default="/tmp/music_catalog_cache")
    ap.add_argument("--refresh", action="store_true")
    ap.add_argument("--per-category", type=int, default=20)
    args = ap.parse_args()
    os.makedirs(args.cache, exist_ok=True)
    raw_path = os.path.join(args.cache, "raw.json")
    if os.path.exists(raw_path) and not args.refresh:
        raw = json.load(open(raw_path))
    else:
        raw = []
        for cat in SOURCE_CATS:
            print("crawl", cat, end=" ", flush=True)
            try:
                got = crawl_category(cat)
            except Exception as e:
                print("ERROR", e)
                continue
            print(len(got))
            raw += got
        json.dump(raw, open(raw_path, "w"), ensure_ascii=False)
    raw = [f for f in raw if f.get("source") != "OpenGameArt"]  # hand-picked pages are (re)checked below
    print("famous pieces")
    raw = famous_entries() + raw
    print("crawl OpenGameArt")
    raw += oga_entries(args.cache)

    seen, seen_titles, cands, reasons = set(), set(), [], Counter()
    for f in raw:
        if f["pageid"] in seen:
            continue
        seen.add(f["pageid"])
        if not free_license(f["license"]):
            reasons["license " + (f["license"] or "?")] += 1
            continue
        if f.get("forced") == "famous":
            pass
        elif BLOCK.search(f["title"] + " " + f["artist"]) or HISTORIC.search(f["title"]) or PARTS.search(clean_title(f)) or any("Old-time" in c for c in f["categories"]):
            reasons["blocked/historic"] += 1
            continue
        maybe = any(MAYBE_VOCAL.search(c) for c in f["categories"]) and not any(INSTRUMENTAL.search(c) for c in f["categories"])
        if f.get("forced") == "famous":
            pass
        elif maybe or any(VOCAL_CATS.search(c) for c in f["categories"]) or VOCAL_TITLE.search(f["title"] + " " + f["description"][:200]):
            reasons["vocal"] += 1
            continue
        d = f["duration"] or 0
        musopen = f["source"] == "Musopen" or f["source"].startswith("Classical")
        if not f.get("forced") and not ((60 if musopen else 8) <= d <= (540 if musopen else 480)):  # classical excerpts make poor stingers
            reasons["duration"] += 1
            continue
        if not (f["mp3"] or f["url"]):
            reasons["no url"] += 1
            continue
        title = f.get("display") or clean_title(f)
        if title.lower() in seen_titles:
            reasons["duplicate"] += 1
            continue
        seen_titles.add(title.lower())
        f["display"], f["artistName"] = title, (f["artist"] if f.get("forced") == "famous" else artist_of(f, title))
        f["category"], f["mood"] = classify(f, title)
        f["chiptune"] = any("Chiptune" in c for c in f["categories"])
        cands.append(f)
    print("candidates", len(cands), "rejected", dict(reasons.most_common(8)))

    # balanced pick: per category cap, at most 3 tracks per artist per category, 14 per artist overall
    by_cat = defaultdict(list)
    for f in cands:
        by_cat[f["category"]].append(f)
    picked, per_artist = [], Counter()
    for key in CATEGORIES:
        pool = by_cat.get(key, [])
        # prefer 1.5-5 minute pieces, then the rest; stable by title for reproducible output
        pool.sort(key=lambda f: (not f.get("forced"), f["chiptune"], 0 if 90 <= (f["duration"] or 120) <= 300 else 1, f["display"]))
        n, art_in_cat = 0, Counter()
        cap = 200 if key == "famous" else args.per_category + (16 if key == "oriental" else 8 if key in ("stinger", "classical", "piano", "children", "comedy", "sleep") else 0)
        for f in pool:
            a = f["artistName"].lower()
            if n >= cap or (not f.get("forced") and (art_in_cat[a] >= (8 if key in ("classical", "piano", "sad", "romance", "epic") else 6) or per_artist[a] >= 70)):
                continue
            art_in_cat[a] += 1
            per_artist[a] += 1
            picked.append(f)
            n += 1
    catalog = []
    for f in picked:
        label, tags = CATEGORIES[f["category"]]
        lic = "CC0" if "cc0" in f["license"].lower() else "公有领域"
        src = "OpenGameArt" if f["source"] == "OpenGameArt" else ("Musopen · Wikimedia Commons" if f["source"] == "Musopen" or "Musopen" in f["description"] else "Wikimedia Commons" if f["source"] == "Commons" else "Free Music Archive · Wikimedia Commons")
        catalog.append({
            "id": str(f["pageid"]), "title": f["display"][:120], "artist": f["artistName"], "category": f["category"], "categoryName": label,
            "tags": list(dict.fromkeys([label] + tags + f.get("extraTags", []) + (CATEGORIES[f["mood"]][1][:2] if f.get("mood") and f["mood"] != f["category"] else []))), "durationMs": int((f["duration"] or 0) * 1000), "url": f["mp3"] or f["url"], "original": f["url"],
            "page": f["page"], "license": lic, "licenseUrl": f["licenseUrl"] or ("https://creativecommons.org/publicdomain/zero/1.0/" if lic == "CC0" else ""),
            "source": src,
        })
    # keep the catalog stable: tracks published before stay (people may already use them), unless a
    # filter now rejects them (e.g. isolated orchestra stems); their category label follows any rename
    if os.path.exists(OUT):
        have = {t["id"] for t in catalog}
        kept = 0
        for t in json.load(open(OUT)).get("tracks", []):
            if t["id"] in have or t["category"] not in CATEGORIES or PARTS.search(t["title"]) or BLOCK.search(t["title"]):
                continue
            label, tags = CATEGORIES[t["category"]]
            old_label = t.get("categoryName")
            t["categoryName"] = label
            t["tags"] = list(dict.fromkeys([label] + [x for x in t["tags"] if x != old_label] + tags))
            catalog.append(t)
            kept += 1
        print("kept from the previous catalog:", kept)
    catalog.sort(key=lambda t: (list(CATEGORIES).index(t["category"]), t["title"]))
    json.dump({"version": 1, "generatedAt": time.strftime("%Y-%m-%d"), "categories": [{"key": k, "name": v[0], "tags": v[1]} for k, v in CATEGORIES.items()], "tracks": catalog},
              open(OUT, "w"), ensure_ascii=False, indent=1)
    counts = Counter(t["categoryName"] for t in catalog)
    print("tracks", len(catalog), "total minutes", round(sum(t["durationMs"] for t in catalog) / 60000))
    for k, (label, _) in CATEGORIES.items():
        print(f"  {label}: {counts.get(label, 0)}  (pool {len(by_cat.get(k, []))})")


if __name__ == "__main__":
    main()

"""Build the official sound-effect library catalog (internal/core/sfx_catalog.json).

Same policy as the music library (scripts/build_music_catalog.py): only recordings that may be
redistributed and used commercially without conditions -- CC0 or public domain, as stated per file by
the source. Sources:
  * Wikimedia Commons: the PDsounds.org collection (public domain) and individual CC0 uploads, many of
    them Freesound CC0 imports. Commons states a machine-readable license for every file.
  * OpenGameArt: CC0 sound packs (license re-checked on each page; pages offering CC0 among several
    licenses are used under CC0). 7z/rar archives are skipped: the server can only open zip files.
Excluded on purpose: Freesound directly (needs an API key), Pixabay/Mixkit/Zapsplat/Sonniss (their
terms forbid redistributing the files), BBC sound effects (non-commercial), and Commons uploads that
are labelled CC0 but are clips from films or games (Wilhelm scream, Sega sounds, ...).

Every entry below was picked by hand for audiobook use and given a Chinese name and search tags. The
script only reads metadata (licenses, download URLs, durations through the Commons API; OpenGameArt
pages, plus the zip packs to check that the listed files exist) -- it never bulk-downloads Commons
media. The server downloads and processes the audio when an administrator imports the library
(core/sfx.go): it picks the clip from the hints below (explicit window, loudest stretch, length cap),
trims silence, fades, normalises loudness and assembles footsteps from single steps.

usage: python3 scripts/build_sfx_catalog.py [--cache DIR] [--only KEY]
"""
import argparse, hashlib, html, json, os, re, time, urllib.error, urllib.parse, urllib.request, zipfile
from collections import Counter

# Wikimedia's User-Agent policy asks for real contact information: set VB_BOT_CONTACT (email or URL).
UA = "VoicebookCatalogBuilder/1.0" + (f" ({os.environ['VB_BOT_CONTACT']})" if os.environ.get("VB_BOT_CONTACT") else "") + " python-urllib"
API = "https://commons.wikimedia.org/w/api.php"
OUT = os.path.join(os.path.dirname(__file__), "..", "internal", "core", "sfx_catalog.json")
CC0_URL = "https://creativecommons.org/publicdomain/zero/1.0/"

# category key -> (Chinese label, tags added to every sound of the category)
CATEGORIES = {
    "weather": ("风雨雷电", ["天气"]),
    "nature": ("山林水声", ["自然"]),
    "animal": ("动物鸣叫", ["动物"]),
    "footsteps": ("脚步动作", ["动作"]),
    "door": ("门窗开关", ["门窗"]),
    "home": ("居家生活", ["生活"]),
    "city": ("城市交通", ["城市"]),
    "crowd": ("人声反应", ["人声"]),
    "combat": ("打斗兵器", ["打斗"]),
    "war": ("枪炮爆炸", ["战争"]),
    "oriental": ("古风国韵", ["古风"]),
    "horror": ("恐怖悬疑", ["恐怖", "悬疑"]),
    "magic": ("魔法科幻", ["魔法", "科幻"]),
    "comedy": ("搞笑综艺", ["搞笑"]),
    "transition": ("转场提示", ["转场", "提示音"]),
}

# never use these even though Commons lists them as free: clips from films, games or commercials
BLOCKLIST = re.compile(r"wilhelm|voxscrm|sega|lighting sound film|krusty|taco bell|simpsons", re.I)


def C(title, **kw):
    """A Wikimedia Commons file (title without "File:", extension optional)."""
    return {"src": "commons", "title": title, **kw}


def O(slug, file, member=None, **kw):
    """An OpenGameArt file; `member` picks a file inside a zip."""
    return {"src": "oga", "slug": slug, "file": file, "member": member, **kw}


def STEPS(slug, file, members, interval, count=8, **kw):
    """Footsteps assembled from single-step recordings inside a zip (server builds the sequence)."""
    return {"src": "oga", "slug": slug, "file": file, "steps": {"members": members, "intervalMs": interval, "count": count}, **kw}


S, A = "sfx", "ambience"
DS = "different-steps-on-wood-stone-leaves-gravel-and-mud", "[kdd]DifferentSteps.zip"
FS = "footsteps-leather-cloth-armor", "footsteps.zip"

# (category, kind, name, tags, source)
ENTRIES = [
    # ---------------- 风雨雷电 ----------------
    ("weather", A, "小雨淅沥", ["雨", "小雨", "下雨", "夜晚"], O("rain-loopable", "Rain OGG.zip", "1.ogg")),
    ("weather", A, "中雨", ["雨", "中雨", "下雨"], O("rain-loopable", "Rain OGG.zip", "3.ogg")),
    ("weather", A, "雨打窗户", ["雨", "窗户", "室内", "夜晚"], C("Rain against the window.ogg", start=5, dur=40)),
    ("weather", A, "屋檐落雨", ["雨", "屋檐", "滴水"], O("rain-gutter-loop", "rain-gutter-loop_0.mp3")),
    ("weather", A, "大雨滂沱", ["大雨", "暴雨", "下雨"], O("amb-rain-loop-1", "amb_rain_loop_1.ogg")),
    ("weather", A, "雨落阳台", ["雨", "阳台", "雷声"], C("Rain on a veranda and t.ogg")),
    ("weather", A, "雷雨交加", ["雷雨", "打雷", "下雨", "暴风雨"], O("rain-long-thunder", "rain-thunder.ogg")),
    ("weather", A, "夏夜雷阵雨", ["雷阵雨", "夏天", "打雷", "下雨"], C("Thunderstorm after hot summer day 17 minutes 01 of 04.ogg", pick=50)),
    ("weather", S, "远处闷雷", ["雷声", "远雷", "闷雷"], C("Light Rain Distant Thunder July 5th 2016.wav", pick=10)),
    ("weather", S, "惊雷炸响", ["雷声", "炸雷", "惊雷", "打雷"], C("Tonitrus.ogg", pick=12)),
    ("weather", S, "滚滚雷声", ["雷声", "打雷", "轰隆"], C("Storm thunderbolts.ogg", pick=12)),
    ("weather", A, "狂风呼啸", ["风", "大风", "呼啸", "寒风"], C("Howling wind.ogg", start=10, dur=40)),
    ("weather", A, "寒风萧瑟", ["风", "寒风", "冬天", "萧瑟"], O("wind1", "wind1.wav")),
    ("weather", A, "微风轻拂", ["微风", "风", "安静"], O("mild-wind-background-noise", "wind background noise 2.wav")),
    ("weather", S, "一阵风", ["风", "风声", "呼"], O("short-wind-sound", "short wind sound.wav")),
    ("weather", A, "风声呜咽", ["风", "呜咽", "阴森", "荒野"], O("windy-ambient-noise", "windy_ambiance.ogg")),
    ("weather", A, "暴风雨与警报", ["暴风雨", "警报", "灾难"], O("storm-siren", "storm_3_siren.ogg")),
    ("weather", S, "地震轰鸣", ["地震", "轰隆", "震动", "灾难"], C("Earthquake.ogg", pick=15)),
    ("weather", S, "大地震动", ["地震", "震动", "崩塌"], O("earthquake-and-open-sesame", "shake.flac")),
    ("weather", A, "雨中集市", ["雨", "集市", "人群"], C("Flea market in the rain.ogg", start=20, dur=45)),
    # ---------------- 山林水声 ----------------
    ("nature", A, "溪流潺潺", ["溪流", "流水", "山林", "小溪"], C("Shallow small river with stony riverbed.ogg")),
    ("nature", A, "山间小溪", ["溪水", "流水", "山林"], C("Hemlock stream.ogg")),
    ("nature", A, "林间泉水", ["泉水", "流水", "森林"], C("Welling rivulet in the woods.ogg")),
    ("nature", A, "河水奔流", ["河流", "流水", "大河"], C("Vojníkov, 3. jez, středa.ogg", start=30, dur=45)),
    ("nature", A, "海浪拍岸", ["海浪", "海边", "大海"], O("water-waves", "wave_02_cc0-11505__transitking__wavesound.flac")),
    ("nature", A, "沙滩浪花", ["海浪", "沙滩", "海边"], O("beach-ocean-waves", "wave_01_cc0-18363__jasinski__alkaibeach.flac")),
    ("nature", A, "卵石海滩", ["海浪", "海滩", "潮水"], C("On a pebble beach.ogg")),
    ("nature", A, "退潮", ["潮水", "海边", "礁石"], C("Water over rocks as tide recedes.ogg")),
    ("nature", A, "喷泉", ["喷泉", "水", "广场"], C("Fountain in toulouse.ogg", dur=40)),
    ("nature", A, "山洞滴水", ["滴水", "山洞", "地下室", "回声"], O("dripping-water-loop", "atmosbasement.mp3_.flac")),
    ("nature", A, "花园鸟鸣", ["鸟鸣", "花园", "清晨"], C("Birds singing in garden.ogg")),
    ("nature", A, "晴日鸟语", ["鸟鸣", "晴天", "春天"], C("Birdsong mild sunny day.ogg")),
    ("nature", A, "清晨鸟鸣", ["清晨", "鸟鸣", "早晨", "天亮"], O("amb-morning-sounds-perfect-loop", "amb_morning.ogg")),
    ("nature", A, "林中鸟叫", ["鸟叫", "树林", "森林"], O("ambient-bird-sounds", "birds-isaiah658.ogg")),
    ("nature", A, "微风鸟鸣", ["微风", "鸟鸣", "田园"], C("Gentle breeze and birds singing.ogg")),
    ("nature", A, "夏夜虫鸣", ["虫鸣", "夏夜", "蟋蟀", "夜晚"], O("crickets-ambient-noise-loopable", "crickets.mp3")),
    ("nature", A, "秋夜蟋蟀", ["蟋蟀", "虫鸣", "秋夜"], O("cricket-sounds", "cricketsounds090613.mp3")),
    ("nature", A, "乡村夜晚", ["乡村", "夜晚", "虫鸣"], C("Country night noise.ogg")),
    ("nature", A, "蝉鸣", ["蝉", "夏天", "午后", "知了"], O("cicada-sounds", "082526-cicadasounds.ogg")),
    ("nature", A, "池塘蛙鸣", ["青蛙", "蛙鸣", "池塘", "夏夜"], O("swamp-environment-audio", "swamp.ogg")),
    ("nature", A, "热带雨林", ["雨林", "丛林", "鸟叫"], C("Walk in the rainforest.ogg")),
    ("nature", A, "秋林落叶", ["落叶", "秋天", "树林"], C("Leaves falling from the trees during autumn in the forest.wav", start=10, dur=45)),
    ("nature", A, "夏日田野", ["田野", "乡村", "羊", "夏天"], C("Corner of a sheep field in summer.ogg")),
    ("nature", S, "柴火噼啪", ["篝火", "火", "柴火", "野外"], O("fire-crackling", "fire-1.wav")),
    ("nature", A, "壁炉柴火", ["壁炉", "柴火", "温暖", "冬夜"], O("fireplace-sound-loop", "fire.wav")),
    ("nature", A, "干草燃烧", ["火", "燃烧", "篝火"], C("Dry grass burning in open fireplace.ogg")),
    ("nature", S, "火焰燃起", ["点火", "火焰", "燃烧"], O("catching-fire", "flame.ogg")),
    ("nature", A, "沸水咕嘟", ["沸水", "煮", "厨房", "熬药"], O("boiling-water-loops", "cooking_without_cover_01.ogg")),
    # ---------------- 动物鸣叫 ----------------
    ("animal", S, "狗叫", ["狗", "狗叫", "汪汪"], O("dog-barking-mono", "dog_barking_mono.wav")),
    ("animal", S, "狗吠连连", ["狗", "狗叫", "看门狗"], C("Dog.ogg", pick=8)),
    ("animal", S, "狼嚎", ["狼", "狼嚎", "夜晚", "荒野"], C("Wolf howls.ogg")),
    ("animal", S, "野兽低吼", ["野兽", "低吼", "狼"], O("wolf-monster-sound", "wolf_monster.mp3")),
    ("animal", S, "猫叫", ["猫", "喵"], O("meow", "Meow.ogg")),
    ("animal", S, "小猫叫", ["小猫", "猫", "喵"], O("kitten-mew", "kitten_mew-1.wav")),
    ("animal", S, "猫咪讨食", ["猫", "喵", "撒娇"], O("cat-purr-meow", "cat_mewfood.wav")),
    ("animal", A, "猫咪呼噜", ["猫", "呼噜", "温馨"], O("cat-purr-meow", "cat_purrsleepy_loop.wav")),
    ("animal", S, "公鸡打鸣", ["公鸡", "鸡叫", "清晨", "天亮"], C("Medium rooster crowing.ogg")),
    ("animal", S, "小公鸡啼叫", ["公鸡", "鸡叫", "农家"], C("Small rooster crowing.ogg")),
    ("animal", S, "母鸡咯咯", ["母鸡", "鸡", "农家"], C("Hen announcing shes lain an egg.ogg")),
    ("animal", A, "鸡舍", ["鸡", "农家", "乡村"], C("Chickens demanding food.ogg")),
    ("animal", S, "羊叫", ["羊", "咩"], C("Sheep bleating.ogg")),
    ("animal", A, "羊群", ["羊群", "山羊", "草原"], C("Herd of goats bleating.ogg", dur=30)),
    ("animal", S, "驴叫", ["驴", "农家"], C("157763 felix-blume a-donkey-is-braying-in-his-enclosure-in-south-of-france.wav", pick=10)),
    ("animal", S, "鸽子咕咕", ["鸽子", "鸟"], C("Dove cooing.ogg")),
    ("animal", S, "海鸥鸣叫", ["海鸥", "海边"], O("solo-seagull-sound-effects", "Seagull Ambient 1.wav")),
    ("animal", A, "海鸥盘旋", ["海鸥", "港口", "清晨"], C("Gulls above the street at dawn.ogg", dur=40)),
    ("animal", S, "乌鸦叫", ["乌鸦", "不祥", "阴森", "尴尬"], O("crow-caw", "crow_caw.wav")),
    ("animal", A, "群鸦", ["乌鸦", "黄昏", "阴森"], O("crows-singing", "Crows.ogg")),
    ("animal", S, "夜鸟啼鸣", ["夜鸟", "夜晚", "猫头鹰"], O("birdcricketfrog-and-mosquito-sounds", "birdsCrickets.zip", "birdsCrickets/birdNight.wav")),
    ("animal", S, "鸟叫", ["鸟", "鸟叫", "叽叽喳喳"], O("bird-chirping-sounds", "birdchirping071414.wav")),
    ("animal", S, "青蛙叫", ["青蛙", "呱呱"], O("ribbit-frog-sounds", "ribbit_01.mp3")),
    ("animal", S, "马蹄奔腾", ["马", "马蹄", "奔跑", "古代", "骑马", "古风"], C("Six Horses Galloping By.ogg")),
    ("animal", S, "马儿小跑", ["马", "马蹄", "古代", "古风"], O("horse-trotting", "Trot.ogg")),
    ("animal", S, "熊吼", ["熊", "野兽", "咆哮"], O("bear-growls", "bear.zip", "ogg/bear_01.ogg")),
    ("animal", S, "蝙蝠尖叫", ["蝙蝠", "山洞", "阴森"], O("bat-screeches", "bat.zip", "ogg/bat_01.ogg")),
    ("animal", S, "大鸟振翅", ["翅膀", "飞翔", "鸟"], O("large-wings-flap", "wings_flap_large.zip", "wings_flap_large.ogg")),
    ("animal", S, "蜜蜂嗡嗡", ["蜜蜂", "嗡嗡", "花园"], C("Hummel bee.ogg", pick=10)),
    ("animal", A, "鹦鹉", ["鹦鹉", "鸟"], C("Parrots perroquets.ogg", dur=30)),
    ("animal", S, "海豚叫", ["海豚", "海洋"], C("161691 felixblume dolphin-screaming-underwater-in-caribbean-sea-mexico.wav", pick=10)),
    ("animal", S, "蚊子嗡嗡", ["蚊子", "嗡嗡", "夏夜"], O("birdcricketfrog-and-mosquito-sounds", "birdsCrickets.zip", "birdsCrickets/distressedMosquito.wav")),
    # ---------------- 脚步动作 ----------------
    ("footsteps", S, "木地板脚步", ["脚步", "走路", "木地板", "室内"], O("steps-in-wood-floor", "steps in wood floor.wav", cap=10)),
    ("footsteps", S, "木地板上走路", ["脚步", "走路", "木地板"], STEPS(*DS, ["wood01.ogg", "wood02.ogg", "wood03.ogg"], 540)),
    ("footsteps", S, "木地板上奔跑", ["脚步", "跑步", "奔跑", "匆忙"], STEPS(*DS, ["wood01.ogg", "wood02.ogg", "wood03.ogg"], 290, count=14)),
    ("footsteps", S, "石板路上走路", ["脚步", "走路", "石板", "古代"], STEPS(*DS, ["stone01.ogg"], 560)),
    ("footsteps", S, "踩着落叶走", ["脚步", "落叶", "秋天", "树林"], STEPS(*DS, ["leaves01.ogg", "leaves02.ogg"], 600)),
    ("footsteps", S, "泥地里走", ["脚步", "泥地", "雨后"], STEPS(*DS, ["mud02.ogg"], 620)),
    ("footsteps", S, "碎石路脚步", ["脚步", "碎石", "山路"], O(*DS, "gravel.ogg")),
    ("footsteps", S, "雪地脚步", ["脚步", "雪地", "冬天"], O("walking-on-snow-sound", "SnowWalk.zip", "SnowWalk/SnowWalk.ogg")),
    ("footsteps", S, "轻手轻脚", ["脚步", "轻", "潜行", "偷偷"], STEPS(*FS, ["footsteps/step_cloth1.ogg", "footsteps/step_cloth2.ogg", "footsteps/step_cloth3.ogg", "footsteps/step_cloth4.ogg"], 680)),
    ("footsteps", S, "皮靴走路", ["脚步", "皮靴", "走路"], STEPS(*FS, ["footsteps/step_lth1.ogg", "footsteps/step_lth2.ogg", "footsteps/step_lth33.ogg", "footsteps/step_lth4.ogg"], 560)),
    ("footsteps", S, "铁板上走路", ["脚步", "金属", "铁板"], STEPS(*FS, ["footsteps/step_metal.ogg", "footsteps/step_metal (2).ogg", "footsteps/step_metal (3).ogg", "footsteps/step_metal (4).ogg"], 540)),
    ("footsteps", A, "林间小路", ["脚步", "森林", "走路", "小路"], C("Forest track.ogg")),
    ("footsteps", S, "空旷大厅的脚步", ["脚步", "回声", "教堂", "空旷"], C("Church people walking steps with reverb.ogg", cap=15)),
    ("footsteps", S, "起跳落地", ["跳", "落地"], O("jump-landing-sound", "jumpland.wav")),
    ("footsteps", S, "书本掉落", ["掉落", "书", "落地"], C("Hardcover books dropped.ogg")),
    ("footsteps", S, "打响指", ["响指", "手指"], C("Finger clicks.ogg", pick=3)),
    # ---------------- 门窗开关 ----------------
    ("door", S, "敲门", ["敲门", "门", "咚咚"], C("Knocking on wood or door.ogg")),
    ("door", S, "轻轻敲门", ["敲门", "门"], C("Knock on door.wav")),
    ("door", S, "叩门环", ["敲门", "门环", "古代", "大门", "古风"], C("Door knocker audio.ogg")),
    ("door", S, "木门吱呀", ["开门", "吱呀", "木门", "老房子"], C("Squeaky door.ogg")),
    ("door", S, "门轴吱呀", ["吱呀", "门", "老房子"], C("Squeaky door hinge.ogg")),
    ("door", S, "阴森的开门声", ["吱呀", "开门", "恐怖", "阴森"], O("creaky-door-hinge-spooky", "creaky_door_hinge.wav")),
    ("door", S, "关上旧门", ["关门", "木门", "老房子"], C("Closing an old door.ogg")),
    ("door", S, "关门", ["关门", "门"], O("4-door-closes", "door_close_-_starninjas.zip", "door.1.ogg")),
    ("door", S, "用力关门", ["关门", "摔门", "生气"], O("4-door-closes", "door_close_-_starninjas.zip", "door.3.ogg")),
    ("door", S, "推开铁门", ["铁门", "开门", "监狱"], C("Open metallic door.ogg")),
    ("door", S, "铁门", ["铁门", "关门", "牢房"], O("iron-door", "iron_door.ogg")),
    ("door", S, "石门开启", ["石门", "机关", "古墓", "密室"], O("stone-door", "stone_door.ogg")),
    ("door", S, "墓园铁门", ["铁门", "墓地", "吱呀"], C("Son de l'ouverture et fermeture du portail du cimetière du Mas Rillier.ogg")),
    ("door", S, "推拉门", ["推拉门", "门"], C("Sliding door of veranda opening.ogg")),
    ("door", S, "卷帘门升起", ["卷帘门", "车库", "店铺"], C("Garage door opening.ogg")),
    ("door", S, "电梯门关闭", ["电梯", "门"], C("Elevator door closing.ogg")),
    ("door", S, "上锁", ["锁门", "门锁", "咔哒"], O("door-lock-sounds", "DoorLockSounds.zip", "DoorLockSounds/DoorLock.ogg")),
    ("door", S, "开锁", ["开锁", "钥匙", "门锁"], O("door-lock-sounds", "DoorLockSounds.zip", "DoorLockSounds/UnlockDoor.ogg")),
    ("door", S, "门锁着推不开", ["门把手", "锁", "推不开"], O("door-lock-sounds", "DoorLockSounds.zip", "DoorLockSounds/LockedDoorHandleJiggle.ogg")),
    ("door", S, "开门", ["开门", "门"], O("door-lock-sounds", "DoorLockSounds.zip", "DoorLockSounds/DoorOpen.ogg")),
    ("door", S, "门铃叮咚", ["门铃", "叮咚", "来客"], O("doorbell", "doorbell.flac")),
    ("door", S, "老式门铃", ["门铃", "铃声"], O("doorbell-ring", "doorbell-old-tring.ogg")),
    ("door", S, "开窗", ["开窗", "窗户"], C("Flapping window opening.ogg")),
    ("door", S, "关窗", ["关窗", "窗户"], C("Flapping window closing.ogg")),
    ("door", S, "拉抽屉", ["抽屉", "书桌"], C("Wooden desk drawer.ogg")),
    ("door", S, "地窖门", ["地窖", "门", "弹簧"], C("Springlocked cellar door.ogg")),
    ("door", S, "打开宝箱", ["宝箱", "箱子", "开启"], O("open-chest-sfx", "open chest.wav")),
    ("door", S, "关车门", ["车门", "汽车"], O("cardoorsfx", "door_closing.wav")),
    # ---------------- 居家生活 ----------------
    ("home", S, "翻书", ["翻书", "书页", "阅读"], C("Turning a page.ogg")),
    ("home", S, "快速翻书", ["翻书", "书"], O("10-book-page-flips", "book_flips_-_starninjas.zip", "book_flip.1.ogg")),
    ("home", A, "翻阅旧书", ["翻书", "旧书", "图书馆"], C("Old book.ogg", start=5, dur=30)),
    ("home", S, "钢笔写字", ["写字", "钢笔", "书信"], C("Writing with inkpen.ogg")),
    ("home", S, "铅笔书写", ["写字", "铅笔", "考试"], O("pencil-sounds", "pencil.zip", "ogg/pencil_write.ogg")),
    ("home", S, "揉纸团", ["纸", "揉纸", "烦躁"], O("various-paper-sound-effects", "sounds.zip", "WAV/Paper Crushed - 1.wav")),
    ("home", S, "撕纸", ["撕纸", "纸", "撕信"], O("various-paper-sound-effects", "sounds.zip", "WAV/Paper Ripped - 1.wav")),
    ("home", S, "剪刀", ["剪刀", "剪"], O("scissors", "scissors.ogg")),
    ("home", A, "飞快打字", ["打字", "键盘", "办公室", "电脑"], C("Typing fast.ogg")),
    ("home", A, "敲键盘", ["键盘", "电脑", "加班"], C("Computer keyboard.ogg", dur=30)),
    ("home", S, "拉链", ["拉链", "衣服", "包"], O("zipper", "zipper-1.wav")),
    ("home", S, "相机快门", ["拍照", "相机", "快门"], O("camera", "photo.ogg")),
    ("home", S, "钟表滴答", ["钟表", "滴答", "时间"], C("Clock ticking.ogg")),
    ("home", S, "闹钟滴答", ["闹钟", "滴答", "深夜"], C("Alarm clock ticking.ogg")),
    ("home", S, "座钟滴答", ["钟", "滴答", "老房子"], O("ticking-clock-0", "ticking_clock.zip", "clock-1.wav")),
    ("home", S, "老式拨号电话", ["电话", "拨号", "老式"], C("Tone dialling phone germany.ogg")),
    ("home", S, "倒开水泡茶", ["倒水", "开水", "泡茶"], C("Boiling water being poured into a mug for tea.ogg")),
    ("home", S, "倒红酒", ["倒酒", "红酒", "酒杯"], C("Pouring wine.ogg")),
    ("home", S, "倒饮料", ["倒水", "饮料"], C("Pouring iced tea.ogg")),
    ("home", S, "喝水", ["喝水", "吞咽"], C("Drinking a glass of wat.ogg")),
    ("home", S, "碰杯", ["碰杯", "酒杯", "干杯", "宴会"], C("Wine glass.ogg")),
    ("home", S, "餐具碰撞", ["餐具", "刀叉", "吃饭"], C("Cutlery on table.ogg")),
    ("home", S, "杯子放到桌上", ["杯子", "桌子"], O("cutlery-sounds", "cup_on_table_01.ogg")),
    ("home", S, "切菜", ["切菜", "厨房", "做饭"], C("Chopping leeks.ogg", cap=10)),
    ("home", S, "炒菜", ["炒菜", "厨房", "做饭"], C("Sauteing leeks.ogg", cap=12)),
    ("home", S, "油炸", ["油炸", "厨房"], C("Frying fries going in and out of oil.ogg", cap=12)),
    ("home", S, "咬苹果", ["咬", "苹果", "吃东西"], O("apple-bite", "apple_bite.ogg")),
    ("home", S, "嚼薯片", ["吃东西", "咀嚼", "零食"], C("Eating potato snacks.ogg", pick=8)),
    ("home", S, "打开易拉罐", ["易拉罐", "饮料", "开罐"], C("Opening a can.ogg")),
    ("home", S, "打火机", ["打火机", "点烟"], C("Cheap cigarette lighter.ogg")),
    ("home", S, "打火石", ["火石", "点火", "古代"], C("Flintstone.ogg", pick=4)),
    ("home", S, "硬币落地", ["硬币", "钱", "掉落"], C("Coin dropped on wooden floor.ogg")),
    ("home", S, "掂硬币", ["硬币", "钱"], C("Shaking coins in palm.ogg")),
    ("home", S, "一袋金币", ["金币", "钱袋", "财宝"], O("sack-of-gold", "gold_sack.wav")),
    ("home", S, "洗手", ["洗手", "水龙头"], C("Washing hands in sink.ogg")),
    ("home", A, "淋浴", ["淋浴", "洗澡", "水"], C("Shower.ogg")),
    ("home", S, "冲马桶", ["马桶", "冲水"], C("Flushing the toilet.ogg", cap=10)),
    ("home", S, "刷牙", ["刷牙", "早晨"], C("Brushing teeth.ogg", cap=10)),
    ("home", S, "微波炉", ["微波炉", "厨房"], C("Microwave oven.ogg", cap=12)),
    ("home", A, "洗衣机", ["洗衣机", "家电"], C("Washing machine mid program.ogg", dur=25)),
    ("home", S, "收音机调频", ["收音机", "调频", "杂音"], C("Radio short wave frequency.ogg")),
    ("home", A, "收音机杂音", ["收音机", "杂音", "电台"], O("radio-noise-1", "radio_noise_loop.wav")),
    ("home", A, "电视雪花噪音", ["静电", "雪花", "电视"], O("static", "ScatterNoise1.mp3")),
    ("home", S, "拨号上网", ["拨号", "上网", "怀旧"], C("Dial up connection.ogg")),
    ("home", S, "八音盒", ["八音盒", "音乐盒", "童年"], C("Musical box la boheme.ogg", cap=20)),
    ("home", S, "给八音盒上发条", ["发条", "八音盒"], C("Winding a music box.ogg")),
    ("home", A, "黑胶唱片沙沙声", ["唱片", "黑胶", "怀旧", "民国"], C("Plattenknistern record.ogg")),
    ("home", S, "磁带倒带", ["磁带", "倒带", "怀旧"], C("Rewinding an automatic.ogg", pick=8)),
    ("home", S, "掷骰子", ["骰子", "赌", "桌游"], O("wooden-dice-on-wodden-table-roll", "Holzwürfel_auf_Holztisch_1.flac")),
    ("home", S, "洗牌", ["洗牌", "扑克", "赌"], O("playing-card-sounds", "shuffleandbridge.wav")),
    ("home", S, "玻璃碎裂", ["玻璃", "打碎", "碎裂"], O("glass-break", "glass_breaking.wav")),
    ("home", S, "冰块碎裂", ["冰", "碎裂"], O("ice-breakingshattering", "IceShatters.zip", "IceShatters/LedasLuzta.ogg")),
    ("home", A, "厨房忙碌", ["厨房", "做饭", "生活"], C("Ambient Kitchen Sounds.wav", start=30, dur=40)),
    # ---------------- 城市交通 ----------------
    ("city", A, "城市街道", ["街道", "城市", "车流"], C("20180323-street-at-Kapuzinerstrasse-Munich.opus")),
    ("city", A, "车水马龙", ["车流", "马路", "城市", "交通"], O("high-traffic-road-sounds", "gatve Varniu.ogg")),
    ("city", A, "周末街头", ["街道", "城市", "行人"], C("Sunday in the city street noise1.ogg", start=30, dur=45)),
    ("city", A, "高速公路", ["高速", "车流"], C("Highway from bridge center.ogg", start=20, dur=40)),
    ("city", S, "汽车鸣笛", ["喇叭", "汽车", "鸣笛"], O("car-sound-effects-pack-low-quality", "car_sound_effects_pack.zip", "Car_Horn.ogg")),
    ("city", S, "发动汽车", ["汽车", "发动", "引擎"], O("car-sound-effects-pack-low-quality", "car_sound_effects_pack.zip", "Car_Engine_Start_Up.ogg")),
    ("city", S, "汽车加速驶离", ["汽车", "加速", "离开"], O("car-sound-effects-pack-low-quality", "car_sound_effects_pack.zip", "Car_Acceleration.ogg")),
    ("city", S, "汽车熄火", ["汽车", "熄火"], O("car-sound-effects-pack-low-quality", "car_sound_effects_pack.zip", "Car_Engine_Turning_Off.ogg")),
    ("city", S, "启动汽车开走", ["汽车", "开车", "离开"], C("Starting a car and driving.ogg", cap=20)),
    ("city", S, "汽车驶过石子路", ["汽车", "驶过"], C("Car on cobblestone.ogg")),
    ("city", S, "摩托车驶过", ["摩托车", "驶过"], C("Motorbike 2.ogg")),
    ("city", S, "警笛", ["警笛", "警车", "警察"], C("American police siren i.ogg", cap=15)),
    ("city", S, "警报声", ["警报", "警笛"], C("Siren.ogg")),
    ("city", S, "防空警报", ["警报", "战争", "空袭"], O("sirens-and-alarm-noise", "siren.mp3")),
    ("city", S, "倒车提示音", ["倒车", "货车"], C("Back-up beeper.ogg")),
    ("city", S, "自行车铃", ["自行车", "铃铛"], C("Bicycle-bell-1.wav")),
    ("city", A, "火车车厢", ["火车", "铁轨", "旅途"], C("Стук колёс поезда.ogg", start=30, dur=45)),
    ("city", A, "火车进站", ["火车站", "火车"], C("Trains through a railwa.ogg", start=10, dur=45)),
    ("city", S, "蒸汽火车汽笛", ["火车", "汽笛", "民国", "蒸汽"], O("steam-whistle", "steam_whistle.wav")),
    ("city", A, "蒸汽机车", ["蒸汽", "火车", "民国"], C("Steam engine.ogg")),
    ("city", S, "列车关门", ["列车", "关门", "地铁"], C("Train doors closing.ogg")),
    ("city", A, "地铁站台", ["地铁", "站台"], C("20180323-underground-u5-at-lehel-in-munich.opus")),
    ("city", A, "公交车里", ["公交", "车厢"], C("20180323-bus-munich.opus")),
    ("city", A, "火车站大厅", ["火车站", "大厅", "人群"], C("20180323-Munich-HBF.opus")),
    ("city", A, "飞机机舱", ["飞机", "机舱", "旅途"], C("Sound in air plane 2.ogg")),
    ("city", S, "飞机飞过", ["飞机", "飞过"], C("Sound of AN-24 airplane.ogg", pick=15)),
    ("city", S, "直升机飞过", ["直升机", "飞过"], C("Helicopter over quiet neighbourhood.ogg")),
    ("city", S, "轮船汽笛", ["轮船", "汽笛", "港口"], C("Fog signal Bremerhaven.ogg", pick=10)),
    ("city", A, "码头", ["码头", "船", "港口"], C("Boat by a wharf 1.ogg")),
    ("city", A, "轮船引擎", ["轮船", "引擎", "船舱"], O("steamboat-engine-sound", "steamboat_engine.wav")),
    ("city", A, "商场", ["商场", "购物", "人群"], C("Shopping mall less crowded.ogg")),
    ("city", A, "餐厅", ["餐厅", "吃饭", "人声"], C("Restaurant ambience.ogg", start=10, dur=45)),
    ("city", A, "学校食堂", ["食堂", "学生", "学校"], C("High school cafeteria.ogg")),
    ("city", A, "教室", ["教室", "学校", "上课"], C("Ambient classroom mono.ogg")),
    ("city", A, "菜市场", ["菜市场", "集市", "叫卖"], C("Le marche daligre.ogg", start=20, dur=45)),
    ("city", A, "游乐园", ["游乐园", "热闹"], C("La fete foraine.ogg", start=20, dur=45)),
    ("city", A, "操场嬉戏", ["孩子", "操场", "学校"], C("Douzen kids on playground.ogg")),
    ("city", A, "游泳馆", ["游泳", "孩子"], C("Indoor swimming pool children.ogg")),
    ("city", A, "隔壁装修", ["装修", "电钻", "噪音"], C("Drilling next door 4.ogg", dur=20)),
    ("city", S, "电锯", ["电锯", "伐木"], C("Chainsaw 3.ogg")),
    ("city", S, "超市收银扫码", ["超市", "收银", "扫码"], C("Prize scan supermarket.ogg")),
    ("city", S, "电梯运行", ["电梯"], C("Elevator ride.ogg", cap=20)),
    ("city", S, "烟雾报警器", ["报警", "火警"], C("Smoke alarm.ogg", cap=10)),
    ("city", S, "上课铃", ["上课铃", "下课", "学校"], C("Old school bell 1.ogg")),
    ("city", S, "教堂钟声", ["教堂", "钟声", "西方"], C("Church bells - Leverkusen, 2007.oga", cap=30)),
    ("city", A, "工厂机器", ["机器", "工厂"], C("Stomping machine.ogg", dur=30)),
    ("city", A, "发电机", ["发电机", "机器"], O("generator-loop", "loop_generator_1.mp3")),
    # ---------------- 人声反应 ----------------
    ("crowd", S, "掌声", ["掌声", "鼓掌"], C("Applause i.ogg")),
    ("crowd", S, "短暂掌声", ["掌声", "鼓掌"], C("Applause ii.ogg")),
    ("crowd", S, "热烈掌声", ["掌声", "热烈", "演出"], C("Sound Effects - Applause after a concert.ogg", cap=20)),
    ("crowd", S, "鼓掌欢呼", ["掌声", "欢呼", "庆祝"], C("Clapping hurray.ogg")),
    ("crowd", S, "掌声渐起", ["掌声", "渐起"], C("Slow starting applause.ogg", cap=25)),
    ("crowd", A, "礼堂掌声", ["掌声", "礼堂", "观众"], O("applause-in-a-large-hall-or-church", "applause-clapping-church-crowd-immersive.wav", dur=30)),
    ("crowd", S, "观众笑声", ["笑声", "观众", "罐头笑声"], C("Lachkonserve 1.ogg")),
    ("crowd", S, "哈哈大笑", ["大笑", "笑声"], C("Bursting out laughing.ogg")),
    ("crowd", S, "开怀大笑", ["笑声", "大笑"], C("Laughter.ogg")),
    ("crowd", S, "各种笑声", ["笑声", "轻笑"], C("Laughter(s).ogg", pick=10)),
    ("crowd", S, "女孩轻笑", ["女孩", "轻笑"], C("Short clip of girl laughing.wav")),
    ("crowd", S, "一群人嬉笑", ["嬉笑", "一群人"], O("group-giggling", "group_giggling.ogg")),
    ("crowd", S, "欢呼", ["欢呼", "好"], C("Hurray.ogg")),
    ("crowd", A, "人群喧哗", ["人群", "喧哗", "热闹"], C("Festival concert people crowd.ogg")),
    ("crowd", A, "人群吵嚷", ["人群", "争吵", "骚乱"], O("crowd-shoutingspeaking-ambience", "crowd_shouting.ogg")),
    ("crowd", A, "孩子们玩耍", ["孩子", "嬉戏"], C("Kids next door.ogg")),
    ("crowd", S, "婴儿哭", ["婴儿", "哭"], C("Baby long wanting cry.ogg")),
    ("crowd", S, "婴儿委屈地哭", ["婴儿", "哭"], C("Babys disappointed crying.oga", pick=12)),
    ("crowd", S, "婴儿咯咯笑", ["婴儿", "笑"], C("Baby 5 months old gabbling laughing practising voice.ogg", pick=10)),
    ("crowd", S, "婴儿打哈欠", ["婴儿", "哈欠"], C("Baby yawning cute.ogg")),
    ("crowd", S, "咳嗽", ["咳嗽"], C("Man coughing.ogg")),
    ("crowd", S, "连续咳嗽", ["咳嗽", "生病"], C("Cough 2.ogg")),
    ("crowd", S, "打喷嚏", ["喷嚏", "感冒"], C("Sneezing.ogg")),
    ("crowd", S, "打哈欠", ["哈欠", "困"], C("Yawning 1.ogg")),
    ("crowd", S, "叹气", ["叹气", "无奈"], C("Three sighs.ogg")),
    ("crowd", S, "气喘吁吁", ["喘气", "疲惫", "跑步"], O("breathing-tired", "breathing tired.wav")),
    ("crowd", S, "沉重的呼吸", ["呼吸", "紧张"], C("Heavy breathing 3.ogg", cap=15)),
    ("crowd", S, "咽口水", ["吞咽", "紧张"], C("Swallowing gulp.ogg")),
    ("crowd", S, "肚子咕咕叫", ["肚子饿", "咕咕"], C("Tummy grumble.ogg", pick=6)),
    ("crowd", S, "打嗝", ["打嗝"], C("Burp.ogg")),
    ("crowd", S, "痛苦闷哼", ["痛", "呻吟", "受伤"], C("Male pain grunts.ogg", pick=5)),
    ("crowd", S, "口哨", ["口哨"], C("Soft whistle.ogg")),
    ("crowd", S, "吹口哨调侃", ["口哨", "调侃"], C("Wolf whistle.ogg")),
    # ---------------- 打斗兵器 ----------------
    *[("combat", S, f"刀剑相击 {i + 1}", ["刀剑", "兵器", "打斗", "武侠", "古风"], O("20-sword-sound-effects-attacks-and-clashes", "sword_clash_-_starninjas.zip", f"sword_clash.{n}.ogg")) for i, n in enumerate((1, 3, 6, 9))],
    *[("combat", S, f"挥剑 {i + 1}", ["挥剑", "剑", "武侠", "古风"], O("20-sword-sound-effects-attacks-and-clashes", "sword_-_starninjas.zip", f"sword - StarNinjas/sword.{n}.ogg")) for i, n in enumerate((1, 5, 9))],
    *[("combat", S, f"挥舞破空 {i + 1}", ["嗖", "挥舞", "破空", "棍棒"], O("swishes-sound-pack", "swishes.zip", f"swishes/swish-{n}.wav")) for i, n in enumerate((3, 7, 9, 12))],
    *[("combat", S, f"拳脚击打 {i + 1}", ["拳头", "打斗", "击打"], O("thwack-sounds", "thwack-1.0.zip", f"PCM/thwack-{n:02d}.wav")) for i, n in enumerate((1, 4, 8))],
    *[("combat", S, f"兵器格挡 {i + 1}", ["格挡", "盾牌", "兵器"], O("10-impactshield-blocks", "impact_-_starninjas.zip", f"impact.{n}.ogg")) for i, n in enumerate((1, 4))],
    ("combat", A, "战鼓", ["战鼓", "出征", "战争", "古风"], O("horde-war-drums-loop", "horde_war_drums_by_william_hector.wav", dur=30)),
    ("combat", S, "号角", ["号角", "出征", "战争", "古风"], O("their-coming-generic-horn-sound", "theircoming.ogg")),
    ("combat", S, "弓弦震动", ["弓", "射箭"], C("Deep twang of loose bow string.ogg", pick=3)),
    ("combat", S, "铁链拖动", ["铁链", "锁链", "监狱"], O("chain-winch-sounds", "winch.zip", "winch - Marker #2.wav", cap=6)),
    ("combat", S, "金属落地", ["金属", "撞击", "掉落"], C("Ringing light metal piece on hard floor.ogg")),
    # ---------------- 枪炮爆炸 ----------------
    ("war", S, "手枪射击", ["手枪", "枪声"], O("gunshots", "22 Pistol.wav", pick=3)),
    ("war", S, "左轮手枪", ["左轮", "枪声", "民国"], O("gunshots", "22 Magnum.wav", pick=3)),
    ("war", S, "老式火枪", ["火枪", "枪声", "古代"], O("gunshots", "Black Powder.wav", pick=4)),
    ("war", S, "步枪射击", ["步枪", "枪声"], O("gunshot-sounds", "sounds.zip", "sounds/mosin.wav", pick=4)),
    ("war", S, "霰弹枪", ["霰弹枪", "枪声"], O("gunshot-sounds", "sounds.zip", "sounds/shotty.wav")),
    ("war", S, "消音手枪", ["消音", "手枪", "暗杀"], O("hollywood-style-pistol-silencer-sound-effect", "silencer.wav")),
    ("war", S, "手枪上膛", ["上膛", "手枪"], O("handgun-reload-sound-effect", "reload.wav")),
    ("war", S, "霰弹枪上膛", ["上膛", "霰弹枪"], O("shotgun-reload-sound-effects", "shotgunsounds.zip", "ShotgunSounds/Rack.mp3")),
    ("war", S, "连续枪声", ["枪声", "交火"], C("Gunshots 8.ogg")),
    ("war", S, "爆炸", ["爆炸", "轰"], O("explosion-8", "explosion.wav")),
    ("war", S, "沉闷的爆炸", ["爆炸"], O("dull-explosion", "explosion_dull.flac")),
    ("war", S, "远处爆炸", ["爆炸", "远处"], O("explosion-somewhere-far", "explosion_somewhere_far.mp3")),
    ("war", S, "远方闷响", ["爆炸", "远处", "闷响"], O("muffled-distant-explosion", "NenadSimic - Muffled Distant Explosion.wav")),
    ("war", S, "剧烈爆炸", ["爆炸", "剧烈"], O("chunky-explosion", "Chunky Explosion.mp3")),
    ("war", S, "大炮开火", ["大炮", "炮声", "战争"], O("cannon-fire", "cannon_fire.ogg")),
    ("war", S, "炮弹击中", ["炮弹", "爆炸"], O("cannon-hit", "cannon_hit.ogg")),
    ("war", S, "炮弹落水", ["炮弹", "落水", "海战"], O("ocean-splash", "cannon_miss.ogg")),
    ("war", S, "炮声", ["炮声", "礼炮"], O("25-cc0-bang-firework-sfx", "25-CC0-bang-sfx.zip", "cannon_01.ogg")),
    ("war", S, "砰的一声", ["砰", "爆响"], O("25-cc0-bang-firework-sfx", "25-CC0-bang-sfx.zip", "bang_01.ogg")),
    ("war", S, "导火索燃烧", ["导火索", "炸药"], O("simple-fuse-sound", "fuse.ogg")),
    ("war", S, "点燃信号弹", ["信号弹", "点燃"], O("flare-ignition", "ignition.flac")),
    # ---------------- 古风国韵 ----------------
    ("oriental", S, "铜锣", ["锣", "铜锣", "开场", "古代"], C("240382 the-very-real-horst chinese-gong-finish-session-2014-06-10-29-143.wav", pick=10)),
    ("oriental", S, "锣声余韵", ["锣", "余韵"], C("Gong55.ogg")),
    ("oriental", S, "大锣", ["锣", "大锣"], C("Gong or bell vibrant (short).ogg")),
    ("oriental", S, "禅锣", ["锣", "禅", "冥想", "寺庙"], C("Meditation Gong.ogg")),
    ("oriental", S, "寺庙钟声", ["钟声", "寺庙", "古刹", "钟"], C("25822 heigh-hoo peace-bell-hiroshima.wav")),
    ("oriental", S, "敲钟", ["钟", "敲钟"], C("Striking a bell 15cm large.ogg", pick=10)),
    ("oriental", S, "风铃", ["风铃", "清脆", "微风"], C("Windchimes.ogg")),
    ("oriental", A, "风铃叮当", ["风铃", "叮当"], C("Windglockenspiel.Koshi.ogg", dur=30)),
    ("oriental", A, "铃铛轻响", ["铃铛", "叮当"], C("Soothing jingling little bells ambience.ogg", dur=30)),
    ("oriental", S, "鞭炮", ["鞭炮", "过年", "喜庆", "婚礼"], C("Firecracker sounds 2022-12-31T23-25-40.ogg")),
    ("oriental", A, "除夕鞭炮", ["鞭炮", "过年", "烟花"], C("Fire crackers - New Year 2023 - 2022-12-31T23-53-56.ogg", start=20, dur=40)),
    ("oriental", S, "烟花绽放", ["烟花", "节日", "庆祝"], C("Fireworks piece.ogg", pick=10)),
    ("oriental", A, "远处的烟花", ["烟花", "夜晚", "节日"], C("Fireworks in distance 3.ogg", dur=40)),
    ("oriental", S, "烟花升空", ["烟花", "升空"], O("25-cc0-bang-firework-sfx", "25-CC0-bang-sfx.zip", "fw_01.ogg")),
    ("oriental", S, "竹笛", ["笛子", "竹笛", "古风"], C("DiZi Chinese Flute Sample.ogg")),
    ("oriental", S, "碰铃", ["碰铃", "铃", "佛"], C("Audio Zill small 1.mp3", pick=6)),
    ("oriental", S, "雨棍沙沙", ["雨声", "竹", "沙沙"], C("Rainstick.ogg")),
    ("oriental", S, "钹", ["钹", "镲", "锣鼓"], C("CrashCymbalSample.ogg", pick=5)),
    ("oriental", S, "鼓声滚奏", ["鼓", "鼓点", "开场"], C("Long Drum Roll with Cymbal Crash at End.wav")),
    ("oriental", S, "把玩铁球", ["铁球", "古玩", "老爷"], O("baoding-balls", "Baoding.ogg", cap=10)),
    # ---------------- 恐怖悬疑 ----------------
    ("horror", A, "恐怖氛围", ["恐怖", "阴森", "诡异"], O("ambient-horror", "ambient_horror.ogg")),
    ("horror", A, "黑暗低鸣", ["黑暗", "低鸣", "压抑"], O("dark-ambiences", "dark_ambiences.zip", "dark_ambiences/ambience-1.wav")),
    ("horror", A, "深渊低鸣", ["黑暗", "低鸣", "深渊"], O("dark-ambiences", "dark_ambiences.zip", "dark_ambiences/ambience-5.wav")),
    ("horror", A, "地牢", ["地牢", "阴森", "回声"], O("loopable-dungeon-ambience", "dungeon_ambient_1.ogg")),
    ("horror", A, "诡异氛围", ["诡异", "不安"], O("upside-down-grin-freaky-ambient", "upside down grin2.ogg")),
    ("horror", A, "不安的窸窣声", ["窸窣", "不安", "有人"], C("Uneasy rustling ambience.ogg", dur=40)),
    ("horror", S, "恐怖音效", ["惊吓", "恐怖"], O("horror-sfx", "horror_sfx.zip", "horror_effect1.wav", pick=10)),
    ("horror", S, "惊悚音效", ["惊悚", "恐怖"], O("horror-sfx", "horror_sfx.zip", "horror_effect2.wav", pick=10)),
    ("horror", S, "低沉重音", ["重音", "惊悚", "转折"], C("647712 unfa braam.flac")),
    ("horror", S, "悬念音", ["悬疑", "悬念", "揭晓"], O("mystery-sting", "mystery_sting.ogg")),
    ("horror", S, "危机逼近", ["危机", "紧张", "逼近"], O("space-impending-danger-sfx", "space1.mp3")),
    ("horror", S, "鬼魂呼吸", ["鬼", "呼吸", "幽灵"], O("ghost-breath", "ghostbreath.flac")),
    ("horror", S, "鬼魂低语", ["鬼", "幽灵", "低语"], O("ghost", "ghost.wav")),
    ("horror", S, "尖利的鬼叫", ["鬼", "尖叫", "幽灵"], O("scary-high-pitched-ghost", "scaryhighpitchedghost.ogg")),
    ("horror", A, "幽灵哼唱", ["幽灵", "哼唱"], O("ghostly-humming", "ghostly_humming.ogg")),
    ("horror", S, "女巫怪笑", ["怪笑", "女巫"], O("witch-cackle", "witch_cackle-1.ogg")),
    ("horror", S, "阴森笑声", ["怪笑", "反派", "阴森"], O("evil-laugh", "laugh-evil-1.ogg")),
    ("horror", S, "邪恶笑声", ["邪笑", "反派"], C("Evil laughter.ogg")),
    ("horror", S, "反派狂笑", ["狂笑", "反派"], O("maniacal-laughter-pack-1", "maniacal_laughter_pack_1.ogg", pick=8)),
    ("horror", S, "幽灵怪笑", ["怪笑", "幽灵"], O("ghostly-cackle-laugh-1", "ghostly_cackle_laugh_1.ogg")),
    ("horror", S, "小丑怪笑", ["小丑", "怪笑"], C("Scotcampbell - clown laugh (cc0) (freesound).mp3")),
    ("horror", S, "女人尖叫", ["尖叫", "女人", "惊恐"], O("female-scream-1", "female_scream_1.ogg")),
    ("horror", S, "惊恐尖叫", ["尖叫", "惊恐"], O("horror-scream1", "scream_horror1.mp3")),
    ("horror", S, "凄厉尖叫", ["尖叫", "凄厉", "恶鬼"], C("Nick121087 - Demonic Woman Scream (cc0) (freesound).mp3")),
    ("horror", S, "心跳（缓慢）", ["心跳", "紧张"], O("heartbeat-sounds", "heartbeat_slow_0.wav")),
    ("horror", S, "心跳（急促）", ["心跳", "急促", "恐惧"], O("heartbeat-sounds", "heartbeat_fast_0.wav")),
    ("horror", S, "心跳回响", ["心跳", "回响", "悬疑"], O("heartbeat-sounds", "heartbeat_slow_reverb.wav")),
    ("horror", A, "真实心跳", ["心跳", "胸口"], C("Human heart rate.flac", dur=30)),
    ("horror", S, "怪物咆哮", ["怪物", "咆哮"], O("cc0-deep-monster-roar", "monster_roar.wav")),
    ("horror", S, "怪物低吼", ["怪物", "低吼"], C("Monster growls.ogg", pick=8)),
    ("horror", S, "怪物嘶吼", ["怪物", "嘶吼"], O("monster-sound-effects-2", "monster_sfx_pack_2.zip", "monster_sfx_pack_2/monster-1.wav")),
    ("horror", S, "僵尸呻吟", ["僵尸", "呻吟"], O("zombie-moans", "darsycho__zombie-moans.ogg")),
    ("horror", S, "老树吱嘎", ["树", "吱嘎", "森林"], O("tree-creaking", "tree_creak.flac")),
    ("horror", S, "刺耳摩擦", ["摩擦", "刺耳"], C("Dreadful jarring of dis.ogg", pick=6)),
    ("horror", S, "钢琴乱键", ["钢琴", "惊吓"], C("Piano hits uncontrolled.ogg", pick=6)),
    ("horror", S, "惊吓", ["惊吓", "突然"], O("excited-horror-sound", "excited horror sound.wav")),
    # ---------------- 魔法科幻 ----------------
    *[("magic", S, f"施法 {i + 1}", ["魔法", "施法", "仙术"], O("magic-spell-sfx", f"magical_{n}.ogg")) for i, n in enumerate((1, 3, 4, 7))],
    ("magic", S, "治愈术", ["治愈", "魔法", "回血"], O("magic-words-healing-sound-effect", "health_restore.wav")),
    ("magic", S, "念咒", ["咒语", "魔法"], O("magic-words-healing-sound-effect", "magic_words.wav")),
    ("magic", S, "冰冻术", ["冰", "冰冻", "魔法"], O("freeze-spell-0", "freeze.wav")),
    ("magic", S, "寒冰法术", ["冰", "寒冰", "法术"], O("ice-spells", "icespells.zip", "coldsnap.wav")),
    ("magic", S, "火球术", ["火球", "魔法", "火焰"], O("fireball-1", "105016__julien-matthey__jm-fx-fireball-01.wav")),
    ("magic", S, "瞬移", ["瞬移", "传送"], O("teleport", "172206__fins__teleport.wav")),
    ("magic", S, "传送阵", ["传送", "阵法"], O("teleport-spell", "teleport.wav")),
    ("magic", S, "大地法术", ["土", "法术", "大地"], O("earth-element-magic-spell", "Earth Element Magic Spell.flac")),
    ("magic", S, "神奇出现", ["出现", "变身", "魔法"], C("Magical appearance.ogg")),
    ("magic", S, "治愈光芒", ["治愈", "光芒", "仙术"], O("cure-magic", "curemagic.zip", "Cure1.wav")),
    ("magic", S, "时间倒流", ["时间", "倒流", "穿越"], O("reversing-time-stuck-in-time", "Reverse-Time-Loop-isaiah658.ogg", cap=8)),
    ("magic", S, "时间静止", ["时间", "静止"], O("time-slow", "time_stop.mp3")),
    ("magic", S, "能量护盾", ["能量", "护盾"], O("seamless-energy-emission-loop", "movingshield_sound.ogg")),
    ("magic", S, "激光枪", ["激光", "科幻"], O("laser-fire-0", "tir.mp3")),
    ("magic", S, "太空激光", ["激光", "太空"], O("space-laser", "space laser.wav")),
    ("magic", S, "跃迁引擎", ["引擎", "飞船", "科幻"], O("warp-engine-engage-sound", "warp engine engage_0-16bit.wav")),
    ("magic", S, "机器人", ["机器人", "科幻"], O("robot-1", "robot.mp3")),
    ("magic", S, "机械变形", ["机械", "变形"], O("robotic-transformations", "ROBOTIC_Transformation_Sweep_03_Up_stereo.wav")),
    ("magic", S, "电流滋滋", ["电流", "短路"], O("electric-buzz", "buzz.ogg")),
    ("magic", S, "飞船警报", ["警报", "飞船"], O("alarm-2", "alarm.wav")),
    ("magic", S, "短促警报", ["警报", "提示"], O("short-alarm", "alarm.ogg")),
    ("magic", A, "未来都市", ["未来", "都市", "赛博"], O("scifi-city-ambient-loop", "busy_cyberworld.ogg")),
    ("magic", A, "飞船舱内", ["飞船", "机房", "嗡嗡"], O("sci-fi-ambience-sfx", "SFX SCI FI Computer room ambience.mp3")),
    ("magic", S, "科幻长音", ["科幻", "悬念"], O("long-sci-fi-sting", "longscifisting.ogg")),
    ("magic", S, "光剑", ["光剑", "科幻"], O("light-sabre-sounds", "light_sabre1.mp3", cap=6)),
    ("magic", S, "飞碟", ["飞碟", "外星"], C("Comic or 50ties sci fi ufo.ogg")),
    ("magic", S, "小机器人嗡嗡", ["机器人", "嗡嗡"], C("Little robot buzzing.ogg")),
    ("magic", A, "能量涌动", ["能量", "涌动"], C("Energy noise.ogg", dur=30)),
    ("magic", S, "传送门", ["传送门", "传送"], O("portal-sound", "PortalSFX.zip", "PortalSFX/porta.ogg")),
    # ---------------- 搞笑综艺 ----------------
    ("comedy", S, "弹簧", ["弹簧", "搞笑", "卡通"], O("boing", "boing.flac")),
    ("comedy", S, "滑稽弹跳", ["弹跳", "卡通"], O("boings", "bung_01.mp3")),
    ("comedy", S, "滑稽弹出", ["弹出", "变魔术"], C("Comic spring up or magic trick.ogg")),
    ("comedy", S, "滑稽消失", ["消失", "变魔术"], C("Comic vanishing magic trick.ogg")),
    ("comedy", S, "失败小号", ["失败", "哇哇", "尴尬"], O("game-over-trumpet-sfx", "losetrumpet.wav")),
    ("comedy", S, "动次打次", ["包袱", "笑点", "鼓点"], C("Ba dum bum stings.ogg", dur=4)),
    ("comedy", S, "滑稽鼓点", ["搞笑", "鼓点"], C("Sting.ogg")),
    ("comedy", S, "敲脑袋", ["敲头", "咚", "卡通"], C("Echo Bong.wav")),
    ("comedy", S, "卡通笑声", ["卡通", "笑声"], C("Cartoon Laugh.ogg", pick=6)),
    ("comedy", S, "怪声怪笑", ["怪笑", "滑稽"], C("Weird cartoonish laughs.ogg", pick=6)),
    ("comedy", S, "捏玩具", ["吱吱", "玩具"], O("squeak-toy-sounds", "squeak_toy.zip", "squeak toy/squeak toy - squeak1.wav")),
    ("comedy", S, "吹气球", ["气球", "吹气"], O("balloon-sounds", "balloon_inflate.flac")),
    ("comedy", S, "气球爆了", ["气球", "爆"], O("balloon-sounds", "balloon_pop.flac")),
    ("comedy", S, "卡通螺旋桨", ["螺旋桨", "卡通"], O("propeller-cartoon-loop", "propeller_cartoon_loop.flac")),
    ("comedy", S, "放屁", ["放屁", "搞笑"], C("Farting sound effects.webm", pick=3)),
    ("comedy", S, "嘟嘟囔囔", ["滑稽", "嘟囔"], O("silly-sound-effects", "booleh.mp3")),
    ("comedy", S, "自行车喇叭", ["喇叭", "滑稽"], O("bicycle-horn", "bicycle-horn-1.wav")),
    ("comedy", S, "拳击喇叭", ["喇叭", "打击"], C("Horn stab.mp3")),
    ("comedy", S, "胜利", ["胜利", "成功"], O("win-jingle", "winfretless.ogg")),
    ("comedy", S, "成功啦", ["成功", "叮"], C("Sucesso.ogg")),
    ("comedy", S, "出错了", ["错误", "失败"], C("Som de Erro.ogg")),
    # ---------------- 转场提示 ----------------
    ("transition", S, "嗖（转场）", ["嗖", "转场", "过渡"], O("air-whoosh", "whoosh2.wav")),
    ("transition", S, "嗖（快速）", ["嗖", "快速"], O("air-woosh-move", "air_move.wav")),
    ("transition", S, "呼啸转场", ["呼啸", "转场"], O("wierd-whooshes", "weird_whoosh_sounds_-_whoosh_12.mp3")),
    ("transition", S, "风声转场", ["风", "转场"], O("wind-whoosh-loop", "wind woosh loop.ogg", cap=3)),
    ("transition", S, "叮", ["叮", "提示", "灵光一闪"], O("bell-dingschimes", "bell_ding1.wav")),
    ("transition", S, "叮咚", ["叮咚", "提示"], O("bell-dingschimes", "bell_ding3.wav")),
    ("transition", S, "清脆铃声", ["铃声", "清脆"], O("pleasing-bell-sound-effect", "pleasing-bell.wav")),
    ("transition", S, "答对了", ["正确", "叮"], O("correct-bell", "bell.wav")),
    ("transition", S, "完成提示", ["完成", "提示"], O("completion-sound", "completetask_0.mp3")),
    ("transition", S, "成就达成", ["成就", "升级"], O("achievement", "achievement.wav")),
    ("transition", S, "钢琴滑音", ["钢琴", "滑音", "转场"], C("Deslizando o dedo no piano.ogg")),
    ("transition", S, "上扬收尾", ["收尾", "上扬"], C("Mini Crescente Finale.ogg")),
    ("transition", S, "提示音", ["提示", "注意"], C("Mini Acerto - Atenção.ogg")),
    ("transition", S, "低音冲击", ["冲击", "重音"], C("Whoom bass.ogg")),
    *[("transition", S, name, ["片头", "片尾", "乐句"] + extra, O("85-short-music-jingles", "jingleSounds_Kenney.zip", f"OGG/{m}.ogg")) for name, m, extra in (
        ("拨弦小乐句 1", "jingles_PIZZA/jingles_PIZZA00", ["轻快"]), ("拨弦小乐句 2", "jingles_PIZZA/jingles_PIZZA07", ["轻快"]),
        ("萨克斯小乐句 1", "jingles_SAX/jingles_SAX01", ["爵士"]), ("萨克斯小乐句 2", "jingles_SAX/jingles_SAX07", ["爵士"]),
        ("管弦重音 1", "jingles_HIT/jingles_HIT09", ["重音"]), ("管弦重音 2", "jingles_HIT/jingles_HIT15", ["重音"]))],
]


# ---------------------------------------------------------------------------

_last_call = [0.0]


def get(url, params=None, tries=6, binary=False):
    """Polite GET: one request at a time, at least 1.5 s apart, Retry-After honoured on 429."""
    if params:
        url += "?" + urllib.parse.urlencode(params)
    for i in range(tries):
        wait = _last_call[0] + 1.5 - time.time()
        if wait > 0:
            time.sleep(wait)
        _last_call[0] = time.time()
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": UA}), timeout=180) as r:
                data = r.read()
                return data if binary else data.decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            if e.code == 403 and b"robot policy" in (e.read() or b""):
                raise RuntimeError("来源网站要求遵守机器人访问规范：请设置 VB_BOT_CONTACT 后稍后再试") from e
            if i == tries - 1 or e.code not in (429, 500, 502, 503, 504):
                raise
            ra = e.headers.get("Retry-After", "")
            time.sleep(min(int(ra), 120) if ra.isdigit() else 15 * (i + 1))
        except Exception:
            if i == tries - 1:
                raise
            time.sleep(5 * (i + 1))


def strip(s):
    return re.sub(r"\s+", " ", html.unescape(re.sub(r"<[^>]+>", " ", s or ""))).strip()


def free_license(lic):
    l = lic.lower()
    if any(x in l for x in ("by-sa", "cc by", "cc-by", "nc", "nd", "gfdl", "attribution")):
        return False
    return "cc0" in l or "public domain" in l or l.startswith("pd")


EXTS = (".ogg", ".wav", ".flac", ".mp3", ".oga", ".opus", ".webm")
_commons = {}


def _info(p):
    ii = p["videoinfo"][0]
    m = ii.get("extmetadata", {})
    return {"pageid": p["pageid"], "title": p["title"], "url": ii["url"], "page": ii.get("descriptionurl", ""), "size": ii.get("size", 0),
            "license": m.get("LicenseShortName", {}).get("value", ""), "licenseUrl": m.get("LicenseUrl", {}).get("value", ""),
            "artist": strip(m.get("Artist", {}).get("value", ""))[:60], "duration": ii.get("duration") or 0}


def resolve_commons(titles):
    """Looks up all Commons files in batches of 50 titles (extension optional), so the whole catalog
    needs a few dozen API calls rather than hundreds."""
    cand = {}
    for t in titles:
        for c in ([t] if re.search(r"\.\w{3,4}$", t) else [t + e for e in EXTS]):
            cand.setdefault("File:" + c, t)
    keys = list(cand)
    for i in range(0, len(keys), 50):
        d = json.loads(get(API, {"action": "query", "format": "json", "formatversion": "2", "prop": "videoinfo", "viprop": "url|size|mime|extmetadata",
                                 "viextmetadatafilter": "LicenseShortName|LicenseUrl|Artist", "titles": "|".join(keys[i:i + 50])}))
        q = d.get("query", {})
        back = {n["to"]: n["from"] for n in q.get("normalized", [])}
        for p in q.get("pages", []):
            if p.get("missing") or not p.get("videoinfo"):
                continue
            orig = cand.get(back.get(p["title"], p["title"]))
            if orig and orig not in _commons:
                _commons[orig] = _info(p)
    for t in titles:  # long Freesound-import names are given as prefixes
        if t in _commons:
            continue
        s = json.loads(get(API, {"action": "query", "format": "json", "formatversion": "2", "list": "allpages", "apnamespace": 6, "apprefix": t, "aplimit": 1}))
        hits = s.get("query", {}).get("allpages", [])
        if hits:
            d = json.loads(get(API, {"action": "query", "format": "json", "formatversion": "2", "prop": "videoinfo", "viprop": "url|size|mime|extmetadata",
                                     "viextmetadatafilter": "LicenseShortName|LicenseUrl|Artist", "titles": hits[0]["title"]}))
            for p in d.get("query", {}).get("pages", []):
                if p.get("videoinfo"):
                    _commons[t] = _info(p)


def commons_info(title):
    return _commons.get(title)


_oga = {}


OGA_CACHE = [None]


def oga_page(slug):
    if slug in _oga:
        return _oga[slug]
    if OGA_CACHE[0] and os.path.exists(OGA_CACHE[0]):
        _oga.update(json.load(open(OGA_CACHE[0])))
        if slug in _oga:
            return _oga[slug]
    page = get("https://opengameart.org/content/" + slug)
    lic = re.findall(r"<div class='license-name'>([^<]+)</div>", page)
    title = strip((re.findall(r'property="dc:title"><h2>([^<]+)</h2>', page) or [""])[0])
    author = strip((re.findall(r'Author:.*?<a href="/users/[^"]+">([^<]+)</a>', page, re.S) or [""])[0])
    files = {html.unescape(n): u for u, n in re.findall(r'<span class="file">.*?<a href="(https://opengameart\.org/sites/default/files/[^"]+)" type="[^"]*"[^>]*>([^<]+)</a>', page)}
    _oga[slug] = {"license": lic, "title": title, "author": author, "files": files, "page": "https://opengameart.org/content/" + slug}
    if OGA_CACHE[0]:
        json.dump(_oga, open(OGA_CACHE[0], "w"), ensure_ascii=False)
    return _oga[slug]


def cached(cache, url):
    path = os.path.join(cache, hashlib.md5(url.encode()).hexdigest()[:12] + "_" + re.sub(r"[^\w.-]", "_", os.path.basename(urllib.parse.unquote(url.split("?")[0])))[-60:])
    if not os.path.exists(path):
        data = get(url, binary=True)
        with open(path + ".part", "wb") as f:
            f.write(data)
        os.replace(path + ".part", path)
    return path


def stable_id(e):
    if e["src"] == "commons":
        base = "c-" + re.sub(r"[^\w]", "", e["title"].lower())[:40]
    else:
        base = "o-" + e["slug"][:40] + "-" + hashlib.md5((e["file"] + (e.get("member") or "") + json.dumps(e.get("steps") or {})).encode()).hexdigest()[:6]
    if e.get("start") is not None or e.get("pick"):
        base += f"-{e.get('start') or 'p'}"
    return base


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cache", default="/tmp/sfx_catalog_cache")
    ap.add_argument("--only", default="")
    args = ap.parse_args()
    os.makedirs(args.cache, exist_ok=True)
    OGA_CACHE[0] = os.path.join(args.cache, "oga_pages.json")
    print("resolving Commons files…", flush=True)
    resolve_commons([e["title"] for c, k, n, t, e in ENTRIES if e["src"] == "commons" and (not args.only or c == args.only)])
    print(f"  {len(_commons)} found", flush=True)
    sounds, problems, seen = [], [], set()
    for cat, kind, name, tags, e in ENTRIES:
        if args.only and args.only != cat:
            continue
        label, cat_tags = CATEGORIES[cat]
        try:
            info = {}
            if e["src"] == "commons":
                info = commons_info(e["title"])
                if not info:
                    raise RuntimeError("Commons 上找不到文件")
                if BLOCKLIST.search(info["title"]):
                    raise RuntimeError("在排除名单中")
                if not free_license(info["license"]):
                    raise RuntimeError("授权不是 CC0/公有领域：" + info["license"])
                url, member = info["url"], None
                lic = "CC0" if "cc0" in info["license"].lower() else "公有领域"
                src = "PDsounds · Wikimedia Commons" if lic == "公有领域" and re.search(r"Original mp3 data|pdsounds", json.dumps(info), re.I) else "Wikimedia Commons"
                meta = {"page": info["page"], "license": lic, "licenseUrl": info["licenseUrl"] or (CC0_URL if lic == "CC0" else ""), "source": src, "author": info["artist"]}
            else:
                pg = oga_page(e["slug"])
                if "CC0" not in pg["license"]:
                    raise RuntimeError("OpenGameArt 页面没有 CC0 授权：" + "/".join(pg["license"]))
                url = pg["files"].get(e["file"])
                if not url:
                    raise RuntimeError("页面上没有文件 " + e["file"])
                member = e.get("member")
                meta = {"page": pg["page"], "license": "CC0", "licenseUrl": CC0_URL, "source": "OpenGameArt", "author": pg["author"]}
            item = {"id": stable_id(e), "name": name, "kind": kind, "category": cat, "categoryName": label,
                    "tags": list(dict.fromkeys([label] + tags + cat_tags)), "url": url}
            if member or e.get("steps"):  # OpenGameArt zip packs: make sure the files we name are inside
                with zipfile.ZipFile(cached(args.cache, url)) as z:
                    names = set(z.namelist())
                need = e["steps"]["members"] if e.get("steps") else [member]
                missing = [m for m in need if m not in names]
                if missing:
                    raise RuntimeError("压缩包里没有 " + ", ".join(missing))
            if member:
                item["member"] = member
            src_s = info["duration"] if e["src"] == "commons" else 0
            if e.get("steps"):
                st = e["steps"]
                item["steps"] = st
                item["durationMs"] = st["intervalMs"] * st["count"] + 400
            else:
                if e.get("start") is not None:
                    item["start"] = int(e["start"] * 1000)
                if e.get("dur") is not None:
                    item["dur"] = int(e["dur"] * 1000)
                if e.get("pick"):
                    item["pickMs"] = int(e["pick"] * 1000)
                if e.get("cap"):
                    item["capMs"] = int(e["cap"] * 1000)
                cap = e.get("cap") or (12 if kind == S else 45)
                guess = e.get("dur") or e.get("pick") or (min(cap, src_s) if src_s else 0)
                item["durationMs"] = int(guess * 1000)
            item["sourceMs"] = int(src_s * 1000)
            if item["id"] in seen:
                raise RuntimeError("重复条目 " + item["id"])
            seen.add(item["id"])
            item.update(meta)
            sounds.append(item)
            print(f"  ok  {label} · {name}", flush=True)
        except Exception as ex:
            problems.append(f"{label} · {name}: {ex}")
            print(f"  !!  {label} · {name}: {ex}", flush=True)
    if args.only:
        return
    json.dump({"version": 1, "generatedAt": time.strftime("%Y-%m-%d"), "categories": [{"key": k, "name": v[0], "tags": v[1]} for k, v in CATEGORIES.items()], "sounds": sounds},
              open(OUT, "w"), ensure_ascii=False, indent=1)
    counts = Counter(s["categoryName"] for s in sounds)
    print(f"\nsounds {len(sounds)} ({sum(1 for s in sounds if s['kind'] == A)} ambience), problems {len(problems)}")
    for k, (label, _) in CATEGORIES.items():
        print(f"  {label}: {counts.get(label, 0)}")
    for p in problems:
        print("  problem:", p)


if __name__ == "__main__":
    main()

"""Generates internal/ai/tts/minimax_voices_gen.go from the official MiniMax system voice list.

Source: https://platform.minimaxi.com/docs/faq/system-voice-id (saved as scripts/data/minimax_system_voices.tsv:
language<TAB>voice_id<TAB>official name). Chinese voices carry hand-written metadata; other languages get a
Chinese display name and gender/age from the table below so casting and filters work the same way.

    python3 scripts/gen_minimax_voices.py
"""
import collections, os, re

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "data", "minimax_system_voices.tsv")
OUT = os.path.join(HERE, "..", "internal", "ai", "tts", "minimax_voices_gen.go")

LANG = {  # table language -> (code, label)
    "中文 (普通话)": ("zh-CN", "普通话"), "中文 (粤语)": ("zh-HK", "粤语"), "英文": ("en-US", "英语"), "日文": ("ja-JP", "日语"),
    "韩文": ("ko-KR", "韩语"), "西班牙文": ("es", "西班牙语"), "葡萄牙文": ("pt", "葡萄牙语"), "法文": ("fr", "法语"),
    "印尼文": ("id-ID", "印尼语"), "德文": ("de", "德语"), "俄文": ("ru", "俄语"), "意大利文": ("it", "意大利语"),
    "阿拉伯文": ("ar", "阿拉伯语"), "土耳其文": ("tr", "土耳其语"), "乌克兰文": ("uk", "乌克兰语"), "荷兰文": ("nl", "荷兰语"),
    "越南文": ("vi", "越南语"), "泰文": ("th", "泰语"), "波兰文": ("pl", "波兰语"), "罗马尼亚文": ("ro", "罗马尼亚语"),
    "希腊文": ("el", "希腊语"), "捷克文": ("cs", "捷克语"), "芬兰文": ("fi", "芬兰语"), "印地文": ("hi", "印地语"),
}

# Chinese voices: id -> (gender, age, traits, extra tags, narration, crowd)
ZH = {
    "male-qn-qingse": ("male", "young", "青涩、清新的青年男声", ["青年", "清新"], False, True),
    "male-qn-jingying": ("male", "young", "干练自信的精英青年", ["精英", "干练"], False, False),
    "male-qn-badao": ("male", "young", "霸道强势的青年男声", ["霸道", "强势"], False, False),
    "male-qn-daxuesheng": ("male", "young", "阳光的男大学生", ["阳光", "学生"], False, True),
    "female-shaonv": ("female", "teen", "清甜的少女音", ["少女", "清甜"], False, True),
    "female-yujie": ("female", "adult", "成熟有气场的御姐音", ["御姐", "气场"], False, False),
    "female-chengshu": ("female", "adult", "成熟知性的女声，适合叙述", ["成熟", "知性"], True, False),
    "female-tianmei": ("female", "young", "甜美温柔的女声", ["甜美", "温柔"], False, True),
    "clever_boy": ("male", "child", "机灵聪明的男童", ["童声", "机灵"], False, False),
    "cute_boy": ("male", "child", "软萌可爱的男童", ["童声", "可爱"], False, False),
    "lovely_girl": ("female", "child", "萌萌的小女孩", ["童声", "可爱"], False, False),
    "cartoon_pig": ("unknown", "child", "憨态可掬的卡通角色", ["卡通", "童趣"], False, False),
    "bingjiao_didi": ("male", "teen", "病娇黏人的弟弟", ["病娇", "黏人"], False, False),
    "junlang_nanyou": ("male", "young", "俊朗温柔的男友音", ["男友", "温柔"], False, False),
    "chunzhen_xuedi": ("male", "teen", "纯真腼腆的学弟", ["纯真", "学生"], False, False),
    "lengdan_xiongzhang": ("male", "young", "冷淡疏离的学长", ["高冷", "学长"], False, False),
    "badao_shaoye": ("male", "young", "骄纵霸道的少爷", ["霸道", "少爷"], False, False),
    "tianxin_xiaoling": ("female", "young", "甜心可人的女孩", ["甜美", "可爱"], False, False),
    "qiaopi_mengmei": ("female", "teen", "俏皮活泼的萌妹", ["俏皮", "活泼"], False, False),
    "wumei_yujie": ("female", "adult", "妩媚动人的御姐", ["妩媚", "御姐"], False, False),
    "diadia_xuemei": ("female", "teen", "嗲声嗲气的学妹", ["嗲", "学生"], False, False),
    "danya_xuejie": ("female", "young", "淡雅温婉的学姐", ["淡雅", "温婉"], False, False),
    "Chinese (Mandarin)_Reliable_Executive": ("male", "middle", "沉稳可靠的高管", ["沉稳", "商务"], True, False),
    "Chinese (Mandarin)_News_Anchor": ("female", "adult", "字正腔圆的新闻女主播", ["播音腔", "新闻"], True, False),
    "Chinese (Mandarin)_Mature_Woman": ("female", "adult", "傲娇的成熟御姐", ["傲娇", "御姐"], False, False),
    "Chinese (Mandarin)_Unrestrained_Young_Man": ("male", "young", "放荡不羁的青年", ["不羁", "洒脱"], False, False),
    "Arrogant_Miss": ("female", "young", "嚣张跋扈的大小姐", ["嚣张", "大小姐"], False, False),
    "Robot_Armor": ("unknown", "adult", "冰冷的机械战甲音", ["机械", "科幻"], False, False),
    "Chinese (Mandarin)_Kind-hearted_Antie": ("female", "middle", "热心肠的大婶", ["热心", "市井"], False, True),
    "Chinese (Mandarin)_HK_Flight_Attendant": ("female", "young", "带港味普通话的空姐", ["港普", "亲切"], False, False),
    "Chinese (Mandarin)_Humorous_Elder": ("male", "senior", "幽默逗趣的大爷", ["幽默", "市井"], False, True),
    "Chinese (Mandarin)_Gentleman": ("male", "adult", "温润儒雅的男声，适合旁白", ["温润", "儒雅"], True, False),
    "Chinese (Mandarin)_Warm_Bestie": ("female", "young", "温暖贴心的闺蜜", ["温暖", "亲切"], False, True),
    "Chinese (Mandarin)_Male_Announcer": ("male", "adult", "专业播报男声", ["播音腔", "播报"], True, False),
    "Chinese (Mandarin)_Sweet_Lady": ("female", "young", "甜美的年轻女声", ["甜美"], False, True),
    "Chinese (Mandarin)_Southern_Young_Man": ("male", "young", "带南方口音的小哥", ["南方口音", "亲切"], False, True),
    "Chinese (Mandarin)_Wise_Women": ("female", "adult", "阅历丰富的知性姐姐，适合叙述", ["知性", "阅历"], True, False),
    "Chinese (Mandarin)_Gentle_Youth": ("male", "young", "温润的青年男声", ["温润", "青年"], True, True),
    "Chinese (Mandarin)_Warm_Girl": ("female", "teen", "温暖的少女音", ["温暖", "少女"], False, True),
    "Chinese (Mandarin)_Kind-hearted_Elder": ("female", "senior", "慈祥的花甲奶奶", ["慈祥", "长辈"], False, True),
    "Chinese (Mandarin)_Cute_Spirit": ("unknown", "child", "憨憨的萌兽", ["卡通", "憨萌"], False, False),
    "Chinese (Mandarin)_Radio_Host": ("male", "adult", "磁性的电台男主播，适合长篇旁白", ["播音腔", "电台", "磁性"], True, False),
    "Chinese (Mandarin)_Lyrical_Voice": ("male", "adult", "抒情感性的男声，适合散文与情感叙述", ["抒情", "感性"], True, False),
    "Chinese (Mandarin)_Straightforward_Boy": ("male", "teen", "率真直爽的弟弟", ["率真", "少年"], False, True),
    "Chinese (Mandarin)_Sincere_Adult": ("male", "young", "真诚朴实的青年", ["真诚", "朴实"], False, True),
    "Chinese (Mandarin)_Gentle_Senior": ("female", "young", "温柔的学姐", ["温柔", "学姐"], False, False),
    "Chinese (Mandarin)_Stubborn_Friend": ("male", "young", "嘴硬心软的竹马", ["嘴硬", "竹马"], False, False),
    "Chinese (Mandarin)_Crisp_Girl": ("female", "teen", "清脆的少女音", ["清脆", "少女"], False, True),
    "Chinese (Mandarin)_Pure-hearted_Boy": ("male", "teen", "清澈的邻家弟弟", ["清澈", "邻家"], False, False),
    "Chinese (Mandarin)_Soft_Girl": ("female", "teen", "柔和的少女音", ["柔和", "少女"], False, True),
    "Cantonese_ProfessionalHost（F)": ("female", "adult", "专业的粤语女主持", ["播音腔", "主持"], True, False),
    "Cantonese_GentleLady": ("female", "adult", "温柔的粤语女声", ["温柔"], False, False),
    "Cantonese_ProfessionalHost（M)": ("male", "adult", "专业的粤语男主持", ["播音腔", "主持"], True, False),
    "Cantonese_PlayfulMan": ("male", "young", "活泼的粤语男声", ["活泼"], False, False),
    "Cantonese_CuteGirl": ("female", "teen", "可爱的粤语女孩", ["可爱"], False, False),
    "Cantonese_KindWoman": ("female", "adult", "善良的粤语女声", ["善良", "亲切"], False, False),
}

# Other languages: lower-case official name -> (Chinese name, gender, age, traits)
EN = {
    "airheaded girl": ("迷糊少女", "female", "teen", "迷糊、可爱"),
    "ambitious woman": ("进取女性", "female", "adult", "有野心、干练"),
    "angry man": ("暴躁男声", "male", "adult", "愤怒、强势"),
    "anime character": ("动漫角色", "unknown", "teen", "二次元、夸张"),
    "arnold": ("阿诺德", "male", "adult", "硬汉、浑厚"),
    "assertive queen": ("强势女王", "female", "adult", "强势、霸气"),
    "assetive woman": ("果敢女性", "female", "adult", "果断"),
    "assured presenter": ("沉稳主持", "male", "adult", "播音腔、沉稳"),
    "athletic girl": ("运动少女", "female", "teen", "活力"),
    "athletic student": ("运动系学生", "male", "teen", "活力、阳光"),
    "attractive girl": ("魅力女孩", "female", "young", "迷人"),
    "attractive guy": ("魅力男生", "male", "young", "迷人"),
    "aussie bloke": ("澳洲大叔", "male", "middle", "澳洲口音、随性"),
    "bad-tempered boy": ("暴脾气少年", "male", "teen", "暴躁"),
    "bossy leader": ("强势领导", "unknown", "middle", "强势、发号施令"),
    "brave adventurer": ("勇敢冒险家", "male", "young", "勇敢"),
    "brave female warrior": ("勇敢女战士", "female", "young", "英气"),
    "brave heroine": ("勇敢女英雄", "female", "young", "英气"),
    "brave youth": ("勇敢少年", "male", "teen", "热血"),
    "bright queen": ("明艳女王", "female", "adult", "明亮、自信"),
    "calm gentleman": ("沉稳绅士", "male", "adult", "沉稳"),
    "calm lady": ("沉静女士", "female", "adult", "沉静、平和"),
    "calm leader": ("沉着领袖", "male", "middle", "沉着"),
    "calm woman": ("沉静女声", "female", "adult", "沉静、平和"),
    "captivating storyteller": ("迷人讲述者", "unknown", "adult", "会讲故事，适合旁白"),
    "caring girlfriend": ("体贴女友", "female", "young", "温柔、体贴"),
    "caring man": ("暖心男声", "male", "adult", "温暖、体贴"),
    "caring woman": ("体贴女性", "female", "adult", "温柔、体贴"),
    "casual man": ("随性男声", "male", "young", "随性、日常"),
    "casual woman": ("随性女声", "female", "young", "随性、日常"),
    "charming elder sister": ("魅力大姐姐", "female", "young", "成熟、迷人"),
    "charming girl": ("迷人少女", "female", "young", "迷人"),
    "charming lady": ("迷人女士", "female", "adult", "迷人、优雅"),
    "charming queen": ("魅力女王", "female", "adult", "魅力、霸气"),
    "charming santa": ("魅力圣诞老人", "male", "senior", "慈祥、欢快"),
    "charming sister": ("迷人姐姐", "female", "young", "迷人"),
    "chatty girl": ("话痨少女", "female", "young", "活泼、健谈"),
    "cheerful boyfriend": ("开朗男友", "male", "young", "开朗、阳光"),
    "cheerful cool junior": ("爽朗学弟", "male", "teen", "开朗、酷"),
    "cheerful little sister": ("开朗妹妹", "female", "teen", "开朗、可爱"),
    "childhood friend girl": ("青梅竹马", "female", "young", "亲切"),
    "cocky guy": ("自负男生", "male", "young", "张扬"),
    "cold girl": ("高冷少女", "female", "young", "高冷"),
    "cold queen": ("冰山女王", "female", "adult", "高冷、霸气"),
    "cold young man": ("冷峻青年", "male", "young", "冷峻"),
    "comedian": ("喜剧演员", "male", "adult", "幽默、搞笑"),
    "compelling girl": ("动人少女", "female", "young", "有感染力"),
    "confident boss": ("自信老板", "unknown", "middle", "自信、强势"),
    "confident woman": ("自信女性", "female", "adult", "自信"),
    "conscientious instructor": ("尽责讲师", "unknown", "adult", "认真、讲解"),
    "considerate senior": ("体贴前辈", "unknown", "young", "体贴"),
    "crazy girl": ("疯狂少女", "female", "young", "疯狂、戏剧化"),
    "cute elf": ("可爱精灵", "female", "child", "可爱、奇幻"),
    "debator": ("辩论家", "unknown", "adult", "犀利、雄辩"),
    "decisive princess": ("果敢公主", "female", "young", "果断、高贵"),
    "decisive queen": ("果决女王", "female", "adult", "果断、霸气"),
    "deep-toned man": ("低沉男声", "male", "adult", "低沉、浑厚"),
    "deep-voiced gentleman": ("低沉绅士", "male", "middle", "低沉、磁性"),
    "dependable woman": ("可靠女性", "female", "adult", "可靠、沉稳"),
    "determined boy": ("坚定少年", "male", "teen", "坚定"),
    "determined manager": ("果断经理", "unknown", "middle", "干练"),
    "diligent leader": ("勤勉领袖", "male", "middle", "沉稳"),
    "diligent man": ("勤恳男声", "male", "adult", "踏实"),
    "dominant man": ("霸气男声", "male", "adult", "霸气、强势"),
    "dramatist": ("戏剧演员", "unknown", "adult", "戏剧化、表现力强"),
    "elegant girl": ("优雅少女", "female", "young", "优雅"),
    "elegant lady": ("优雅女士", "female", "adult", "优雅"),
    "elegant princess": ("优雅公主", "female", "young", "优雅、高贵"),
    "enchanting sister": ("妩媚姐姐", "female", "young", "妩媚"),
    "energetic boy": ("活力男孩", "male", "teen", "活力"),
    "energetic woman": ("活力女性", "female", "young", "活力"),
    "energetic youth": ("活力青年", "male", "young", "活力"),
    "enthusiastic teen": ("热情少年", "unknown", "teen", "热情"),
    "fascinating boy": ("迷人少年", "male", "teen", "迷人"),
    "female anchor": ("女主播", "female", "adult", "播音腔、新闻"),
    "fragile boy": ("脆弱少年", "male", "teen", "柔弱"),
    "frank lady": ("直爽女士", "female", "adult", "直爽"),
    "friendly big sister": ("亲切大姐姐", "female", "young", "亲切"),
    "friendly boy": ("友善男孩", "male", "teen", "友善"),
    "friendly guy": ("友善男生", "male", "young", "友善"),
    "friendly man": ("亲切男声", "male", "adult", "亲切"),
    "friendly neighbor": ("友善邻居", "unknown", "adult", "亲切、日常"),
    "fussy hostess": ("挑剔女主人", "female", "middle", "挑剔"),
    "generous izakaya owner": ("豪爽居酒屋老板", "male", "middle", "豪爽"),
    "gentle boss": ("温和上司", "unknown", "middle", "温和"),
    "gentle butler": ("温柔管家", "male", "adult", "温柔、优雅"),
    "gentle girl": ("温柔少女", "female", "young", "温柔"),
    "gentle lady": ("温柔女士", "female", "adult", "温柔"),
    "gentle teacher": ("温柔老师", "unknown", "adult", "温柔、讲解"),
    "gentle woman": ("温柔女性", "female", "adult", "温柔"),
    "gentle-voiced man": ("温声男子", "male", "adult", "温柔"),
    "ghost": ("幽灵", "unknown", "adult", "阴森、奇幻"),
    "girl next door": ("邻家女孩", "female", "young", "亲切"),
    "godfather": ("教父", "male", "senior", "威严、低沉"),
    "gorgeous lady": ("华丽女士", "female", "adult", "华丽、迷人"),
    "graceful lady": ("端庄女士", "female", "adult", "端庄、优雅"),
    "graceful maiden": ("端庄少女", "female", "young", "端庄"),
    "grim reaper": ("死神", "male", "adult", "阴森、低沉"),
    "grinch": ("格林奇", "male", "adult", "怪诞、卡通"),
    "handsome childhood friend": ("帅气竹马", "male", "young", "帅气"),
    "haughty lady": ("高傲女士", "female", "adult", "高傲"),
    "humorous elder": ("幽默长者", "male", "senior", "幽默"),
    "innocent boy": ("天真少年", "male", "teen", "天真"),
    "inspiring lady": ("励志女士", "female", "adult", "鼓舞人心"),
    "intellectual man": ("知性男士", "male", "adult", "知性"),
    "intellectual senior": ("知性前辈", "unknown", "young", "知性"),
    "intonate girl": ("抑扬少女", "female", "young", "富有韵律"),
    "jovial man": ("爽朗男声", "male", "adult", "爽朗"),
    "kind lady": ("和善女士", "female", "adult", "和善"),
    "kind-hearted girl": ("善良少女", "female", "young", "善良"),
    "level-headed man": ("冷静男声", "male", "adult", "冷静、沉稳"),
    "lonely warrior": ("孤独战士", "male", "adult", "孤独、沧桑"),
    "lovely lady": ("可爱女士", "female", "young", "可爱"),
    "loyal knight": ("忠诚骑士", "male", "young", "忠诚、英气"),
    "male anchor": ("男主播", "male", "adult", "播音腔、新闻"),
    "male narrator": ("男声旁白", "male", "adult", "适合旁白"),
    "mature lady": ("成熟女士", "female", "middle", "成熟"),
    "mature partner": ("成熟伴侣", "unknown", "adult", "成熟"),
    "movie lead female": ("电影女主角", "female", "young", "戏剧化、表现力强"),
    "mysterious girl": ("神秘少女", "female", "young", "神秘"),
    "narrator": ("旁白", "unknown", "adult", "适合旁白"),
    "naughty schoolgirl": ("调皮女学生", "female", "teen", "调皮"),
    "news anchor": ("新闻主播", "female", "adult", "播音腔、新闻"),
    "optimistic youth": ("乐观青年", "unknown", "young", "乐观"),
    "passionate warrior": ("热血战士", "male", "young", "热血"),
    "patient female presenter": ("耐心女主持", "female", "adult", "播音腔、耐心"),
    "pessimistic girl": ("悲观少女", "female", "young", "忧郁"),
    "playboy charmer": ("花花公子", "male", "young", "轻佻、迷人"),
    "playful girl": ("俏皮少女", "female", "teen", "俏皮"),
    "playful man": ("风趣男声", "male", "adult", "风趣"),
    "playful spirit": ("顽皮精灵", "unknown", "child", "顽皮、奇幻"),
    "pompous lady": ("浮夸女士", "female", "adult", "浮夸"),
    "possessive man": ("占有欲男友", "male", "young", "霸道"),
    "powerful soldier": ("硬汉士兵", "male", "adult", "刚劲"),
    "powerful veteran": ("老兵", "male", "middle", "刚劲、沧桑"),
    "quirky girl": ("古灵精怪少女", "female", "teen", "古怪"),
    "rational man": ("理性男声", "male", "adult", "理性"),
    "reliable man": ("可靠男声", "male", "adult", "可靠、沉稳"),
    "reliable sister": ("可靠姐姐", "female", "young", "可靠"),
    "reliable youth": ("可靠青年", "male", "young", "可靠"),
    "reserved young man": ("内敛青年", "male", "young", "内敛"),
    "romantic husband": ("浪漫丈夫", "male", "adult", "浪漫、温柔"),
    "rudolph": ("鲁道夫", "unknown", "child", "卡通、欢快"),
    "sad teen": ("忧伤少年", "unknown", "teen", "忧伤"),
    "santa claus": ("圣诞老人", "male", "senior", "慈祥、欢快"),
    "sassy girl": ("泼辣少女", "female", "young", "泼辣"),
    "sensible manager": ("明理经理", "unknown", "middle", "理性"),
    "sentimental lady": ("多愁女士", "female", "adult", "感性"),
    "serene elder": ("安详长者", "unknown", "senior", "安详"),
    "serene man": ("平和男声", "male", "adult", "平和"),
    "serene woman": ("恬静女声", "female", "adult", "恬静"),
    "serious commander": ("严肃指挥官", "male", "middle", "严肃、威严"),
    "shy girl": ("害羞少女", "female", "teen", "害羞"),
    "sincere teen": ("真诚少年", "unknown", "teen", "真诚"),
    "smart young girl": ("聪慧少女", "female", "teen", "聪慧"),
    "soothing lady": ("舒缓女声", "female", "adult", "舒缓、温柔"),
    "sophisticated lady": ("精致女士", "female", "adult", "精致、优雅"),
    "sporty student": ("运动系学生", "male", "teen", "活力"),
    "steadfast narrator": ("沉稳旁白", "unknown", "adult", "沉稳，适合旁白"),
    "steady mentor": ("稳重导师", "male", "middle", "稳重"),
    "stressed lady": ("焦虑女士", "female", "adult", "紧张"),
    "strict boss": ("严厉上司", "unknown", "middle", "严厉"),
    "strong-willed boy": ("倔强少年", "male", "teen", "倔强"),
    "sweet girl": ("甜美少女", "female", "young", "甜美"),
    "sweet lady": ("甜美女士", "female", "adult", "甜美"),
    "theatrical actor": ("舞台演员", "male", "adult", "戏剧化、表现力强"),
    "thoughtful lady": ("体贴女士", "female", "adult", "体贴"),
    "thoughtful man": ("体贴男士", "male", "adult", "体贴"),
    "thoughtful mentor": ("睿智导师", "male", "middle", "睿智"),
    "thoughtful woman": ("细腻女性", "female", "adult", "细腻"),
    "tough boss": ("强硬老板", "unknown", "middle", "强硬"),
    "tranquil woman": ("宁静女声", "female", "adult", "宁静"),
    "trustworthy advisor": ("可信顾问", "male", "middle", "可靠"),
    "trustworthy man": ("可信男声", "male", "adult", "可靠、沉稳"),
    "upbeat man": ("乐天男声", "male", "adult", "乐观"),
    "upset girl": ("委屈少女", "female", "teen", "委屈"),
    "wandering sorcerer": ("流浪术士", "male", "adult", "神秘、奇幻"),
    "whimsical girl": ("奇想少女", "female", "teen", "古灵精怪"),
    "whispering girl": ("耳语少女", "female", "young", "耳语、气声"),
    "wise elf": ("睿智精灵", "unknown", "adult", "睿智、奇幻"),
    "wise lady": ("睿智女士", "female", "middle", "睿智"),
    "wise scholar": ("睿智学者", "male", "middle", "睿智、讲解"),
    "wise teacher": ("睿智老师", "unknown", "middle", "睿智"),
}

NARRATION_WORDS = ("旁白", "讲述者", "主播", "主持")


def go(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main():
    rows = []
    for line in open(SRC, encoding="utf-8"):
        line = line.rstrip("\n")
        if not line.strip():
            continue
        lang, vid, name = line.split("\t")
        rows.append((lang.strip(), vid.strip(), name.strip()))
    ids = [r[1] for r in rows]
    dup = [k for k, n in collections.Counter(ids).items() if n > 1]
    assert not dup, dup

    out, names = [], collections.Counter()
    for lang, vid, official in rows:
        code, label = LANG[lang]
        if code.startswith("zh"):
            g, age, traits, tags, narr, crowd = ZH[re.sub(r"-jingpin$", "", vid)]
            disp = re.sub(r"-beta$", "（Beta）", official)
            if code == "zh-HK":
                disp += "（粤语）"
                tags = tags + ["粤语"]
            en = ""
        else:
            zh, g, age, traits = EN[official.lower()]
            # the voice id is more reliable than the name for gender when it says so
            low = vid.lower()
            if re.search(r"(^|_)female", low):
                g = "female"
            elif re.search(r"(^|_)male", low):
                g = "male"
            disp = f"{zh}（{label}）"
            tags = [t for t in re.split(r"[、，]", traits) if t and len(t) <= 6] + [label]
            narr = any(w in zh for w in NARRATION_WORDS) or "旁白" in traits
            crowd = False
            en = official
        if "旁白" in traits or narr:
            tags = tags + ["旁白"]
        if "-beta" in official:
            tags = tags + ["Beta"]
        names[(code, disp)] += 1
        if names[(code, disp)] > 1:
            disp = disp.replace("（", f" {names[(code, disp)]}（", 1) if "（" in disp else f"{disp} {names[(code, disp)]}"
        seen, uniq = set(), []
        for t in tags:
            if t not in seen:
                seen.add(t)
                uniq.append(t)
        out.append((vid, disp, en, g, age, code, label, traits, uniq, narr, crowd))

    with open(OUT, "w", encoding="utf-8") as f:
        f.write("// Code generated by scripts/gen_minimax_voices.py from the official MiniMax system voice list. DO NOT EDIT.\n\n")
        f.write("package tts\n\n")
        f.write("// miniMaxVoices lists the MiniMax system voices. A system voice works with every speech model.\n")
        f.write("var miniMaxVoices = []MiniMaxVoice{\n")
        for vid, disp, en, g, age, code, label, traits, tags, narr, crowd in out:
            f.write(f"\t{{{go(vid)}, {go(disp)}, {go(en)}, {go(g)}, {go(age)}, {go(code)}, {go(label)}, {go(traits)}, []string{{{', '.join(go(t) for t in tags)}}}, {str(narr).lower()}, {str(crowd).lower()}}},\n")
        f.write("}\n")
    print(f"wrote {len(out)} voices to {os.path.relpath(OUT)}")


if __name__ == "__main__":
    main()

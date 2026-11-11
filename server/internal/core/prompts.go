package core

import (
	"math/rand"
	"strings"

	"voicebook/internal/model"
)

// Prompt template keys (editable with versions in the admin console).
const (
	PromptCharacters = "character_analysis"
	PromptScript     = "script_annotation"
	PromptCasting    = "voice_casting"
	PromptLexicon    = "lexicon_extract"
	PromptVoiceDesc  = "voice_description"
	PromptVoiceAttrs = "voice_attributes"
	PromptSFX        = "sfx_match"
	PromptModerate   = "moderation"
	PromptChapters   = "chapter_detect"
	PromptBookIntro  = "book_intro"
	PromptCover      = "cover_prompt"
)

type PromptDef struct {
	Key     string
	Name    string
	Content string
}

var DefaultPrompts = []PromptDef{
	{PromptCharacters, "角色分析", `你是一位资深的有声书导演和文学编辑。请从给定的小说片段中找出所有“会说话或有心理活动”的角色，为有声书选角做准备。

要求：
1. 合并同一角色的不同称呼（如“林黛玉/黛玉/颦儿/林姑娘”），把最常用的全名作为 name，其余放入 aliases。
2. 仅凭片段推断，不要编造；无法判断的字段使用 "unknown" 或空字符串。
3. kind 取值：lead（主角）、supporting（重要配角）、minor（次要角色）、crowd（路人、群演）。
4. gender 取值：male、female、unknown；ageGroup 取值：child、teen、young、adult、middle、senior、unknown。
5. voiceTraits 用一句话描述适合这个角色的声音（音色、音高、语速、气质），供配音参考。
6. profile 用 2~3 句话写角色小传（身份、性格、在故事中的作用）。
7. lines 估算该角色在片段中的台词句数。
8. 不要把旁白/叙述者当成角色（第一人称叙述时，把“我”作为角色并在 identity 中注明“第一人称叙述者”）。`},

	{PromptScript, "画本标注", `你是一位专业的有声书画本师。我会给你一章小说中按顺序编号的文本单元，以及已知角色表。请为每个单元标注演播信息。

标注规则：
1. type：narration（旁白叙述）、dialogue（角色说出的话，通常在引号内）、monologue（角色的内心独白/心理活动）。
2. speaker：narration 填 "NARRATOR"；dialogue/monologue 填角色表中的角色编号（如 "C3"）；若说话人不在角色表中，填 "NEW:角色名"（同一新角色保持名称一致）；确实无法判断填 "UNKNOWN"。
3. 结合上下文判断说话人：引号前后的“某某说/问/道/笑道”、对话轮替、称呼与语气。连续对话中两人交替说话时要保持交替。
4. emotion 取值：neutral、happy、sad、angry、fearful、surprised、disgusted、gentle、nervous、tired、whisper、shout；intensity 取 1（轻）、2（中）、3（强）。
5. cues：可选的表演标签，告诉配音演员这句台词在什么位置用什么语气、发出什么声音，每个单元最多 3 个，没有依据就给空数组。
   - 只标在角色的台词上（dialogue/monologue）。叙述里描写的动作和神态（如“叹了口气”“笑道”“声音发颤”“冷冷地问”）是给台词的依据：把它体现到紧挨着的那句台词上，不要给旁白（narration）单元加声音标签。
   - 每个标签给出 tag（取下方词表中的英文键）和 after（标签插入点之前紧挨着的台词原文，原样摘录 2~10 个字；插在句首时 after 为空字符串）。
   - 例：她叹了口气：“罢了，你走吧。” → 台词单元标 [{"tag":"sighing","after":""}]，旁白单元不标。
     例：“你这个人啊，真是拿你没办法。”她笑着摇头 → 台词单元标 [{"tag":"laughing","after":"你这个人啊，"}]。
   {{tag_rule}}
5.1 instruction：写给配音演员的表演指导，只写“怎么读”：语气、情绪、节奏、音量的要求，不超过 20 字，如“冷淡，带着质问”“压低声音，带着试探”“前半句强忍，后半句爆发”。
   - 不要写“角色台词”“旁白叙述”“说话提示语”这类说明，不要解释理由，不要复述原文。
   - 旁白和说话提示语通常留空字符串；只有气氛需要特别处理时（如悬疑段落要放慢、压低）才写。
6. confidence：你对 speaker 判断的把握（0~1）。
7. speechTag：仅当该单元是纯粹的说话提示语（如“他说。”“她笑着问道：”）时为 true，用于广播剧模式删除提示语。
8. prons：只列出该单元中需要特别注音的多音字或生僻字，给出所在词语 word、该字 char、正确拼音 pinyin（带声调符号，如 "háng"）以及一个读音相同的常用字 homophone；没有就给空数组。重点检查“待注意多音字”列表中的字。
9. 不得改写、增删原文，只做标注；每个单元都必须返回且只返回一次。
{{sfx_rule}}`},

	{PromptCasting, "AI 选角", `你是有声书选角导演。请根据角色资料，从候选音色中为每个角色挑选最合适的音色，按匹配度从高到低给出最多 5 个，并说明理由（一句话，说明音色与角色年龄、性别、性格、戏份的契合点）。
规则：
1. 性别必须匹配（中性/未知角色除外）；年龄段尽量接近。
2. 主角与重要配角之间尽量不要使用同一个音色。
3. 旁白选择清晰、耐听、适合长时间收听的音色。
4. score 为 0~1 的匹配分。`},

	{PromptLexicon, "发音词条提取", `你是播音审校专家。请从文本中找出朗读时容易读错的词语，包括：人名、地名、生造词、古词、专有名词、多音字组成的词。对每个词给出正确拼音（带声调符号，字与字之间用空格分隔）和简短说明。只输出确有误读风险的词，最多 40 个。`},

	{PromptVoiceDesc, "音色描述生成", `你是配音导演。请根据角色资料，写一段用于“AI 设计音色”的中文声音描述（60~120 字），描述性别、年龄感、音色质感（如清亮、低沉、沙哑、温润）、音高、语速、语气和情绪底色，以及适合的口音；不要写角色经历，不要使用人名。`},

	{PromptVoiceAttrs, "音色属性解析", `请从这段声音描述中提取结构化属性：gender（male/female/unknown）、ageGroup（child/teen/young/adult/middle/senior/unknown）、tags（3~6 个简短风格标签，如“清亮”“温柔”“少年感”“播音腔”）、name（给这个声音起一个 2~6 个字的好听名字）。`},

	{PromptSFX, "音效匹配", `你是音效师。请根据画本中的音效提示，从候选音效素材中选出最合适的一个；如果都不合适，给出用于 AI 生成音效的简短英文/中文描述。`},

	{PromptModerate, "内容审核", `你是内容安全审核员。请判断文本是否包含违法违规内容（如色情低俗、暴力血腥的过度描写、违法犯罪教唆、政治敏感、歧视仇恨、侵犯他人隐私等）。正常的文学创作（包括悬疑、战争、情感题材）不应误判。verdict 取值：pass（通过）、review（需要人工复核）、block（明确违规）。categories 给出命中的类别，reason 用一句话说明。`},

	{PromptChapters, "智能拆章", `下面是从一本书中摘取的较短的行（带行号）。请找出其中属于“章节标题”的行（例如“第一章”“楔子”“Chapter 1”或明显的小标题），返回它们的行号。不要把正文中的短句当成标题。`},

	{PromptBookIntro, "作品简介", `请根据书稿开头的内容，为这部有声书写一段吸引听众的简介（80~150 字），不剧透结局，语气生动；同时给出 3~5 个题材标签和一个题材分类（如：言情、悬疑、玄幻、历史、科幻、都市、儿童、文学、知识）。`},

	{PromptCover, "封面提示词", `请根据书名、题材和简介，写一段用于 AI 生成有声书封面的画面描述（中文，60 字以内），突出氛围、主色调和构图，不包含文字。`},
}

// Prompt returns the active prompt content for a key (admin edited or default).
// Gray release: several versions may be active; the newest one serves `rollout`% of calls and the
// remainder fall through to the next active version (the oldest active version always serves).
func (a *App) Prompt(key string) string {
	var list []model.PromptTemplate
	a.DB.Where("key = ? AND active = ?", key, true).Order("version desc").Find(&list)
	for i, p := range list {
		if strings.TrimSpace(p.Content) == "" {
			continue
		}
		if i == len(list)-1 || p.Rollout >= 100 || p.Rollout <= 0 && i == 0 || rand.Intn(100) < p.Rollout {
			return p.Content
		}
	}
	for _, d := range DefaultPrompts {
		if d.Key == key {
			return d.Content
		}
	}
	return ""
}

func fill(tpl string, vars map[string]string) string {
	for k, v := range vars {
		tpl = strings.ReplaceAll(tpl, "{{"+k+"}}", v)
	}
	return tpl
}

// JSON schema helpers (all objects closed, all properties required — valid for Claude
// structured outputs and OpenAI strict json_schema).
func obj(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}
func arr(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
func str() map[string]any          { return map[string]any{"type": "string"} }
func num() map[string]any          { return map[string]any{"type": "number"} }
func integer() map[string]any      { return map[string]any{"type": "integer"} }
func boolean() map[string]any      { return map[string]any{"type": "boolean"} }
func enum(vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals}
}

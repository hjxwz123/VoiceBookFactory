package core

import (
	"testing"

	"voicebook/internal/model"
)

func TestCuesFromLLM(t *testing.T) {
	text := "你这个人啊，真是拿你没办法。"
	got := CuesFromLLM(text, []LLMCue{
		{Tag: "laughing", After: "你这个人啊"},  // exact anchor
		{Tag: "sighing", After: "真是拿你没办法"}, // anchor before the final punctuation
		{Tag: "sarcastic", After: ""},      // start of line
		{Tag: "gasp", After: "不存在的片段"},     // unknown anchor: sounds are dropped
		{Tag: "excited", After: "完全找不到"},   // unknown anchor: styles fall back to the start
		{Tag: "made-up", After: "你"},       // unknown tag
		{Tag: "清嗓", After: "你 这个人"},        // label + whitespace-tolerant anchor
	}, []string{"叹气"})
	want := []model.Cue{{At: 0, Tag: "sighing"}, {At: 0, Tag: "sarcastic"}, {At: 0, Tag: "excited"}, {At: 4, Tag: "clears throat"}, {At: 5, Tag: "laughing"}, {At: 13, Tag: "sighing"}}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cue %d: got %v want %v (all %v)", i, got[i], want[i], got)
		}
	}
}

func TestRemapAnnotationsAfterEdit(t *testing.T) {
	old := "他走进银行，叹了口气。"
	prons := []model.Pronunciation{{Offset: 3, Char: "银"}, {Offset: 4, Char: "行", Pinyin: "háng"}}
	cues := []model.Cue{{At: 0, Tag: "tired"}, {At: 6, Tag: "sighing"}, {At: 11, Tag: "cough"}}
	// insert "慢慢地" before 走 and change nothing after
	p, c := RemapAnnotations(old, "他慢慢地走进银行，叹了口气。", prons, cues)
	if len(p) != 2 || p[1].Offset != 7 || p[1].Char != "行" {
		t.Errorf("prons %v", p)
	}
	if c[0].At != 0 || c[1].At != 9 || c[2].At != 14 {
		t.Errorf("cues %v", c)
	}
	// rewrite the bank: pronunciation on the replaced char disappears
	p2, _ := RemapAnnotations(old, "他走进商店，叹了口气。", prons, cues)
	if len(p2) != 0 {
		t.Errorf("stale pronunciations kept: %v", p2)
	}
}

func TestBuildTTSInputMappedShiftsCues(t *testing.T) {
	lex := []model.LexiconEntry{{Term: "Dr.", Replacement: "博士"}}
	out, _, pos := BuildTTSInputMapped("Dr.王笑了", nil, lex, true)
	if out != "博士王笑了" {
		t.Fatalf("out %q", out)
	}
	cues := mapCues([]model.Cue{{At: 4, Tag: "laughing"}, {At: 0, Tag: "serious"}, {At: 6, Tag: "sighing"}}, pos)
	if cues[0].At != 3 || cues[1].At != 0 || cues[2].At != 5 {
		t.Errorf("mapped %v", cues)
	}
}

func TestCleanInstruction(t *testing.T) {
	cases := map[string]string{
		"角色台词，语气冷淡，带质问意味。":                "语气冷淡，带质问意味",
		"说话提示语，标明说话人及冷淡语气，广播剧模式可删除。":      "",
		"旁白叙述，语速稍缓，营造夜深氛围，末尾带出轻叹。":        "语速稍缓，营造夜深氛围，末尾带出轻叹",
		"压低声音，带着试探":                       "压低声音，带着试探",
		"前半句强忍，后半句爆发，最后一个字几乎是喊出来的，然后停顿很久": "前半句强忍，后半句爆发，最后一个字几乎是喊出来的",
		"": "",
	}
	for in, want := range cases {
		if got := CleanInstruction(in); got != want {
			t.Errorf("CleanInstruction(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNarrationDropsSoundCues(t *testing.T) {
	cs := []model.Cue{{At: 0, Tag: "tired"}, {At: 10, Tag: "sighing"}}
	got := withoutSoundCues(cs)
	if len(got) != 1 || got[0].Tag != "tired" {
		t.Fatalf("got %v", got)
	}
}

package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/ai/tts"
	"voicebook/internal/core"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

func (s *Server) characterView(c *model.Character) map[string]any {
	var voice map[string]any
	if c.VoiceID != "" {
		var v model.Voice
		if s.app.DB.First(&v, "id = ?", c.VoiceID).Error == nil {
			voice = map[string]any{"id": v.ID, "name": v.Name, "gender": v.Gender, "ageGroup": v.AgeGroup, "tags": v.Tags.V, "provider": v.Provider, "priceTier": v.PriceTier, "sampleUrl": s.app.VoiceSampleURL(&v), "status": v.Status,
				"model": v.Model, "modelLabel": tts.ModelLabel(v.Model), "capabilities": core.VoiceCaps(&v)}
		}
	}
	var assignee map[string]any
	if c.AssigneeID != "" {
		var u model.User
		if s.app.DB.Select("id", "nickname", "avatar_url").First(&u, "id = ?", c.AssigneeID).Error == nil {
			assignee = map[string]any{"id": u.ID, "nickname": u.Nickname, "avatarUrl": u.AvatarURL}
		}
	}
	casting := []map[string]any{}
	for _, cs := range c.Casting.V {
		var v model.Voice
		if s.app.DB.First(&v, "id = ?", cs.VoiceID).Error != nil {
			continue
		}
		casting = append(casting, map[string]any{"voiceId": cs.VoiceID, "score": cs.Score, "reason": cs.Reason, "voice": map[string]any{
			"id": v.ID, "name": v.Name, "gender": v.Gender, "ageGroup": v.AgeGroup, "tags": v.Tags.V, "provider": v.Provider, "priceTier": v.PriceTier,
			"sampleUrl": s.app.VoiceSampleURL(&v), "status": v.Status, "description": v.Description, "model": v.Model, "modelLabel": tts.ModelLabel(v.Model)}})
	}
	return map[string]any{
		"id": c.ID, "bookId": c.BookID, "name": c.Name, "aliases": c.Aliases.V, "kind": c.Kind, "gender": c.Gender, "ageGroup": c.AgeGroup,
		"identity": c.Identity, "personality": c.Personality, "voiceTraits": c.VoiceTraits, "profile": c.Profile, "firstChapter": c.FirstChapter,
		"lineCount": c.LineCount, "charCount": c.CharCount, "voiceId": c.VoiceID, "voice": voice, "params": c.Params.V, "mode": c.Mode,
		"assigneeId": c.AssigneeID, "assignee": assignee, "claimStatus": c.ClaimStatus, "claimantId": c.ClaimantID, "color": c.Color,
		"avatarUrl": c.AvatarURL, "candidate": c.Candidate, "useCrowdPool": c.UseCrowdPool, "sortOrder": c.SortOrder, "casting": casting,
	}
}

func (s *Server) loadCharacter(r *http.Request) (*core.Access, *model.Character, error) {
	var c model.Character
	if err := s.app.DB.First(&c, "id = ?", param(r, "chid")).Error; err != nil {
		return nil, nil, core.NotFound("角色")
	}
	x, err := s.app.BookAccess(userOf(r), c.BookID)
	return x, &c, err
}

func (s *Server) productionRoutes(r chi.Router) {
	// ======================= characters =======================
	r.Get("/books/{bid}/characters", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var cs []model.Character
		s.app.DB.Where("book_id = ?", x.Book.ID).Order("candidate asc, CASE kind WHEN 'narrator' THEN 0 WHEN 'lead' THEN 1 WHEN 'supporting' THEN 2 WHEN 'minor' THEN 3 ELSE 4 END, line_count desc, sort_order asc").Find(&cs)
		out := make([]map[string]any, 0, len(cs))
		for i := range cs {
			out = append(out, s.characterView(&cs[i]))
		}
		return out, nil
	}))
	r.Post("/books/{bid}/characters", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		var in model.Character
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" {
			return nil, core.Bad("请填写角色名")
		}
		var n int64
		s.app.DB.Model(&model.Character{}).Where("book_id = ?", x.Book.ID).Count(&n)
		c := model.Character{BookID: x.Book.ID, Name: strings.TrimSpace(in.Name), Aliases: in.Aliases, Kind: firstNonEmptyStr(in.Kind, model.CharSupporting), Gender: firstNonEmptyStr(in.Gender, "unknown"),
			AgeGroup: firstNonEmptyStr(in.AgeGroup, "adult"), Identity: in.Identity, Personality: in.Personality, VoiceTraits: in.VoiceTraits, Profile: in.Profile, Mode: "ai",
			Color: firstNonEmptyStr(in.Color, []string{"#6366f1", "#ec4899", "#0ea5e9", "#f59e0b", "#10b981", "#8b5cf6", "#ef4444", "#14b8a6"}[n%8]), SortOrder: int(n)}
		if c.Aliases.V == nil {
			c.Aliases.V = []string{}
		}
		c.Casting.V = []model.CastingCandidate{}
		return s.characterView(&c), s.app.DB.Create(&c).Error
	}))
	r.Post("/books/{bid}/characters/analyze", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		var in struct {
			ChapterIDs []string `json:"chapterIds"`
		}
		bind(r, &in)
		j, err := s.app.EnqueueCharacterAnalysis(x, x.Book.ID)
		if err != nil {
			return nil, err
		}
		if len(in.ChapterIDs) > 0 {
			s.app.DB.Model(j).Update("payload", model.Map{V: map[string]any{"chapterIds": in.ChapterIDs}})
		}
		return j, nil
	}))
	r.Post("/books/{bid}/casting", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermCasting); err != nil {
			return nil, err
		}
		var in struct {
			CharacterIDs []string `json:"characterIds"`
			Apply        bool     `json:"apply"`
		}
		bind(r, &in)
		if len(in.CharacterIDs) == 1 {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			res, err := s.app.Cast(ctx, x, in.CharacterIDs, in.Apply, nil)
			return map[string]any{"result": res}, err
		}
		j := &model.Job{Type: "casting", StudioID: x.StudioID, BookID: x.Book.ID, RefType: "book", RefID: x.Book.ID, Title: "AI 选角", CreatedBy: x.User.ID, MaxAttempts: 1,
			Payload: model.Map{V: map[string]any{"characterIds": toAnySlice(in.CharacterIDs), "apply": in.Apply}}}
		return map[string]any{"job": j}, s.app.Q.Enqueue(j)
	}))
	r.Route("/characters/{chid}", func(r chi.Router) {
		r.Patch("/", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				Name         *string            `json:"name"`
				Aliases      *[]string          `json:"aliases"`
				Kind         *string            `json:"kind"`
				Gender       *string            `json:"gender"`
				AgeGroup     *string            `json:"ageGroup"`
				Identity     *string            `json:"identity"`
				Personality  *string            `json:"personality"`
				VoiceTraits  *string            `json:"voiceTraits"`
				Profile      *string            `json:"profile"`
				Params       *model.VoiceParams `json:"params"`
				Color        *string            `json:"color"`
				Candidate    *bool              `json:"candidate"`
				UseCrowdPool *bool              `json:"useCrowdPool"`
				Mode         *string            `json:"mode"`
				AssigneeID   *string            `json:"assigneeId"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			scriptChange := in.Name != nil || in.Aliases != nil || in.Kind != nil || in.Gender != nil || in.AgeGroup != nil || in.Identity != nil || in.Personality != nil || in.VoiceTraits != nil || in.Profile != nil || in.Color != nil || in.Candidate != nil
			if scriptChange {
				if err := core.Need(x, core.PermScript); err != nil {
					return nil, err
				}
			}
			if in.Params != nil || in.UseCrowdPool != nil || in.Mode != nil || in.AssigneeID != nil {
				if err := core.Need(x, core.PermCasting); err != nil {
					return nil, err
				}
			}
			up := map[string]any{}
			str := func(col string, v *string) {
				if v != nil {
					up[col] = strings.TrimSpace(*v)
				}
			}
			str("name", in.Name)
			str("kind", in.Kind)
			str("gender", in.Gender)
			str("age_group", in.AgeGroup)
			str("identity", in.Identity)
			str("personality", in.Personality)
			str("voice_traits", in.VoiceTraits)
			str("profile", in.Profile)
			str("color", in.Color)
			if in.Aliases != nil {
				up["aliases"] = model.Strings(*in.Aliases...)
			}
			if in.Candidate != nil {
				up["candidate"] = *in.Candidate
			}
			if in.UseCrowdPool != nil {
				up["use_crowd_pool"] = *in.UseCrowdPool
			}
			if in.Params != nil {
				up["params"] = model.JSON[model.VoiceParams]{V: *in.Params}
				s.app.DB.Model(&model.Segment{}).Where("character_id = ? AND audio_status = ?", c.ID, model.AudioReady).Update("audio_status", model.AudioStale)
			}
			if in.Mode != nil {
				up["mode"] = *in.Mode
			}
			if in.AssigneeID != nil {
				up["assignee_id"] = *in.AssigneeID
				up["claim_status"] = ""
				if *in.AssigneeID != "" {
					up["claim_status"] = "confirmed"
					// make sure the CV is on the crew with the voice position
					var bm model.BookMember
					if s.app.DB.First(&bm, "book_id = ? AND user_id = ?", c.BookID, *in.AssigneeID).Error != nil {
						s.app.DB.Create(&model.BookMember{BookID: c.BookID, UserID: *in.AssigneeID, Positions: model.Strings(model.PosVoice), AddedBy: x.User.ID})
					} else if !containsStr(bm.Positions.V, model.PosVoice) {
						s.app.DB.Model(&bm).Update("positions", model.Strings(append(bm.Positions.V, model.PosVoice)...))
					}
					s.app.Notify(*in.AssigneeID, x.StudioID, "role_assigned", "你被指派为《"+x.Book.Title+"》「"+c.Name+"」的配音", "在「我的台词」中查看并录制", "/my-lines")
				}
			}
			if err := s.app.DB.Model(c).Updates(up).Error; err != nil {
				return nil, err
			}
			s.app.DB.First(c, "id = ?", c.ID)
			if in.Mode != nil || in.AssigneeID != nil {
				s.app.EnsureChapterTasks(c.BookID)
			}
			return s.characterView(c), nil
		}))
		r.Delete("/", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			if c.Kind == model.CharNarrator {
				var n int64
				s.app.DB.Model(&model.Character{}).Where("book_id = ? AND kind = ?", c.BookID, model.CharNarrator).Count(&n)
				if n <= 1 {
					return nil, core.Bad("至少需要保留一个旁白")
				}
			}
			s.app.DB.Model(&model.Segment{}).Where("character_id = ?", c.ID).Updates(map[string]any{"character_id": "", "needs_review": true})
			return nil, s.app.DB.Delete(c).Error
		}))
		r.Post("/merge", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			var in struct{ IntoID string }
			bind(r, &in)
			return nil, s.app.MergeCharacters(x, in.IntoID, c.ID)
		}))
		r.Get("/recast-impact", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			n, chars := s.app.AffectedByRecast(c)
			tier := ""
			if vid := r.URL.Query().Get("voiceId"); vid != "" {
				var v model.Voice
				if s.app.DB.First(&v, "id = ?", vid).Error == nil {
					tier = v.PriceTier
				}
			}
			_ = x
			return map[string]any{"segments": n, "chars": chars, "credits": s.app.Estimate(core.FeatTTS, tier, float64(chars))}, nil
		}))
		r.Put("/voice", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			var in struct{ VoiceID string }
			bind(r, &in)
			n, err := s.app.SetCharacterVoice(x, c, in.VoiceID)
			if err != nil {
				return nil, err
			}
			s.app.DB.First(c, "id = ?", c.ID)
			return map[string]any{"character": s.characterView(c), "staleSegments": n}, nil
		}))
		r.Get("/audition-lines", h(func(r *http.Request) (any, error) {
			_, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			return s.app.AuditionLines(c, 3), nil
		}))
		r.Post("/audition", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				VoiceID string  `json:"voiceId"`
				Text    string  `json:"text"`
				Emotion string  `json:"emotion"`
				Speed   float64 `json:"speed"`
			}
			bind(r, &in)
			var v model.Voice
			if err := s.app.DB.First(&v, "id = ?", in.VoiceID).Error; err != nil {
				return nil, core.NotFound("音色")
			}
			if !s.app.VoiceVisible(x.User, x.StudioID, &v) {
				return nil, core.Forbidden("")
			}
			txt := in.Text
			if txt == "" {
				txt = s.app.AuditionLines(c, 1)[0]
			}
			url, err := s.app.Preview(r.Context(), x, &v, txt, in.Emotion, firstNonZeroF(in.Speed, f64ptr(c.Params.V.Speed)), f64ptr(c.Params.V.Pitch))
			return map[string]any{"url": url, "text": txt}, err
		}))
		r.Post("/describe-voice", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			h, err := s.app.LLMFor(core.FeatDesign, "", x.StudioID)
			if err != nil {
				return map[string]any{"description": strings.TrimSpace(c.VoiceTraits + " " + c.Personality)}, nil
			}
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			d, err := s.app.DescribeVoice(ctx, h, *c)
			return map[string]any{"description": d}, err
		}))
		r.Post("/claim", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			if c.AssigneeID != "" {
				return nil, core.Bad("该角色已有配音演员")
			}
			s.app.DB.Model(c).Updates(map[string]any{"claim_status": "pending", "claimant_id": x.User.ID})
			s.app.NotifyMany([]string{x.Book.LeadID}, x.StudioID, "claim", x.User.Nickname+" 申请认领角色「"+c.Name+"」", "《"+x.Book.Title+"》", "/books/"+x.Book.ID+"/characters")
			return nil, nil
		}))
		r.Post("/claim/review", h(func(r *http.Request) (any, error) {
			x, c, err := s.loadCharacter(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermCasting); err != nil {
				return nil, err
			}
			var in struct{ Approve bool }
			bind(r, &in)
			if in.Approve {
				s.app.DB.Model(c).Updates(map[string]any{"claim_status": "confirmed", "assignee_id": c.ClaimantID, "mode": "human"})
				var bm model.BookMember
				if s.app.DB.First(&bm, "book_id = ? AND user_id = ?", c.BookID, c.ClaimantID).Error != nil {
					s.app.DB.Create(&model.BookMember{BookID: c.BookID, UserID: c.ClaimantID, Positions: model.Strings(model.PosVoice), AddedBy: x.User.ID})
				}
				s.app.Notify(c.ClaimantID, x.StudioID, "claim", "认领成功：「"+c.Name+"」", "现在可以在「我的台词」中开始录音了", "/my-lines")
				s.app.EnsureChapterTasks(c.BookID)
			} else {
				s.app.DB.Model(c).Updates(map[string]any{"claim_status": "", "claimant_id": ""})
				s.app.Notify(c.ClaimantID, x.StudioID, "claim", "认领未通过：「"+c.Name+"」", "", "/books/"+c.BookID)
			}
			return nil, nil
		}))
	})

	// ======================= lexicon (book) =======================
	r.Get("/books/{bid}/lexicon", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var list []model.LexiconEntry
		s.app.DB.Where("(scope = 'book' AND book_id = ?) OR (scope = 'studio' AND studio_id = ?) OR (scope = 'platform' AND status = 'active')", x.Book.ID, x.StudioID).Order("scope asc, created_at desc").Find(&list)
		// how often each book term appears in the manuscript helps decide which candidates matter
		var texts []string
		s.app.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", x.Book.ID, true).Pluck("source_text", &texts)
		all := strings.Join(texts, "\n")
		out := make([]lexiconView, 0, len(list))
		for _, e := range list {
			v := lexiconView{LexiconEntry: e}
			if e.Term != "" && (e.Scope == "book" || strings.Contains(all, e.Term)) {
				v.Occurrences = strings.Count(all, e.Term)
			}
			if e.Scope != "book" && v.Occurrences == 0 {
				continue // studio/platform entries that never occur in this book are noise here
			}
			out = append(out, v)
		}
		return out, nil
	}))
	r.Post("/books/{bid}/lexicon", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		var in model.LexiconEntry
		bind(r, &in)
		in.Term = strings.TrimSpace(in.Term)
		if in.Term == "" {
			return nil, core.Bad("请填写词条")
		}
		var dup int64
		s.app.DB.Model(&model.LexiconEntry{}).Where("scope = 'book' AND book_id = ? AND term = ?", x.Book.ID, in.Term).Count(&dup)
		if dup > 0 {
			return nil, core.Conflict("「" + in.Term + "」已经在本书读音词典里了，直接修改那一条即可")
		}
		e := model.LexiconEntry{Scope: "book", StudioID: x.StudioID, BookID: x.Book.ID, Term: in.Term, Pinyin: strings.TrimSpace(in.Pinyin), Replacement: strings.TrimSpace(in.Replacement), Note: in.Note, Status: "active", CreatedBy: x.User.ID}
		if err := s.app.DB.Create(&e).Error; err != nil {
			return nil, err
		}
		return lexiconView{LexiconEntry: e, StaleSegments: s.app.StaleLinesWithTerms(x.Book.ID, e.Term)}, nil
	}))
	r.Post("/books/{bid}/lexicon/activate", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		var in struct {
			IDs []string `json:"ids"`
		}
		bind(r, &in)
		var list []model.LexiconEntry
		s.app.DB.Where("scope = 'book' AND book_id = ? AND id IN ?", x.Book.ID, in.IDs).Find(&list)
		var terms []string
		for _, e := range list {
			terms = append(terms, e.Term)
		}
		if len(list) > 0 {
			s.app.DB.Model(&model.LexiconEntry{}).Where("scope = 'book' AND book_id = ? AND id IN ?", x.Book.ID, in.IDs).Update("status", "active")
		}
		return map[string]any{"activated": len(list), "staleSegments": s.app.StaleLinesWithTerms(x.Book.ID, terms...)}, nil
	}))
	r.Post("/books/{bid}/lexicon/extract", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		return s.app.ExtractBookLexicon(ctx, x)
	}))
	r.Patch("/lexicon/{id}", h(func(r *http.Request) (any, error) {
		var e model.LexiconEntry
		if err := s.app.DB.First(&e, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("词条")
		}
		if err := s.lexiconGuard(r, &e); err != nil {
			return nil, err
		}
		var in struct {
			Term, Pinyin, Replacement, Note, Status *string
		}
		bind(r, &in)
		up := map[string]any{}
		for col, v := range map[string]*string{"term": in.Term, "pinyin": in.Pinyin, "replacement": in.Replacement, "note": in.Note, "status": in.Status} {
			if v != nil {
				up[col] = strings.TrimSpace(*v)
			}
		}
		if t, ok := up["term"].(string); ok && t == "" {
			return nil, core.Bad("词条不能为空")
		}
		if err := s.app.DB.Model(&e).Updates(up).Error; err != nil {
			return nil, err
		}
		var after model.LexiconEntry
		s.app.DB.First(&after, "id = ?", e.ID)
		stale := 0
		readingChanged := after.Term != e.Term || after.Pinyin != e.Pinyin || after.Replacement != e.Replacement || after.Status != e.Status
		if e.Scope == "book" && readingChanged && (e.Status == "active" || after.Status == "active") {
			stale = s.app.StaleLinesWithTerms(e.BookID, e.Term, after.Term)
		}
		return lexiconView{LexiconEntry: after, StaleSegments: stale}, nil
	}))
	r.Delete("/lexicon/{id}", h(func(r *http.Request) (any, error) {
		var e model.LexiconEntry
		if err := s.app.DB.First(&e, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("词条")
		}
		if err := s.lexiconGuard(r, &e); err != nil {
			return nil, err
		}
		if err := s.app.DB.Delete(&e).Error; err != nil {
			return nil, err
		}
		stale := 0
		if e.Scope == "book" && e.Status == "active" {
			stale = s.app.StaleLinesWithTerms(e.BookID, e.Term)
		}
		return map[string]any{"staleSegments": stale}, nil
	}))
	r.Post("/lexicon/{id}/submit", h(func(r *http.Request) (any, error) {
		var e model.LexiconEntry
		if err := s.app.DB.First(&e, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("词条")
		}
		if err := s.lexiconGuard(r, &e); err != nil {
			return nil, err
		}
		cp := model.LexiconEntry{Scope: "platform", Term: e.Term, Pinyin: e.Pinyin, Replacement: e.Replacement, Note: e.Note, Status: "submitted", CreatedBy: userOf(r).ID, SourceID: e.ID}
		return nil, s.app.DB.Create(&cp).Error
	}))
	r.Get("/pinyin", h(func(r *http.Request) (any, error) {
		return core.PinyinOptions(r.URL.Query().Get("char")), nil
	}))
	r.Get("/pinyin/word", h(func(r *http.Request) (any, error) {
		return core.WordReadings(r.URL.Query().Get("text")), nil
	}))

	// ======================= scripts & segments =======================
	r.Post("/books/{bid}/scripts/parse", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChapterIDs []string `json:"chapterIds"`
			Mode       string   `json:"mode"`
		}
		bind(r, &in)
		return s.app.EnqueueScriptParse(x, in.ChapterIDs, in.Mode)
	}))
	r.Get("/chapters/{cid}/script", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var versions []model.Script
		s.app.DB.Where("chapter_id = ?", ch.ID).Order("version desc").Find(&versions)
		var cur *model.Script
		for i := range versions {
			if versions[i].ID == ch.CurrentScriptID {
				cur = &versions[i]
			}
		}
		segs := []map[string]any{}
		if cur != nil {
			var list []model.Segment
			s.app.DB.Where("script_id = ?", cur.ID).Order("seq asc").Find(&list)
			segs = s.app.SegmentViews(list)
		}
		var lockUser string
		if ch.EditLockUserID != "" && ch.EditLockAt != nil && ch.EditLockAt.After(time.Now().Add(-2*time.Minute)) {
			var u model.User
			s.app.DB.Select("nickname").First(&u, "id = ?", ch.EditLockUserID)
			lockUser = u.Nickname
		}
		var running []model.Job
		s.app.DB.Where("chapter_id = ? AND status IN ?", ch.ID, []string{model.JobQueued, model.JobRunning}).Find(&running)
		return map[string]any{"script": cur, "segments": segs, "versions": versions, "lock": map[string]any{"userId": ch.EditLockUserID, "nickname": lockUser, "mine": ch.EditLockUserID == x.User.ID},
			"jobs": running, "scriptStale": ch.ScriptStale, "permissions": x.PermList(), "voiceOnly": x.VoiceOnly}, nil
	}))
	r.Post("/chapters/{cid}/scripts/{sid2}/activate", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.ActivateScript(x, ch, param(r, "sid2"))
	}))
	r.Get("/chapters/{cid}/scripts/compare", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b")
		var n int64
		s.app.DB.Model(&model.Script{}).Where("chapter_id = ? AND id IN ?", ch.ID, []string{a, b}).Count(&n)
		if n != 2 {
			return nil, core.Bad("版本不存在")
		}
		return s.app.CompareScripts(a, b), nil
	}))
	r.Get("/chapters/{cid}/integrity", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		return s.app.IntegrityCheck(ch), nil
	}))
	r.Post("/chapters/{cid}/lock", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct{ Force bool }
		bind(r, &in)
		got, who := s.app.AcquireLock(x, ch, in.Force)
		return map[string]any{"acquired": got, "holder": who}, nil
	}))
	r.Delete("/chapters/{cid}/lock", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		s.app.ReleaseLock(x, ch)
		return nil, nil
	}))
	r.Post("/chapters/{cid}/presence", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			SegmentID string `json:"segmentId"`
			Leave     bool   `json:"leave"`
		}
		bind(r, &in)
		return s.app.Touch(x, ch, in.SegmentID, in.Leave), nil
	}))
	r.Post("/characters/{chid}/avatar", h(func(r *http.Request) (any, error) {
		x, c, err := s.loadCharacter(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		as, err := s.saveUpload(r, "file", core.AssetInput{StudioID: x.StudioID, Kind: model.AssetImage, Name: c.Name + " 头像", Origin: "upload", CreatedBy: x.User.ID})
		if err != nil {
			return nil, err
		}
		url := s.app.Store.SignedPath(as.StorageKey, 10*365*24*time.Hour, "")
		s.app.DB.Model(c).Update("avatar_url", url)
		return map[string]any{"url": url}, nil
	}))
	r.Post("/characters/{chid}/avatar/generate", h(func(r *http.Request) (any, error) {
		x, c, err := s.loadCharacter(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		prompt := strings.TrimSpace(c.Name + "，" + c.Identity + "，" + c.Personality + "，人物肖像")
		return s.app.EnqueueCharacterAvatar(x, c, prompt)
	}))
	r.Post("/chapters/{cid}/segments", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct{ AfterID, Type, Text, CharacterID string }
		bind(r, &in)
		segs, err := s.app.InsertSegment(x, ch, in.AfterID, in.Type, in.Text, in.CharacterID)
		if err != nil {
			return nil, err
		}
		return s.app.SegmentViews(segs), nil
	}))
	r.Patch("/books/{bid}/segments", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			IDs   []string          `json:"ids"`
			Patch core.SegmentPatch `json:"patch"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		segs, err := s.app.UpdateSegments(x, in.IDs, in.Patch)
		if err != nil {
			return nil, err
		}
		return s.app.SegmentViews(segs), nil
	}))
	r.Post("/books/{bid}/segments/merge", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			IDs []string `json:"ids"`
		}
		bind(r, &in)
		segs, err := s.app.MergeSegments(x, in.IDs)
		if err != nil {
			return nil, err
		}
		return s.app.SegmentViews(segs), nil
	}))
	r.Post("/books/{bid}/segments/delete", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			IDs []string `json:"ids"`
		}
		bind(r, &in)
		deleted, locked, err := s.app.DeleteSegments(x, in.IDs)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": deleted, "locked": locked}, nil
	}))
	r.Post("/books/{bid}/segments/restore", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Segments []model.Segment `json:"segments"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		n, err := s.app.RestoreSegments(x, in.Segments)
		return map[string]any{"restored": n}, err
	}))
	r.Post("/segments/{segid}/split", h(func(r *http.Request) (any, error) {
		x, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		var in struct{ At int }
		bind(r, &in)
		segs, err := s.app.SplitSegment(x, seg, in.At)
		if err != nil {
			return nil, err
		}
		return s.app.SegmentViews(segs), nil
	}))
	r.Post("/segments/{segid}/move", h(func(r *http.Request) (any, error) {
		x, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Dir     int     `json:"dir"`
			AfterID *string `json:"afterId"` // present (even "") = move right after that line; "" = to the top
		}
		bind(r, &in)
		var segs []model.Segment
		if in.AfterID != nil {
			segs, err = s.app.MoveSegmentAfter(x, seg, *in.AfterID)
		} else {
			segs, err = s.app.MoveSegment(x, seg, in.Dir)
		}
		if err != nil {
			return nil, err
		}
		return s.app.SegmentViews(segs), nil
	}))
	r.Get("/segments/{segid}/takes", h(func(r *http.Request) (any, error) {
		_, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		var takes []model.Take
		s.app.DB.Where("segment_id = ?", seg.ID).Order("created_at desc").Find(&takes)
		var out []map[string]any
		for _, t := range takes {
			var u model.User
			s.app.DB.Select("nickname").First(&u, "id = ?", t.CreatedBy)
			var vname string
			if t.VoiceID != "" {
				var v model.Voice
				s.app.DB.Select("name").First(&v, "id = ?", t.VoiceID)
				vname = v.Name
			}
			out = append(out, map[string]any{"id": t.ID, "origin": t.Origin, "voiceId": t.VoiceID, "voiceName": vname, "provider": t.Provider, "durationMs": t.DurationMs,
				"url": s.app.AssetURLByID(t.AssetID), "createdAt": t.CreatedAt, "createdBy": u.Nickname, "selected": t.ID == seg.SelectedTakeID, "qc": t.QC, "qcDetail": t.QCDetail.V,
				"credits": t.Credits, "note": t.Note, "params": t.Params.V, "current": t.ContentHash == seg.ContentHash})
		}
		return out, nil
	}))
	r.Post("/segments/{segid}/takes/{tid}/select", h(func(r *http.Request) (any, error) {
		x, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		if err := s.app.SelectTake(x, seg, param(r, "tid")); err != nil {
			return nil, err
		}
		var fresh model.Segment
		s.app.DB.First(&fresh, "id = ?", seg.ID)
		return s.app.SegmentViews([]model.Segment{fresh})[0], nil
	}))
	r.Delete("/segments/{segid}/takes/{tid}", h(func(r *http.Request) (any, error) {
		x, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		if !x.Can(core.PermGenerate) && !x.Can(core.PermRecord) {
			return nil, core.Forbidden("")
		}
		if param(r, "tid") == seg.SelectedTakeID {
			return nil, core.Bad("不能删除正在使用的版本，请先选择其他版本")
		}
		return nil, s.app.DB.Where("id = ? AND segment_id = ?", param(r, "tid"), seg.ID).Delete(&model.Take{}).Error
	}))
	r.Post("/segments/{segid}/record", h(func(r *http.Request) (any, error) {
		x, seg, err := s.segment(r)
		if err != nil {
			return nil, err
		}
		path, _, cleanup, err := s.tempUpload(r, "file")
		if err != nil {
			return nil, err
		}
		defer cleanup()
		origin := model.TakeRecord
		if r.FormValue("origin") == "upload" {
			origin = model.TakeUpload
		}
		t, hints, err := s.app.SaveRecordedTake(r.Context(), x, seg, path, origin, r.FormValue("denoise") == "1")
		if err != nil {
			return nil, err
		}
		return map[string]any{"take": t, "hints": hints}, nil
	}))
	r.Post("/books/{bid}/reassign", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct{ ChapterID, FromID, ToID string }
		bind(r, &in)
		n, err := s.app.ReassignCharacter(x, in.ChapterID, in.FromID, in.ToID)
		return map[string]any{"updated": n}, err
	}))
	r.Post("/books/{bid}/find-replace", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChapterID, Find, Replace string
			Apply                    bool
		}
		bind(r, &in)
		n, matches, err := s.app.FindReplace(x, in.ChapterID, in.Find, in.Replace, in.Apply)
		return map[string]any{"count": n, "matches": matches}, err
	}))
	r.Post("/books/{bid}/pronounce", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Char, Word, Pinyin string
			Apply              bool
		}
		bind(r, &in)
		n, err := s.app.BatchPronounce(x, in.Char, in.Word, in.Pinyin, in.Apply)
		return map[string]any{"count": n}, err
	}))
	r.Post("/books/{bid}/script-doc", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChapterIDs []string `json:"chapterIds"`
			Kind       string   `json:"kind"`
		}
		bind(r, &in)
		if in.Kind != "xlsx" {
			in.Kind = "docx"
		}
		url, name, err := s.app.ScriptDocument(x, in.ChapterIDs, in.Kind)
		return map[string]any{"url": url, "name": name}, err
	}))

	// ======================= generation, timeline, render =======================
	r.Post("/books/{bid}/generate/estimate", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChapterIDs []string `json:"chapterIds"`
			core.GenerateRequest
		}
		bind(r, &in)
		return s.app.EstimateGeneration(x, in.ChapterIDs, in.GenerateRequest)
	}))
	r.Post("/books/{bid}/generate", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChapterIDs []string `json:"chapterIds"`
			core.GenerateRequest
		}
		bind(r, &in)
		return s.app.EnqueueGeneration(x, in.ChapterIDs, in.GenerateRequest)
	}))
	r.Get("/chapters/{cid}/timeline", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		return s.app.BuildTimeline(ch, true)
	}))
	r.Put("/chapters/{cid}/mix", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Tracks    []model.Track `json:"tracks"`
			DuckingDb float64       `json:"duckingDb"`
			Intro     bool          `json:"intro"`
			Outro     bool          `json:"outro"`
			Version   int           `json:"version"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return s.app.SaveMix(x, ch, in.Tracks, in.DuckingDb, in.Intro, in.Outro, in.Version)
	}))
	r.Post("/chapters/{cid}/render", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Preset       string `json:"preset"`
			AllowMissing bool   `json:"allowMissing"`
		}
		bind(r, &in)
		return s.app.EnqueueRender(x, ch, in.Preset, in.AllowMissing)
	}))
	r.Get("/chapters/{cid}/renders", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var list []model.Render
		s.app.DB.Where("chapter_id = ?", ch.ID).Order("created_at desc").Limit(10).Find(&list)
		var out []map[string]any
		for _, rr := range list {
			var as model.Asset
			s.app.DB.First(&as, "id = ?", rr.AssetID)
			out = append(out, map[string]any{"id": rr.ID, "status": rr.Status, "durationMs": rr.DurationMs, "preset": rr.Preset, "format": rr.Format, "report": rr.Report.V,
				"error": rr.Error, "createdAt": rr.CreatedAt, "current": rr.ID == ch.RenderID, "url": s.app.AssetURL(&as),
				"downloadUrl": s.app.AssetDownloadURL(&as, ch.Title+"."+rr.Format), "size": as.Size, "peaksAssetId": as.ID})
		}
		return out, nil
	}))
	r.Get("/chapters/{cid}/subtitles", func(w http.ResponseWriter, r *http.Request) {
		_, ch, err := s.chapter(r)
		if err != nil {
			fail(w, err)
			return
		}
		var rr model.Render
		if s.app.DB.First(&rr, "id = ?", ch.RenderID).Error != nil {
			fail(w, core.Bad("请先合成成品"))
			return
		}
		ok(w, rr.Timings.V)
	})
	r.Post("/chapters/{cid}/renders/{rid}/restore", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermPost); err != nil {
			return nil, err
		}
		var rr model.Render
		if err := s.app.DB.First(&rr, "id = ? AND chapter_id = ? AND status = ?", param(r, "rid"), ch.ID, "ready").Error; err != nil {
			return nil, core.NotFound("成品版本")
		}
		return nil, s.app.DB.Model(ch).Updates(map[string]any{"render_id": rr.ID, "duration_ms": rr.DurationMs}).Error
	}))

	// ======================= alignment & review =======================
	r.Post("/chapters/{cid}/recordings", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			AssetIDs    []string `json:"assetIds"`
			Mode        string   `json:"mode"`
			CharacterID string   `json:"characterId"`
		}
		bind(r, &in)
		return s.app.CreateRecording(x, ch, in.AssetIDs, in.Mode, in.CharacterID)
	}))
	r.Get("/chapters/{cid}/recordings", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var list []model.Recording
		s.app.DB.Where("chapter_id = ?", ch.ID).Order("created_at desc").Find(&list)
		return list, nil
	}))
	r.Get("/recordings/{rid}", h(func(r *http.Request) (any, error) {
		var rec model.Recording
		if err := s.app.DB.First(&rec, "id = ?", param(r, "rid")).Error; err != nil {
			return nil, core.NotFound("录音")
		}
		if _, err := s.app.BookAccess(userOf(r), rec.BookID); err != nil {
			return nil, err
		}
		var as model.Asset
		s.app.DB.First(&as, "id = ?", rec.MergedAsset)
		return map[string]any{"recording": rec, "mergedUrl": s.app.AssetURL(&as), "mergedAssetId": as.ID}, nil
	}))
	r.Post("/recordings/{rid}/adjust", h(func(r *http.Request) (any, error) {
		var rec model.Recording
		if err := s.app.DB.First(&rec, "id = ?", param(r, "rid")).Error; err != nil {
			return nil, core.NotFound("录音")
		}
		x, err := s.app.BookAccess(userOf(r), rec.BookID)
		if err != nil {
			return nil, err
		}
		var in struct {
			SegmentID      string `json:"segmentId"`
			StartMs, EndMs int64
		}
		bind(r, &in)
		return s.app.AdjustAlignment(r.Context(), x, &rec, in.SegmentID, in.StartMs, in.EndMs)
	}))
	r.Post("/chapters/{cid}/review", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct{ AutoIssue bool }
		bind(r, &in)
		return s.app.EnqueueReview(x, ch, in.AutoIssue)
	}))
	r.Get("/chapters/{cid}/reviews", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var list []model.ReviewRun
		s.app.DB.Where("chapter_id = ?", ch.ID).Order("created_at desc").Limit(10).Find(&list)
		return list, nil
	}))
	r.Post("/reviews/{rid}/findings/{fid}", h(func(r *http.Request) (any, error) {
		var run model.ReviewRun
		if err := s.app.DB.First(&run, "id = ?", param(r, "rid")).Error; err != nil {
			return nil, core.NotFound("审听记录")
		}
		x, err := s.app.BookAccess(userOf(r), run.BookID)
		if err != nil {
			return nil, err
		}
		var in struct{ Action, Assignee string }
		bind(r, &in)
		return s.app.FindingAction(x, &run, param(r, "fid"), in.Action, in.Assignee)
	}))
	r.Post("/chapters/{cid}/conclude", h(func(r *http.Request) (any, error) {
		x, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Pass           bool
			Reason, BackTo string
		}
		bind(r, &in)
		return nil, s.app.ConcludeReview(x, ch, in.Pass, in.Reason, in.BackTo)
	}))

	// ======================= comments & issues & shares =======================
	r.Get("/chapters/{cid}/comments", h(func(r *http.Request) (any, error) {
		_, ch, err := s.chapter(r)
		if err != nil {
			return nil, err
		}
		var list []model.Comment
		s.app.DB.Where("chapter_id = ?", ch.ID).Order("created_at asc").Find(&list)
		return s.commentViews(list), nil
	}))
	r.Get("/books/{bid}/comments", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.Comment{}).Where("book_id = ?", x.Book.ID)
		if r.URL.Query().Get("unresolved") == "1" {
			q = q.Where("resolved = ?", false)
		}
		p, err := paginate[model.Comment](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		p.Items = s.commentViews(p.Items.([]model.Comment))
		return p, nil
	}))
	r.Post("/books/{bid}/comments", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in core.CommentInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		c, err := s.app.CreateComment(x, in)
		if err != nil {
			return nil, err
		}
		return s.commentViews([]model.Comment{*c})[0], nil
	}))
	r.Patch("/comments/{id}", h(func(r *http.Request) (any, error) {
		var c model.Comment
		if err := s.app.DB.First(&c, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("评论")
		}
		x, err := s.app.BookAccess(userOf(r), c.BookID)
		if err != nil {
			return nil, err
		}
		var in struct {
			Resolved *bool   `json:"resolved"`
			Body     *string `json:"body"`
		}
		bind(r, &in)
		up := map[string]any{}
		if in.Resolved != nil {
			up["resolved"] = *in.Resolved
		}
		if in.Body != nil {
			if c.AuthorID != x.User.ID {
				return nil, core.Forbidden("只能编辑自己的评论")
			}
			up["body"] = *in.Body
		}
		return nil, s.app.DB.Model(&c).Updates(up).Error
	}))
	r.Delete("/comments/{id}", h(func(r *http.Request) (any, error) {
		var c model.Comment
		if err := s.app.DB.First(&c, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("评论")
		}
		x, err := s.app.BookAccess(userOf(r), c.BookID)
		if err != nil {
			return nil, err
		}
		if c.AuthorID != x.User.ID && !x.Can(core.PermManage) {
			return nil, core.Forbidden("")
		}
		return nil, s.app.DB.Delete(&c).Error
	}))
	r.Get("/books/{bid}/issues", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.Issue{}).Where("book_id = ?", x.Book.ID)
		for p, col := range map[string]string{"chapterId": "chapter_id", "status": "status", "assigneeId": "assignee_id", "type": "type", "segmentId": "segment_id"} {
			if v := r.URL.Query().Get(p); v != "" {
				if col == "status" && v == "active" {
					q = q.Where("status <> ?", model.IssueClosed)
					continue
				}
				q = q.Where(col+" = ?", v)
			}
		}
		p, err := paginate[model.Issue](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		p.Items = s.issueViews(p.Items.([]model.Issue))
		return p, nil
	}))
	r.Post("/books/{bid}/issues", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in core.IssueInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		iss, err := s.app.CreateIssue(x, in)
		if err != nil {
			return nil, err
		}
		return s.issueViews([]model.Issue{*iss})[0], nil
	}))
	r.Post("/issues/{id}/action", h(func(r *http.Request) (any, error) {
		var iss model.Issue
		if err := s.app.DB.First(&iss, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("工单")
		}
		x, err := s.app.BookAccess(userOf(r), iss.BookID)
		if err != nil {
			return nil, err
		}
		var in struct{ Action, Note, Assignee, Type string }
		bind(r, &in)
		return nil, s.app.ActOnIssue(x, &iss, in.Action, in.Note, in.Assignee, in.Type)
	}))
	r.Get("/books/{bid}/shares", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var list []model.ShareLink
		s.app.DB.Where("book_id = ? AND revoked_at IS NULL", x.Book.ID).Order("created_at desc").Find(&list)
		var out []map[string]any
		for _, l := range list {
			out = append(out, map[string]any{"link": l, "url": s.app.Cfg.PublicURL + "/s/" + l.Token})
		}
		return out, nil
	}))
	r.Post("/books/{bid}/shares", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in core.ShareInput
		bind(r, &in)
		l, err := s.app.CreateShareLink(x, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"link": l, "url": s.app.Cfg.PublicURL + "/s/" + l.Token}, nil
	}))
	r.Delete("/shares/{id}", h(func(r *http.Request) (any, error) {
		var l model.ShareLink
		if err := s.app.DB.First(&l, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("分享链接")
		}
		x, err := s.app.BookAccess(userOf(r), l.BookID)
		if err != nil {
			return nil, err
		}
		if !x.Can(core.PermExport) && l.CreatedBy != x.User.ID {
			return nil, core.Forbidden("")
		}
		t := time.Now()
		return nil, s.app.DB.Model(&l).Update("revoked_at", &t).Error
	}))

	// ======================= exports & publishing =======================
	r.Get("/books/{bid}/exports", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var list []model.Export
		s.app.DB.Where("book_id = ?", x.Book.ID).Order("created_at desc").Limit(30).Find(&list)
		var out []map[string]any
		for _, e := range list {
			v := map[string]any{"export": e}
			if e.AssetID != "" {
				var as model.Asset
				if s.app.DB.First(&as, "id = ?", e.AssetID).Error == nil {
					v["downloadUrl"] = s.app.AssetDownloadURL(&as, e.FileName)
				}
			}
			out = append(out, v)
		}
		return out, nil
	}))
	r.Post("/books/{bid}/exports", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in core.ExportInput
		bind(r, &in)
		return s.app.CreateExport(x, in)
	}))
	r.Post("/books/{bid}/publish/check", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChannelID  string   `json:"channelId"`
			ChapterIDs []string `json:"chapterIds"`
		}
		bind(r, &in)
		var ch *model.Channel
		if in.ChannelID != "" {
			var c model.Channel
			if s.app.DB.First(&c, "id = ? AND studio_id = ?", in.ChannelID, x.StudioID).Error == nil {
				ch = &c
			}
		}
		return s.app.PrePublishCheck(x.Book, in.ChapterIDs, ch), nil
	}))
	r.Get("/books/{bid}/publications", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var list []model.Publication
		s.app.DB.Where("book_id = ?", x.Book.ID).Order("created_at desc").Find(&list)
		var out []map[string]any
		for _, p := range list {
			var c model.Channel
			s.app.DB.First(&c, "id = ?", p.ChannelID)
			var eps []model.PublishedEpisode
			s.app.DB.Where("publication_id = ?", p.ID).Order("\"index\" asc").Find(&eps)
			v := map[string]any{"publication": p, "channel": c, "episodes": eps}
			if c.Kind == "rss" {
				v["feedUrl"] = s.app.Cfg.PublicURL + "/feeds/" + c.Token + "/" + x.Book.ID + ".xml"
			}
			out = append(out, v)
		}
		return out, nil
	}))
	r.Post("/books/{bid}/publications", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ChannelID  string    `json:"channelId"`
			ChapterIDs []string  `json:"chapterIds"`
			StartAt    time.Time `json:"startAt"`
			IntervalH  int       `json:"intervalHours"`
			PerBatch   int       `json:"perBatch"`
		}
		bind(r, &in)
		return s.app.CreatePublication(x, in.ChannelID, in.ChapterIDs, in.StartAt, in.IntervalH, in.PerBatch)
	}))
	r.Post("/publications/{id}/action", h(func(r *http.Request) (any, error) {
		var p model.Publication
		if err := s.app.DB.First(&p, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("发布计划")
		}
		x, err := s.app.BookAccess(userOf(r), p.BookID)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermExport); err != nil {
			return nil, err
		}
		var in struct{ Action string }
		bind(r, &in)
		switch in.Action {
		case "pause":
			s.app.DB.Model(&model.PublishedEpisode{}).Where("publication_id = ? AND status = ?", p.ID, "scheduled").Update("status", "paused")
			return nil, s.app.DB.Model(&p).Update("status", "paused").Error
		case "resume":
			s.app.DB.Model(&model.PublishedEpisode{}).Where("publication_id = ? AND status = ?", p.ID, "paused").Update("status", "scheduled")
			return nil, s.app.DB.Model(&p).Update("status", "scheduled").Error
		case "retry":
			s.app.DB.Model(&model.PublishedEpisode{}).Where("publication_id = ? AND status = ?", p.ID, "failed").Updates(map[string]any{"status": "scheduled", "publish_at": time.Now()})
			return nil, s.app.DB.Model(&p).Update("status", "scheduled").Error
		case "cancel":
			s.app.DB.Where("publication_id = ? AND status IN ?", p.ID, []string{"scheduled", "paused"}).Delete(&model.PublishedEpisode{})
			return nil, s.app.DB.Model(&p).Update("status", "canceled").Error
		}
		return nil, core.Bad("未知操作")
	}))

	// ======================= workflow & tasks =======================
	r.Post("/books/{bid}/workflow", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			TemplateID string                `json:"templateId"`
			Stages     []model.WorkflowStage `json:"stages"`
		}
		bind(r, &in)
		return nil, s.app.EnableWorkflow(x, in.TemplateID, in.Stages)
	}))
	r.Delete("/books/{bid}/workflow", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.DisableWorkflow(x)
	}))
	r.Get("/books/{bid}/tasks", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var list []model.ProductionTask
		s.app.DB.Where("book_id = ?", x.Book.ID).Order("stage_index asc").Find(&list)
		return s.taskViews(list), nil
	}))
	r.Post("/tasks/{id}/action", h(func(r *http.Request) (any, error) {
		var t model.ProductionTask
		if err := s.app.DB.First(&t, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("任务")
		}
		x, err := s.app.BookAccess(userOf(r), t.BookID)
		if err != nil {
			return nil, err
		}
		var in core.TaskAction
		bind(r, &in)
		if err := s.app.ActOnTask(x, &t, in); err != nil {
			return nil, err
		}
		return s.taskViews([]model.ProductionTask{t})[0], nil
	}))

	// ======================= pipeline =======================
	r.Post("/books/{bid}/pipeline/estimate", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var cfg model.PipelineConfig
		bind(r, &cfg)
		return s.app.EstimatePipeline(x.Book, cfg), nil
	}))
	r.Post("/books/{bid}/pipeline", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var cfg model.PipelineConfig
		bind(r, &cfg)
		run, err := s.app.StartPipeline(x, cfg, "", 0)
		if err != nil {
			return nil, err
		}
		return s.app.PipelineView(run), nil
	}))
	r.Get("/books/{bid}/pipeline", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var run model.PipelineRun
		if s.app.DB.Where("book_id = ?", x.Book.ID).Order("created_at desc").First(&run).Error != nil {
			return nil, nil
		}
		return s.app.PipelineView(&run), nil
	}))
	r.Get("/pipelines/{id}", h(func(r *http.Request) (any, error) {
		var run model.PipelineRun
		if err := s.app.DB.First(&run, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("任务")
		}
		if _, err := s.app.BookAccess(userOf(r), run.BookID); err != nil {
			return nil, err
		}
		return s.app.PipelineView(&run), nil
	}))
	r.Post("/pipelines/{id}/action", h(func(r *http.Request) (any, error) {
		var run model.PipelineRun
		if err := s.app.DB.First(&run, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("任务")
		}
		x, err := s.app.BookAccess(userOf(r), run.BookID)
		if err != nil {
			return nil, err
		}
		var in struct{ Action string }
		bind(r, &in)
		return nil, s.app.PipelineAction(x, &run, in.Action)
	}))
}

// lexiconView is a lexicon entry plus where it shows up and what changing it affected.
type lexiconView struct {
	model.LexiconEntry
	Occurrences   int `json:"occurrences"`
	StaleSegments int `json:"staleSegments"`
}

func (s *Server) lexiconGuard(r *http.Request, e *model.LexiconEntry) error {
	switch e.Scope {
	case "book":
		x, err := s.app.BookAccess(userOf(r), e.BookID)
		if err != nil {
			return err
		}
		return core.Need(x, core.PermScript)
	case "studio":
		x, err := s.app.StudioAccess(userOf(r), e.StudioID)
		if err != nil {
			return err
		}
		if !x.IsManager() && x.Role != model.RoleProducer && e.CreatedBy != x.User.ID {
			return core.Forbidden("")
		}
		return nil
	}
	return core.Forbidden("平台词条只能由管理员维护")
}

func (s *Server) commentViews(list []model.Comment) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		avatar, name := "", c.AuthorName
		if c.AuthorID != "" {
			var u model.User
			if s.app.DB.Select("nickname", "avatar_url").First(&u, "id = ?", c.AuthorID).Error == nil {
				avatar, name = u.AvatarURL, u.Nickname
			}
		}
		out = append(out, map[string]any{"id": c.ID, "bookId": c.BookID, "chapterId": c.ChapterID, "segmentId": c.SegmentID, "atMs": c.AtMs, "body": c.Body, "authorId": c.AuthorID,
			"authorName": name, "avatarUrl": avatar, "parentId": c.ParentID, "resolved": c.Resolved, "mentions": c.Mentions.V, "external": c.ShareLinkID != "", "issueId": c.IssueID, "createdAt": c.CreatedAt})
	}
	return out
}

func (s *Server) issueViews(list []model.Issue) []map[string]any {
	names := map[string]string{}
	name := func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := names[id]; ok {
			return n
		}
		var u model.User
		s.app.DB.Select("nickname").First(&u, "id = ?", id)
		names[id] = u.Nickname
		return u.Nickname
	}
	out := make([]map[string]any, 0, len(list))
	for _, i := range list {
		var seg model.Segment
		s.app.DB.Select("text", "character_id").First(&seg, "id = ?", i.SegmentID)
		var ch model.Chapter
		s.app.DB.Select("title", "index").First(&ch, "id = ?", i.ChapterID)
		v := map[string]any{"issue": i, "assignee": name(i.AssigneeID), "reporter": name(i.ReporterID), "reviewer": name(i.ReviewerID), "segmentText": seg.Text,
			"characterId": seg.CharacterID, "chapterTitle": ch.Title, "chapterIndex": ch.Index, "typeName": core.IssueTypes[i.Type]}
		if i.OldTakeID != "" {
			var t model.Take
			if s.app.DB.First(&t, "id = ?", i.OldTakeID).Error == nil {
				v["oldUrl"] = s.app.AssetURLByID(t.AssetID)
			}
		}
		if i.NewTakeID != "" {
			var t model.Take
			if s.app.DB.First(&t, "id = ?", i.NewTakeID).Error == nil {
				v["newUrl"] = s.app.AssetURLByID(t.AssetID)
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) taskViews(list []model.ProductionTask) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		var u model.User
		s.app.DB.Select("nickname", "avatar_url").First(&u, "id = ?", t.AssigneeID)
		var ch model.Chapter
		s.app.DB.Select("title", "index").First(&ch, "id = ?", t.ChapterID)
		var b model.Book
		s.app.DB.Select("title").First(&b, "id = ?", t.BookID)
		var c model.Character
		if t.CharacterID != "" {
			s.app.DB.Select("name", "color").First(&c, "id = ?", t.CharacterID)
		}
		out = append(out, map[string]any{"task": t, "assignee": u.Nickname, "assigneeAvatar": u.AvatarURL, "chapterTitle": ch.Title, "chapterIndex": ch.Index, "bookTitle": b.Title, "characterName": c.Name, "characterColor": c.Color,
			"overdue": t.DueAt != nil && t.DueAt.Before(time.Now()) && t.Status != model.TaskDone})
	}
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func toAnySlice(xs []string) []any {
	out := make([]any, len(xs))
	for i, v := range xs {
		out[i] = v
	}
	return out
}

func f64ptr(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func firstNonZeroF(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

var _ = text.WordCount

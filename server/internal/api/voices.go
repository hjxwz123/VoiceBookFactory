package api

import (
	"net/http"
	"strings"
	"time"
	"voicebook/internal/ai"
	"voicebook/internal/ai/tts"
	dbx "voicebook/internal/db"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"voicebook/internal/audio"
	"voicebook/internal/core"
	"voicebook/internal/model"
)

func (s *Server) voiceRoutes(r chi.Router) {
	r.Get("/studios/{sid}/voices", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		qp := r.URL.Query()
		q := s.app.DB.Model(&model.Voice{})
		switch qp.Get("tab") {
		case "studio":
			q = q.Where("(scope = ? AND studio_id = ?) OR (scope = ? AND studio_id = ? AND shared = ?)", model.ScopeStudio, x.StudioID, model.ScopeUser, x.StudioID, true)
		case "mine":
			q = q.Where("scope = ? AND owner_user_id = ?", model.ScopeUser, x.User.ID)
		case "favorites":
			q = q.Where("id IN (?)", s.app.DB.Model(&model.VoiceFavorite{}).Select("voice_id").Where("user_id = ?", x.User.ID))
		case "market":
			q = q.Where("id IN (?)", s.app.DB.Model(&model.VoiceListing{}).Select("voice_id").Where("status = ?", "listed"))
		case "all":
			q = q.Where("status = ?", model.VoiceReady).Where(s.app.DB.Where("scope = ? AND listed = ?", model.ScopeSystem, true).
				Or("scope = ? AND studio_id = ?", model.ScopeStudio, x.StudioID).Or("scope = ? AND owner_user_id = ?", model.ScopeUser, x.User.ID).
				Or("scope = ? AND studio_id = ? AND shared = ?", model.ScopeUser, x.StudioID, true))
		default:
			q = q.Where("scope = ? AND listed = ?", model.ScopeSystem, true)
		}
		q = q.Where("(scope <> ? OR provider IN ?)", model.ScopeSystem, s.app.ActiveTTSProviders())
		if m := qp.Get("model"); m != "" {
			if m == "placeholder" {
				q = q.Where("provider = ?", "placeholder")
			} else {
				q = q.Where("model = ?", m)
			}
		}
		if qp.Get("series") == "system" {
			q = q.Where(dbx.NotLike(q, "tags"), "%基础音色%")
		} else if qp.Get("series") == "base" {
			q = q.Where(dbx.Like(q, "tags"), "%基础音色%")
		}
		if g := qp.Get("gender"); g != "" {
			q = q.Where("gender = ?", g)
		}
		if a := qp.Get("age"); a != "" {
			q = q.Where("age_group = ?", a)
		}
		if t := qp.Get("tier"); t != "" {
			q = q.Where("price_tier = ?", t)
		}
		if p := qp.Get("provider"); p != "" {
			q = q.Where("provider = ?", p)
		}
		if l := qp.Get("lang"); l != "" {
			q = q.Where("language = ?", l)
		}
		if st := qp.Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if tag := qp.Get("tag"); tag != "" {
			q = q.Where(dbx.Like(q, "tags"), "%\""+tag+"\"%")
		}
		if kw := strings.TrimSpace(qp.Get("q")); kw != "" {
			// semantic-ish search: every keyword fragment must match name, tags or description
			for _, part := range strings.Fields(kw) {
				q = q.Where("("+dbx.Like(q, "name")+" OR "+dbx.Like(q, "tags")+" OR "+dbx.Like(q, "description")+")", "%"+part+"%", "%"+part+"%", "%"+part+"%")
			}
		}
		const order = "CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, recommended desc, sort_order asc, created_at desc"
		// group=1: a platform voice offered on several models (e.g. 龙逸尘 on CosyVoice v3 and v2) is listed once,
		// as its first version in the normal order; the other versions ride along in "versions".
		group := qp.Get("group") == "1" && qp.Get("model") == ""
		if group {
			ranked := q.Session(&gorm.Session{}).Select("id, ROW_NUMBER() OVER (PARTITION BY CASE WHEN scope = '" + model.ScopeSystem + "' THEN provider || ':' || name ELSE id END ORDER BY " + order + ") AS rn")
			q = s.app.DB.Model(&model.Voice{}).Where("id IN (?)", s.app.DB.Table("(?) AS g", ranked).Select("id").Where("rn = 1"))
		}
		p, err := paginate[model.Voice](r, q, order)
		if err != nil {
			return nil, err
		}
		vs := p.Items.([]model.Voice)
		items := make([]map[string]any, 0, len(vs))
		for i := range vs {
			items = append(items, s.app.VoiceView(&vs[i], x.User.ID))
		}
		if group {
			var names []string
			for _, v := range vs {
				if v.Scope == model.ScopeSystem {
					names = append(names, v.Name)
				}
			}
			if len(names) > 0 {
				var sib []model.Voice
				s.app.DB.Where("scope = ? AND listed = ? AND name IN ? AND provider IN ?", model.ScopeSystem, true, names, s.app.ActiveTTSProviders()).Order(order).Find(&sib)
				byKey := map[string][]map[string]any{}
				for i := range sib {
					k := sib[i].Provider + ":" + sib[i].Name
					byKey[k] = append(byKey[k], s.app.VoiceView(&sib[i], ""))
				}
				for i, v := range vs {
					if vers := byKey[v.Provider+":"+v.Name]; v.Scope == model.ScopeSystem && len(vers) > 1 {
						items[i]["versions"] = vers
					}
				}
			}
		}
		p.Items = items
		return p, nil
	}))
	r.Get("/voices/{vid}", h(func(r *http.Request) (any, error) {
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		view := s.app.VoiceView(v, userOf(r).ID)
		var chars []model.Character
		s.app.DB.Where("voice_id = ?", v.ID).Find(&chars)
		var usage []map[string]any
		for _, c := range chars {
			var b model.Book
			if s.app.DB.Select("id", "title", "studio_id").First(&b, "id = ?", c.BookID).Error == nil {
				if _, err := s.app.BookAccess(userOf(r), b.ID); err == nil {
					usage = append(usage, map[string]any{"bookId": b.ID, "bookTitle": b.Title, "characterId": c.ID, "characterName": c.Name})
				}
			}
		}
		view["usage"] = usage
		if v.Scope == model.ScopeSystem {
			var sib []model.Voice
			s.app.DB.Where("scope = ? AND listed = ? AND name = ? AND provider = ?", model.ScopeSystem, true, v.Name, v.Provider).
				Order("recommended desc, sort_order asc, created_at desc").Find(&sib)
			if len(sib) > 1 {
				vers := make([]map[string]any, 0, len(sib))
				for i := range sib {
					vers = append(vers, map[string]any{"id": sib[i].ID, "model": sib[i].Model, "modelLabel": tts.ModelLabel(sib[i].Model), "priceTier": sib[i].PriceTier})
				}
				view["versions"] = vers
			}
		}
		if v.ConsentID != "" && (v.OwnerUserID == userOf(r).ID || userOf(r).IsAdmin()) {
			var c model.VoiceConsent
			if s.app.DB.First(&c, "id = ?", v.ConsentID).Error == nil {
				view["consent"] = map[string]any{"type": c.Type, "status": c.Status, "speakerName": c.SpeakerName, "reviewNote": c.ReviewNote}
			}
		}
		return view, nil
	}))
	r.Patch("/studios/{sid}/voices/{vid}", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		if !s.app.CanManageVoice(x, v) {
			return nil, core.Forbidden("只能编辑自己的音色")
		}
		var in struct {
			Name        *string              `json:"name"`
			Tags        *[]string            `json:"tags"`
			Description *string              `json:"description"`
			Gender      *string              `json:"gender"`
			AgeGroup    *string              `json:"ageGroup"`
			Defaults    *model.VoiceDefaults `json:"defaults"`
			Shared      *bool                `json:"shared"`
			SampleText  *string              `json:"sampleText"`
			CrowdPool   *bool                `json:"crowdPool"`
		}
		bind(r, &in)
		up := map[string]any{}
		if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
			up["name"] = strings.TrimSpace(*in.Name)
		}
		if in.Tags != nil {
			up["tags"] = model.Strings(*in.Tags...)
		}
		if in.Description != nil {
			up["description"] = *in.Description
		}
		if in.Gender != nil {
			up["gender"] = *in.Gender
		}
		if in.AgeGroup != nil {
			up["age_group"] = *in.AgeGroup
		}
		if in.Defaults != nil {
			up["defaults"] = model.JSON[model.VoiceDefaults]{V: *in.Defaults}
		}
		if in.SampleText != nil {
			up["sample_text"] = *in.SampleText
		}
		if in.CrowdPool != nil {
			up["crowd_pool"] = *in.CrowdPool
		}
		if in.Shared != nil {
			if v.Scope != model.ScopeUser {
				return nil, core.Bad("只有个人音色可以共享")
			}
			up["shared"] = *in.Shared
			up["studio_id"] = x.StudioID
			if !*in.Shared {
				var n int64
				s.app.DB.Model(&model.Character{}).Where("voice_id = ? AND book_id IN (?)", v.ID, s.app.DB.Model(&model.Book{}).Select("id").Where("studio_id = ?", x.StudioID)).Count(&n)
				if n > 0 {
					s.app.NotifyManagers(x.StudioID, "voice_unshared", "音色「"+v.Name+"」已被取消共享", "仍有角色在使用它，请注意重新选角", "/voices")
				}
			}
		}
		if err := s.app.DB.Model(v).Updates(up).Error; err != nil {
			return nil, err
		}
		s.app.DB.First(v, "id = ?", v.ID)
		return s.app.VoiceView(v, x.User.ID), nil
	}))
	r.Delete("/studios/{sid}/voices/{vid}", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		if !s.app.CanManageVoice(x, v) {
			return nil, core.Forbidden("只能删除自己的音色")
		}
		return nil, s.app.DeleteVoice(r.Context(), v)
	}))
	r.Post("/voices/{vid}/favorite", h(func(r *http.Request) (any, error) {
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		u := userOf(r)
		var f model.VoiceFavorite
		if s.app.DB.First(&f, "user_id = ? AND voice_id = ?", u.ID, v.ID).Error == nil {
			s.app.DB.Delete(&f)
			return map[string]any{"favorite": false}, nil
		}
		s.app.DB.Create(&model.VoiceFavorite{UserID: u.ID, VoiceID: v.ID})
		return map[string]any{"favorite": true}, nil
	}))
	r.Post("/studios/{sid}/voices/{vid}/preview", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		if !s.app.VoiceVisible(x.User, x.StudioID, v) {
			return nil, core.Forbidden("")
		}
		var in struct {
			Text    string  `json:"text"`
			Emotion string  `json:"emotion"`
			Speed   float64 `json:"speed"`
			Pitch   float64 `json:"pitch"`
		}
		bind(r, &in)
		url, err := s.app.Preview(r.Context(), x, v, in.Text, in.Emotion, in.Speed, in.Pitch)
		return map[string]any{"url": url}, err
	}))
	// ---- design ----
	r.Post("/studios/{sid}/voices/design", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in struct{ Description, PreviewText, Provider, CharacterID string }
		bind(r, &in)
		d, j, err := s.app.StartVoiceDesign(x, in.Description, in.PreviewText, in.Provider, in.CharacterID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"design": s.app.DesignView(d), "job": j}, nil
	}))
	r.Get("/voice-designs/{id}", h(func(r *http.Request) (any, error) {
		var d model.VoiceDesign
		if err := s.app.DB.First(&d, "id = ? AND user_id = ?", param(r, "id"), userOf(r).ID).Error; err != nil {
			return nil, core.NotFound("设计记录")
		}
		return s.app.DesignView(&d), nil
	}))
	r.Get("/studios/{sid}/voice-designs", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var list []model.VoiceDesign
		s.app.DB.Where("user_id = ? AND studio_id = ?", x.User.ID, x.StudioID).Order("created_at desc").Limit(20).Find(&list)
		var out []map[string]any
		for i := range list {
			out = append(out, s.app.DesignView(&list[i]))
		}
		return out, nil
	}))
	r.Post("/studios/{sid}/voice-designs/{id}/save", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var d model.VoiceDesign
		if err := s.app.DB.First(&d, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("设计记录")
		}
		var in struct {
			Index int      `json:"index"`
			Name  string   `json:"name"`
			Tags  []string `json:"tags"`
			Scope string   `json:"scope"`
		}
		bind(r, &in)
		v, err := s.app.SaveDesignedVoice(x, &d, in.Index, in.Name, in.Tags, in.Scope)
		if err != nil {
			return nil, err
		}
		return s.app.VoiceView(v, x.User.ID), nil
	}))
	// ---- clone ----
	r.Get("/studios/{sid}/voices/design-options", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return s.app.DesignOptions(x.StudioID), nil
	}))
	r.Get("/studios/{sid}/voices/clone-options", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return s.app.CloneOptions(x.StudioID), nil
	}))
	r.Get("/voices/challenge", h(func(r *http.Request) (any, error) {
		return map[string]any{"sentence": core.ChallengeSentence()}, nil
	}))
	r.Post("/studios/{sid}/voices/sample-check", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		as, err := s.saveUpload(r, "file", core.AssetInput{Scope: model.ScopeUser, StudioID: x.StudioID, OwnerUserID: x.User.ID, Kind: model.AssetSample, Origin: "upload", CreatedBy: x.User.ID})
		if err != nil {
			return nil, err
		}
		path, _, _ := s.app.AssetPath(as.ID)
		check, err := s.app.CheckSample(r.Context(), path, r.FormValue("tier"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"assetId": as.ID, "url": s.app.AssetURL(as), "check": check}, nil
	}))
	r.Post("/studios/{sid}/voices/clone", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Name        string   `json:"name"`
			Tier        string   `json:"tier"`
			ConsentType string   `json:"consentType"`
			SpeakerName string   `json:"speakerName"`
			Identity    string   `json:"identity"`
			Statement   string   `json:"statement"`
			Challenge   string   `json:"challenge"`
			Gender      string   `json:"gender"`
			AgeGroup    string   `json:"ageGroup"`
			Provider    string   `json:"provider"`
			Denoise     bool     `json:"denoise"`
			SampleIDs   []string `json:"sampleIds"`
			EvidenceID  string   `json:"evidenceId"`
			DocumentID  string   `json:"documentId"`
			Scope       string   `json:"scope"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		v, err := s.app.StartClone(r.Context(), x, core.CloneInput{Name: in.Name, Tier: in.Tier, ConsentType: in.ConsentType, SpeakerName: in.SpeakerName, Identity: in.Identity,
			Statement: in.Statement, Challenge: in.Challenge, Gender: in.Gender, AgeGroup: in.AgeGroup, Provider: in.Provider, Denoise: in.Denoise, SampleIDs: in.SampleIDs,
			EvidenceID: in.EvidenceID, DocumentID: in.DocumentID, Scope: in.Scope})
		if err != nil {
			return nil, err
		}
		return s.app.VoiceView(v, x.User.ID), nil
	}))
	// ---- marketplace ----
	r.Post("/studios/{sid}/voices/{vid}/listing", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		v, err := s.loadVoice(r)
		if err != nil {
			return nil, err
		}
		if v.Scope != model.ScopeUser || v.OwnerUserID != x.User.ID {
			return nil, core.Forbidden("只能上架自己的个人音色")
		}
		if v.Origin == model.VoiceCloned {
			var c model.VoiceConsent
			if s.app.DB.First(&c, "id = ?", v.ConsentID).Error != nil || c.Type != "self" || c.Status != "approved" {
				return nil, core.Bad("只有通过本人验证的克隆音色才能上架声音市场")
			}
		}
		if x.User.RealNameStatus != "verified" {
			return nil, core.Bad("上架声音市场需要先完成实名认证")
		}
		var in struct {
			PricePer1K int64  `json:"pricePer1k"`
			Commercial bool   `json:"commercial"`
			Intro      string `json:"intro"`
		}
		bind(r, &in)
		var l model.VoiceListing
		if s.app.DB.First(&l, "voice_id = ?", v.ID).Error == nil {
			return l, s.app.DB.Model(&l).Updates(map[string]any{"price_per1_k": in.PricePer1K, "commercial": in.Commercial, "intro": in.Intro, "status": "pending"}).Error
		}
		l = model.VoiceListing{VoiceID: v.ID, OwnerUserID: x.User.ID, PricePer1K: in.PricePer1K, Commercial: in.Commercial, Intro: in.Intro, Status: "pending"}
		return l, s.app.DB.Create(&l).Error
	}))
	r.Delete("/studios/{sid}/voices/{vid}/listing", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.DB.Model(&model.VoiceListing{}).Where("voice_id = ? AND owner_user_id = ?", param(r, "vid"), x.User.ID).Update("status", "delisted").Error
	}))
	r.Get("/me/earnings", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var listings []model.VoiceListing
		s.app.DB.Where("owner_user_id = ?", u.ID).Find(&listings)
		var out []map[string]any
		for _, l := range listings {
			var v model.Voice
			s.app.DB.Select("id", "name").First(&v, "id = ?", l.VoiceID)
			out = append(out, map[string]any{"listing": l, "voiceName": v.Name})
		}
		var ws []model.Withdrawal
		s.app.DB.Where("user_id = ?", u.ID).Order("created_at desc").Find(&ws)
		var income int64
		s.app.DB.Model(&model.CreditLedger{}).Where("studio_id = ? AND kind = ?", u.PersonalStudio, "market_income").Select("COALESCE(SUM(delta),0)").Scan(&income)
		return map[string]any{"listings": out, "withdrawals": ws, "income": income, "balance": s.app.Balance(u.PersonalStudio)}, nil
	}))
	r.Post("/me/withdrawals", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var in struct {
			Amount  int64  `json:"amount"`
			Account string `json:"account"`
		}
		bind(r, &in)
		var income int64
		s.app.DB.Model(&model.CreditLedger{}).Where("studio_id = ? AND kind = ?", u.PersonalStudio, "market_income").Select("COALESCE(SUM(delta),0)").Scan(&income)
		var withdrawn int64
		s.app.DB.Model(&model.Withdrawal{}).Where("user_id = ? AND status <> ?", u.ID, "rejected").Select("COALESCE(SUM(amount),0)").Scan(&withdrawn)
		if in.Amount <= 0 || in.Amount > income-withdrawn {
			return nil, core.Bad("可提现的音色收益为 %d 积分", income-withdrawn)
		}
		if strings.TrimSpace(in.Account) == "" {
			return nil, core.Bad("请填写收款账户")
		}
		if err := s.app.Deduct(u.PersonalStudio, in.Amount, "音色收益提现", u.ID); err != nil {
			return nil, err
		}
		w := model.Withdrawal{UserID: u.ID, Amount: in.Amount, Account: in.Account, Status: "pending"}
		return w, s.app.DB.Create(&w).Error
	}))
}

func (s *Server) loadVoice(r *http.Request) (*model.Voice, error) {
	var v model.Voice
	if err := s.app.DB.First(&v, "id = ?", param(r, "vid")).Error; err != nil {
		return nil, core.NotFound("音色")
	}
	u := userOf(r)
	if v.Scope == model.ScopeSystem || v.OwnerUserID == u.ID || u.IsAdmin() {
		return &v, nil
	}
	var n int64
	s.app.DB.Model(&model.StudioMember{}).Where("user_id = ? AND studio_id = ? AND status = ?", u.ID, v.StudioID, "active").Count(&n)
	if n > 0 && (v.Scope == model.ScopeStudio || v.Shared) {
		return &v, nil
	}
	var l model.VoiceListing
	if s.app.DB.First(&l, "voice_id = ? AND status = ?", v.ID, "listed").Error == nil {
		return &v, nil
	}
	return nil, core.Forbidden("无权访问该音色")
}

func (s *Server) assetRoutes(r chi.Router) {
	// music categories of the official library that actually have tracks (for filter chips)
	r.Get("/music/categories", h(func(r *http.Request) (any, error) {
		out := []map[string]any{}
		for _, c := range s.app.MusicLibraryStatus().Categories {
			if c.Imported > 0 {
				out = append(out, map[string]any{"key": c.Key, "name": c.Name, "count": c.Imported})
			}
		}
		return out, nil
	}))
	// categories of the official sound-effect library with imported sounds (?kind=sfx|ambience)
	r.Get("/sfx/categories", h(func(r *http.Request) (any, error) {
		return s.app.SfxCategories(r.URL.Query().Get("kind")), nil
	}))
	r.Get("/studios/{sid}/assets", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		qp := r.URL.Query()
		q := s.app.DB.Model(&model.Asset{})
		kinds := model.LibraryKinds
		if k := qp.Get("kind"); k != "" {
			kinds = strings.Split(k, ",")
		}
		q = q.Where("kind IN ?", kinds)
		switch qp.Get("scope") {
		case "official":
			q = q.Where("scope = ? AND listed = ?", model.ScopeSystem, true)
		case "mine":
			q = q.Where("studio_id = ? AND created_by = ?", x.StudioID, x.User.ID)
		case "favorites":
			q = q.Where("id IN (?)", s.app.DB.Model(&model.AssetFavorite{}).Select("asset_id").Where("user_id = ?", x.User.ID))
		case "studio":
			q = q.Where("studio_id = ? AND scope <> ?", x.StudioID, model.ScopeSystem)
		default:
			q = q.Where("(scope = ? AND listed = ?) OR studio_id = ?", model.ScopeSystem, true, x.StudioID)
		}
		if tag := qp.Get("tag"); tag != "" {
			q = q.Where(dbx.Like(q, "tags"), "%\""+tag+"\"%")
		}
		if kw := strings.TrimSpace(qp.Get("q")); kw != "" {
			for _, part := range strings.FieldsFunc(kw, func(r rune) bool { return r == ' ' || r == '，' || r == ',' }) {
				q = q.Where("("+dbx.Like(q, "name")+" OR "+dbx.Like(q, "tags")+" OR "+dbx.Like(q, "description")+")", "%"+part+"%", "%"+part+"%", "%"+part+"%")
			}
		}
		if mn := qint(r, "minMs", 0); mn > 0 {
			q = q.Where("duration_ms >= ?", mn)
		}
		if mx := qint(r, "maxMs", 0); mx > 0 {
			q = q.Where("duration_ms <= ?", mx)
		}
		p, err := paginate[model.Asset](r, q, s.app.LibraryOrder())
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.Asset)
		favs := map[string]bool{}
		var fl []model.AssetFavorite
		s.app.DB.Where("user_id = ?", x.User.ID).Find(&fl)
		for _, f := range fl {
			favs[f.AssetID] = true
		}
		items := make([]map[string]any, 0, len(list))
		for i := range list {
			v := s.app.AssetView(&list[i])
			v["favorite"] = favs[list[i].ID]
			items = append(items, v)
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/studios/{sid}/assets", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		if !x.CanUploadAssets() {
			return nil, core.Forbidden("外部协作者不能上传工作室素材")
		}
		kind := r.FormValue("kind")
		allowed := map[string]bool{model.AssetBGM: true, model.AssetSFX: true, model.AssetAmbience: true, model.AssetImage: true, model.AssetSample: true, model.AssetRecording: true, model.AssetDocument: true, model.AssetAttachment: true, model.AssetJingle: true}
		if !allowed[kind] {
			kind = model.AssetSFX
		}
		var tags []string
		if t := r.FormValue("tags"); t != "" {
			tags = strings.Split(t, ",")
		}
		as, err := s.saveUpload(r, "file", core.AssetInput{StudioID: x.StudioID, OwnerUserID: x.User.ID, Kind: kind, Name: r.FormValue("name"), Origin: "upload", License: firstNonEmptyStr(r.FormValue("license"), "commercial"), Tags: tags, CreatedBy: x.User.ID})
		if err != nil {
			return nil, err
		}
		// dedupe identical library uploads
		if kind == model.AssetBGM || kind == model.AssetSFX || kind == model.AssetAmbience {
			var dup model.Asset
			if s.app.DB.Where("checksum = ? AND studio_id = ? AND id <> ?", as.Checksum, x.StudioID, as.ID).First(&dup).Error == nil {
				s.app.DeleteAssetFile(as)
				s.app.DB.Delete(as)
				v := s.app.AssetView(&dup)
				v["duplicate"] = true
				return v, nil
			}
		}
		return s.app.AssetView(as), nil
	}))
	r.Patch("/assets/{aid}", h(func(r *http.Request) (any, error) {
		as, x, err := s.loadAsset(r, true)
		if err != nil {
			return nil, err
		}
		_ = x
		var in struct {
			Name        *string   `json:"name"`
			Tags        *[]string `json:"tags"`
			License     *string   `json:"license"`
			Description *string   `json:"description"`
			Kind        *string   `json:"kind"`
		}
		bind(r, &in)
		up := map[string]any{}
		if in.Name != nil {
			up["name"] = *in.Name
		}
		if in.Tags != nil {
			up["tags"] = model.Strings(*in.Tags...)
		}
		if in.License != nil {
			up["license"] = *in.License
		}
		if in.Description != nil {
			up["description"] = *in.Description
		}
		if in.Kind != nil && (*in.Kind == model.AssetBGM || *in.Kind == model.AssetSFX || *in.Kind == model.AssetAmbience) {
			up["kind"] = *in.Kind
		}
		if err := s.app.DB.Model(as).Updates(up).Error; err != nil {
			return nil, err
		}
		s.app.DB.First(as, "id = ?", as.ID)
		return s.app.AssetView(as), nil
	}))
	r.Delete("/assets/{aid}", h(func(r *http.Request) (any, error) {
		as, _, err := s.loadAsset(r, true)
		if err != nil {
			return nil, err
		}
		refs := s.assetRefs(as.ID)
		if len(refs) > 0 && r.URL.Query().Get("force") != "1" {
			return nil, &core.Error{Status: 409, Code: "in_use", Msg: "素材正在被使用", Data: refs}
		}
		s.app.DeleteAssetFile(as)
		return nil, s.app.DB.Delete(as).Error
	}))
	r.Get("/assets/{aid}/usage", h(func(r *http.Request) (any, error) {
		as, _, err := s.loadAsset(r, false)
		if err != nil {
			return nil, err
		}
		return s.assetRefs(as.ID), nil
	}))
	r.Get("/assets/{aid}/peaks", h(func(r *http.Request) (any, error) {
		as, _, err := s.loadAsset(r, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"peaks": s.app.AssetPeaks(r.Context(), as), "perSecond": 50, "durationMs": as.DurationMs}, nil
	}))
	r.Post("/assets/{aid}/favorite", h(func(r *http.Request) (any, error) {
		as, _, err := s.loadAsset(r, false)
		if err != nil {
			return nil, err
		}
		u := userOf(r)
		var f model.AssetFavorite
		if s.app.DB.First(&f, "user_id = ? AND asset_id = ?", u.ID, as.ID).Error == nil {
			s.app.DB.Delete(&f)
			return map[string]any{"favorite": false}, nil
		}
		s.app.DB.Create(&model.AssetFavorite{UserID: u.ID, AssetID: as.ID})
		return map[string]any{"favorite": true}, nil
	}))
	r.Post("/studios/{sid}/uploads", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		kind := firstNonEmptyStr(r.FormValue("kind"), model.AssetAttachment)
		as, err := s.saveUpload(r, "file", core.AssetInput{StudioID: x.StudioID, OwnerUserID: x.User.ID, Kind: kind, Origin: "upload", CreatedBy: x.User.ID})
		if err != nil {
			return nil, err
		}
		return s.app.AssetView(as), nil
	}))
	// what the configured generation services can do, so the form offers only what will be honoured
	r.Get("/studios/{sid}/generate/options", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return s.app.GenOptions(x.StudioID), nil
	}))
	r.Post("/studios/{sid}/generate", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in core.GenRequest
		bind(r, &in)
		if in.BookID != "" {
			bx, err := s.app.BookAccess(x.User, in.BookID)
			if err != nil {
				return nil, err
			}
			if err := core.Need(bx, core.PermEditInfo); err != nil {
				return nil, err
			}
		}
		return s.app.EnqueueGenerate(x, in)
	}))
	// ---- intro/outro templates ----
	tplView := func(t model.AudioTemplate, studioID string) map[string]any {
		v := map[string]any{"template": t}
		var music, voice model.Asset
		if t.MusicAssetID != "" && s.app.DB.Select("id", "name").First(&music, "id = ?", t.MusicAssetID).Error == nil {
			v["musicName"] = music.Name
		}
		if t.VoiceID != "" {
			var vo model.Voice
			if s.app.DB.Select("id", "name").First(&vo, "id = ?", t.VoiceID).Error == nil {
				v["voiceName"] = vo.Name
			}
		}
		if t.AudioAssetID != "" {
			if s.app.DB.First(&voice, "id = ?", t.AudioAssetID).Error == nil {
				v["audio"] = s.app.AssetView(&voice)
			} else {
				v["audioMissing"] = true
			}
		}
		if t.Scope != model.ScopeSystem || studioID != "" {
			n, def := s.app.TemplateUsage(studioID, t.ID)
			v["books"], v["studioDefault"] = n, def
		}
		v["placeholders"] = core.TemplatePlaceholders(t.Text)
		return v
	}
	r.Get("/studios/{sid}/templates", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var list []model.AudioTemplate
		s.app.DB.Where("scope = ? OR studio_id = ?", model.ScopeSystem, x.StudioID).Order("scope desc, created_at asc").Find(&list)
		out := []map[string]any{}
		for _, t := range list {
			out = append(out, tplView(t, x.StudioID))
		}
		return out, nil
	}))
	r.Post("/studios/{sid}/templates", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in model.AudioTemplate
		bind(r, &in)
		in.ID, in.Scope, in.StudioID, in.CreatedBy = "", model.ScopeStudio, x.StudioID, x.User.ID
		if err := s.app.NormalizeTemplate(x.StudioID, &in); err != nil {
			return nil, err
		}
		if err := s.app.DB.Create(&in).Error; err != nil {
			return nil, err
		}
		return tplView(in, x.StudioID), nil
	}))
	r.Put("/studios/{sid}/templates/{tid}", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var t model.AudioTemplate
		if err := s.app.DB.First(&t, "id = ? AND studio_id = ?", param(r, "tid"), x.StudioID).Error; err != nil {
			return nil, core.NotFound("模板")
		}
		id, created, by := t.ID, t.CreatedAt, t.CreatedBy
		bind(r, &t)
		t.ID, t.StudioID, t.Scope, t.CreatedAt, t.CreatedBy = id, x.StudioID, model.ScopeStudio, created, by
		if err := s.app.NormalizeTemplate(x.StudioID, &t); err != nil {
			return nil, err
		}
		if err := s.app.DB.Save(&t).Error; err != nil {
			return nil, err
		}
		s.app.MarkTemplateStale(x.StudioID, t.ID)
		return tplView(t, x.StudioID), nil
	}))
	r.Delete("/studios/{sid}/templates/{tid}", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "tid"), x.StudioID).Delete(&model.AudioTemplate{}).Error
	}))
	previewBook := func(r *http.Request, x *core.Access) *model.Book {
		if bid := r.URL.Query().Get("bookId"); bid != "" {
			if bx, err := s.app.BookAccess(x.User, bid); err == nil && bx.Book.StudioID == x.StudioID {
				return bx.Book
			}
		}
		return nil
	}
	r.Post("/studios/{sid}/templates/{tid}/preview", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var t model.AudioTemplate
		if err := s.app.DB.First(&t, "id = ? AND (studio_id = ? OR scope = ?)", param(r, "tid"), x.StudioID, model.ScopeSystem).Error; err != nil {
			return nil, core.NotFound("模板")
		}
		url, err := s.app.TemplatePreview(r.Context(), x, &t, previewBook(r, x))
		return map[string]any{"url": url}, err
	}))
	// preview of a template that is still being edited
	r.Post("/studios/{sid}/templates/preview", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var t model.AudioTemplate
		bind(r, &t)
		t.ID = ""
		if err := s.app.NormalizeTemplate(x.StudioID, &t); err != nil {
			return nil, err
		}
		url, err := s.app.TemplatePreview(r.Context(), x, &t, previewBook(r, x))
		return map[string]any{"url": url}, err
	}))
	// render a compose recipe once into a fixed audio
	r.Post("/studios/{sid}/templates/generate", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var t model.AudioTemplate
		bind(r, &t)
		as, err := s.app.GenerateTemplateAudio(r.Context(), x, t)
		if err != nil {
			return nil, err
		}
		return s.app.AssetView(as), nil
	}))
	r.Get("/effects", h(func(r *http.Request) (any, error) { return audio.Effects, nil }))
	r.Get("/presets", h(func(r *http.Request) (any, error) { return s.app.Presets(), nil }))
	r.Get("/performance-tags", h(func(r *http.Request) (any, error) { return tts.PerformanceTags, nil }))
	r.Get("/ai-catalog", h(func(r *http.Request) (any, error) {
		out := []ai.Preset{}
		for _, p := range ai.Presets {
			if p.Provider != "placeholder" {
				out = append(out, p)
			}
		}
		return out, nil
	}))
}

func (s *Server) loadAsset(r *http.Request, write bool) (*model.Asset, *core.Access, error) {
	var as model.Asset
	if err := s.app.DB.First(&as, "id = ?", param(r, "aid")).Error; err != nil {
		return nil, nil, core.NotFound("素材")
	}
	u := userOf(r)
	if as.Scope == model.ScopeSystem {
		if write && !u.IsAdmin() {
			return nil, nil, core.Forbidden("官方素材只能由平台管理员维护")
		}
		return &as, nil, nil
	}
	if as.Scope == model.ScopeUser && as.StudioID == "" {
		if as.OwnerUserID != u.ID {
			return nil, nil, core.Forbidden("")
		}
		return &as, nil, nil
	}
	x, err := s.app.StudioAccess(u, as.StudioID)
	if err != nil {
		return nil, nil, err
	}
	if write && !x.IsManager() && as.CreatedBy != u.ID {
		return nil, nil, core.Forbidden("只能修改自己上传的素材")
	}
	return &as, x, nil
}

// assetRefs lists where an asset is used (mix tracks, sfx cues, templates, covers).
func (s *Server) assetRefs(id string) []map[string]any {
	var refs []map[string]any
	var mixes []model.Mix
	s.app.DB.Where(dbx.Like(s.app.DB, "tracks"), "%"+id+"%").Find(&mixes)
	for _, m := range mixes {
		var ch model.Chapter
		s.app.DB.Select("id", "title", "book_id").First(&ch, "id = ?", m.ChapterID)
		refs = append(refs, map[string]any{"type": "mix", "chapterId": ch.ID, "title": ch.Title, "bookId": ch.BookID})
	}
	var n int64
	s.app.DB.Model(&model.Segment{}).Where("sfx_asset_id = ?", id).Count(&n)
	if n > 0 {
		refs = append(refs, map[string]any{"type": "sfx_cue", "count": n})
	}
	var tpls []model.AudioTemplate
	s.app.DB.Where("music_asset_id = ?", id).Find(&tpls)
	for _, t := range tpls {
		refs = append(refs, map[string]any{"type": "template", "title": t.Name})
	}
	var books []model.Book
	s.app.DB.Where("cover_asset_id = ?", id).Find(&books)
	for _, b := range books {
		refs = append(refs, map[string]any{"type": "cover", "bookId": b.ID, "title": b.Title})
	}
	return refs
}

var _ = time.Second

package api

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/ai/tts"
	"voicebook/internal/core"
	"voicebook/internal/model"
)

//go:embed openapi.yaml
var openAPIYAML string

// openAPISpec serves the OpenAPI 3.1 description with this site's address filled in.
func (s *Server) openAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="voicebook-openapi.yaml"`)
	w.Write([]byte(strings.ReplaceAll(openAPIYAML, "{{PUBLIC_URL}}", strings.TrimRight(s.app.Cfg.PublicURL, "/"))))
}

// openAPIDocs opens the guide in the help center.
func (s *Server) openAPIDocs(w http.ResponseWriter, r *http.Request) {
	var a model.Article
	if s.app.DB.Select("id").Where("kind = ? AND slug = ? AND published = ?", "help", core.OpenAPIDocSlug, true).Limit(1).Find(&a).Error == nil && a.ID != "" {
		http.Redirect(w, r, "/help/"+a.ID, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/help", http.StatusFound)
}

func ttsReq(v *model.Voice, text string) tts.SynthesizeRequest {
	return tts.SynthesizeRequest{Text: text, ProviderVoiceID: v.ProviderVoiceID, Model: v.Model, Speed: firstNonZeroF(v.Defaults.V.Speed, 1), VoiceGender: v.Gender, VoiceAge: v.AgeGroup, VoiceSeed: v.ID}
}

func (s *Server) apiKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-API-Key")
		if raw == "" {
			raw = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		k, x, err := s.app.AuthAPIKey(raw, clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), keyAPIKey, k)
		ctx = context.WithValue(ctx, keyAPIAccess, x)
		ctx = context.WithValue(ctx, keyUser, x.User)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func apiAccess(r *http.Request) (*model.APIKey, *core.Access) {
	k, _ := r.Context().Value(keyAPIKey).(*model.APIKey)
	x, _ := r.Context().Value(keyAPIAccess).(*core.Access)
	return k, x
}

func needScope(k *model.APIKey, scope string) error {
	if core.HasScope(k, scope) {
		return nil
	}
	return core.Forbidden("该 API Key 没有 " + scope + " 权限，请在「工作室设置 → 开发者」里新建一个带这项权限的密钥")
}

func (s *Server) openBook(r *http.Request) (*core.Access, error) {
	_, x := apiAccess(r)
	var b model.Book
	if err := s.app.DB.First(&b, "id = ? AND studio_id = ? AND deleted_at IS NULL", param(r, "id"), x.StudioID).Error; err != nil {
		return nil, core.NotFound("图书")
	}
	return s.app.BookAccess(x.User, b.ID)
}

func (s *Server) openRoutes(r chi.Router) {
	r.Get("/voices", h(func(r *http.Request) (any, error) {
		k, x := apiAccess(r)
		if err := needScope(k, "read"); err != nil {
			return nil, err
		}
		var out []map[string]any
		for _, v := range s.app.CastingPool(x.StudioID, x.User.ID, "") {
			out = append(out, map[string]any{"id": v.ID, "name": v.Name, "gender": v.Gender, "ageGroup": v.AgeGroup, "tags": v.Tags.V, "language": v.Language, "priceTier": v.PriceTier, "sampleUrl": s.app.Cfg.PublicURL + s.app.AssetURLByID(v.SampleAssetID)})
		}
		return map[string]any{"voices": out}, nil
	}))
	r.Post("/books", h(func(r *http.Request) (any, error) {
		k, x := apiAccess(r)
		if err := needScope(k, "produce"); err != nil {
			return nil, err
		}
		var in struct {
			Title       string               `json:"title"`
			Author      string               `json:"author"`
			Text        string               `json:"text"`
			Description string               `json:"description"`
			Mode        string               `json:"mode"`
			Language    string               `json:"language"`
			AutoProduce bool                 `json:"autoProduce"`
			Config      model.PipelineConfig `json:"config"`
			Priority    int                  `json:"priority"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Text) == "" {
			return nil, core.Bad("text 不能为空")
		}
		p, err := s.app.PreviewImport(firstNonEmptyStr(in.Title, "api")+".txt", []byte(in.Text))
		if err != nil {
			return nil, err
		}
		b, err := s.app.CreateBook(r.Context(), x, core.CreateBookInput{ImportID: p.ID, Title: firstNonEmptyStr(in.Title, p.Title), Author: in.Author, Description: in.Description, Mode: in.Mode, Language: in.Language, Tier: in.Config.Tier})
		if err != nil {
			return nil, err
		}
		out := map[string]any{"bookId": b.ID, "chapters": len(p.Chapters), "detection": p.Detection}
		if in.AutoProduce {
			bx, _ := s.app.BookAccess(x.User, b.ID)
			if in.Config.Mode == "" {
				in.Config.Mode = b.Mode
			}
			if in.Priority > 0 && !s.app.HasFeature(x.StudioID, model.FeatBatch) {
				in.Priority = 0
			}
			run, err := s.app.StartPipeline(bx, in.Config, k.ID, in.Priority)
			if err != nil {
				return nil, err
			}
			out["pipelineId"] = run.ID
		}
		return out, nil
	}))
	r.Get("/books/{id}", h(func(r *http.Request) (any, error) {
		k, _ := apiAccess(r)
		if err := needScope(k, "read"); err != nil {
			return nil, err
		}
		x, err := s.openBook(r)
		if err != nil {
			return nil, err
		}
		var chs []model.Chapter
		s.app.DB.Where("book_id = ? AND included = ?", x.Book.ID, true).Order("\"index\" asc").Find(&chs)
		states := s.app.ChapterStates(x.Book.ID)
		var cv []map[string]any
		for _, c := range chs {
			cv = append(cv, map[string]any{"id": c.ID, "index": c.Index, "title": c.Title, "wordCount": c.WordCount, "durationMs": c.DurationMs, "stage": states[c.ID].Stage, "rendered": c.RenderID != "" && !c.RenderStale})
		}
		return map[string]any{"id": x.Book.ID, "title": x.Book.Title, "status": x.Book.Status, "wordCount": x.Book.WordCount, "chapters": cv, "pipelineId": x.Book.PipelineRunID}, nil
	}))
	r.Post("/books/{id}/produce", h(func(r *http.Request) (any, error) {
		k, _ := apiAccess(r)
		if err := needScope(k, "produce"); err != nil {
			return nil, err
		}
		x, err := s.openBook(r)
		if err != nil {
			return nil, err
		}
		var cfg model.PipelineConfig
		bind(r, &cfg)
		run, err := s.app.StartPipeline(x, cfg, k.ID, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"pipelineId": run.ID}, nil
	}))
	r.Get("/pipelines/{id}", h(func(r *http.Request) (any, error) {
		_, x := apiAccess(r)
		var run model.PipelineRun
		if err := s.app.DB.First(&run, "id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Error; err != nil {
			return nil, core.NotFound("任务")
		}
		v := s.app.PipelineView(&run)
		delete(v, "book")
		return v, nil
	}))
	r.Get("/books/{id}/outputs", h(func(r *http.Request) (any, error) {
		k, _ := apiAccess(r)
		if err := needScope(k, "read"); err != nil {
			return nil, err
		}
		x, err := s.openBook(r)
		if err != nil {
			return nil, err
		}
		var chs []model.Chapter
		s.app.DB.Where("book_id = ? AND included = ? AND render_id <> ''", x.Book.ID, true).Order("\"index\" asc").Find(&chs)
		var out []map[string]any
		for _, c := range chs {
			var rr model.Render
			if s.app.DB.First(&rr, "id = ?", c.RenderID).Error != nil {
				continue
			}
			var as model.Asset
			s.app.DB.First(&as, "id = ?", rr.AssetID)
			out = append(out, map[string]any{"chapterId": c.ID, "index": c.Index, "title": c.Title, "durationMs": rr.DurationMs, "format": rr.Format, "stale": c.RenderStale,
				"url": s.app.Cfg.PublicURL + s.app.Store.SignedPath(as.StorageKey, 24*time.Hour, ""), "timings": rr.Timings.V})
		}
		return map[string]any{"outputs": out, "expiresIn": 86400}, nil
	}))
	r.Post("/tts", h(func(r *http.Request) (any, error) {
		k, x := apiAccess(r)
		if err := needScope(k, "tts"); err != nil {
			return nil, err
		}
		var in struct {
			Text    string  `json:"text"`
			VoiceID string  `json:"voiceId"`
			Emotion string  `json:"emotion"`
			Speed   float64 `json:"speed"`
		}
		bind(r, &in)
		if utf8.RuneCountInString(in.Text) == 0 || utf8.RuneCountInString(in.Text) > 2000 {
			return nil, core.Bad("text 长度需在 1~2000 字之间")
		}
		var v model.Voice
		if err := s.app.DB.First(&v, "id = ?", in.VoiceID).Error; err != nil || !s.app.VoiceVisible(x.User, x.StudioID, &v) {
			return nil, core.NotFound("音色")
		}
		p, acc, err := s.app.TTSFor(&v)
		if err != nil {
			return nil, err
		}
		req := ttsReq(&v, in.Text)
		req.Emotion = in.Emotion
		if in.Speed > 0 {
			req.Speed = in.Speed
		}
		out, err := p.Synthesize(r.Context(), req)
		if err != nil {
			return nil, fmt.Errorf("配音失败%s账号「%s」：%w", core.DetailSep, acc.Name, err)
		}
		credits, err := s.app.ChargeCredits(core.Charge{StudioID: x.StudioID, UserID: x.User.ID, Feature: core.FeatQuick, Tier: v.PriceTier, Quantity: float64(utf8.RuneCountInString(in.Text)), Unit: "chars", Provider: acc.Provider, AccountID: acc.ID, Note: "Open API"})
		if err != nil {
			return nil, err
		}
		as, err := s.app.SaveAssetBytes(r.Context(), core.AssetInput{StudioID: x.StudioID, Kind: model.AssetPreview, Name: "openapi-tts", Ext: firstNonEmptyStr(out.Format, "wav"), Origin: "generated", NoPeaks: true}, out.Data)
		if err != nil {
			return nil, err
		}
		return map[string]any{"url": s.app.Cfg.PublicURL + s.app.Store.SignedPath(as.StorageKey, 24*time.Hour, ""), "durationMs": as.DurationMs, "credits": credits}, nil
	}))
}

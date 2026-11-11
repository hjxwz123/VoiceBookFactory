package core

import (
	"fmt"
	"strings"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

// LaunchItem is one line of the operator's pre-launch checklist on the admin dashboard.
type LaunchItem struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	OK     bool   `json:"ok"`
	Level  string `json:"level"` // required | recommended
	Detail string `json:"detail"`
	FixAt  string `json:"fixAt"` // admin route that fixes it
}

// LaunchCheck lists what an operator must set up before real users arrive, most critical first.
func (a *App) LaunchCheck() []LaunchItem {
	// accounts that point at this machine (test doubles) do not count as a real connection
	local := func(base string) bool {
		b := strings.ToLower(base)
		return strings.Contains(b, "://127.") || strings.Contains(b, "://localhost") || strings.Contains(b, "://0.0.0.0") || strings.Contains(b, "://[::1]")
	}
	var localNames []string
	enabled := func(kind string) (n int64) {
		var accs []model.ProviderAccount
		a.DB.Where("kind = ? AND scope = ? AND enabled = ? AND provider <> ?", kind, "platform", true, "placeholder").Find(&accs)
		for _, acc := range accs {
			if local(acc.BaseURL) {
				localNames = append(localNames, acc.Name)
				continue
			}
			n++
		}
		return
	}
	site := a.Site()
	var items []LaunchItem
	add := func(it LaunchItem) { items = append(items, it) }

	var admin model.User
	defaultPw := a.DB.Where("email = ?", NormalizeEmail(a.Cfg.AdminEmail)).First(&admin).Error == nil && secure.CheckPassword(admin.PasswordHash, "Admin@123456")
	add(LaunchItem{Key: "admin_password", Title: "修改默认管理员密码", OK: !defaultPw, Level: "required",
		Detail: map[bool]string{true: "管理员账号还在用默认密码 Admin@123456，任何人都能登录后台。请在右上角头像菜单里修改密码。", false: "已修改"}[defaultPw]})
	add(LaunchItem{Key: "dev_mode", Title: "关闭开发模式", OK: !a.Cfg.DevMode, Level: "required", FixAt: "/system",
		Detail: map[bool]string{true: "开发模式下登录验证码会直接显示在页面上，声音克隆也允许用演示引擎。正式上线前设置环境变量 VB_DEV=false 后重启。", false: "已关闭"}[a.Cfg.DevMode]})

	llm := enabled(model.KindLLM)
	add(LaunchItem{Key: "llm", Title: "接入大模型", OK: llm > 0, Level: "required", FixAt: "/providers",
		Detail: map[bool]string{true: fmt.Sprintf("已启用 %d 个账号，用于角色分析、画本、选角", llm), false: "还没有可用的大模型账号：角色分析、画本、选角都无法使用"}[llm > 0]})
	tts := enabled(model.KindTTS)
	add(LaunchItem{Key: "tts", Title: "接入语音合成", OK: tts > 0, Level: "required", FixAt: "/providers",
		Detail: map[bool]string{true: fmt.Sprintf("已启用 %d 个语音合成账号", tts), false: "只有内置演示引擎，生成的声音不能用于正式作品"}[tts > 0]})
	asr := enabled(model.KindASR)
	add(LaunchItem{Key: "asr", Title: "接入语音识别", OK: asr > 0, Level: "recommended", FixAt: "/providers",
		Detail: map[bool]string{true: "已接入，智能审听会逐字比对原文，智能对轨可用", false: "没有语音识别账号时，智能审听只检查音量和杂音，不能发现漏读、错读；智能对轨不可用"}[asr > 0]})

	img := enabled(model.KindImage)
	add(LaunchItem{Key: "image", Title: "接入图片生成", OK: img > 0, Level: "recommended", FixAt: "/providers",
		Detail: map[bool]string{true: "已接入，AI 封面可以写上书名，角色头像也会更像真人插画", false: "只有内置生成器：AI 封面只能画抽象图案，不能写书名。推荐接入 OpenAI GPT Image 或 Google Nano Banana"}[img > 0]})

	missing := []string{}
	if strings.TrimSpace(site.Operator) == "" {
		missing = append(missing, "运营主体")
	}
	if strings.TrimSpace(site.SupportEmail) == "" {
		missing = append(missing, "客服邮箱")
	}
	add(LaunchItem{Key: "operator", Title: "填写运营主体和客服邮箱", OK: len(missing) == 0, Level: "required", FixAt: "/settings",
		Detail: map[bool]string{true: "协议正文和侵权投诉渠道会使用这些信息", false: "还没有填写：" + strings.Join(missing, "、") + "。协议里会显示为空，用户也找不到投诉渠道"}[len(missing) == 0]})

	untouched := 0
	for _, d := range defaultLegal {
		var art model.Article
		if a.DB.Where("kind = ? AND slug = ?", "legal", d.Slug).First(&art).Error == nil && art.Body == strings.TrimSpace(d.Body) {
			untouched++
		}
	}
	add(LaunchItem{Key: "legal", Title: "审核协议与政策", OK: untouched == 0, Level: "recommended", FixAt: "/legal",
		Detail: map[bool]string{true: "协议已按你的情况修改过", false: fmt.Sprintf("%d 份协议仍是系统默认文本，正式上线前请让法务审核", untouched)}[untouched == 0]})

	smtp := SMTPSettings{}
	a.Setting("smtp", &smtp)
	add(LaunchItem{Key: "smtp", Title: "配置发信邮箱", OK: smtp.Host != "", Level: "recommended", FixAt: "/settings",
		Detail: map[bool]string{true: "已配置，可以发送注册验证和找回密码邮件", false: "没有配置时用户无法通过邮件找回密码"}[smtp.Host != ""]})

	if len(localNames) > 0 {
		add(LaunchItem{Key: "local_accounts", Title: "停用本机测试账号", OK: false, Level: "required", FixAt: "/providers",
			Detail: "这些账号指向本机地址（通常是开发测试用的模拟服务），正式环境里无法工作：" + strings.Join(localNames, "、")})
	}

	music, sfx := a.MusicLibraryStatus(), a.SfxLibraryStatus()
	libOK := music.Imported > 0 && sfx.Imported > 0
	add(LaunchItem{Key: "library", Title: "导入官方配乐库和音效库", OK: libOK, Level: "recommended", FixAt: "/assets",
		Detail: fmt.Sprintf("配乐 %d/%d 首，音效 %d/%d 条已导入。自动配乐和自动配音效依赖这两个素材库", music.Imported, music.Total, sfx.Imported, sfx.Total)})
	return items
}

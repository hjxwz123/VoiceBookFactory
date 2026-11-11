# 更新记录

版本号遵循语义化版本：`主版本.次版本.修订号`。

## [0.1.0] - 2026-11-11

第一个公开版本。

### 制作
- 导入书稿（txt、docx、epub、pdf），自动拆章；一键成书：角色分析 → 选角 → 画本 → 配音 → 配乐 → 合成。
- 画本：AI 逐句标注说话人、情绪、表演标签和读音，不改原文；制作台逐句精修，支持拖拽排序、撤销、快捷键。
- 配音：千问（Qwen-Audio / CosyVoice）与 MiniMax 语音，音色库、AI 设计音色与声音克隆；真人配音演员逐句录音或整章上传自动对轨。
- 读音表全书统一；审听打点、智能审听、返工工单；交付前检查，导出 MP3 / WAV / M4A / 整本 M4B、字幕与分轨。
- 素材：官方曲库与音效库、片头片尾模板；AI 生成封面（GPT Image、Nano Banana）、音效和配乐（Suno，经 New API / One API 中转）。

### 团队与平台
- 工作室成员与角色、生产流程看板、计件结算；会员方案、积分、兑换码。
- 管理后台：用户、内容、AI 供应商与路由、积分定价、提示词、内容安全、运行状态、上线前检查。
- 第三方登录（标准 OAuth 2.0 / OpenID Connect），可选阿里云 OSS 文件存储。
- 产品首页与「纸与墨」视觉风格，白天 / 夜间模式。

### 部署
- 版本号：`VERSION` 文件，`voicebook version`、`/healthz` 和后台「运行状态」可以看到当前版本。
- Docker 镜像 `ghcr.io/hjxwz123/voicebookfactory:<版本>`（amd64 / arm64），`docker-compose.yml` 一次启动应用、PostgreSQL 17 和 Redis 7，通过 `VB_VERSION` 部署指定版本。
- 支持 PostgreSQL：
  - 搜索不区分大小写，JSON 字段在 PostgreSQL 上按文本匹配；
  - 字符串长度不受 varchar 限制；
  - 多个实例同时启动时，迁移和初始数据依次进行。
- 支持 Redis，用于多实例：
  - 实时消息；
  - 任务唤醒与取消；
  - 限流；
  - 缓存失效；
  - 定时任务锁。
- 生产模式缺少 `VB_JWT_SECRET` / `VB_SECRET_KEY` 时拒绝启动；镜像以非 root 用户运行，自带健康检查。

# 更新记录

版本号遵循语义化版本：`主版本.次版本.修订号`。

## [0.1.1] - 2026-11-12

### 修复
- 用 `http://IP:端口` 访问时，登录后刷新页面需要重新登录。原因是登录 cookie 是否标记为仅限 HTTPS，是按 `VB_PUBLIC_URL` 判断的。现在改为按这次请求实际是否通过 HTTPS 到达来判断：直接 TLS、反向代理的 `X-Forwarded-Proto` / `Forwarded`，或者请求的正是 https 公开域名。经反向代理的 HTTPS 访问仍然带 Secure。
- 复制按钮在 http 访问时没有反应：浏览器只在 HTTPS 下提供剪贴板接口，现在 http 下会改用兼容方式复制，失败时给出提示。
- 在 http 访问时点录音，提示改为说明浏览器只允许 HTTPS 页面使用麦克风。

### 部署
- 国内服务器部署：
  - `.env` 里可以用 `VB_IMAGE`、`POSTGRES_IMAGE`、`REDIS_IMAGE` 指定国内镜像地址；
  - CI 可以把应用、PostgreSQL、Redis 镜像同步到自己的阿里云容器镜像服务（配置 `ALIYUN_NAMESPACE` 等变量和密钥后启用）；
  - 从源码构建镜像时，可以使用 npmmirror、goproxy.cn、阿里云 Debian 镜像（`.env` 里的 `BUILD_*`）；
  - README 新增「国内服务器部署」一节。
- 镜像不再附带 provenance 证明清单，只包含 amd64 和 arm64 两个架构，兼容更多镜像仓库和镜像站。

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

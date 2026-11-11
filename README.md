# 声书工坊 VoiceBook Factory

AI 有声书工作台：导入书稿，AI 分析角色、生成画本（演播脚本）、挑选声音并配音；制作台逐句精修、真人录音、审听返工，最后交付整本有声书。包括用户端（工作台、制作台）和管理后台。

- 后端：Go（单个二进制，内含任务队列），PostgreSQL 或 SQLite，可选 Redis（多实例）
- 前端：React + Vite + Tailwind
- AI：大模型（OpenAI / Claude 协议及兼容服务）、语音合成（千问、MiniMax）、图片（GPT Image、Nano Banana）、音乐（Suno 中转）等，在管理后台配置

当前版本见 [`VERSION`](VERSION)，更新记录见 [`CHANGELOG.md`](CHANGELOG.md)。

## 用 Docker 部署（推荐）

镜像发布在 GitHub Container Registry，每个版本一个标签，支持 linux/amd64 和 linux/arm64：

```
ghcr.io/hjxwz123/voicebookfactory:0.1.0   # 指定版本（推荐）
ghcr.io/hjxwz123/voicebookfactory:0.1     # 0.1.x 的最新版
ghcr.io/hjxwz123/voicebookfactory:latest  # 最新正式版
ghcr.io/hjxwz123/voicebookfactory:edge    # main 分支最新构建（开发版）
```

生产环境由三个容器组成：应用、PostgreSQL、Redis。

```bash
git clone https://github.com/hjxwz123/VoiceBookFactory.git && cd VoiceBookFactory
cp .env.example .env
# 编辑 .env：VB_VERSION、VB_PUBLIC_URL、VB_JWT_SECRET、VB_SECRET_KEY、POSTGRES_PASSWORD、VB_ADMIN_PASSWORD
docker compose up -d
docker compose ps        # app 显示 healthy 即可访问 http://服务器:8080
```

- **管理后台**：`/admin`，用 `.env` 里的管理员账号登录，先看「数据看板 → 上线前检查」，再到「模型与供应商」接入 AI 服务。
- **升级或回滚**：把 `.env` 里的 `VB_VERSION` 改成目标版本，再执行 `docker compose up -d`。数据库结构在启动时自动迁移。
- **数据**：都在 Docker 卷里：`app-data`（书稿、音频、导出文件）、`pg-data`、`redis-data`。备份时用 `pg_dump` 导出数据库，再备份 `app-data` 卷。也可以在后台把文件存到阿里云 OSS。
- **密钥**：`VB_SECRET_KEY` 用来加密数据库里保存的 API Key，设置后不要更改。生产模式（`VB_DEV=false`）下，没有设置 `VB_JWT_SECRET` 和 `VB_SECRET_KEY` 时服务会拒绝启动。
- **数据库密码**：`POSTGRES_PASSWORD` 请只用字母和数字（它会写进连接串）。
- **从源码构建镜像**：`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`

### 多实例

多个应用实例可以连同一个 PostgreSQL 和 Redis，放在负载均衡后面。Redis 负责在实例之间共享以下内容：

- 实时消息：浏览器连到任意实例都能收到；
- 后台任务的唤醒和取消；
- 限流计数；
- 设置缓存失效；
- 定时任务锁：每个定时任务只在一个实例上运行。

各实例需要共享文件存储：在后台启用阿里云 OSS，或者挂载同一个共享卷。

## 配置（环境变量）

| 变量 | 说明 |
|---|---|
| `VB_DB_DRIVER` / `VB_DB_DSN` | `postgres` 和连接串（`host=… user=… password=… dbname=… sslmode=disable`）；默认 `sqlite`，数据文件放在 `VB_DATA_DIR` 下 |
| `VB_REDIS_URL` | `redis://host:6379/0`；只跑一个实例时可以不填 |
| `VB_JWT_SECRET` / `VB_SECRET_KEY` | 生产模式必填 |
| `VB_PUBLIC_URL` | 站点对外地址 |
| `VB_DATA_DIR` | 本地文件目录（容器内为 `/data`） |
| `VB_DEV` | `true` 为开发模式：验证码直接显示在页面上，允许演示引擎 |
| `VB_WORKERS` | 后台任务并发数 |
| `VB_ADMIN_EMAIL` / `VB_ADMIN_PASSWORD` | 首次启动时创建的管理员 |

健康检查：`GET /healthz` 返回版本、数据库和 Redis 状态；任一不可用时返回 503。容器镜像自带 `HEALTHCHECK`。

## 本地开发

```bash
# 后端（SQLite，开发模式）
cd server && go run ./cmd/voicebook serve        # :8080；VB_ADDR 可改端口
# 前端
cd web && npm ci && npm run dev                   # :5173，接口代理到 :18080（见 vite.config.ts）
```

测试：

- `cd server && go test ./...`：默认用 SQLite 和内存版 Redis。设置 `VB_TEST_PG_DSN` 和 `VB_TEST_REDIS_URL` 后，同一套测试会改用真实的 PostgreSQL 和 Redis。
- 端到端脚本在 `server/scripts/`，配合 `fake_llm.py` 使用本机替身服务，不会调用真实的 AI 平台：
  - `smoke.py`：主流程冒烟测试；
  - `full_smoke.py`：其余功能；
  - `cluster_check.py`：两个实例之间的共享。

## 发布流程

GitHub Actions（`.github/workflows/ci.yml`）在每次推送时运行：

- SQLite 上的 Go 测试；
- PostgreSQL + Redis 上的 Go 测试；
- 前端构建；
- 端到端测试：两个实例共用 PostgreSQL + Redis，跑冒烟、全功能和集群检查。

全部通过后：

- **推送到 main**：发布 `edge` 镜像。
- **推送标签 `vX.Y.Z`**：要求与 `VERSION`、`web/package.json`、`server/internal/version` 一致。然后发布 `X.Y.Z`、`X.Y`、`latest` 镜像，用 docker compose 实际部署一次做验证，再根据 `CHANGELOG.md` 创建 GitHub Release。

发新版本：修改这三处版本号，在 `CHANGELOG.md` 写更新内容，提交后执行 `git tag vX.Y.Z && git push origin vX.Y.Z`。

## 版权与责任

本平台只提供制作工具，不审查也不保证用户内容的版权；用户上传的书稿、素材和声音需自行确认拥有使用权。详见站内《版权与素材使用声明》。

# 声书工坊 VoiceBook Factory

AI 有声书工作台：导入书稿，AI 分析角色、生成画本（演播脚本）、挑选声音并配音；制作台逐句精修、真人录音、审听返工，最后交付整本有声书。包括用户端（工作台、制作台）和管理后台。

- 后端：Go（单个程序，内含任务队列），数据库用 PostgreSQL 或 SQLite，可选 Redis（多实例时使用）
- 前端：React + Vite + Tailwind
- AI：大模型（OpenAI / Claude 协议及兼容服务）、语音合成（千问、MiniMax）、图片（GPT Image、Nano Banana）、音乐（Suno 中转）等，都在管理后台配置

当前版本见 [`VERSION`](VERSION)，更新记录见 [`CHANGELOG.md`](CHANGELOG.md)，各版本下载见 [Releases](https://github.com/hjxwz123/VoiceBookFactory/releases)。

---

## 部署

### 选择部署方式

| 方式 | 适合 | 组成 |
|---|---|---|
| **标准部署**（推荐） | 正式运营 | Docker Compose：应用 + PostgreSQL + Redis |
| **单容器** | 试用、个人或小团队 | 一个 Docker 容器，数据库用内置 SQLite |
| **源码 / 二进制** | 不想用 Docker | 自己编译，用 systemd 运行 |

镜像发布在 GitHub Container Registry，公开可拉取，支持 linux/amd64 和 linux/arm64。每个版本有自己的标签：

```
ghcr.io/hjxwz123/voicebookfactory:0.1.0   # 指定版本（推荐，部署和回滚都按版本号）
ghcr.io/hjxwz123/voicebookfactory:0.1     # 0.1.x 系列的最新版
ghcr.io/hjxwz123/voicebookfactory:latest  # 最新正式版
ghcr.io/hjxwz123/voicebookfactory:edge    # main 分支最新构建，仅供测试
```

### 服务器要求

- Linux 64 位（x86_64 或 ARM64），Docker 24 以上并带 Compose 插件
- 推荐 2 核 4 GB 内存起步。配音、合成和导出会占用 CPU，并发多时可以加大配置并调高 `VB_WORKERS`
- 磁盘：系统和镜像约 2 GB；书稿、音频和导出文件按实际使用增长（一本书的成品音频通常几百 MB）。也可以在后台改为存到阿里云 OSS
- 需要能访问你使用的 AI 平台接口（大模型、语音合成等）

### 第一步：安装 Docker

已经装好的可以跳过。用 Docker 官方安装脚本：

```bash
curl -fsSL https://get.docker.com | sh
# 国内服务器可以用阿里云镜像源：
# curl -fsSL https://get.docker.com | sh -s -- --mirror Aliyun
sudo systemctl enable --now docker
docker compose version          # 能显示版本号即可
```

### 第二步：准备部署目录和配置

只需要两个文件：`docker-compose.yml` 和 `.env`。按版本号下载（把 `v0.1.0` 换成要部署的版本）：

```bash
mkdir -p /opt/voicebook && cd /opt/voicebook
curl -fsSLO https://raw.githubusercontent.com/hjxwz123/VoiceBookFactory/v0.1.0/docker-compose.yml
curl -fsSL -o .env https://raw.githubusercontent.com/hjxwz123/VoiceBookFactory/v0.1.0/.env.example
```

也可以克隆整个仓库：`git clone https://github.com/hjxwz123/VoiceBookFactory.git && cd VoiceBookFactory && cp .env.example .env`。

**生成密钥并填写 `.env`**。下面的命令会自动填好两个密钥和数据库密码：

```bash
sed -i "s/^VB_JWT_SECRET=.*/VB_JWT_SECRET=$(openssl rand -hex 32)/" .env
sed -i "s/^VB_SECRET_KEY=.*/VB_SECRET_KEY=$(openssl rand -hex 32)/" .env
sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 16)/" .env
```

然后用编辑器打开 `.env`，至少检查下面几项：

| 配置 | 说明 |
|---|---|
| `VB_VERSION` | 要部署的版本，例如 `0.1.0`。升级和回滚只改这里 |
| `VB_PUBLIC_URL` | 用户访问的地址，例如 `https://voicebook.example.com`。邮件链接、分享链接和第三方登录回调都会用到，必须和实际访问地址一致 |
| `VB_PORT` | 应用在宿主机上监听的端口，默认 `8080`。前面有 Nginx / Caddy 时建议写 `127.0.0.1:8080`，只允许本机访问 |
| `VB_JWT_SECRET` | 登录令牌签名密钥，必填 |
| `VB_SECRET_KEY` | 用来加密数据库里保存的 AI 平台 API Key，必填。**设置后不要再改**，否则已保存的密钥无法解密 |
| `POSTGRES_PASSWORD` | 数据库密码，必填。**只用字母和数字**（它会写进连接串）。数据库第一次初始化后再改这里不会生效 |
| `VB_ADMIN_EMAIL` / `VB_ADMIN_PASSWORD` | 第一次启动时创建的管理员账号。不填密码会使用默认密码 `Admin@123456`，务必填写或在登录后立即修改 |
| `VB_WORKERS` | 后台任务（配音、合成、导出）的并发数，默认 4 |

`.env` 里有密码和密钥，请妥善保管，不要提交到代码仓库：`chmod 600 .env`。

### 第三步：启动

```bash
docker compose up -d       # 第一次会拉取镜像，需要几分钟
docker compose ps          # app、db、redis 都显示 healthy 即启动完成
curl http://127.0.0.1:8080/healthz
```

`/healthz` 的返回里应能看到 `"version":"0.1.0"`、`"db":"postgres"`、`"bus":"redis"`。第一次启动时后台会写入内置音色目录和素材库，期间 `"seeding"` 为 `true`，通常一分钟内变为 `false`，不影响访问。

启动失败时用 `docker compose logs app` 查看原因，见下方[常见问题](#常见问题)。

### 第四步：配置域名和 HTTPS

正式使用请用 Nginx 或 Caddy 做反向代理并开启 HTTPS，同时把 `.env` 里的 `VB_PORT` 改成 `127.0.0.1:8080`，`VB_PUBLIC_URL` 改成 https 地址，然后执行 `docker compose up -d` 生效。

注意两点：
- 实时进度用 SSE 长连接（`/api/v1/events`、`/api/admin/v1/events`），代理不能缓冲这两个地址，超时也要放长。
- 上传书稿和音频的大小上限默认是 200 MB（后台「系统设置 → 站点」可以修改），代理的上传限制要不小于它。

**Caddy**（自动申请和续期证书，最简单）：

```caddyfile
voicebook.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

**Nginx**：

```nginx
server {
    listen 443 ssl http2;          # nginx 1.25.1 以上可写成 listen 443 ssl; 再加一行 http2 on;
    server_name voicebook.example.com;
    ssl_certificate     /etc/nginx/ssl/voicebook.pem;
    ssl_certificate_key /etc/nginx/ssl/voicebook.key;

    client_max_body_size 512m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;
    }

    # 实时进度（SSE）：关闭缓冲，长连接
    location ~ ^/api/(v1|admin/v1)/events$ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 1h;
    }
}

server {
    listen 80;
    server_name voicebook.example.com;
    return 301 https://$host$request_uri;
}
```

### 第五步：在管理后台完成初始化

打开 `https://你的域名/admin`，用 `.env` 里的管理员账号登录：

1. **修改管理员密码**：右上角头像 → 修改密码。
2. **数据看板 → 上线前检查**：逐项处理，标记「必须」的都要完成。
3. **模型与供应商**：
   - 至少接入一个大模型账号（角色分析、画本、选角都要用），以及一个语音合成账号（千问或 MiniMax）。
   - 按需接入图片生成（封面）、音乐生成（Suno）和语音识别（对轨、智能审听）。
   - 每个账号添加后点「测试」确认能连通。
4. **系统设置**：
   - 站点：名称、客服邮箱、运营主体、ICP 备案号，以及是否开放注册。
   - 邮件：SMTP 发信。不配置的话，注册时不校验邮箱，用户也收不到找回密码和验证码登录的邮件。
   - 文件存储：可选阿里云 OSS。
   - 第三方登录：可选。
5. **协议与政策**：系统自带默认文本，正式上线前请让法务审核。
6. 打开 `https://你的域名/` 注册一个普通账号，试做一本书，确认全流程正常。

### 升级与回滚

升级前先[备份](#备份与恢复)。然后修改 `.env` 里的 `VB_VERSION`：

```bash
cd /opt/voicebook
sed -i "s/^VB_VERSION=.*/VB_VERSION=0.1.1/" .env     # 换成目标版本
docker compose pull app
docker compose up -d
docker compose exec app /app/voicebook version       # 确认版本
```

- 数据库结构在启动时自动迁移。
- 新版本的 `docker-compose.yml` 或 `.env.example` 有变化时，Release 说明里会写明，按说明更新即可。
- 回滚就是把 `VB_VERSION` 改回旧版本再执行 `docker compose up -d`。迁移只会新增表和字段，旧版本一般可以直接运行；如果新版本说明里写了不能回滚，请用升级前的备份恢复。

### 备份与恢复

数据在三个 Docker 卷里：

| 卷 | 内容 |
|---|---|
| `voicebook_pg-data` | 数据库：账号、图书、画本、设置等 |
| `voicebook_app-data` | 文件：书稿、音频、导出文件（启用 OSS 后只是缓存） |
| `voicebook_redis-data` | 临时数据（限流、锁），不需要备份 |

下面的命令假设 `POSTGRES_USER` 和 `POSTGRES_DB` 保持默认的 `voicebook`；改过的话请把命令里的 `voicebook` 换成实际值。

**备份**（建议每天用 cron 执行，并把备份文件复制到另一台机器）：

```bash
cd /opt/voicebook
mkdir -p backups
docker compose exec -T db pg_dump -U voicebook -d voicebook | gzip > backups/db-$(date +%F).sql.gz
docker run --rm -v voicebook_app-data:/data:ro -v "$PWD/backups":/backup alpine \
  tar czf /backup/app-data-$(date +%F).tgz -C /data .
cp .env backups/env-$(date +%F)        # .env 里的 VB_SECRET_KEY 丢了就无法解密已保存的 API Key
```

**恢复**：

```bash
cd /opt/voicebook
docker compose stop app
docker compose exec -T db dropdb -U voicebook voicebook
docker compose exec -T db createdb -U voicebook voicebook
gunzip -c backups/db-2026-11-11.sql.gz | docker compose exec -T db psql -U voicebook -d voicebook -q
docker run --rm -v voicebook_app-data:/data -v "$PWD/backups":/backup alpine \
  sh -c "rm -rf /data/* && tar xzf /backup/app-data-2026-11-11.tgz -C /data && chown -R 10001:10001 /data"
docker compose start app
```

恢复时请使用备份时的 `.env`，`VB_SECRET_KEY` 必须一致。

### 日常运维

```bash
docker compose ps                    # 状态
docker compose logs -f app           # 应用日志
docker compose restart app           # 重启应用
docker compose down                  # 停止（数据保留在卷里）
docker compose exec app /app/voicebook version
```

管理后台「运行状态」页可以看到版本、内存、数据库大小、是否连上 Redis、ffmpeg 和邮件是否可用；「任务队列」页可以看到后台任务。

### 单容器部署（SQLite）

试用或小规模使用时，可以只跑一个容器，不需要 PostgreSQL 和 Redis：

```bash
docker run -d --name voicebook --restart unless-stopped \
  -p 8080:8080 \
  -v voicebook-data:/data \
  -e VB_PUBLIC_URL=http://服务器IP:8080 \
  -e VB_JWT_SECRET=$(openssl rand -hex 32) \
  -e VB_SECRET_KEY=$(openssl rand -hex 32) \
  -e VB_ADMIN_PASSWORD='换成你的管理员密码' \
  ghcr.io/hjxwz123/voicebookfactory:0.1.0
```

- 数据库和文件都在 `voicebook-data` 卷里。
- 上面的命令每次运行都会生成新的密钥。请先把生成的两个密钥记下来，以后升级重建容器时传入同样的值；`VB_SECRET_KEY` 变了，已保存的 API Key 就无法解密。
- 升级：`docker pull` 新版本镜像，删除旧容器，再用相同参数和新的版本号重新运行。
- SQLite 数据目前没有一键迁移到 PostgreSQL 的工具。正式运营建议一开始就用标准部署。

### 不用 Docker：源码构建

需要 Go 1.27、Node.js 22 以上、ffmpeg。数据库可以用 PostgreSQL，也可以用默认的 SQLite。

```bash
git clone https://github.com/hjxwz123/VoiceBookFactory.git && cd VoiceBookFactory
git checkout v0.1.0
(cd web && npm ci && npm run build)
(cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X voicebook/internal/version.Version=$(cat ../VERSION)" -o voicebook ./cmd/voicebook)
sudo mkdir -p /opt/voicebook/data
sudo cp server/voicebook /opt/voicebook/ && sudo cp -r web/dist /opt/voicebook/web
sudo useradd --system --home /opt/voicebook voicebook && sudo chown -R voicebook: /opt/voicebook
```

`/etc/voicebook.env`（权限 600）：

```ini
VB_ADDR=127.0.0.1:8080
VB_PUBLIC_URL=https://voicebook.example.com
VB_DEV=false
VB_DATA_DIR=/opt/voicebook/data
VB_WEB_DIR=/opt/voicebook/web
VB_JWT_SECRET=用 openssl rand -hex 32 生成
VB_SECRET_KEY=用 openssl rand -hex 32 生成
VB_ADMIN_PASSWORD=你的管理员密码
# 使用 PostgreSQL / Redis 时：
# VB_DB_DRIVER=postgres
# VB_DB_DSN=host=127.0.0.1 port=5432 user=voicebook password=xxx dbname=voicebook sslmode=disable
# VB_REDIS_URL=redis://127.0.0.1:6379/0
```

`/etc/systemd/system/voicebook.service`：

```ini
[Unit]
Description=VoiceBook Factory
After=network-online.target postgresql.service redis-server.service

[Service]
User=voicebook
WorkingDirectory=/opt/voicebook
EnvironmentFile=/etc/voicebook.env
ExecStart=/opt/voicebook/voicebook serve
Restart=always
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now voicebook
journalctl -u voicebook -f
```

### 多实例部署

访问量大时可以运行多个应用实例，放在负载均衡后面：

- **共享数据库和 Redis**：所有实例连同一个 PostgreSQL 和 Redis（同样的 `VB_DB_DSN`、`VB_REDIS_URL`）。
- **相同密钥**：`VB_JWT_SECRET` 和 `VB_SECRET_KEY` 必须完全相同。
- **共享文件存储**：在后台启用阿里云 OSS，或者把 `/data` 挂到同一个共享卷（NFS 等）。

Redis 负责在实例之间共享以下内容，用户连到哪个实例都一样：

- 实时消息；
- 后台任务的唤醒和取消；
- 限流计数；
- 设置缓存；
- 定时任务锁：每个定时任务只在一个实例上运行。

多个实例同时启动时，会依次执行数据库迁移和初始数据写入。

### 国内服务器拉取镜像慢

- **给 Docker 配代理**：为 Docker 服务配置 HTTP 代理（systemd 的 `Environment=HTTPS_PROXY=...`），或者使用你信任的 ghcr.io 镜像加速服务。
- **离线导入**：在能访问 ghcr.io 的机器上导出镜像，再拷到服务器导入：
  ```bash
  docker pull ghcr.io/hjxwz123/voicebookfactory:0.1.0
  docker save ghcr.io/hjxwz123/voicebookfactory:0.1.0 | gzip > voicebook-0.1.0.tar.gz
  # 拷到服务器后：
  gunzip -c voicebook-0.1.0.tar.gz | docker load
  ```
  PostgreSQL 和 Redis 镜像（`postgres:17-alpine`、`redis:7-alpine`）可以用同样方法导入。注意服务器和导出机器的 CPU 架构要一致（`--platform linux/arm64` 可以指定）。
- **在服务器上自己构建**：克隆仓库后执行 `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`。

### 常见问题

| 现象 | 原因和解决 |
|---|---|
| app 不断重启，日志提示「生产模式必须设置 VB_JWT_SECRET 和 VB_SECRET_KEY」 | `.env` 里没有填这两个密钥，按第二步生成 |
| `docker compose up` 提示 `required variable ... is missing a value` | `.env` 里缺少必填项（`VB_VERSION`、`POSTGRES_PASSWORD`），或者 `.env` 不在 `docker-compose.yml` 同一目录 |
| app 日志里数据库认证失败（password authentication failed） | 数据库第一次初始化后又改了 `POSTGRES_PASSWORD`。把它改回原来的值；或者在库里修改密码：`docker compose exec db psql -U voicebook -c "ALTER USER voicebook PASSWORD '新密码'"` |
| `port is already allocated` | 端口被占用，修改 `.env` 里的 `VB_PORT` |
| 页面里任务进度不更新 | 反向代理缓冲了 SSE，按第四步关闭 `/api/v1/events` 的缓冲 |
| 上传大文件失败（413） | 反向代理上传限制太小，调大 Nginx 的 `client_max_body_size` |
| 用户收不到验证码邮件，找回密码用不了 | 后台「系统设置 → 邮件」没有配置 SMTP |
| 第三方登录回调地址不对 | `VB_PUBLIC_URL` 和实际访问地址不一致 |
| 单容器或源码部署时提示数据目录没有写权限 | 镜像以 uid 10001 运行。挂载宿主机目录时先执行 `chown -R 10001:10001 目录`，或者改用命名卷 |
| 已保存的 API Key 显示无法解密 | `VB_SECRET_KEY` 被改过。改回原值，或在后台重新填写各账号的 API Key |

---

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `VB_ADDR` | `:8080` | 监听地址 |
| `VB_PUBLIC_URL` | `http://localhost:8080` | 站点对外地址 |
| `VB_DEV` | 镜像内 `false`，源码运行 `true` | 开发模式：验证码直接显示在页面上，允许演示引擎。生产必须为 `false` |
| `VB_JWT_SECRET` / `VB_SECRET_KEY` | 无 | 生产模式必填 |
| `VB_DB_DRIVER` | `sqlite` | `sqlite` 或 `postgres` |
| `VB_DB_DSN` | `数据目录/voicebook.db` | PostgreSQL 连接串：`host=… port=5432 user=… password=… dbname=… sslmode=disable` |
| `VB_REDIS_URL` | 无 | `redis://[:密码@]host:6379/0`；单实例可以不填 |
| `VB_DATA_DIR` | `./data`（镜像内 `/data`） | 本地文件目录 |
| `VB_WEB_DIR` | `../web/dist`（镜像内 `/app/web`） | 前端文件目录 |
| `VB_WORKERS` | `4` | 后台任务并发数 |
| `VB_ADMIN_EMAIL` / `VB_ADMIN_PASSWORD` | `admin@example.com` / `Admin@123456` | 第一次启动时创建的管理员 |
| `VB_LLM_PROTOCOL` / `VB_LLM_BASE_URL` / `VB_LLM_API_KEY` / `VB_LLM_MODEL` | 无 | 可选：第一次启动时自动创建的大模型账号 |
| `VB_BOT_CONTACT` | 无 | 下载 Wikimedia / OpenGameArt 曲库时附带的联系方式 |
| `VB_FFMPEG` / `VB_FFPROBE` | `ffmpeg` / `ffprobe` | ffmpeg 路径（镜像已内置） |

健康检查：`GET /healthz` 返回版本、数据库和 Redis 状态，任一不可用时返回 503；镜像自带 `HEALTHCHECK`（`voicebook health`）。

## 本地开发

```bash
# 后端（SQLite，开发模式）
cd server && VB_ADDR=:18080 go run ./cmd/voicebook serve
# 前端（:5173，接口代理到 :18080）
cd web && npm ci && npm run dev
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
- **推送标签 `vX.Y.Z`**：
  1. 检查标签与 `VERSION`、`web/package.json`、`server/internal/version` 一致；
  2. 发布 `X.Y.Z`、`X.Y`、`latest` 镜像；
  3. 用 docker compose 实际部署一次做验证；
  4. 根据 `CHANGELOG.md` 创建 GitHub Release。

发新版本：修改这三处版本号，在 `CHANGELOG.md` 写更新内容，提交后执行 `git tag vX.Y.Z && git push origin vX.Y.Z`。

## 版权与责任

本平台只提供制作工具，不审查也不保证用户内容的版权；用户上传的书稿、素材和声音需自行确认拥有使用权。详见站内《版权与素材使用声明》。

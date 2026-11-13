开放接口让你的系统直接调用声书工坊：批量导入书稿、发起一键成书、查询进度、下载成品，或者把一段文字配成语音。

- 接口地址：`站点地址/openapi/v1`，例如 `https://voicebook.example.com/openapi/v1`
- 描述文件：`/openapi/v1/openapi.yaml`（OpenAPI 3.1，可以导入 Postman、Apifox 等工具）
- 需要会员套餐包含「开放平台」

## 1. 创建密钥

在「工作室设置 → 开发者」点击 **创建密钥**，选择权限：

| 权限 | 能做什么 |
| --- | --- |
| `read` | 查音色、查图书和任务进度、获取成品下载地址 |
| `produce` | 导入书稿、发起一键成书（消耗积分） |
| `tts` | 单段配音（消耗积分） |

密钥只显示一次，请保存在服务器端，不要放进网页或 App 里。还可以设置：

- **每分钟请求上限**：超出后返回 429，等一分钟再试；
- **IP 白名单**：只允许这些 IP 调用。

密钥以工作室所有者的身份操作，消耗的是工作室的积分。不用了就在同一页停用或删除。

## 2. 认证

每个请求带上密钥，两种写法任选：

```
Authorization: Bearer vb_xxxxxxxx
X-API-Key: vb_xxxxxxxx
```

请求和响应都是 JSON（UTF-8）。

## 3. 出错时

HTTP 状态码不是 200 时，返回：

```json
{ "error": { "code": "insufficient_credits", "message": "积分不足：开始制作至少需要 120 积分（角色分析和第一章），当前可用 30" } }
```

`message` 是写给人看的，可以直接展示；程序请按 `code` 判断：

| code | HTTP | 含义 |
| --- | --- | --- |
| `bad_request` | 400 | 参数不对 |
| `unauthorized` | 401 | 密钥无效或已停用 |
| `forbidden` | 403 | 密钥没有这项权限，或 IP 不在白名单 |
| `plan_required` | 403 | 会员套餐不包含这项功能（例如精品音色） |
| `not_found` | 404 | 图书、任务或音色不存在 |
| `conflict` | 409 | 例如这本书已经有进行中的一键成书 |
| `insufficient_credits` | 402 | 积分不足 |
| `rate_limited` | 429 | 超过每分钟请求上限 |
| `internal` | 500 | 服务端出错。`message` 里带一个编号，把编号告诉平台客服即可查到原因 |

## 4. 接口

### 列出音色 `GET /voices`（read）

返回工作室能用的音色，`id` 用于配音和指定旁白。

```json
{ "voices": [ { "id": "01a1…", "name": "沉稳旁白·男", "gender": "male", "ageGroup": "adult", "tags": ["旁白"], "language": "zh-CN", "priceTier": "premium", "sampleUrl": "https://…" } ] }
```

### 导入书稿 `POST /books`（produce）

| 字段 | 说明 |
| --- | --- |
| `text` | 必填。整本书稿的纯文本，章节标题单独成行（如「第一章 初见」） |
| `title`、`author`、`description` | 可选 |
| `mode` | `single` 单播、`multi` 多播有声书（默认）、`drama` 广播剧 |
| `language` | 默认 `zh-CN` |
| `autoProduce` | `true` 时创建后立即开始一键成书 |
| `config` | 一键成书的设置，见下方「一键成书设置」 |

```bash
curl -X POST "$BASE/books" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"title":"雨夜","text":"第一章 初见\n林黛玉坐在窗前……","autoProduce":true,"config":{"tier":"standard","bgm":true}}'
```

```json
{ "bookId": "01a1…", "chapters": 24, "detection": "rules", "pipelineId": "01a1…" }
```

`detection` 说明章节是怎么识别的：`structure` 文档目录、`rules` 章节标题、`size` 没找到标题时按字数切分、`single` 整篇一章。

### 查看图书 `GET /books/{id}`（read）

返回每一章的进度：`stage` 为 `script` 待生成画本、`voice` 配音中、`render` 待合成、`review` 待审听、`done` 已完成；`rendered` 表示有最新的成品。

### 开始一键成书 `POST /books/{id}/produce`（produce）

请求体就是「一键成书设置」，全部可选。返回 `{"pipelineId": "…"}`。同一本书同时只能有一个进行中的任务（409）。

**一键成书设置**

| 字段 | 说明 |
| --- | --- |
| `mode` | `single` / `multi` / `drama`，默认沿用图书的设置 |
| `tier` | 选角用的音色价位：`economy` / `standard`（默认）/ `premium` |
| `narratorVoiceId` | 指定旁白音色，不填由 AI 挑选 |
| `bgm`、`bgmAssetId` | 自动配背景音乐；可以指定一首 |
| `introTemplateId`、`outroTemplateId` | 片头、片尾模板 |
| `preset` | 成品响度标准，如 `generic` |
| `chapterIds` | 只做这些章节，不填做全部纳入制作的章节 |
| `removeSpeechTags` | 去掉「他说道」之类的提示语；不填时广播剧去掉、其余保留 |
| `pauseAfterCast` | 选角后暂停，等人在网页上确认声音 |

积分按章冻结：开始时只需够角色分析和第一章。中途积分用完，任务会暂停在 `waiting`（`waitReason` 为 `credits`），充值后在网页「一键成书」里点 **继续**，从没做完的章节接着做。

### 查询进度 `GET /pipelines/{id}`（任何权限）

| 字段 | 说明 |
| --- | --- |
| `status` | `queued` 排队、`running` 进行中、`paused` 已暂停、`waiting` 等待（见 `waitReason`）、`completed` 完成、`failed` 有章节没完成、`canceled` 已取消 |
| `waitReason` | `cast` 等待确认选角，`credits` 等待充值 |
| `progress` | 0 到 1 |
| `stages` | 各阶段进度；逐章的阶段带 `done` / `total` 章数 |
| `chapters` | 每章的 `status`（`pending` / `running` / `done` / `failed`）、当前步骤 `step` 和出错原因 `error` |
| `usedCredits`、`estCredits` | 已用与预计积分 |

建议每 10–30 秒查一次，或者用 Webhook 等完成通知。

### 下载成品 `GET /books/{id}/outputs`（read）

```json
{
  "outputs": [
    { "chapterId": "…", "index": 1, "title": "第一章 初见", "durationMs": 612000, "format": "mp3", "stale": false,
      "url": "https://…", "timings": [ { "segmentId": "…", "characterId": "…", "startMs": 1000, "endMs": 3400, "text": "林黛玉坐在窗前。" } ] }
  ],
  "expiresIn": 86400
}
```

`url` 24 小时内有效，过期后重新获取。`timings` 是逐句的起止时间，可以直接生成字幕。`stale` 为 `true` 表示章节合成后又改过，成品不是最新的。

### 单段配音 `POST /tts`（tts）

| 字段 | 说明 |
| --- | --- |
| `text` | 必填，1–2000 字 |
| `voiceId` | 必填，来自 `GET /voices` |
| `emotion` | 可选：`neutral`、`happy`、`sad`、`angry`、`gentle`、`whisper` 等，音色不支持时忽略 |
| `speed` | 可选，语速 |

```json
{ "url": "https://…", "durationMs": 2300, "credits": 3 }
```

## 5. Webhook 通知

在「工作室设置 → 开发者 → Webhook 通知」填写你的地址并选择事件。事件发生时，平台向这个地址发送 POST 请求：

```json
{ "event": "pipeline.completed", "id": "01a1…", "createdAt": "2026-11-13T08:00:00Z", "data": { "runId": "…", "bookId": "…" } }
```

| 事件 | `data` |
| --- | --- |
| `pipeline.completed` | `runId`、`bookId` |
| `pipeline.failed` | `runId`、`bookId`、`error` |
| `chapter.rendered` | `bookId`、`chapterId`、`renderId`、`durationMs` |
| `export.completed` | `bookId`、`exportId`、`fileName`、`size` |
| `voice.ready` | `voiceId`、`name`（声音克隆完成） |
| `ping` | 添加 Webhook 时的测试 |

请在 15 秒内返回 2xx。失败会重试，最多 5 次，间隔逐渐拉长。同一个事件可能送达不止一次，请按 `id` 去重。

**校验签名**：请求头 `X-Voicebook-Signature: t=<时间戳>,v1=<签名>`，签名是用添加 Webhook 时得到的密钥，对 `时间戳 + "." + 原始请求体` 计算的 HMAC-SHA256（十六进制）。

```python
import hmac, hashlib, time

def verify(secret: str, header: str, body: bytes) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    if abs(time.time() - int(parts["t"])) > 300:  # 拒绝 5 分钟前的旧请求
        return False
    want = hmac.new(secret.encode(), parts["t"].encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, parts["v1"])
```

```js
const crypto = require('crypto')
function verify(secret, header, rawBody) {
  const p = Object.fromEntries(header.split(',').map((s) => s.split('=')))
  if (Math.abs(Date.now() / 1000 - Number(p.t)) > 300) return false
  const want = crypto.createHmac('sha256', secret).update(p.t + '.' + rawBody).digest('hex')
  return crypto.timingSafeEqual(Buffer.from(want), Buffer.from(p.v1))
}
```

## 6. 完整示例：导入、成书、下载

```bash
BASE=https://voicebook.example.com/openapi/v1
KEY=vb_xxxxxxxx

# 1. 导入并开始一键成书
RUN=$(curl -s -X POST "$BASE/books" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data-binary @<(jq -n --rawfile text book.txt '{title:"雨夜", text:$text, autoProduce:true}') | jq -r .pipelineId)

# 2. 等它做完
while true; do
  S=$(curl -s "$BASE/pipelines/$RUN" -H "Authorization: Bearer $KEY")
  STATUS=$(echo "$S" | jq -r .status)
  echo "$STATUS $(echo "$S" | jq -r .progressText)"
  case $STATUS in completed|failed|canceled|waiting) break;; esac
  sleep 20
done

# 3. 下载每一章
BOOK=$(echo "$S" | jq -r .bookId)
curl -s "$BASE/books/$BOOK/outputs" -H "Authorization: Bearer $KEY" \
  | jq -r '.outputs[] | "\(.index) \(.url)"' | while read i url; do curl -s -o "chapter-$i.mp3" "$url"; done
```

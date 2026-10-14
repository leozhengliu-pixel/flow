# 开发 Flow 应用与 Agent

[English](developers.md)

Flow 应用是你自己运行的服务，通过 HTTP 接入 Flow。把应用安装到工作区，
是建立应用身份与授权关系，并不是把程序上传到 Flow 服务器中执行。

普通集成通过 REST API 读写事项、项目、文档和评论；OAuth 负责授权。
Agent 在此基础上接收任务、报告进度，让成员能在 Flow 中看到执行过程并继续交流。
可以使用任意编程语言、模型服务或 Agent 框架。

OAuth 授权码流程（含/不含 PKCE）、client credentials、`actor=app`、权限范围、刷新令牌轮换、撤销、内省、“via App”归属和应用清单见 [OAuth 参考](oauth.md)（英文）。
接口目录见 [OpenAPI](openapi.json)，资源定义见 [领域模型](domain-model.md)，
完整的任务存储、活动与 webhook 约定见 [应用成员与 Agent 任务](application-agents.md)。

[Webhook](webhooks.md) 文档介绍数据变更事件、负载、签名校验、重试、投递日志、应用级 Webhook 与 Agent 会话事件。

[TypeScript SDK](sdk.md)（`@flow/sdk`，英文）封装了 REST API、Webhook 签名校验与 OAuth 流程：
`new FlowClient({ apiKey, apiUrl })`，支持类型化模型、分页连接（`fetchNext()`、`for await`）、
类型化错误与限流信息、自动重试、文件上传和 Webhook 处理器；示例见 [examples/sdk-quickstart.ts](examples/sdk-quickstart.ts)。

## 选择执行身份

| 场景 | 执行身份 | 凭据 |
| --- | --- | --- |
| 自己使用的小脚本 | 个人用户 | 个人 API key |
| 工具代表每个登录者操作 | `actor=user` | OAuth 授权码与刷新令牌 |
| 安装到工作区的外部应用或 Agent | `actor=app` | OAuth 授权码与刷新令牌 |
| CI、定时任务、内部服务 | 应用用户 | 开启后使用 `client_credentials` |
| 从应用设置页测试接口 | 明确选择 App 或 User | 开发者访问令牌 |

应用用户的角色是 `app`，不能通过密码或浏览器会话登录。一个应用在不同工作区
安装后，各有独立且稳定的用户 ID。管理员只是安装者或签发者，应用不会继承
管理员的权限。事项和评论的操作记录属于应用，而不是安装它的管理员。

内置 Flow Agent 还受发起人的权限约束。服务器自动化应注册独立应用，
不要把内置系统身份作为通用服务账号使用。

## 第一步：注册应用

工作区 Owner 或 Admin 打开 **设置 > API > 新建 OAuth 应用**，填写：

- 名称，以及可选的描述和图标。
- 精确匹配的 OAuth 回调地址。生产环境使用 HTTPS，本地开发允许回环地址；不支持通配符。
- 应用允许申请的最大 scopes，必须包含 `read`。
- 如果需要服务器间调用，开启 **客户端凭据**。
- 需要分发给其他工作区时，开启 **允许安装到其他工作区**。

自建应用默认仅允许安装到注册它的工作区。开放给其他工作区安装，
仍然要求对方管理员批准，并不会自动取得对方的数据权限。

创建后取得 Client ID 与 Client secret。密钥应保存在服务端的 Secret 管理器或环境变量中。
普通应用列表不会再次返回密钥；遗失时可在应用详情中使用 **轮换密钥**，取得新的密钥。

不持有客户端密钥的公共 PKCE 客户端，也可以动态注册：

```http
POST /oauth/register
Content-Type: application/json

{
  "client_name": "故障助手",
  "redirect_uris": ["http://127.0.0.1:3000/oauth/callback"],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

注册只产生客户端记录，尚未创建工作区应用成员。动态注册的公共客户端
使用 PKCE 授权码流程，不支持 `client_credentials`。

## 第二步：让管理员安装应用

安装服务端生成随机 `state` 和 PKCE verifier，保留到回调完成。
`code_challenge` 是 verifier 的 SHA256 摘要，以不带填充的 base64url 编码。
Flow 要求使用 `S256`。

把管理员引导到目标 Flow 部署的授权地址；参数必须进行 URL 编码：

```text
/oauth/authorize?response_type=code
  &client_id=YOUR_CLIENT_ID
  &redirect_uri=YOUR_REGISTERED_CALLBACK
  &state=YOUR_RANDOM_STATE
  &code_challenge=YOUR_S256_CHALLENGE
  &code_challenge_method=S256
  &actor=app
  &scope=read%20app%3Amentionable%20app%3Aassignable
```

管理员选择工作区、确认权限，再选择团队范围：

1. 所有公开团队，包含将来新建的公开团队。
2. 所有公开团队，加上明确选定的私有团队。
3. 仅选定的团队。

工作区开启第三方应用审批时，外部应用及所请求的 scopes 必须先获批准。
管理员在当前工作区注册的自建应用，受注册时配置的 scopes 约束；
在一个工作区注册应用，不会自动获得其他工作区的审批或权限。

回调到你的服务后，先验证 `state`，再把 code 换成 token：

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode 'grant_type=authorization_code' \
  --data-urlencode "client_id=$FLOW_CLIENT_ID" \
  --data-urlencode "code=$FLOW_AUTHORIZATION_CODE" \
  --data-urlencode "redirect_uri=$FLOW_REDIRECT_URI" \
  --data-urlencode "code_verifier=$FLOW_PKCE_VERIFIER"
```

返回 `access_token`、`refresh_token`、`token_type`、`expires_in` 和以空格分隔的 `scope`。
访问令牌有效期为一小时，刷新授权有效期为 30 天。刷新时旧 refresh token 会被消费，
因此需要原子地保存新的凭据，避免多个进程同时使用同一个旧 token。

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode 'grant_type=refresh_token' \
  --data-urlencode "client_id=$FLOW_CLIENT_ID" \
  --data-urlencode "refresh_token=$FLOW_REFRESH_TOKEN"
```

需要代表个人操作时使用 `actor=user`，或省略 actor。
Agent scopes 属于应用安装授权，不会赋给个人授权。

## 内部服务与 CI：客户端凭据流程

管理员为自建 OAuth 应用开启 **客户端凭据** 后，服务可以在每次运行开始时获取 token：

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --user "$FLOW_CLIENT_ID:$FLOW_CLIENT_SECRET" \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'scope=read app:mentionable app:assignable'
```

也可以把 `client_id`、`client_secret` 放在表单中；同一次请求只能使用一种客户端认证方式。
请求 scopes 必须是应用配置的子集。省略 scope 时，使用配置的 scopes。

第一次调用会在应用注册的工作区创建应用身份，默认允许访问公开团队。
返回的 Bearer token 有效期为 30 天，无 refresh token，并带有 `actor=app` 和 `app_user_id`。
管理员随后可在 **成员 > 应用** 中缩小团队范围。机器 token 不能自行安装到任意工作区，
也不能自行恢复已停用的应用。

最多允许 1,000 个有效应用 token 并存。通过此流程请求不同 scopes 时，
原有 scopes 不同的应用 token 会被撤销。token 到期或密钥轮换后，重新获取 token。
轮换 Client secret 会立即使旧客户端凭据 token 失效；授权码和开发者 token
仍按各自的有效期与撤销规则处理。

旧的 `POST /api/oauth/token` 地址保留兼容，也接受旧 JSON 请求。
新集成使用表单编码的标准 `/oauth/token` 即可。不要把临时访问令牌当成永久 API key 手工保存。

## 开发者 token

在应用详情点击 **创建并复制令牌**，与 参照应用一样会打开命令菜单：
**用户** 立即创建并复制一个带 `read`/`write` 的个人 token；**应用** 为应用用户创建 token。
第一次选 **应用** 时会弹出 **选择权限范围**（默认开启 `read` 和 `write`）；
该 token 有效期间，再次选 **应用** 会复制同一个 token 并列出其权限范围，
因为一个应用用户同时只有一个开发者 token。要换权限范围，先撤销它
（设置 › 应用 › 该应用 › **撤销访问权限**，或 `POST /oauth/revoke`）。

开发者 token 有效期一小时。应用用户的开发者 token 与 Webhook 签名密钥存放方式相同（配置 `FLOW_CONNECTOR_SECRET_KEY` 时加密），
以便再次复制；个人 token 只保存哈希。
签发开发者 token 和轮换 Client secret 都要求交互式管理员会话，
不能拿已有的 API token 来继续签发新凭据。

## 权限边界

| Scope | 能力 |
| --- | --- |
| `read` | 读取普通团队资源 |
| `write` | 修改普通团队资源 |
| `create_issues`，也接受 `issues:create` | 创建或更新事项，不包含通用修改权限 |
| `create_comments`，也接受 `comments:create` | 创建事项评论 |
| `app:mentionable` | 允许结构化提及触发 Agent 任务 |
| `app:assignable` | 允许把事项委派给 Agent |
| `customer:read` / `customer:write` | 读取 / 读写客户资源 |
| `initiative:read` / `initiative:write` | 读取 / 读写目标资源 |
| `release:read` | 读取发布版本 |
| `release:create` | 创建发布版本 |
| `releaseStage:update` | 修改发布版本阶段 |

应用不能申请 `admin`、`owner`、`openid` 或 `email`。
客户、目标、发布版本需要各自明确的 scopes，通用 `write` 不会自动授予这些权限。
提及和委派也不等于通用写权限；Agent 可以不持有 write，而只回报自己任务的执行结果。

最终访问能力是以下条件的交集：token scopes、当前应用授权、团队授权、
资源自身权限、工作区策略。私有文档等资源的单独权限仍然生效。
每次请求都会检查当前安装状态和团队范围；缩小团队授权会立即影响已有 token。
单独限制过团队范围的 token 不会被扩大成应用的完整授权。

## 第三步：调用业务 API

请求携带 `Authorization: Bearer ACCESS_TOKEN`。Flow 根据凭据确定工作区。
也可以明确发送同一工作区的 `X-Workspace-Key`；该请求头不能让 token 越过工作区边界。

```bash
# 确认执行身份。
curl --fail-with-body "$FLOW_BASE_URL/api/bootstrap" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN"

# 分页读取事项，避免一次拉取整个工作区。
curl --fail-with-body "$FLOW_BASE_URL/api/issue-records?limit=50&projection=list" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN"

# 需要 write 或 create_issues，并有目标团队的访问权限。
curl --fail-with-body "$FLOW_BASE_URL/api/issues" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"title":"排查故障","teamId":"YOUR_TEAM_ID"}'
```

事项分页返回 `items`、`hasMore` 和可选的 `nextCursor`。
读取下一页时保持筛选和排序参数一致，再附加 cursor。
大工作区的 bootstrap 不保证包含全部事项，不要以它代替分页查询。

应用发出的操作归属于应用用户。凭据记录中 `subjectUserId` 是实际执行身份，
`issuedByUserId` 是签发管理员；`creatorId` 为兼容旧客户端，仍表示凭据主体。
审计记录保存身份、客户端和授权类型，不保存密钥内容。

## 第四步：接收 Agent 任务

如果应用是 Agent，先配置 Agent scopes 并安装。成员在评论编辑器中选择应用进行
结构化 `@提及`，或把事项委派给应用，即可产生任务。人仍是负责人，应用是委派对象。

仓库提供了不依赖第三方包的 [应用 Agent 示例](examples/application-agent.mjs)：

```bash
export FLOW_BASE_URL='http://127.0.0.1:8080'
export FLOW_CLIENT_ID='YOUR_CLIENT_ID'
export FLOW_CLIENT_SECRET='YOUR_CLIENT_SECRET'
node docs/examples/application-agent.mjs
```

应用 scopes 配置为 `read`、`app:mentionable`、`app:assignable`，并开启客户端凭据。
不设置模型密钥时，示例读取事项并输出固定摘要。要启用真实模型的流式对话，在启动前设置：

```bash
export FLOW_LLM_API_KEY='YOUR_PROVIDER_KEY'
export FLOW_LLM_BASE_URL='https://api.deepseek.com'
export FLOW_LLM_MODEL='deepseek-flash'
node docs/examples/application-agent.mjs
```

`deepseek-flash` 对应 DeepSeek-V4.1-Flash。示例从环境变量读取凭据，不落盘、不打印。
它会流式报告思考和 Markdown 消息，通过 `request_input` 工具向用户提问，并提供选项。
在评论线程里选择选项后点击 Continue，或输入文字回复，即可继续。任务完成后仍能追加
问题，模型会读取之前的对话。停止请求会取消模型生成，版本检查会阻止旧生成覆盖新输入。
输出每 500 ms 合并写入，避免逐 token 写数据库。示例只读取事项并报告活动，不修改事项
或执行任意代码。

### 轮询或 webhook

`GET /api/agent-tasks` 只返回当前应用自己的未终结任务。
也可以由管理员在 **成员 > 应用** 配置 HTTPS webhook。保存时返回一次签名密钥，
再次保存会轮换密钥，接收端需要同步更新。

`AgentSessionEvent` 包含 `agentSession` 及事项、项目或文档上下文。
`action=created` 表示新任务，`action=prompted` 表示用户继续输入。
用 `eventId` 和 `X-Flow-Delivery` 识别重复投递。

收到请求后，在解析 JSON 和入队前验证签名：

```js
import { createHmac, timingSafeEqual } from 'node:crypto'

const expected = createHmac('sha256', webhookSecret).update(rawBody).digest()
const signature = request.headers.get('x-flow-signature') ?? ''
const valid = /^[a-f0-9]{64}$/i.test(signature) &&
  timingSafeEqual(expected, Buffer.from(signature, 'hex'))
```

签名使用原始请求体计算，不要用重新序列化后的 JSON。
生产环境把投递 ID、任务版本和业务执行结果保存在持久化存储中，
避免 webhook 重试或进程重启造成重复业务操作。
成功响应只是确认收到投递，任务完成要通过活动报告；失败投递会重试。

### 回报进度

```http
POST /api/agent-tasks/TASK_ID/activities
Authorization: Bearer APPLICATION_TOKEN
Content-Type: application/json

{"expectedVersion":1,"type":"thought","body":"正在读取事项"}
```

返回更新后的任务。后续操作使用返回的 `version`。
遇到 409 表示其他写入推进了任务，先重新读取，不要盲目重做业务操作。

| 活动 | 含义与状态 |
| --- | --- |
| `thought`、`action`、`output` | 正在执行，`active` |
| `elicitation` | 向用户请求信息，`awaitingInput` |
| `response` | 执行完成，`complete` |
| `error` | 执行失败，`error` |

用 `GET /api/agent-tasks/TASK_ID?after=ACTIVITY_CURSOR` 读取任务及活动。

### 提问、回复和停止

应用可发送以下活动，任务进入等待输入状态：

```json
{
  "expectedVersion": 2,
  "content": { "type": "elicitation", "body": "你希望使用哪种语言？" },
  "signal": "select",
  "signalMetadata": {
    "options": [{ "label": "英文", "value": "English" }, { "label": "中文", "value": "Chinese" }]
  }
}
```

选项值必须非空且唯一，数量为 1 到 20 个。用户回复发送 `type=prompt`、`body=Chinese`
和最新 `expectedVersion`，同一个任务重新进入 pending；最终 response 将任务标记为
complete，用户以后仍可在同一线程追问。事项评论提及触发的任务显示在原评论内，委派任务
没有源评论时单独显示在动态区。

账号连接使用 `signal=auth`，`signalMetadata` 包含 HTTPS `url`，以及可选的
`providerName` 和 `userId`。只有目标用户看到连接链接，其他成员看到等待谁完成连接。
应用服务负责处理第三方授权回调，并在连接完成后恢复任务；这个信号本身不完成 OAuth。

用户通过 `type=canceled` 或 `type=prompt, signal=stop` 停止任务。业务工具审批是独立的
明确批准或拒绝操作，只允许请求者或管理员执行，普通澄清回复不会批准待执行的写操作。
每次活动产生 `agent_task.updated` SSE 事件，携带任务、资源和版本，前端增量刷新该线程。

用户反馈保存为 `type=feedback` 活动，包含目标 `activityId`、表情 `body`、明确的
`enabled=true|false` 和当前 `expectedVersion`。反馈不重新启动已结束或等待输入的任务，
也不会批准待执行工具。在等待输入或执行结束后可以反馈；生成期间版本由工作进程使用。
应用不能冒充用户提交反馈。
每页最多 100 条活动，下一页传最后一条活动的 ID。单条外部活动正文最多 64 KiB。
UI 支持 Markdown。

只有任务所属应用可以报告 Agent 输出。人的 `prompt`、`retry`、`canceled`
使用单独的权限检查，应用不能伪造这些操作，也不能向另一个应用的任务写入结果。

## 撤销、错误处理与上线

通过表单 `POST /oauth/revoke`，发送 `token`，可撤销一个访问或刷新令牌；重复撤销是安全的。
停用应用会撤销当前凭据及授权，重新启用后需要重新签发 token。
缩小团队范围会立即影响已有 token。删除自建 OAuth 注册后，该注册签发的 token 也会失效。

| 响应 | 应对方式 |
| --- | --- |
| `400 invalid_scope` | 检查支持的 scopes 和应用配置，必须包含 read |
| `400 unauthorized_client` | 开启客户端凭据，或改用授权码流程 |
| `401` | 检查到期、密钥轮换、凭据撤销和应用审批 |
| `403` | 检查团队范围、资源权限、工作区策略和执行身份 |
| `404` | 资源可能不存在或无权访问，不要改用管理员 token 绕过 |
| `409` | 重新读取任务版本，避免重复执行业务写入 |
| `429` | 减少并发签发 token，在同一次运行中复用现有 token |

生产环境使用 HTTPS，回调 state 和 PKCE verifier 保存在服务端。
密钥和 token 不进入源代码仓库；日志记录请求 ID、任务 ID 和状态码，
不要记录 Authorization 请求头或完整 token 响应。
任务恢复执行时重新检查权限；token 或 webhook 事件不等于任意代码执行授权。

## 旧集成迁移

旧 `/api/oauth/token` 客户端凭据实现把操作归属于应用创建者。
这些未绑定应用主体的旧机器 token 会被拒绝。
请开启客户端凭据，重新获取应用 token，确认 app_user_id 后再恢复 worker。
个人 API key 和已有 PKCE 个人授权保留自己的身份语义。

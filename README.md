# Flow

Flow 是面向团队的项目规划与协作应用，覆盖工作区、团队、任务、项目、视图、通知、文档、客户请求和管理设置。

Flow 的产品交互设计借鉴 Linear。

## 技术栈

- Web：React、TypeScript、Vite、shadcn/ui
- API：Go
- 数据库：SQLite
- 实时协作：SSE、Yjs
- 编辑器：Tiptap、ProseMirror
- 国际化：简体中文、英文

## 本地开发

启动 API：

```bash
cd api
go run ./cmd/server
```

默认数据库位于 `api/data/flow.db`，附件保存在 `api/data/uploads/`。可使用 `FLOW_DB_PATH` 和 `FLOW_UPLOAD_PATH` 修改路径。

本地种子管理员为 `leo.zheng.liu@example.com`，密码为 `flow-demo`。可使用 `FLOW_SEED_PASSWORD` 修改种子密码。

启动 Web：

```bash
cd web
npm install
npm run dev
```

Web 默认运行在 `http://127.0.0.1:5173/`，API 健康检查地址为 `http://127.0.0.1:8080/api/health`。

## 邮件与生产配置

```bash
export FLOW_APP_URL=https://flow.example
export FLOW_SMTP_HOST=smtp.example.com
export FLOW_SMTP_PORT=587
export FLOW_SMTP_USERNAME=apikey
export FLOW_SMTP_PASSWORD=secret
export FLOW_SMTP_FROM=notifications@flow.example
export FLOW_COOKIE_SECURE=true
export FLOW_DEV_AUTH_TOKENS=false
export FLOW_TRUST_PROXY_HEADERS=true
```

仅当 API 位于可信代理之后，且代理会覆盖 `X-Forwarded-For` 时，才启用 `FLOW_TRUST_PROXY_HEADERS`。

## 验证

```bash
cd web && npm run lint && npm run build
cd ../api && go test ./...
```

## 文档

- [产品模块](docs/product-modules.md)
- [交付路线图](docs/delivery-roadmap.md)
- [领域模型](docs/domain-model.md)
- [任务页模块](docs/issue-page-modules.md)
- [项目页模块](docs/projects-page-modules.md)
- [工作区模块](docs/workspace-modules.md)

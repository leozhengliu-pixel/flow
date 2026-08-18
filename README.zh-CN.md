<div align="center">

# Flow

**面向规划、任务管理与团队协作的自托管工作空间。**

[![CI](https://github.com/leozhengliu-pixel/flow/actions/workflows/ci.yml/badge.svg)](https://github.com/leozhengliu-pixel/flow/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md)

</div>

Flow 将任务、项目、周期、计划、文档、客户请求与工作区管理集中在一个专注的应用中。项目由 React Web 客户端、Go API 与 SQLite 组成，本地开发无需额外部署数据库。

> [!IMPORTANT]
> Flow 仍处于活跃开发阶段。将实例暴露到互联网之前，请审查安全配置并替换全部开发凭据。

## 核心能力

- **任务工作流**：创建、编辑、归档、关联、订阅、评论、附件和批量修改。
- **规划管理**：项目、项目更新、里程碑、计划、周期、保存视图与团队工作流。
- **团队协作**：活动时间线、通知、在线状态、全文搜索、草稿和富文本文档。
- **工作区管理**：账户生命周期、邀请、角色、团队、标签、模板、导入与导出。
- **客户上下文**：客户、需求、发布、请求审批、订阅与 SLA 规则。
- **国际化**：支持英文和简体中文，并持久化语言偏好。

## 快速开始

### 环境要求

- Go `1.26.3`，或 [`api/go.mod`](api/go.mod) 中声明的版本
- Node.js `24.19+` LTS（生产基线声明在 [`.nvmrc`](.nvmrc)）
- npm `10.9.2`

CI 还会在 Node.js 26 上执行完整的 Web 验证，作为前向兼容检查。在新版本进入 LTS 并经过明确升级前，Node.js 24 仍是受支持的生产基线。

### 启动 API

```bash
git clone https://github.com/leozhengliu-pixel/flow.git
cd flow/api
go run ./cmd/server
```

API 默认监听 `http://127.0.0.1:8080`，健康检查地址为 `http://127.0.0.1:8080/api/health`。

### 启动 Web

在另一个终端中执行：

```bash
cd flow/web
npm ci
npm run dev
```

打开 `http://127.0.0.1:5173`。

### Docker

本地 Docker 启动时，先生成前端与 API 产物，再启动单容器 Compose 服务：

```bash
./scripts/build-local-docker-assets.sh
docker compose up -d --build
```

打开 `http://127.0.0.1:5173`。API 健康检查也暴露在
`http://127.0.0.1:8080/api/health`。

### 开发账户

```text
邮箱：leo.zheng.liu@example.com
密码：flow-demo
```

该账户仅用于本地开发。首次启动 API 前可通过 `FLOW_SEED_PASSWORD` 修改种子密码，生产环境不得使用默认凭据。

## 常用配置

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `FLOW_DB_PATH` | `data/flow.db` | SQLite 数据库路径。 |
| `FLOW_UPLOAD_PATH` | `data/uploads` | 附件存储目录。 |
| `FLOW_SEED_PASSWORD` | `flow-demo` | 本地管理员初始密码。 |
| `FLOW_APP_URL` | 未设置 | 账户邮件中使用的 Web 地址。 |
| `FLOW_SMTP_HOST` | 未设置 | SMTP 主机。 |
| `FLOW_SMTP_PORT` | `587` | SMTP 端口。 |
| `FLOW_SMTP_USERNAME` | 未设置 | SMTP 用户名。 |
| `FLOW_SMTP_PASSWORD` | 未设置 | SMTP 密码。 |
| `FLOW_SMTP_FROM` | 未设置 | 账户邮件发件地址。 |
| `FLOW_COOKIE_SECURE` | `false` | 强制使用安全 Cookie，生产环境应启用。 |
| `FLOW_DEV_AUTH_TOKENS` | `true` | 在开发响应中返回账户操作令牌，生产环境必须关闭。 |
| `FLOW_TRUST_PROXY_HEADERS` | `false` | 信任受控反向代理传入的转发信息。 |

仅当可信代理会覆盖客户端传入的转发请求头时，才启用 `FLOW_TRUST_PROXY_HEADERS`。

## 验证

```bash
cd web
npm ci
npm run lint
npm run build

cd ../api
go test ./...
```

默认 Web Job 使用 Node.js 24 LTS；独立且必须通过的兼容性 Job 会在 Node.js 26 上重复安装、lint 与构建。

## 参与项目

- 提交改动前请阅读[贡献指南](CONTRIBUTING.md)。
- 所有项目空间均遵循[行为准则](CODE_OF_CONDUCT.md)。
- 可复现的问题与范围明确的功能建议请提交到 [GitHub Issues](https://github.com/leozhengliu-pixel/flow/issues)。
- 安全漏洞请按[安全策略](SECURITY.md)私密报告，不要创建公开 Issue。
- 使用与排错方式见[支持说明](SUPPORT.md)。

## 许可证

Flow 使用 [Apache License 2.0](LICENSE) 发布。署名信息见 [NOTICE](NOTICE)。

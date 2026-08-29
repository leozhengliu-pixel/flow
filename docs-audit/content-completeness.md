# flow-docs 内容完整性审查

审查范围为 `docs/*.md` 的 17 个条目（不含 `README`、`artifacts` 和源码）。
段落数按连续的非标题、非列表、非表格、非代码块文本计数；“步骤”只计数有序列表，
因此这是结构性指标，不是字数评分。FAQ 以显式的 FAQ/常见问题/Q&A 标题或条目计数。

## 总体结论

- 17 页中 **0 页有 FAQ**。即使是部署、路由、设置等面向操作的文档，也没有把常见故障和决策问题写成可检索问答。
- 只有 4 页有有序步骤：`delivery-roadmap`（8）、`inbox-replication-plan`（5）、`issue-page-modules`（6）、`my-issues-page-modules`（10）。其余页面主要是声明式清单，缺少“从哪里开始、如何验证、失败后怎么办”的流程。
- 明显偏模板/目录的页面：`cycles-page-modules.md`、`initiatives-page-modules.md`、`pulse-page-modules.md`、`workspace-modules.md`、`settings-modules.md`。它们多为一层层标题加要点，几乎没有实测尺寸、验收证据、示例或故障处理。
- 存在内容重复：`product-modules.md` 与 `delivery-roadmap.md` 重复模块职责/依赖；`inbox-replication-plan.md` 与 `inbox-page-modules.md` 重复 Inbox 模块边界；Issue/My Issues/Team Issues/Projects 多次复制 44px 行、28px 控件、共享属性菜单、乐观更新和回滚约定。建议保留一个规范页，其余页面用链接引用并只记录差异。
- `configuration.md` 的 Redis 变量表在说明段落后直接续接表格行（第 86 行），渲染时容易被当成普通文本；这是结构性完整性问题，应补回表头或拆成两张表。

## 逐页统计与建议

| 文档 | 章节 | 段落 | FAQ | 有序步骤 | 完整性判断 | 主要问题与建议 |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| `configuration.md` | 9 | 27 | 0 | 0 | **较完整，操作缺口** | 环境变量表、示例和安全说明充分，但没有安装/升级/回滚步骤、启动失败 FAQ、最小生产配置；修复 Redis 表断裂并增加按 SQLite/Postgres/S3/Redis 的端到端配置流程。 |
| `cycles-page-modules.md` | 7 | 10 | 0 | 0 | **偏模板** | 只有领域/路由/尺寸/Deferred 要点，没有当前实现状态、接口示例、状态矩阵或验收清单；补“创建→开始→完成→滚动未完成 Issue”的可执行步骤和失败/权限状态。 |
| `delivery-roadmap.md` | 8 | 32 | 0 | 8 | **中等，重复规划** | 阶段目标和退出条件清楚，但实施状态只标注 2026-08-12，和各页面更晚的审计状态可能漂移；将状态拆成可更新表（负责人、证据链接、阻塞项），并链接到各模块规范，避免重复职责描述。 |
| `domain-model.md` | 12 | 15 | 0 | 0 | **架构草案，非使用文档** | ER 图、依赖方向和 Issue 聚合较丰富，但前半部分是实体名清单，且有“Do not scaffold”“placeholder”式未来语气；补约束/不变量、示例事件载荷、迁移兼容策略和读者导览，明确已实现与提议部分。 |
| `inbox-page-modules.md` | 30 | 34 | 0 | 0 | **较完整，重复计划** | 有大量几何、键盘、状态和 QA 证据；但与 `inbox-replication-plan.md` 大量重叠，且“当前持久化边界/父路由待集成”信息分散；保留本页为证据规范，计划页只保留里程碑链接。增加故障排查 FAQ。 |
| `inbox-replication-plan.md` | 8 | 25 | 0 | 5 | **计划清单** | M12.1–M12.7 划分合理，但多为“应当”语句，没有 owner、完成状态、API 示例或验收链接；补依赖图、每门交付物状态和从通知生成到回滚的测试步骤，避免与 Inbox 页面证据重复。 |
| `initiatives-page-modules.md` | 10 | 1 | 0 | 0 | **明显偏模板** | 几乎全是模块 bullet（仅 1 个正文段），无尺寸、截图/DOM 证据、实现勾选、错误/空状态细节；I1–I9 各补输入/输出、持久化端点、验收标准和最小操作示例，并把 Deferred 外部集成单独列为路线图链接。 |
| `issue-page-modules.md` | 26 | 53 | 0 | 6 | **最完整之一** | 模块地图、测量、行为和勾选验收很充分；仍缺显式 FAQ，且未完成项（Favorite 实体、inline comment anchoring、拖放附件）散落在多处；增加未完成项汇总和排障问答，链接对应测试/fixture。 |
| `my-issues-page-modules.md` | 37 | 52 | 0 | 10 | **最完整之一，偏审计日志** | 尺寸、交互矩阵、状态与验证记录详细；“Remaining modules”重复出现，且将历史 Chrome/build 结果埋在长文中；把每模块状态改为统一表格，增加常见筛选/持久化失败 FAQ，并链接共享 Issue 规范而非重复描述。 |
| `product-modules.md` | 26 | 65 | 0 | 0 | **目录/架构模板** | M01–M22 职责与依赖覆盖面广，但没有模块 owner、实现状态、用户流程或证据，和路线图重复；保留为一页索引，删除重复职责细节，给每模块补状态、入口文档和一条验收路径。 |
| `projects-page-modules.md` | 25 | 42 | 0 | 0 | **较完整，状态混杂** | 交互矩阵、数据契约、QA 和审计记录丰富；开头仍写“implementation in progress”，后文大量“Implemented”，拖放/Timeline 等边界分散；增加状态更新时间、未实现列表和按视图的步骤/FAQ，统一与路线图的状态来源。 |
| `pulse-page-modules.md` | 6 | 0 | 0 | 0 | **明显偏模板** | 全页是短 bullet，缺任何正文段落、测量、截图、空/加载/错误状态、验收或示例；补 Following/Popular/All 的样例数据、排序规则、权限边界、发布/编辑/删除步骤和持久化失败处理。 |
| `routing-system.md` | 3 | 2 | 0 | 0 | **契约完整，使用指引不足** | 路由表、行为契约和一次验证记录足够作开发参考，但没有新页面接入步骤、重定向/404 排障 FAQ，也没有测试命令；补“添加 route builder→App 解析→链接→深链测试”的短流程和错误案例。 |
| `settings-modules.md` | 5 | 2 | 0 | 0 | **明显偏模板/矩阵** | 页面矩阵覆盖广，但每个页面只有一行，缺字段、权限、保存失败/加载态及外部 OAuth 边界示例；补按 Personal/Administration 的页面模板、逐页控制项与验收清单，并明确“模拟”状态。 |
| `team-workspace-issues-modules.md` | 8 | 1 | 0 | 5* | **部分实现，待补路线** | 已实现的列表契约清楚，但几乎无正文说明；末尾 5 项是“Next complex modules”而非操作步骤，Board/DnD/Saved View/完整详情仍是占位边界；补当前截图/测试证据、实现状态表和从 scope→filter→bulk→retry 的流程。 |
| `views-page-modules.md` | 7 | 3 | 0 | 7 | **较完整的测量稿** | 路由、尺寸、菜单和 SavedView shape 完整；有序项主要是目录模块而非用户步骤，缺编辑/复制/改 owner/删除的端到端示例、权限与失败态；补 CRUD 流程和与 Issue/Project 详情页的链接。 |
| `workspace-modules.md` | 6 | 0 | 0 | 0 | **明显偏模板** | 全部为 bullet 合约，未说明 join/create/switch/delete 的界面步骤、校验错误、权限和迁移回滚；补新账号和多 workspace 的逐步演练、API 请求样例、删除最终 workspace 的保护 FAQ。 |

`*` 该数值来自有序列表语法；在此页语义上是“后续模块”，不应被当作用户操作步骤。

## 建议的统一补齐模板

对每个面向页面的文档（Cycles、Initiatives、Pulse、Settings、Workspace、Views、Issues）统一增加：

1. **读者与范围**：页面入口、适用角色、依赖模块、参考版本/日期。
2. **用户流程**：创建/编辑/删除各一条 happy path，附键盘和响应式差异。
3. **状态矩阵**：loading、empty、populated、permission denied、mutation error、retry。
4. **证据与验收**：截图/DOM 或测试链接、完成勾选、未完成项及负责人。
5. **FAQ/排障**：至少覆盖路由 404、权限、保存失败、刷新后状态、外部集成不可用。
6. **规范引用**：共享的 44px/28px/属性菜单/乐观回滚等只在一个基础规范中定义，页面文档写差异。

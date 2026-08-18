# AI 原生交付特性与 Flow 数据模型映射

## 已导入的维度

| 平台维度 | Flow 原生模型 | 演示数据 |
|---|---|---|
| 战略层 | Initiative、Document、Project Update | 企业流程体验升级、战略与业务目标文档 |
| 业务视角 | Ask、Customer Request、Issue | 已接收诉求、已拒绝诉求、运营反哺待审批诉求 |
| 产品视角 | Issue、父子 Issue、Document | #105130、#108415、车商城订单流程 SPEC |
| 项目/版本 | Project、Cycle、Release、Milestone | 车商城项目、两条真实迭代快照、3 个演示版本 |
| 需求管理 | Issue、Workflow State、Assignee、Parent/Sub-issue | 需求拆解到开发、测试和缺陷任务 |
| 管理视角 | Saved View、Project Progress/Health、Initiative | 管理驾驶舱及战略/业务/产品等视角看板 |
| 开发视角 | Issue、Label、Comment、Activity、Attachment | 开发任务、运维任务、指派/状态变更、评论 |
| 测试视角 | Issue、Label、Document | 禅道用例 #49219/#49216/#49215、测试方案/报告 |
| 上线评审 | Issue、Document、Release | REV-2026-08-001、评审报告、发布门禁 |
| 审计视角 | Audit Log、Issue、Document | 合规检查项、审批拒绝日志、审计清单 |
| 运维/运营 | Project Update、Issue、Document、Customer Request | 5121.5h 交付快照、改进清单、线上体验反馈 |

## 真实禅道样本

- 产品：智能印控平台(S04763)，产品 ID 3124。
- 项目：汽车之家车商城项目2026，项目 ID 10155。
- 项目规模：21 条研发需求、628 个任务、606 个 Bug。
- 管理快照：64 人、累计消耗 5121.5h、剩余需求 15、任务 4、Bug 1。
- 迭代：车商城316迭代预计 3885.5h、消耗 3973h；车商城一期迭代预计 519h、消耗 591h。两者均为 100% 且已延期。
- 测试：智能印控产品共有 11 个用例，抓取时已执行 0 个；导入 3 个代表用例。
- 版本/发布：上述禅道产品和项目均无记录，因此 Flow 中的 Release 明确标记为演示规划数据。

## 借用现有模型承载

| 能力 | 当前承载方式 | 限制 |
|---|---|---|
| 产品 | Project + 产品标签 | Project 与 Product 无法独立关联 |
| 需求/任务/缺陷/运维任务 | Issue + Label | 没有独立类型实体与类型级字段 |
| 测试方案、用例、执行、报告 | Issue + Document + Label | 无测试步骤、执行批次、结果统计等结构 |
| 上线评审 | Issue + Document + Release | 无评审参与人、门禁规则和决策流模型 |
| 开发日志/工时 | Comment、Activity、Project Update | 无可汇总的 Worklog/Timesheet |
| 业务价值与交付价值 | Initiative/Project 文本与更新 | 无指标事实、目标值、实际值和趋势 |
| 预算费用合规 | Document | 无预算、费用、付款和审批字段 |

## 当前无法对应的独立数据模型

- Product、产品 5 码及 Project-Product 关系。
- Test Plan、Test Case、Test Run、Test Report、测试步骤与断言。
- Repository、Commit、Branch、Pull Request/Merge Request 及任务代码关联。
- CI/CD Pipeline、Build、Deployment、Environment、Release Gate。
- Rollback、Alert、Incident、生产指标与可观测性数据。
- Worklog、Timesheet、开发日志和人天核算。
- Budget、Cost、Revenue、ROI、Payment、费用合规。
- Token 消耗、人天承载、AI 转型指标。
- KPI/Metric 的目标、实际、趋势和多维分析事实表。
- 通用交付遥测和过程分析事件模型。

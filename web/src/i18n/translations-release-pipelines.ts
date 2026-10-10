/** Settings › Releases: pipeline list, new pipeline form, pipeline settings, access keys and CI setup. */
export const releasePipelinesZhCN: Record<string, string> = {
  // One term for "pipeline" across the releases surfaces (发布管线).
  "Filter by pipeline name": "按管线名称筛选",
  "Filter by pipeline name…": "按管线名称筛选…",
  "Pipeline name": "管线名称",
  "Pipeline state": "管线状态",
  "Pipeline status": "管线状态",
  "Select pipeline": "选择管线",
  "All production pipelines": "所有生产管线",
  "No release pipelines": "没有发布管线",
  "No matching release pipelines": "没有匹配的发布管线",
  "Find release pipelines…": "查找发布管线…",
  "No matching pipelines": "没有匹配的管线",
  "No pipelines": "没有管线",
  "Create a new release pipeline": "创建新的发布管线",
  "Configure release pipeline": "配置发布管线",
  "Create pipeline": "创建管线",
  "Release pipelines": "发布管线",

  // Settings list
  "Active pipelines": "活动",
  "Deleted pipelines are retained here for 30 days before being permanently deleted along with all their releases.":
    "已删除的管线会在此保留 30 天，之后将连同其所有发布版本被永久删除。",
  "Create release pipelines": "创建发布管线",
  "Create a new release pipeline to manage upcoming releases and track what's ready to ship.":
    "创建新的发布管线来管理即将推出的发布版本，并跟踪哪些内容已可交付。",
  "Continuous pipelines capture changes as they deploy. Scheduled pipelines model planned releases with stages, target dates, and freezes.":
    "持续式管线会在部署时捕获变更。计划式管线通过阶段、目标日期和冻结来规划发布版本。",
  "View 1 release": "查看 1 个发布版本",
  "View {count} releases": "查看 {count} 个发布版本",
  "View releases": "查看发布版本",
  "Latest release": "最新发布",
  "Restore pipeline": "恢复管线",
  "Pipeline restored": "管线已恢复",
  "Could not restore release pipeline": "无法恢复发布管线",
  "Pipeline deleted": "管线已删除",
  "View recently deleted pipelines": "查看最近删除的管线",
  "Delete the pipeline \"{name}\"?": "删除管线“{name}”？",
  "Deleted pipelines are available in the \"Recently deleted pipelines\" view for 30 days, before being permanently deleted along with all their releases.":
    "已删除的管线会在“最近删除的发布管线”视图中保留 30 天，之后将连同其所有发布版本被永久删除。",
  "Type {name} to confirm": "输入 {name} 以确认",

  // New pipeline form and settings
  "Optionally set team ownership": "可选：设置负责团队",
  "Teams set ownership and improve default behaviors like suggested releases. Issues from other teams can still be added to releases.":
    "团队用于设置归属，并改进建议发布版本等默认行为。其他团队的事项仍可添加到发布版本中。",
  "More information": "更多信息",
  "Create new release stage": "创建新的发布阶段",
  "Your teams": "你的团队",
  "Other teams": "其他团队",
  "Choose how releases are created and tracked in this pipeline": "选择此管线中发布版本的创建和跟踪方式",
  "Collect changes into a release and track its progress over time": "将变更汇集到一个发布版本中，并持续跟踪其进度",
  "Common for teams with a release cadence or mobile releases": "适合有固定发布节奏或发布移动应用的团队",
  "Create a new release automatically for each deploy": "每次部署时自动创建一个新的发布版本",
  "Common for continuous delivery or frequent deploys": "适合持续交付或频繁部署",
  "Name is required.": "名称为必填项。",
  "Name cannot exceed 120 characters.": "名称不能超过 120 个字符。",
  "Define the template used when generating release notes. Include the sections you want; a {{issues}} line receives the list of completed issues.":
    "定义生成发布说明时使用的模板。写入需要的章节；{{issues}} 所在行会替换为已完成事项列表。",
  "Release API": "发布 API",
  "GitHub Actions": "GitHub Actions",

  // Access key
  "Allows external integrations to interact with this pipeline.": "允许外部集成与此管线交互。",
  "This access key will not be visible again. Please copy it now.": "此访问密钥之后将不再显示，请立即复制。",
  "Access key copied to clipboard": "访问密钥已复制到剪贴板",
  "Access key created and copied to clipboard": "访问密钥已创建并复制到剪贴板",
  "Access key rotated and copied to clipboard": "访问密钥已轮换并复制到剪贴板",
  "Access key created": "访问密钥已创建",
  "Access key rotated": "访问密钥已轮换",
  "You will not be able to see this key again once you navigate away.": "离开此页面后将无法再次查看此密钥。",
  "You will not be able to see this key again once you navigate away. Please copy it before leaving the page.":
    "离开此页面后将无法再次查看此密钥。请在离开前复制它。",
  "Failed to create access key": "无法创建访问密钥",
  "Failed to rotate access key": "无法轮换访问密钥",
  "Failed to revoke access key": "无法吊销访问密钥",
  "Access key actions": "访问密钥操作",
  "Active access key": "有效",
  "Expiring {date}": "将于 {date} 失效",
  "Created {time}": "创建于{time}",
  "Last used {time}": "上次使用于{time}",
  Rotate: "轮换",
  Revoke: "吊销",
  "Revoke now": "立即吊销",
  "Rotate access key?": "轮换访问密钥？",
  "Revoke access key?": "吊销访问密钥？",
  "Revoke access key now?": "立即吊销访问密钥？",
  "A new access key will be generated. The current key will continue working for 1 hour to allow you to update your CI configuration.":
    "将生成新的访问密钥。当前密钥会继续有效 1 小时，方便你更新 CI 配置。",
  "A new access key will be generated. The current key is already scheduled for revocation at {date} and will continue working until then.":
    "将生成新的访问密钥。当前密钥已计划于 {date} 吊销，在此之前仍然有效。",
  "Revoke old key immediately (no grace period)": "立即吊销旧密钥（无宽限期）",
  "Revoke old key now instead of at {date}": "立即吊销旧密钥，而不是在 {date}",
  "Revoke immediately (no grace period)": "立即吊销（无宽限期）",
  "The access key will stop working after 1 hour. Any CI pipelines using this key will fail after that time.":
    "访问密钥将在 1 小时后失效。之后所有使用此密钥的 CI 管线都会失败。",
  "The access key will stop working immediately. Any CI pipelines using this key will fail.":
    "访问密钥将立即失效。所有使用此密钥的 CI 管线都会失败。",
  "Access key revoked": "访问密钥已吊销",
  "Access key will be revoked in 1 hour": "访问密钥将在 1 小时后吊销",

  // Path filters and CI setup
  "Optionally filter releases to only include commits affecting specific paths. Useful for monorepos where multiple projects share one repository. Supports wildcards, one pattern per line.":
    "可选：让发布版本只包含影响特定路径的提交。适用于多个项目共用一个仓库的 monorepo。支持通配符，每行一个模式。",
  "Filter releases to only include commits affecting specific paths. Generate an access key to configure path filters.":
    "让发布版本只包含影响特定路径的提交。生成访问密钥后即可配置路径筛选器。",
  // Multi-select
  "{count} selected": "已选择 {count} 项",
  "Clear selection": "清除选择",
  "Duplicate pipeline…": "复制管线…",
  "Delete pipeline": "删除管线",
  "Delete {count} pipelines": "删除 {count} 个管线",
  "Delete {count} pipelines?": "删除 {count} 个管线？",
  "{count} pipelines deleted": "已删除 {count} 个管线",
  "{count} pipelines restored": "已恢复 {count} 个管线",
  "Restore {count} pipelines": "恢复 {count} 个管线",
  "Confirmation": "确认",
}

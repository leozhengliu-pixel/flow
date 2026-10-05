/** Release pipeline stage editor and email intake (Asks, team, customer requests) — Linear parity copy. */
export const releaseIntakeZhCN: Record<string, string> = {
  // Release pipeline stages
  "Can’t delete the \"{name}\" release stage": "无法删除“{name}”发布阶段",
  unnamed: "未命名",
  "Each type of release stage must have at least one option. You can edit this stage, or create a replacement stage prior to deleting it.":
    "每种类型的发布阶段至少要保留一个。你可以编辑此阶段，或先创建替代阶段再删除它。",
  "Can’t archive the \"{name}\" release stage": "无法归档“{name}”发布阶段",
  "This stage has releases associated with it. You can edit this stage, or move the releases to a different stage prior to archiving it.":
    "此阶段关联了发布版本。你可以编辑此阶段，或先将发布版本移到其他阶段再归档。",
  "At least one started stage must remain non-frozen.": "至少要有一个已开始阶段保持未冻结。",
  "Syncs won’t automatically add issues to this stage": "同步不会自动向此阶段添加事项",
  Frozen: "已冻结",
  Freeze: "冻结",
  "Stage color": "阶段颜色",

  // Asks email intake wizard and settings
  "Edit email intake": "编辑邮件接收",
  "Edit email forwarding": "编辑邮件转发",
  "Specify your custom address, then in your email provider’s settings, configure it to forward emails to the intake address below.":
    "填写你的自定义地址，然后在邮件服务商的设置中，将其配置为把邮件转发到下方的接收地址。",
  "Edit your custom address, then in your email provider’s settings, configure it to forward emails to the intake address below.":
    "编辑你的自定义地址，然后在邮件服务商的设置中，将其配置为把邮件转发到下方的接收地址。",
  "Template creates issues in the {team} team": "模板会在 {team} 团队中创建事项",
  "DNS records": "DNS 记录",
  Generating: "正在生成",
  Loading: "加载中",
  Verified: "已验证",
  Unverified: "未验证",
  "Copy to clipboard": "复制到剪贴板",
  "Verifying DNS records": "正在验证 DNS 记录",
  "DNS record changes could take up to 72 hours to propagate. Please check back later": "DNS 记录变更最多可能需要 72 小时才能生效，请稍后再查看",
  "Please select a team": "请选择团队",
  "Please enter a name": "请输入名称",
  "Please enter a valid email address": "请输入有效的邮箱地址",
  "Could not save email intake": "无法保存邮件接收",
  "Email address already in use": "邮箱地址已被使用",
  "Intake address": "接收地址",
  "Email copied to clipboard": "邮箱已复制到剪贴板",
  "Sender name": "发件人名称",
  "e.g. Helpdesk": "例如：Helpdesk",
  "Custom address": "自定义地址",
  "e.g. helpdesk@acme.com": "例如：helpdesk@acme.com",
  "By default, outgoing emails from Flow are sent from": "默认情况下，Flow 发出的邮件来自",
  ". To use your own email domain, access the DNS settings for": "。如需使用你自己的邮件域名，请打开",
  ", and add the following DNS records to authenticate your domain.": " 的 DNS 设置，并添加以下 DNS 记录来验证你的域名。",
  "Outbound sending status": "外发状态",
  "Sending from": "发件域名",
  "Provide a custom address if you’d like to send email responses from your own email domain, otherwise, outgoing emails will be sent from":
    "如果希望从你自己的邮件域名发送回复，请提供自定义地址；否则外发邮件将来自",
  "DNS Active": "DNS 已生效",
  "sending from": "发件域名",
  "DNS incomplete": "DNS 未完成",
  "DNS loading…": "DNS 加载中…",
  "Email intake deleted": "邮件接收已删除",
  "Could not delete email intake": "无法删除邮件接收",
  "Intake address copied to clipboard": "接收地址已复制到剪贴板",
  "Copy address": "复制地址",
  "Automatically link inbound emails with customers based on the sender’s email domain": "根据发件人的邮件域名，自动将收到的邮件关联到客户",
  "Link incoming emails as customer requests": "将收到的邮件关联为客户请求",
  "Customer requests are not enabled for this workspace": "此工作区未启用客户请求",
  "Waiting for DNS verification": "等待 DNS 验证",
  "Verified and receiving email": "已验证，正在接收邮件",
  "1 email": "1 个邮箱",
  "{count} emails": "{count} 个邮箱",
  "{name} settings": "{name} 设置",

  // Team "Create issues by email"
  "Reset the email address?": "重置邮箱地址？",
  "A new email address will be generated for {subject} and the previous address will be permanently disabled.": "将为{subject}生成新的邮箱地址，之前的地址将被永久停用。",
  "Reset address": "重置地址",
  "The email address for {subject} has been reset.": "{subject}的邮箱地址已重置。",

  // Customer requests settings
  "When a new issue is created from a customer page, it will be routed to the default team’s triage or backlog. This centralizes customer requests for ease of management and prioritization.":
    "从客户页面创建新事项时，它会被路由到默认团队的分诊或待办中，从而集中管理客户请求，便于处理和排定优先级。",
  "Data imports must be in annual figures, but can be displayed as monthly or annual": "数据导入必须为年度数值，但可以按月或按年显示",
  "Domains and emails that are not associated with a specific customer. Common providers like Gmail, Outlook, etc. are already included.":
    "不与特定客户关联的域名和邮箱。Gmail、Outlook 等常见邮件服务商已默认包含在内。",
};

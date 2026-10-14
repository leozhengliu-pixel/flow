package main

// The "Getting started" tutorial text (see workspace_tutorial.go). Each entry
// matches one clip in docs/media and its caption in docs/media/README.md.
// ‹key› becomes inline code and {{clip}} / {{welcome}} a mention of that
// tutorial issue. English UI names follow the app's labels and the Chinese
// ones its zh-CN dictionary (web/src/i18n), so the steps match what people
// see on screen.

const (
	tutorialSectionCore     = "core"
	tutorialSectionPlanning = "planning"
	tutorialSectionDocs     = "docs"
	tutorialSectionAgent    = "agent"
)

var tutorialSections = []struct{ id, title, titleZH string }{
	{tutorialSectionCore, "Core workflow", "核心工作流"},
	{tutorialSectionPlanning, "Planning", "规划"},
	{tutorialSectionDocs, "Documents and collaboration", "文档与协作"},
	{tutorialSectionAgent, "Agent", "Agent"},
}

type tutorialEntry struct {
	clip, section  string
	title, titleZH string
	body, bodyZH   string
}

const tutorialWelcomeIntro = `Welcome to Flow! This workspace starts with a short tour: one issue for each part of Flow, each with a short clip, a few steps to try and the shortcuts worth knowing. They are the sub-issues below, assigned to you and labeled **Getting started**.

Go through them in any order and close each one when you are done: press ‹S› and pick **Done**. When you have finished the tour, close this issue too, or delete the whole set.`

const tutorialWelcomeTip = `**Tip:** Press ‹⌘ K› to open the command menu from anywhere and ‹⌘ /› to see every keyboard shortcut. On Windows and Linux, use ‹Ctrl› instead of ‹⌘›.`

const tutorialWelcomeIntroZH = `欢迎使用 Flow！这个工作区附带一套简短的导览：Flow 的每个功能模块各有一个事项，里面有一段演示动图、几个可以动手试试的步骤，以及值得记住的快捷键。它们就是下面的子事项，已经指派给你，并带有 **新手入门** 标签。

可以按任意顺序完成，做完一个就关闭一个：按 ‹S› 并选择 **完成**。全部完成后，把这个事项也关闭，或者直接删除整套教程事项。`

const tutorialWelcomeTipZH = `**提示：** 在任意位置按 ‹⌘ K› 打开命令菜单，按 ‹⌘ /› 查看全部键盘快捷键。在 Windows 和 Linux 上，用 ‹Ctrl› 代替 ‹⌘›。`

func tutorialEntries() []tutorialEntry {
	return []tutorialEntry{
		{
			clip: "issues-board", section: tutorialSectionCore,
			title: "Work with issues in a list or on a board", titleZH: "在列表和看板中处理事项",
			body: `Every issue in a team lives in one fast list. Switch to a board when you want to watch work move across statuses.

**Try it**

1. Open **Issues** under your team in the sidebar. The tutorial issues are already there.
2. Press ‹⇧ V› (or click **Display options**) and switch the layout to **Board**.
3. Drag this issue's card from **Todo** to **In Progress**.
4. Back in the list, move between rows with ‹J› and ‹K› and press ‹Enter› to open one.

**Tip:** Display options also set grouping, ordering and which properties each row shows, and every view keeps its own.

**Next:** {{issue-detail}} · {{filters-views}}`,
			bodyZH: `团队的所有事项都在一个快速的列表里。想看工作在各个状态之间流转时，切换到看板即可。

**动手试试**

1. 在侧边栏中打开你团队下的 **事项**，这些教程事项已经在里面了。
2. 按 ‹⇧ V›（或点击 **显示选项**），把布局切换为 **看板**。
3. 把这个事项的卡片从 **待办** 拖到 **进行中**。
4. 回到列表，用 ‹J› 和 ‹K› 在行之间移动，按 ‹Enter› 打开事项。

**提示：** 显示选项还能设置分组、排序和每一行显示的属性，每个视图都会记住自己的设置。

**接下来：** {{issue-detail}} · {{filters-views}}`,
		},
		{
			clip: "issue-detail", section: tutorialSectionCore,
			title: "Open an issue and change its properties", titleZH: "打开事项并修改属性",
			body: `Everything about a piece of work is on its issue page: the description, the properties in the sidebar, sub-issues and a full activity trail.

**Try it**

1. You are on an issue page now. Look at the properties on the right: status, priority, assignee, labels and project.
2. Press ‹P› to set a priority and ‹S› to change the status. ‹A› assigns the issue and ‹L› adds labels.
3. Open {{welcome}}: its sub-issues are listed under it. Use **Add sub-issue** to break your own work down the same way.
4. Scroll down to the activity feed, where every change and comment is recorded.

**Tip:** Press ‹⌘ K› on an issue to see every action for it, each with its shortcut.

**Next:** {{issue-create}} · {{issue-comments}}`,
			bodyZH: `一项工作的全部信息都在它的事项页上：描述、右侧的属性、子事项以及完整的活动记录。

**动手试试**

1. 你现在就在一个事项页上。看看右侧的属性：状态、优先级、负责人、标签和项目。
2. 按 ‹P› 设置优先级，按 ‹S› 修改状态；按 ‹A› 指派负责人，按 ‹L› 添加标签。
3. 打开 {{welcome}}，它的子事项都列在下面。用 **添加子事项** 也可以这样拆分你自己的工作。
4. 向下滚动到活动记录，每一次修改和评论都记录在那里。

**提示：** 在事项上按 ‹⌘ K›，可以看到针对它的所有操作，每个操作都标有快捷键。

**接下来：** {{issue-create}} · {{issue-comments}}`,
		},
		{
			clip: "issue-create", section: tutorialSectionCore,
			title: "Create an issue", titleZH: "创建事项",
			body: `Capture work the moment you think of it. The create dialog takes a title, a description and every property in one go.

**Try it**

1. Press ‹C› anywhere in Flow to open the create dialog.
2. Type a title, press ‹Enter› and write a description. Screenshots can be pasted straight in.
3. Set the status, priority, assignee, project and labels with the buttons below the description.
4. Press ‹⌘ Enter› to create the issue. Turn on **Create more** to keep the dialog open for the next one.

**Tip:** Issues are created in the team you are looking at; pick another team at the top of the dialog.

**Next:** {{filters-views}} · {{doc-selection-toolbar}}`,
			bodyZH: `想到什么就马上记下来。在创建对话框里可以一次填好标题、描述和所有属性。

**动手试试**

1. 在 Flow 的任意位置按 ‹C› 打开创建对话框。
2. 输入标题，按 ‹Enter› 后写描述。截图可以直接粘贴进去。
3. 用描述下方的按钮设置状态、优先级、负责人、项目和标签。
4. 按 ‹⌘ Enter› 创建事项。打开 **继续创建**，对话框会保持打开，方便接着创建下一个。

**提示：** 事项会创建在你当前查看的团队里，也可以在对话框顶部换成其他团队。

**接下来：** {{filters-views}} · {{doc-selection-toolbar}}`,
		},
		{
			clip: "filters-views", section: tutorialSectionCore,
			title: "Filter issues and save a view", titleZH: "筛选事项并保存为视图",
			body: `Filters narrow any list down to the issues you care about. Save a combination you use often as a view and it is always one click away.

**Try it**

1. Open your team's issues and press ‹F› (or click **Add filter**).
2. Pick **Labels** and then **Getting started** to show only the tutorial issues.
3. Add another filter, such as **Status** or **Assignee**. Filters combine.
4. Click **Add new view** (‹⌥ V›) and then **Create view** to save the result.

**Tip:** Saved views appear under **Views** in the sidebar, and you can favorite them to pin them at the top.

**Next:** {{inbox}} · {{issues-board}}`,
			bodyZH: `筛选能把任何列表缩小到你关心的事项。常用的筛选组合可以保存为视图，随时一键打开。

**动手试试**

1. 打开团队的事项列表，按 ‹F›（或点击 **添加筛选条件**）。
2. 选择 **标签**，再选 **新手入门**，只显示这些教程事项。
3. 再添加一个筛选条件，比如 **状态** 或 **负责人**，多个条件会叠加生效。
4. 点击 **添加新视图**（‹⌥ V›），然后点 **创建视图** 保存结果。

**提示：** 保存的视图会出现在侧边栏的 **视图** 中，收藏后还能固定在顶部。

**接下来：** {{inbox}} · {{issues-board}}`,
		},
		{
			clip: "inbox", section: tutorialSectionCore,
			title: "Keep up with your Inbox", titleZH: "用收件箱跟进动态",
			body: `Mentions, assignments, comments and status changes on the issues you follow land in your Inbox, so you never have to go looking for what needs you.

**Try it**

1. Press ‹G› then ‹I› (or click **Inbox** at the top of the sidebar).
2. Open a notification to read it in context on the right.
3. Press ‹H› to snooze one until later, ‹U› to mark it read or unread, and ‹Backspace› to remove it.
4. A new workspace has a quiet Inbox: invite a teammate and ask them to comment on this issue to see one arrive.

**Tip:** You follow the issues you create or are assigned. Use **Subscribe** or **Unsubscribe** in an issue's ‹⌘ K› menu to change that.

**Next:** {{issue-comments}} · {{agent-mention}}`,
			bodyZH: `你关注的事项上发生的提及、指派、评论和状态变更都会进入收件箱，不用再到处找需要你处理的事情。

**动手试试**

1. 按 ‹G› 再按 ‹I›（或点击侧边栏顶部的 **收件箱**）。
2. 打开一条通知，在右侧查看它的上下文。
3. 按 ‹H› 稍后提醒，按 ‹U› 标记为已读或未读，按 ‹Backspace› 删除。
4. 新工作区的收件箱还很安静：邀请一位同事，请他在这个事项下评论，就能看到通知到达。

**提示：** 你会自动关注自己创建或被指派的事项。在事项的 ‹⌘ K› 菜单中选择 **订阅** 或 **取消订阅** 可以调整。

**接下来：** {{issue-comments}} · {{agent-mention}}`,
		},
		{
			clip: "projects-overview", section: tutorialSectionPlanning,
			title: "Plan work with projects", titleZH: "用项目规划工作",
			body: `A project groups the issues behind one deliverable, with a lead, dates, milestones and progress you can follow at a glance.

**Try it**

1. Open **Projects** in the sidebar and press ‹N› then ‹P› to create a project.
2. Give it a name, a lead and a target date, then open it to see its overview.
3. Add milestones to split the project into stages.
4. Move issues into it: open an issue and press ‹⇧ P›.

**Tip:** The project list can be filtered, grouped and saved as a view, just like issues.

**Next:** {{project-update}} · {{initiatives}}`,
			bodyZH: `项目把为同一个交付成果服务的事项组织在一起，并带有负责人、日期、里程碑和一目了然的进度。

**动手试试**

1. 打开侧边栏中的 **项目**，按 ‹N› 再按 ‹P› 新建项目。
2. 填写名称、负责人和目标日期，然后打开它查看概览。
3. 添加里程碑，把项目拆分成几个阶段。
4. 把事项移入项目：打开一个事项，按 ‹⇧ P›。

**提示：** 项目列表也能像事项一样筛选、分组并保存为视图。

**接下来：** {{project-update}} · {{initiatives}}`,
		},
		{
			clip: "initiatives", section: tutorialSectionPlanning,
			title: "Group projects into initiatives", titleZH: "用目标汇总项目",
			body: `Initiatives collect the projects that serve one goal, so you can follow a larger effort in one place.

**Try it**

1. Open **Initiatives** in the sidebar and press ‹N› then ‹I› to create one.
2. Give it an owner, a target date and a short description of the goal.
3. Add the projects that contribute to it.
4. Follow the initiative's progress as its projects move forward.

**Tip:** Initiatives can contain other initiatives, so a company goal can hold the efforts of several teams.

**Next:** {{projects-overview}} · {{cycles}}`,
			bodyZH: `目标（Initiative）把服务于同一个方向的项目汇总在一起，让你在一个地方跟进更大范围的工作。

**动手试试**

1. 打开侧边栏中的 **目标**，按 ‹N› 再按 ‹I› 新建目标。
2. 填写负责人、目标日期，并简要描述要达成的方向。
3. 添加为它出力的项目。
4. 随着项目推进，跟进目标的整体进度。

**提示：** 目标下面还可以包含子目标，这样一个公司级目标就能汇总多个团队的工作。

**接下来：** {{projects-overview}} · {{cycles}}`,
		},
		{
			clip: "cycles", section: tutorialSectionPlanning,
			title: "Work in cycles", titleZH: "按周期推进工作",
			body: `Cycles are short, repeating time boxes (one or two weeks) that give a team a steady rhythm. Work that is not finished can roll over to the next cycle.

**Try it**

1. Cycles are off for a new team: open **Team settings**, go to **Cycles** and choose **Enable cycles**.
2. Open the team's current cycle from the sidebar.
3. Add issues to it: open an issue and press ‹⇧ C›.
4. Follow scope, started and completed work in the cycle's sidebar, and see how the work is spread across people.

**Tip:** Projects describe what you deliver; cycles describe when the team works on it. Most teams use both.

**Next:** {{projects-overview}} · {{issues-board}}`,
			bodyZH: `周期（Cycle）是一段段重复的短时间盒（一到两周），让团队保持稳定的节奏。没完成的工作可以顺延到下一个周期。

**动手试试**

1. 新团队默认没有开启周期：打开 **团队设置**，进入 **周期**，选择 **启用周期**。
2. 从侧边栏打开团队的当前周期。
3. 把事项加入周期：打开一个事项，按 ‹⇧ C›。
4. 在周期的侧栏中查看范围、已开始和已完成的工作，以及工作在成员之间的分布。

**提示：** 项目说明要交付什么，周期说明团队什么时候做。大多数团队会同时使用两者。

**接下来：** {{projects-overview}} · {{issues-board}}`,
		},
		{
			clip: "project-update", section: tutorialSectionPlanning,
			title: "Post a project update", titleZH: "发布项目更新",
			body: `A short update with a health status keeps everyone informed without another meeting.

**Try it**

1. Open a project and click **Update**.
2. Pick the health: **On track**, **At risk** or **Off track**.
3. Write what changed and what comes next. Mention people with ‹@› and link issues by their ID.
4. Post it. The project's members are notified and the health shows in the project list.

**Tip:** Post updates on a regular rhythm, for example every week, so the project list always shows a current health.

**Next:** {{projects-overview}} · {{doc-editing}}`,
			bodyZH: `一条带健康度的简短更新，就能让大家了解进展，不必再开一次会。

**动手试试**

1. 打开一个项目，点击 **更新**。
2. 选择健康度：**进展正常**、**存在风险** 或 **偏离计划**。
3. 写下有什么变化、接下来做什么。用 ‹@› 提及成员，输入事项 ID 即可关联事项。
4. 发布更新。项目成员会收到通知，健康度也会显示在项目列表中。

**提示：** 按固定节奏发布更新（比如每周一次），项目列表就能一直反映最新的健康度。

**接下来：** {{projects-overview}} · {{doc-editing}}`,
		},
		{
			clip: "doc-editing", section: tutorialSectionDocs,
			title: "Write in documents", titleZH: "编写文档",
			body: `Documents keep specs, notes and plans next to the work they describe. Issue descriptions use the same editor.

**Try it**

1. Press ‹⌘ K›, choose **Create new document in…** and pick your team.
2. Type ‹/› on an empty line for headings, lists, checklists, code blocks and more.
3. Select text to bold, italicize or link it from the toolbar, or use ‹⌘ B› and ‹⌘ I›.
4. Paste images or drop files into the page. Everything is saved as you type.

**Tip:** Markdown shortcuts work too: type ‹#› and a space for a heading, or ‹-› and a space for a list.

**Next:** {{doc-selection-toolbar}} · {{doc-comment}}`,
			bodyZH: `文档把需求说明、笔记和计划放在它们所描述的工作旁边。事项描述用的也是同一个编辑器。

**动手试试**

1. 按 ‹⌘ K›，选择 **在…中新建文档**，再选择你的团队。
2. 在空行输入 ‹/›，可以插入标题、列表、待办清单、代码块等。
3. 选中文字后，用工具栏加粗、设为斜体或添加链接，也可以按 ‹⌘ B› 和 ‹⌘ I›。
4. 直接粘贴图片或拖入文件。所有内容都会在输入时自动保存。

**提示：** 也支持 Markdown 快捷输入：输入 ‹#› 加空格变成标题，输入 ‹-› 加空格变成列表。

**接下来：** {{doc-selection-toolbar}} · {{doc-comment}}`,
		},
		{
			clip: "doc-selection-toolbar", section: tutorialSectionDocs,
			title: "Use the selection toolbar", titleZH: "使用选区工具栏",
			body: `Select text in a document or a description and a toolbar appears with everything you can do with it.

**Try it**

1. Select a sentence in a document, or in this description.
2. Format it, add a link, turn it into a quote or click **Comment**.
3. Click **Create issue from selection** (‹⌘ ⇧ O›): the issue is created right away and the first line becomes its title.
4. Click **Ask agent** to talk about the selection with the agent.

**Tip:** Turning a sentence of a spec into an issue is the fastest way to go from planning to work.

**Next:** {{doc-comment}} · {{agent-floating-chat}}`,
			bodyZH: `在文档或描述中选中文字，就会出现一个工具栏，列出可以对它进行的所有操作。

**动手试试**

1. 在文档里（或者就在这段描述里）选中一句话。
2. 设置格式、添加链接、转为引用，或者点击 **评论**。
3. 点击 **从选区创建事项**（‹⌘ ⇧ O›）：事项会立即创建，选区的第一行成为它的标题。
4. 点击 **询问 Agent**，就选中的内容和 Agent 讨论。

**提示：** 把需求文档里的一句话直接变成事项，是从规划走向执行最快的方式。

**接下来：** {{doc-comment}} · {{agent-floating-chat}}`,
		},
		{
			clip: "doc-comment", section: tutorialSectionDocs,
			title: "Comment inline on a document", titleZH: "在文档中添加行内评论",
			body: `Discuss a specific sentence right where it is written instead of in a separate thread.

**Try it**

1. Select text in a document and click **Comment** in the toolbar.
2. Write your comment and send it. The text stays highlighted while the thread is open.
3. Reply in the thread or add a reaction. Mention people with ‹@› to notify them.
4. Click **Resolve thread** once the discussion is settled.

**Tip:** Resolved threads are kept, so the reasoning behind a decision stays with the document.

**Next:** {{issue-comments}} · {{doc-editing}}`,
			bodyZH: `在文字所在的位置直接讨论某一句话，而不用另开一个讨论。

**动手试试**

1. 在文档中选中文字，点击工具栏里的 **评论**。
2. 写下评论并发送。讨论未解决时，这段文字会一直高亮。
3. 在讨论串中回复或添加回应。用 ‹@› 提及成员来通知他们。
4. 讨论有结论后，点击 **解决讨论串**。

**提示：** 已解决的讨论串会保留下来，决策背后的理由也就一直留在文档里。

**接下来：** {{issue-comments}} · {{doc-editing}}`,
		},
		{
			clip: "issue-comments", section: tutorialSectionDocs,
			title: "Discuss work in issue comments", titleZH: "在事项评论中讨论",
			body: `Comments keep the conversation next to the work. Threads, reactions and mentions keep it easy to follow.

**Try it**

1. Scroll to the bottom of this issue and write in **Leave a comment…**.
2. Mention a teammate with ‹@›, or reference another issue by its ID, such as {{issue-detail}}.
3. Hover over a comment to react with an emoji or reply in its thread.
4. Everyone following the issue is notified in their Inbox.

**Tip:** Mention ‹@Flow› in a comment and the agent answers in the thread (see {{agent-mention}}).

**Next:** {{inbox}} · {{doc-comment}}`,
			bodyZH: `评论让讨论紧挨着工作本身。讨论串、回应和提及让对话一目了然。

**动手试试**

1. 滚动到这个事项底部，在 **发表评论…** 中输入内容。
2. 用 ‹@› 提及同事，或输入其他事项的 ID 来引用它，比如 {{issue-detail}}。
3. 把鼠标移到评论上，可以添加表情回应，或在讨论串中回复。
4. 关注这个事项的所有人都会在收件箱中收到通知。

**提示：** 在评论中提及 ‹@Flow›，Agent 会在讨论串中回复（见 {{agent-mention}}）。

**接下来：** {{inbox}} · {{doc-comment}}`,
		},
		{
			clip: "agent-chat", section: tutorialSectionAgent,
			title: "Ask the Flow agent", titleZH: "向 Flow Agent 提问",
			body: `The Flow agent knows your workspace. Ask it about issues, projects and documents, or ask it to make changes for you.

**Try it**

1. Press ‹G› then ‹J› to open the **Agent** page, or press ‹⌘ J› to ask from anywhere.
2. Ask a question, for example: "Which of my Getting started issues are still open?"
3. Watch it look things up with its tools. Answers can include tables and live issue rows you can open.
4. Ask for changes too, such as creating issues or updating their status.

**Tip:** The agent needs an AI model configured on this Flow server. If it says **Flow Agent is not configured**, ask your administrator.

**Next:** {{agent-options}} · {{agent-floating-chat}}`,
			bodyZH: `Flow Agent 了解你的工作区。可以问它关于事项、项目和文档的问题，也可以让它替你修改。

**动手试试**

1. 按 ‹G› 再按 ‹J› 打开 **Agent** 页面，或在任意位置按 ‹⌘ J› 提问。
2. 问一个问题，比如：“我的新手入门事项还有哪些没完成？”
3. 看它调用工具查找信息。回答中可以包含表格和可直接打开的实时事项行。
4. 也可以让它修改内容，比如创建事项或更新状态。

**提示：** Agent 需要在 Flow 服务器上配置 AI 模型。如果提示 **Flow Agent 尚未配置**，请联系管理员。

**接下来：** {{agent-options}} · {{agent-floating-chat}}`,
		},
		{
			clip: "agent-floating-chat", section: tutorialSectionAgent,
			title: "Ask the agent about a selection", titleZH: "就选中内容询问 Agent",
			body: `Select text in a document and ask the agent about it without leaving the page.

**Try it**

1. Open a document (or this description) and select a few sentences.
2. Click **Ask agent** in the selection toolbar.
3. The floating chat opens with your selection attached as context. Type your question and press ‹Enter›.
4. Read the answer next to the page you are working on.

**Tip:** Try asking "Which open issues already cover this?" on a sentence from a spec.

**Next:** {{doc-selection-toolbar}} · {{agent-chat}}`,
			bodyZH: `在文档中选中文字，不用离开页面就能向 Agent 提问。

**动手试试**

1. 打开一个文档（或者就用这段描述），选中几句话。
2. 点击选区工具栏中的 **询问 Agent**。
3. 浮动对话框会打开，并把你的选区作为上下文附上。输入问题后按 ‹Enter›。
4. 在你正在编辑的页面旁边查看回答。

**提示：** 选中需求文档中的一句话，问问它：“有哪些未完成的事项已经覆盖了这一点？”

**接下来：** {{doc-selection-toolbar}} · {{agent-chat}}`,
		},
		{
			clip: "agent-mention", section: tutorialSectionAgent,
			title: "Mention @Flow in a comment", titleZH: "在评论中 @Flow",
			body: `Bring the agent into any issue by mentioning it. It picks up the request, works on it and answers in the thread.

**Try it**

1. In a comment on this issue, type ‹@Flow› and pick **Flow** from the list.
2. Say what you need, for example: "@Flow summarize this issue and suggest sub-issues."
3. A working row shows while the agent is busy, and its reply arrives in the thread.
4. Reply in the thread to follow up.

**Tip:** Everyone on the issue sees the agent's answer, so it works well for questions the whole team cares about.

**Next:** {{agent-chat}} · {{issue-comments}}`,
			bodyZH: `在任意事项中提及 Agent，就能把它拉进来。它会接手请求、开始处理，并在讨论串中回复。

**动手试试**

1. 在这个事项的评论中输入 ‹@Flow›，从列表中选择 **Flow**。
2. 说出你的需求，比如：“@Flow 总结一下这个事项，并建议几个子事项。”
3. Agent 处理时会显示一行进行中的状态，回复会出现在讨论串里。
4. 在讨论串中回复，继续追问。

**提示：** 事项的所有参与者都能看到 Agent 的回答，很适合全队都关心的问题。

**接下来：** {{agent-chat}} · {{issue-comments}}`,
		},
		{
			clip: "agent-options", section: tutorialSectionAgent,
			title: "Answer the agent's questions with option pills", titleZH: "用选项按钮回答 Agent 的提问",
			body: `When a request is ambiguous, the agent asks instead of guessing and offers the likely answers as pills.

**Try it**

1. On the **Agent** page, ask for something open-ended, such as "Create an issue to update our onboarding docs."
2. If it needs more, for example which team to use, it asks and shows the options as pills.
3. Click a pill, or type your own answer, and it carries on.

**Tip:** Answering a question is all it takes to keep the agent going, so you stay in control of what it changes.

**Next:** {{agent-chat}} · {{agent-mention}}`,
			bodyZH: `当请求不够明确时，Agent 会先提问而不是瞎猜，并把可能的答案做成选项按钮。

**动手试试**

1. 在 **Agent** 页面提一个开放式的请求，比如：“创建一个事项，用来更新我们的入职文档。”
2. 如果它需要更多信息（比如放到哪个团队），就会提问并把选项显示为按钮。
3. 点击一个选项，或者自己输入答案，它就会继续执行。

**提示：** 只需回答问题就能让 Agent 继续，它要改动什么始终由你掌控。

**接下来：** {{agent-chat}} · {{agent-mention}}`,
		},
	}
}

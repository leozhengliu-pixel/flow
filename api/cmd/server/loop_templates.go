package main

import "time"

// loopTemplate is one of Linear's "or pick a template" cards plus the draft it creates.
type loopTemplate struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Icon          string         `json:"icon"`
	Color         string         `json:"color"`
	TriggerLabel  string         `json:"triggerLabel"`
	TriggerIcon   string         `json:"triggerIcon"`
	ActionLabel   string         `json:"actionLabel"`
	ActionIcon    string         `json:"actionIcon"`
	RequiresTeam  bool           `json:"requiresTeam"`
	LevelHint     string         `json:"levelHint,omitempty"`
	TriggerType   string         `json:"triggerType"`
	TriggerConfig map[string]any `json:"triggerConfig"`
	Instructions  string         `json:"instructions"`
	CodeAccess    string         `json:"codeAccess,omitempty"`
	// Questions are what the loop-builder agent asks before publishing.
	Questions []loopTemplateQuestion `json:"questions"`
	schedule  map[string]any
}

type loopTemplateQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// config returns the template's trigger configuration; schedules start today.
func (template loopTemplate) config(now time.Time) map[string]any {
	config := map[string]any{}
	source := template.TriggerConfig
	if template.TriggerType == "schedule" {
		source = template.schedule
		config["startDate"] = now.Format("2006-01-02")
	}
	for key, value := range source {
		config[key] = value
	}
	return config
}

func loopTemplateByID(id string) *loopTemplate {
	for index := range loopTemplates {
		if loopTemplates[index].ID == id {
			return &loopTemplates[index]
		}
	}
	return nil
}

const triageLevelHint = "Triage loops must belong to a team"

var loopTemplates = []loopTemplate{
	{
		ID: "autofix-bugs", Name: "Autofix bugs", Description: "Investigates new bug reports and starts a coding session when it finds a clear fix.",
		Icon: "Bug", Color: "#eb5757", TriggerLabel: "On triage", TriggerIcon: "Triage", ActionLabel: "Start a coding session", ActionIcon: "Code",
		RequiresTeam: true, LevelHint: triageLevelHint, TriggerType: "issue", CodeAccess: "readWrite",
		TriggerConfig: map[string]any{"event": "triage", "filters": []any{map[string]any{"field": "label", "operator": "is", "value": "Bug"}}},
		Instructions: `Investigate each new bug report that arrives in triage and start a coding session only when you find a clear, low-risk fix.

1. Read the issue, its comments and attachments. Restate the reported behaviour, the expected behaviour and the reproduction steps in your own words. If the report lacks the steps or the affected area needed to act, comment asking the reporter for exactly what is missing and stop.
2. Search for duplicates and related issues. If this is a duplicate of an open issue, link it, mention the original in a comment and stop.
3. Use code access to locate the code involved. Identify the root cause, not just the symptom, and check recent changes that touched it.
4. Decide whether the fix is clear: the cause is certain, the change is small and contained (a few files), it does not change public APIs, data migrations, security or billing behaviour, and it can be covered by a test.
5. If the fix is clear, start a coding session with a concise plan: the root cause, the files to change, the change itself and the test to add. Comment on the issue with a link to the session and a one-paragraph summary of the fix.
6. If the fix is not clear, do not change code. Comment with your findings: the likely cause, the relevant files and functions, and what a developer should verify first. Set the priority based on user impact if it is not set.

Never close the issue or mark it done yourself; the team reviews every fix.`,
		Questions: []loopTemplateQuestion{
			{Question: "When should the loop start a coding session?", Options: []string{"Only for clear fixes", "For any likely fix", "Never, just investigate"}},
			{Question: "Which bug reports should it pick up?", Options: []string{"Issues labeled Bug", "All triage issues"}},
		},
	},
	{
		ID: "triage-agent", Name: "Triage agent", Description: "Reviews incoming issues, adds context, and routes each one to the right owner.",
		Icon: "Triage", Color: "#f2994a", TriggerLabel: "On triage", TriggerIcon: "Triage", ActionLabel: "Route the issue to an owner", ActionIcon: "Assignee",
		RequiresTeam: true, LevelHint: triageLevelHint, TriggerType: "issue", CodeAccess: "disabled",
		TriggerConfig: map[string]any{"event": "triage"},
		Instructions: `Triage each incoming issue so it reaches the right owner with enough context to act on it.

Review the issue description, comments, attachments, linked issues, customer requests, and related work. Search for duplicates and use ownership documents, past issue assignments, and code intelligence when they help identify the responsible team or person.

For each issue:

1. Confirm what is being requested and whether the report has enough detail.
2. Link and close clear duplicates when appropriate.
3. Set the most relevant team, assignee, labels, and priority when the available evidence supports the choice.
4. Add a short comment only when you need missing information or when the routing decision needs context.

Do not invent details or make low-confidence ownership changes. When ownership is unclear, leave the issue in triage and name the missing context in one concise comment.`,
		Questions: []loopTemplateQuestion{
			{Question: "How much should the triage loop do on its own?", Options: []string{"Route and close clear duplicates", "Route, but don't close", "Suggest changes only"}},
			{Question: "Should the triage loop skip any issues that arrive in triage?", Options: []string{"Review all", "Skip already assigned", "Specify exceptions"}},
		},
	},
	{
		ID: "slack-qa", Name: "Slack Q&A assistant", Description: "Answers product questions from the team in a selected Slack channel.",
		Icon: "Slack", Color: "#4a154b", TriggerLabel: "Hourly", TriggerIcon: "Clock", ActionLabel: "Reply in the Slack thread", ActionIcon: "Slack",
		TriggerType: "schedule", CodeAccess: "read",
		schedule: map[string]any{"interval": 1, "unit": "hour", "time": "09:00"},
		Instructions: `Every hour, answer new product questions the team asked in the selected Slack channel.

1. Read the messages posted in the channel since the last run. Pick out direct questions about the product, its behaviour, the roadmap or the status of work. Skip messages that already have an answer in the thread, social chatter and questions addressed to a specific person.
2. For each question, look up the answer in the workspace: search issues, projects, project updates, documents and release notes. Use code access only to confirm how a feature behaves today.
3. Reply in the message's thread with a short, direct answer (two or three sentences), followed by links to the issues, projects or documents you relied on.
4. If you cannot find a confident answer, say so in the thread, share what you did find, and name the team or project owner who is most likely to know.
5. Never guess about dates, pricing, security or customer commitments; point to the owner instead.

End the run with a summary listing the questions you answered and the ones you could not.`,
		Questions: []loopTemplateQuestion{
			{Question: "Which Slack channel should the assistant watch?", Options: []string{"#product-questions", "#ask-engineering", "I'll set it later"}},
			{Question: "What should it do when it is unsure?", Options: []string{"Say so and tag an owner", "Skip the question"}},
		},
	},
	{
		ID: "weekly-wrap", Name: "Weekly wrap", Description: "Summarizes the week's accomplishments for a person or team and shares the highlights.",
		Icon: "Calendar", Color: "#5e6ad2", TriggerLabel: "Weekly", TriggerIcon: "Clock", ActionLabel: "Share the highlights", ActionIcon: "Megaphone",
		TriggerType: "schedule", CodeAccess: "disabled",
		schedule: map[string]any{"interval": 1, "unit": "week", "time": "16:00", "weekdays": []any{"fri"}},
		Instructions: `Every Friday afternoon, summarize what the team accomplished this week and share the highlights.

1. Collect the issues completed in the last seven days, the project updates posted this week, and projects or milestones that were started, completed or changed status.
2. Group the work by project (or by area for issues without a project). For each group write one or two sentences about what shipped and why it matters to users; do not list every issue.
3. Call out notable wins: launches, completed milestones, customer-facing fixes and large pieces of cleanup.
4. Add a short "Watch next week" section with projects that are at risk or off track, blocked issues, and anything due next week.
5. Keep the wrap under 250 words, with links to the projects and the most important issues. Share it as a post in the team's channel or as a project update on the team's main project, and reply with a link to where you posted it.`,
		Questions: []loopTemplateQuestion{
			{Question: "Whose week should the wrap cover?", Options: []string{"The whole team", "Just me"}},
			{Question: "Where should the highlights go?", Options: []string{"Post in Slack", "Reply here only"}},
		},
	},
	{
		ID: "feature-request-report", Name: "Feature request report", Description: "Groups recent feature requests into a weekly report with themes and customer impact.",
		Icon: "Lightbulb", Color: "#26b5ce", TriggerLabel: "Weekly", TriggerIcon: "Clock", ActionLabel: "Post a weekly report", ActionIcon: "Document",
		TriggerType: "schedule", CodeAccess: "disabled",
		schedule: map[string]any{"interval": 1, "unit": "week", "time": "09:00", "weekdays": []any{"mon"}},
		Instructions: `Every Monday morning, turn last week's feature requests into a report the product team can act on.

1. Collect feature requests from the last seven days: new issues labeled Feature or Feature request, customer requests linked to issues, and requests that arrived through Slack, email or other intake channels.
2. Group the requests into themes (for example "Reporting exports" or "SSO for guests"). Merge requests that describe the same need even when worded differently.
3. For each theme give: a one-sentence description of the underlying need, the number of requests, the customers and their size or tier where known, links to the issues and customer requests, and any existing project or issue that already covers it.
4. Rank themes by customer impact: number and size of requesting customers, revenue at risk, and how often the theme came up before.
5. Post the report as a document or project update titled "Feature requests – week of <date>", with the top five themes first and a short list of one-off requests at the end. Do not create or change issues; the report is for review.`,
		Questions: []loopTemplateQuestion{
			{Question: "Which requests should the report include?", Options: []string{"All teams", "Only this team"}},
			{Question: "Where should the report be posted?", Options: []string{"As a document", "In Slack"}},
		},
	},
	{
		ID: "security-alerts", Name: "Security alerts", Description: "Alerts a Slack channel and sets an SLA whenever a security issue is reported.",
		Icon: "Shield", Color: "#eb5757", TriggerLabel: "On issue change", TriggerIcon: "Issue", ActionLabel: "Alert the Slack channel", ActionIcon: "Slack",
		TriggerType: "issue", CodeAccess: "disabled",
		TriggerConfig: map[string]any{"event": "labels", "value": "Security"},
		Instructions: `Whenever an issue is labeled Security, make sure the right people know right away and that it has an SLA.

1. Read the issue and assess severity from the description: Critical (active exploitation, data exposure or account takeover), High (exploitable vulnerability without evidence of abuse), Medium (hardening or limited-impact finding) or Low (informational).
2. Set the priority to match: Critical → Urgent, High → High, Medium → Medium, Low → Low. Set the SLA so the issue must be resolved within 24 hours for Critical, 3 days for High, 14 days for Medium and 30 days for Low.
3. Post an alert in the security Slack channel with the issue link, the severity, a one-sentence summary of the risk and the SLA deadline. Do not paste secrets, credentials, exploit details or customer data into the alert.
4. If the issue has no assignee, assign it to the security owner for the affected area, or mention in the alert that it needs an owner.
5. Add a short comment on the issue noting the severity you chose, the SLA and where the alert was posted.

Never close, downgrade or make a security issue public.`,
		Questions: []loopTemplateQuestion{
			{Question: "Which Slack channel should receive alerts?", Options: []string{"#security", "#incidents", "I'll set it later"}},
			{Question: "Should the loop also assign an owner?", Options: []string{"Assign an owner", "Alert only"}},
		},
	},
}

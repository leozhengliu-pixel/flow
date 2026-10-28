package main

import "testing"

// A report named by its period ("周报", "日报", "月报", "周总结", "weekly report", "digest") is not a status update by
// itself: written or appended to a document it is a document output, posted as a project or initiative update it
// is a status update, and otherwise the run's reply.
func TestInferLoopReportNouns(t *testing.T) {
	for _, item := range []loopIntentCase{
		// A plain reply.
		{text: "每周五写周报", readOnly: true},
		{text: "生成本周周报。", readOnly: true},
		{text: "汇总本周完成的事项，写一份周报", readOnly: true},
		{text: "月报：汇总本月完成的事项", readOnly: true},
		{text: "每天写日报，节假日跳过。", readOnly: true, skips: true},
		{text: "写周总结", readOnly: true},
		{text: "Write the weekly report.", readOnly: true},
		{text: "Send the daily digest.", readOnly: true},
		{text: "Summarize this month's work as a monthly summary.", readOnly: true},
		// Into a document.
		{text: "把周报追加到团队文档", outputs: "document", operations: "document:append"},
		{text: "生成周报，并追加到团队周报文档", outputs: "document", operations: "document:append"},
		{text: "将本周周报写入团队文档", outputs: "document", operations: "document:update"},
		{text: "每月把月报追加到已有的月度文档末尾", outputs: "document", operations: "document:append"},
		{text: "Write the weekly report and append it to the team doc.", outputs: "document", operations: "document:append"},
		{text: "Append the daily digest to the existing team document.", outputs: "document", operations: "document:append"},
		{text: "Create a new document with the weekly report.", outputs: "document", operations: "document:create"},
		// As a project or initiative update.
		{text: "每周五把周报发布为项目更新", outputs: "statusUpdate", operations: "statusUpdate:write"},
		{text: "汇总本周进展，生成周报并发布到 Flow 项目更新", outputs: "statusUpdate", operations: "statusUpdate:write"},
		{text: "Post the weekly digest as a project update.", outputs: "statusUpdate", operations: "statusUpdate:write"},
		{text: "Post the weekly report as an initiative update on each initiative.", outputs: "statusUpdate", operations: "statusUpdate:write"},
		// Status updates named as such keep their meaning.
		{text: "每周五为每个项目发布状态更新", outputs: "statusUpdate", operations: "statusUpdate:write"},
		{text: "Post a weekly update on each project.", outputs: "statusUpdate", operations: "statusUpdate:write"},
		// A refusal still refuses.
		{text: "写周报，不要发布项目更新", readOnly: true, prohibited: "statusUpdate"},
	} {
		checkLoopIntent(t, item)
	}
}

// The loop's name alone ("周报", "Weekly digest") does not ask for a status update.
func TestLoopReportNameIsNotAStatusUpdate(t *testing.T) {
	for _, item := range []struct{ name, text, want string }{
		{"周报", "汇总本周完成的事项", ""},
		{"周报", "汇总本周完成的事项，追加到团队文档", "document"},
		{"周报", "每周五为每个项目发布状态更新", "statusUpdate"},
		{"Weekly digest", "Summarize the week's merged work.", ""},
	} {
		got := inferLoopOutputIntentParts(item.name, item.text)
		if outputs := joinKinds(got.Outputs); outputs != item.want {
			t.Errorf("%q / %q: outputs %q, want %q", item.name, item.text, outputs, item.want)
		}
	}
}

func joinKinds(kinds []string) string {
	text := ""
	for index, kind := range kinds {
		if index > 0 {
			text += ","
		}
		text += kind
	}
	return text
}

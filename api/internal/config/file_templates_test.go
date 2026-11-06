package config

import (
	"strings"
	"testing"

	"flow/api/internal/domain"
)

const workflowFile = `
defaults:
  workspace:
    workflowStates:
      - {name: Inbox, type: backlog, color: "#BEC2C8"}
      - {name: Ready, type: unstarted, color: "#e2e2e2", default: true, description: Groomed and ready}
      - {name: Doing, type: started, color: "lch(80% 90 85)"}
      - {name: Review, type: started}
      - {name: Shipped, type: completed, color: "#5E6AD2"}
      - {name: Dropped, type: canceled, color: "#95A2B3"}
    labels:
      - {name: Bug, color: "#EB5757"}
      - {name: Roadmap, color: "#18B99A", resourceType: project, description: On the public roadmap}
    labelGroups:
      - name: Area
        color: "#4EA7FC"
        labels:
          - {name: Backend}
          - {name: Frontend, color: "#BB87FC"}
      - name: Value
        resourceType: project
        labels:
          - {name: Revenue}
`

func TestConfigFileWorkflowStatesAndLabels(t *testing.T) {
	workspace := mustParse(t, workflowFile).Defaults.Workspace
	want := []domain.WorkflowStateTemplate{
		{Name: "Inbox", Type: "backlog", Color: "#BEC2C8", Position: 0},
		{Name: "Ready", Type: "unstarted", Color: "#e2e2e2", Description: "Groomed and ready", Position: 1, Default: true},
		{Name: "Doing", Type: "started", Color: "lch(80% 90 85)", Position: 2},
		{Name: "Review", Type: "started", Color: defaultStateColor, Position: 3},
		{Name: "Shipped", Type: "completed", Color: "#5E6AD2", Position: 4},
		{Name: "Dropped", Type: "canceled", Color: "#95A2B3", Position: 5},
		// Flow's reserved Duplicate status, after the configured ones.
		{Name: "Duplicate", Type: "canceled", Color: "#95A2B3", Position: 6, Reserved: true},
	}
	if len(workspace.WorkflowStates) != len(want) {
		t.Fatalf("states = %+v", workspace.WorkflowStates)
	}
	for index := range want {
		if workspace.WorkflowStates[index] != want[index] {
			t.Errorf("state %d = %+v, want %+v", index, workspace.WorkflowStates[index], want[index])
		}
	}
	if !workspace.CustomLabels || len(workspace.Labels) != 2 || len(workspace.LabelGroups) != 2 {
		t.Fatalf("labels = %+v, groups %+v", workspace.Labels, workspace.LabelGroups)
	}
	if workspace.Labels[0] != (domain.LabelTemplate{Name: "Bug", Color: "#EB5757", ResourceType: "issue"}) || workspace.Labels[1] != (domain.LabelTemplate{Name: "Roadmap", Color: "#18B99A", Description: "On the public roadmap", ResourceType: "project"}) {
		t.Errorf("labels = %+v", workspace.Labels)
	}
	area, value := workspace.LabelGroups[0], workspace.LabelGroups[1]
	if area.Name != "Area" || area.ResourceType != "issue" || area.Color != "#4EA7FC" || len(area.Labels) != 2 || area.Labels[0] != (domain.LabelTemplate{Name: "Backend", Color: defaultLabelColor, ResourceType: "issue"}) || area.Labels[1].Color != "#BB87FC" {
		t.Errorf("area group = %+v", area)
	}
	if value.ResourceType != "project" || value.Color != defaultLabelGroupColor || len(value.Labels) != 1 || value.Labels[0].ResourceType != "project" {
		t.Errorf("value group = %+v", value)
	}
}

func TestConfigFileWorkflowStateDefaults(t *testing.T) {
	// No default: the first backlog status. A duplicate status replaces the built-in one.
	states := mustParse(t, `
defaults:
  workspace:
    workflowStates:
      - {name: 待处理, type: unstarted, position: 10}
      - {name: 规划中, type: backlog, position: 5}
      - {name: 进行中, type: started, position: 20}
      - {name: 已完成, type: completed, position: 30}
      - {name: 已取消, type: canceled, position: 40}
      - {name: 重复, type: duplicate, position: 50, color: "#8A8F98"}
`).Defaults.Workspace.WorkflowStates
	if len(states) != 6 || !states[1].Default || states[0].Default || states[0].Position != 10 {
		t.Fatalf("states = %+v", states)
	}
	if last := states[5]; last != (domain.WorkflowStateTemplate{Name: "重复", Type: "canceled", Color: "#8A8F98", Position: 50, Reserved: true}) {
		t.Fatalf("duplicate status = %+v", last)
	}
	// Labels alone replace the built-in labels; an empty list means none.
	workspace := mustParse(t, "defaults:\n  workspace:\n    labels: []\n").Defaults.Workspace
	if !workspace.CustomLabels || len(workspace.Labels) != 0 || workspace.LabelGroups != nil || workspace.WorkflowStates != nil {
		t.Fatalf("empty labels = %+v", workspace)
	}
	// Leaving them out (or empty) keeps Flow's built-in statuses and labels.
	workspace = mustParse(t, "defaults:\n  workspace:\n    workflowStates:\n    labels: ~\n").Defaults.Workspace
	if workspace.CustomLabels || workspace.WorkflowStates != nil {
		t.Fatalf("unset = %+v", workspace)
	}
}

func TestConfigFileWorkflowStateAndLabelErrors(t *testing.T) {
	states := func(lines ...string) string {
		return "defaults:\n  workspace:\n    workflowStates:\n" + strings.Join(lines, "\n") + "\n"
	}
	complete := []string{
		"      - {name: Backlog, type: backlog}",
		"      - {name: Todo, type: unstarted}",
		"      - {name: Doing, type: started}",
		"      - {name: Done, type: completed}",
		"      - {name: Canceled, type: canceled}",
	}
	with := func(extra ...string) string { return states(append(append([]string{}, complete...), extra...)...) }
	labels := func(lines ...string) string {
		return "defaults:\n  workspace:\n" + strings.Join(lines, "\n") + "\n"
	}
	for _, test := range []struct{ name, content, want string }{
		{"not a list", "defaults:\n  workspace:\n    workflowStates: Backlog\n", `config file /etc/flow/config.yaml:3: defaults.workspace.workflowStates: must be a list of statuses (got "Backlog")`},
		{"missing types", states(complete[0], complete[2]), "config file /etc/flow/config.yaml:4: defaults.workspace.workflowStates: needs at least one status of each type (backlog, unstarted, started, completed, canceled); missing unstarted, completed, canceled"},
		{"empty list", "defaults:\n  workspace:\n    workflowStates: []\n", "needs at least one status of each type"},
		{"duplicate alone is not canceled", states(append(complete[:4:4], "      - {name: Dup, type: duplicate}")...), "missing canceled"},
		{"unknown type", with("      - {name: Triage, type: triage}"), `config file /etc/flow/config.yaml:9: defaults.workspace.workflowStates[5].type: must be one of backlog, unstarted, started, completed, canceled, duplicate (got "triage")`},
		{"unknown key", with("      - {name: Blocked, type: started, colour: red}"), "config file /etc/flow/config.yaml:9: defaults.workspace.workflowStates[5].colour: unknown key (allowed here: color, default, description, name, position, type)"},
		{"missing name", with("      - {type: started}"), "config file /etc/flow/config.yaml:9: defaults.workspace.workflowStates[5]: needs a name"},
		{"empty name", with("      - {name: ' ', type: started}"), "defaults.workspace.workflowStates[5].name: must not be empty"},
		{"missing type", with("      - {name: Blocked}"), "defaults.workspace.workflowStates[5]: needs a type"},
		{"same name", with("      - {name: todo, type: started}"), `config file /etc/flow/config.yaml:9: defaults.workspace.workflowStates[5].name: "todo" names another status too; status names must be unique`},
		{"bad colour", with("      - {name: Blocked, type: started, color: red}"), `defaults.workspace.workflowStates[5].color: must be a colour such as #5E6AD2 or lch(48% 59.31 288.43) (got "red")`},
		{"short hex", with("      - {name: Blocked, type: started, color: '#abc'}"), `workflowStates[5].color: must be a colour`},
		{"two defaults", states("      - {name: Backlog, type: backlog, default: true}", "      - {name: Todo, type: unstarted, default: true}", complete[2], complete[3], complete[4]), `config file /etc/flow/config.yaml:5: defaults.workspace.workflowStates[1].default: only one status can be the default ("Backlog" already is)`},
		{"default not bool", with("      - {name: Blocked, type: started, default: yes please}"), `workflowStates[5].default: must be true or false (got "yes please")`},
		{"duplicate default", with("      - {name: Dup, type: duplicate, default: true}"), "workflowStates[5].default: the duplicate status cannot be the default"},
		{"two duplicates", with("      - {name: Dup, type: duplicate}", "      - {name: Dup2, type: duplicate}"), "config file /etc/flow/config.yaml:10: defaults.workspace.workflowStates[6].type: only one status can have type duplicate"},
		{"duplicate name without type", with("      - {name: Duplicate, type: canceled}"), `defaults.workspace.workflowStates: the status named "Duplicate" must have type duplicate`},
		{"same position", with("      - {name: Blocked, type: started, position: 2}"), `config file /etc/flow/config.yaml:9: defaults.workspace.workflowStates[5].position: 2 is also the position of "Doing"; positions must be unique`},
		{"bad position", with("      - {name: Blocked, type: started, position: -1}"), "workflowStates[5].position: must be a number from 0 to 1000000 (got number -1)"},
		{"text position", with("      - {name: Blocked, type: started, position: first}"), `workflowStates[5].position: must be a number (got "first")`},
		{"status not mapping", with("      - Blocked"), `workflowStates[5]: must be a mapping of keys (got "Blocked")`},
		{"labels not list", labels("    labels: {name: Bug}"), "defaults.workspace.labels: must be a list of labels (got a mapping)"},
		{"label colour", labels("    labels:", "      - {name: Bug, color: crimson}"), `config file /etc/flow/config.yaml:4: defaults.workspace.labels[0].color: must be a colour such as #5E6AD2 (got "crimson")`},
		{"label lch", labels("    labels:", "      - {name: Bug, color: 'lch(48% 59.31 288.43)'}"), "labels[0].color: must be a colour such as #5E6AD2"},
		{"label resource", labels("    labels:", "      - {name: Bug, resourceType: document}"), `defaults.workspace.labels[0].resourceType: must be one of issue, project, initiative (got "document")`},
		{"label without name", labels("    labels:", "      - {color: '#EB5757'}"), "defaults.workspace.labels[0]: needs a name"},
		{"same label", labels("    labels:", "      - {name: Bug}", "      - {name: BUG}"), `config file /etc/flow/config.yaml:5: defaults.workspace.labels[1].name: "BUG" names another issue label too; label names must be unique`},
		{"same label in a group", labels("    labels:", "      - {name: Backend}", "    labelGroups:", "      - name: Area", "        labels:", "          - {name: backend}"), `defaults.workspace.labelGroups[0].labels[0].name: "backend" names another issue label too`},
		{"group label resource", labels("    labelGroups:", "      - name: Area", "        labels:", "          - {name: Backend, resourceType: project}"), "defaults.workspace.labelGroups[0].labels[0].resourceType: unknown key (allowed here: color, description, name)"},
		{"same group", labels("    labelGroups:", "      - {name: Area}", "      - {name: area}"), `defaults.workspace.labelGroups[1].name: "area" names another issue label group too`},
		{"group unknown key", labels("    labelGroups:", "      - {name: Area, exclusive: true}"), "defaults.workspace.labelGroups[0].exclusive: unknown key (allowed here: color, description, labels, name, resourceType)"},
		{"group secret-like key", labels("    labelGroups:", "      - {name: Area, apiKey: x}"), "defaults.workspace.labelGroups[0].apiKey: unknown key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parse(t, test.content)
			if err == nil {
				t.Fatalf("parsed without error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err, test.want)
			}
		})
	}
	// The same label name may be used by different resource types.
	mustParse(t, labels("    labels:", "      - {name: Delivery}", "      - {name: Delivery, resourceType: project}"))
}

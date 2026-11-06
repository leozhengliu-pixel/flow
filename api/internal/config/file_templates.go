package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"flow/api/internal/domain"
)

// A new workspace's workflow statuses and labels (defaults.workspace.
// workflowStates, labels and labelGroups). Statuses and labels belong to the
// workspace and its teams share them, so they are seeded when a workspace is
// created and teams created later use the workspace's set, as they use the
// built-in one.

// duplicateStateType is the file's name for the reserved status duplicates
// are moved to (a canceled status that cannot be renamed or deleted).
const duplicateStateType = "duplicate"

// The built-in reserved status, added when the file does not list one.
var builtinDuplicateState = domain.WorkflowStateTemplate{Name: "Duplicate", Type: "canceled", Color: "#95A2B3", Reserved: true}

// Colours of a new status, label and label group when the file gives none
// (the colours the API gives them).
const (
	defaultStateColor      = "#6B6F76"
	defaultLabelColor      = "#5E6AD2"
	defaultLabelGroupColor = "#8B8D98"
)

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	// lchColorPattern is the lch() form of the built-in status colours.
	lchColorPattern = regexp.MustCompile(`^lch\(\d+(\.\d+)?% \d+(\.\d+)? \d+(\.\d+)?\)$`)
)

var labelResourceTypes = []string{"issue", "project", "initiative"}

func labelColor(value string) error {
	if !hexColorPattern.MatchString(value) {
		return errors.New("must be a colour such as #5E6AD2")
	}
	return nil
}

func stateColor(value string) error {
	if !hexColorPattern.MatchString(value) && !lchColorPattern.MatchString(value) {
		return errors.New("must be a colour such as #5E6AD2 or lch(48% 59.31 288.43)")
	}
	return nil
}

// fields reads a mapping whose keys must be among allowed.
func (p fileParser) fields(node *yaml.Node, path string, allowed ...string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, p.errorf(node, path, "must be a mapping of keys (got %s)", describe(node))
	}
	values := map[string]*yaml.Node{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key := keyNode.Value
		if keyNode.Kind != yaml.ScalarNode || !slices.Contains(allowed, key) {
			return nil, p.errorf(keyNode, join(path, key), "unknown key (allowed here: %s)", strings.Join(slices.Sorted(slices.Values(allowed)), ", "))
		}
		if _, dup := values[key]; dup {
			return nil, p.errorf(keyNode, join(path, key), "is set twice")
		}
		if !isNull(valueNode) {
			values[key] = valueNode
		}
	}
	return values, nil
}

// text reads an optional text value of a mapping ("" when it is absent);
// check validates it.
func (p fileParser) text(values map[string]*yaml.Node, path, key string, check func(string) error) (string, error) {
	node, ok := values[key]
	if !ok {
		return "", nil
	}
	text, err := p.scalarString(node, join(path, key))
	if err != nil {
		return "", err
	}
	if check != nil {
		if err := check(text); err != nil {
			return "", p.errorf(node, join(path, key), "%v (got %q)", err, text)
		}
	}
	return text, nil
}

// requiredText reads a text value the mapping (item) must have.
func (p fileParser) requiredText(item *yaml.Node, values map[string]*yaml.Node, path, key string, check func(string) error) (string, error) {
	node, ok := values[key]
	if !ok {
		return "", p.errorf(item, path, "needs a %s", key)
	}
	if text, err := p.scalarString(node, join(path, key)); err == nil && text == "" {
		return "", p.errorf(node, join(path, key), "must not be empty")
	}
	return p.text(values, path, key, check)
}

func (p fileParser) list(node *yaml.Node, path, what string) error {
	if node.Kind != yaml.SequenceNode {
		return p.errorf(node, path, "must be a list of %s (got %s)", what, describe(node))
	}
	return nil
}

func oneOf(values ...string) func(string) error {
	return func(value string) error {
		if !slices.Contains(values, value) {
			return fmt.Errorf("must be one of %s", strings.Join(values, ", "))
		}
		return nil
	}
}

// parseWorkflowStates reads defaults.workspace.workflowStates: the statuses
// of a new workspace, replacing the built-in ones. Every status type needs at
// least one status, names are unique, at most one status is the default (the
// first backlog status otherwise) and at most one has type duplicate (Flow
// adds its reserved Duplicate status otherwise).
func parseWorkflowStates(p fileParser, node *yaml.Node, path string) error {
	if err := p.list(node, path, "statuses"); err != nil {
		return err
	}
	types := append(slices.Clone(domain.WorkflowStateTypes), duplicateStateType)
	states := []domain.WorkflowStateTemplate{}
	names := map[string]bool{}
	positions := map[float64]string{}
	defaultIndex, duplicateIndex := -1, -1
	for index, item := range node.Content {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		values, err := p.fields(item, itemPath, "name", "type", "color", "description", "position", "default")
		if err != nil {
			return err
		}
		name, err := p.requiredText(item, values, itemPath, "name", nil)
		if err != nil {
			return err
		}
		if names[strings.ToLower(name)] {
			return p.errorf(values["name"], join(itemPath, "name"), "%q names another status too; status names must be unique", name)
		}
		names[strings.ToLower(name)] = true
		stateType, err := p.requiredText(item, values, itemPath, "type", oneOf(types...))
		if err != nil {
			return err
		}
		color, err := p.text(values, itemPath, "color", stateColor)
		if err != nil {
			return err
		}
		if color == "" {
			color = defaultStateColor
		}
		description, err := p.text(values, itemPath, "description", nil)
		if err != nil {
			return err
		}
		position := float64(index)
		if positionNode, ok := values["position"]; ok {
			if positionNode.Kind != yaml.ScalarNode || (positionNode.Tag != "!!int" && positionNode.Tag != "!!float") {
				return p.errorf(positionNode, join(itemPath, "position"), "must be a number (got %s)", describe(positionNode))
			}
			if position, err = strconv.ParseFloat(positionNode.Value, 64); err != nil || position < 0 || position > 1e6 {
				return p.errorf(positionNode, join(itemPath, "position"), "must be a number from 0 to 1000000 (got %s)", describe(positionNode))
			}
		}
		if other, taken := positions[position]; taken {
			at := item
			if positionNode, ok := values["position"]; ok {
				at = positionNode
			}
			return p.errorf(at, join(itemPath, "position"), "%v is also the position of %s; positions must be unique", position, other)
		}
		positions[position] = strconv.Quote(name)
		state := domain.WorkflowStateTemplate{Name: name, Type: stateType, Color: color, Description: description, Position: position}
		if stateType == duplicateStateType {
			if duplicateIndex >= 0 {
				return p.errorf(values["type"], join(itemPath, "type"), "only one status can have type duplicate")
			}
			duplicateIndex = index
			state.Type, state.Reserved = "canceled", true
		}
		if defaultNode, ok := values["default"]; ok {
			if defaultNode.Kind != yaml.ScalarNode || defaultNode.Tag != "!!bool" {
				return p.errorf(defaultNode, join(itemPath, "default"), "must be true or false (got %s)", describe(defaultNode))
			}
			if defaultNode.Value == "true" {
				if state.Reserved {
					return p.errorf(defaultNode, join(itemPath, "default"), "the duplicate status cannot be the default")
				}
				if defaultIndex >= 0 {
					return p.errorf(defaultNode, join(itemPath, "default"), "only one status can be the default (%q already is)", states[defaultIndex].Name)
				}
				defaultIndex = index
				state.Default = true
			}
		}
		states = append(states, state)
	}
	missing := []string{}
	for _, stateType := range domain.WorkflowStateTypes {
		if !slices.ContainsFunc(states, func(state domain.WorkflowStateTemplate) bool { return state.Type == stateType && !state.Reserved }) {
			missing = append(missing, stateType)
		}
	}
	if len(missing) > 0 {
		return p.errorf(node, path, "needs at least one status of each type (%s); missing %s", strings.Join(domain.WorkflowStateTypes, ", "), strings.Join(missing, ", "))
	}
	if defaultIndex < 0 {
		defaultIndex = slices.IndexFunc(states, func(state domain.WorkflowStateTemplate) bool { return state.Type == "backlog" })
		states[defaultIndex].Default = true
	}
	if duplicateIndex < 0 {
		if names[strings.ToLower(builtinDuplicateState.Name)] {
			return p.errorf(node, path, "the status named %q must have type duplicate: it is the status duplicates are moved to", builtinDuplicateState.Name)
		}
		duplicate := builtinDuplicateState
		for position := range positions {
			duplicate.Position = max(duplicate.Position, position+1)
		}
		states = append(states, duplicate)
	}
	p.file.Defaults.Workspace.WorkflowStates = states
	return nil
}

// labelNames tracks label and group names per resource type.
type labelNames map[string]map[string]bool

func (n labelNames) add(resource, name string) bool {
	if n[resource] == nil {
		n[resource] = map[string]bool{}
	}
	key := strings.ToLower(name)
	if n[resource][key] {
		return false
	}
	n[resource][key] = true
	return true
}

// parseLabel reads one label; resource is the group's resource type, or ""
// for a label outside a group (which may set its own).
func (p fileParser) parseLabel(item *yaml.Node, path, groupResource string) (domain.LabelTemplate, error) {
	allowed := []string{"name", "color", "description"}
	if groupResource == "" {
		allowed = append(allowed, "resourceType")
	}
	values, err := p.fields(item, path, allowed...)
	if err != nil {
		return domain.LabelTemplate{}, err
	}
	name, err := p.requiredText(item, values, path, "name", nil)
	if err != nil {
		return domain.LabelTemplate{}, err
	}
	color, err := p.text(values, path, "color", labelColor)
	if err != nil {
		return domain.LabelTemplate{}, err
	}
	if color == "" {
		color = defaultLabelColor
	}
	description, err := p.text(values, path, "description", nil)
	if err != nil {
		return domain.LabelTemplate{}, err
	}
	resource := groupResource
	if resource == "" {
		if resource, err = p.text(values, path, "resourceType", oneOf(labelResourceTypes...)); err != nil {
			return domain.LabelTemplate{}, err
		}
		if resource == "" {
			resource = "issue"
		}
	}
	if !p.labels.add(resource, name) { // unique among the resource type's labels, in or outside groups
		return domain.LabelTemplate{}, p.errorf(values["name"], join(path, "name"), "%q names another %s label too; label names must be unique", name, resource)
	}
	return domain.LabelTemplate{Name: name, Color: color, Description: description, ResourceType: resource}, nil
}

// parseLabels reads defaults.workspace.labels: labels outside a group.
func parseLabels(p fileParser, node *yaml.Node, path string) error {
	if err := p.list(node, path, "labels"); err != nil {
		return err
	}
	labels := []domain.LabelTemplate{}
	for index, item := range node.Content {
		label, err := p.parseLabel(item, fmt.Sprintf("%s[%d]", path, index), "")
		if err != nil {
			return err
		}
		labels = append(labels, label)
	}
	p.file.Defaults.Workspace.Labels, p.file.Defaults.Workspace.CustomLabels = labels, true
	return nil
}

// parseLabelGroups reads defaults.workspace.labelGroups: groups with their labels.
func parseLabelGroups(p fileParser, node *yaml.Node, path string) error {
	if err := p.list(node, path, "label groups"); err != nil {
		return err
	}
	groups := []domain.LabelGroupTemplate{}
	groupNames := labelNames{}
	for index, item := range node.Content {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		values, err := p.fields(item, itemPath, "name", "color", "description", "resourceType", "labels")
		if err != nil {
			return err
		}
		name, err := p.requiredText(item, values, itemPath, "name", nil)
		if err != nil {
			return err
		}
		color, err := p.text(values, itemPath, "color", labelColor)
		if err != nil {
			return err
		}
		if color == "" {
			color = defaultLabelGroupColor
		}
		description, err := p.text(values, itemPath, "description", nil)
		if err != nil {
			return err
		}
		resource, err := p.text(values, itemPath, "resourceType", oneOf(labelResourceTypes...))
		if err != nil {
			return err
		}
		if resource == "" {
			resource = "issue"
		}
		if !groupNames.add(resource, name) {
			return p.errorf(values["name"], join(itemPath, "name"), "%q names another %s label group too; group names must be unique", name, resource)
		}
		group := domain.LabelGroupTemplate{Name: name, Color: color, Description: description, ResourceType: resource, Labels: []domain.LabelTemplate{}}
		if labelsNode, ok := values["labels"]; ok {
			labelsPath := join(itemPath, "labels")
			if err := p.list(labelsNode, labelsPath, "labels"); err != nil {
				return err
			}
			for labelIndex, labelNode := range labelsNode.Content {
				label, err := p.parseLabel(labelNode, fmt.Sprintf("%s[%d]", labelsPath, labelIndex), resource)
				if err != nil {
					return err
				}
				group.Labels = append(group.Labels, label)
			}
		}
		groups = append(groups, group)
	}
	p.file.Defaults.Workspace.LabelGroups, p.file.Defaults.Workspace.CustomLabels = groups, true
	return nil
}

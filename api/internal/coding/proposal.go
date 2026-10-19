package coding

// Agent-assisted environment setup: "Configure with agent" and "Fix with
// agent" (docs/coding-sessions-design.md §18). Owner: track K.
//
// Both start a coding session with source environmentSetup. The agent
// inspects the repository, writes mise tools and a prepare script, runs them
// in its sandbox until they succeed, and calls the runner's
// propose_environment MCP tool. The runner posts the proposal to
// POST /internal/coding-runtime/environment-proposal; Flow validates it with
// NormalizeProposal and stores it as the environment's pendingProposal. An
// admin or owner then applies (ApplyProposal) or discards it.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codingenv"
	"flow/api/internal/domain"
)

// Turn kinds of environment-setup sessions (coding.Turn.Kind).
const (
	TurnConfigureEnvironment = "configureEnvironment"
	TurnFixEnvironment       = "fixEnvironment"
)

// ProposalExplanationMax bounds the agent's explanation.
const ProposalExplanationMax = 16 << 10

// ErrNoProposal reports an apply without a pending proposal.
var ErrNoProposal = errors.New("coding: the environment has no pending proposal")

// NormalizeProposal validates a proposal from the runner. Proposals never
// carry secret values: environment variables that look like secrets are
// removed and their names moved to MissingSecrets, so the user adds them as
// secrets.
func NormalizeProposal(proposal *domain.CodingEnvironmentProposal, now time.Time) error {
	missing := []string{}
	for _, name := range proposal.MissingSecrets {
		name = strings.TrimSpace(name)
		if name != "" && ValidEnvName(name) && !codingenv.ReservedEnvName(name) && !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if proposal.Mise != nil {
		kept := proposal.Mise.EnvironmentVariables[:0:0]
		for _, variable := range proposal.Mise.EnvironmentVariables {
			if IsSecretEnv(variable.Name, variable.Value) {
				if ValidEnvName(variable.Name) && !slices.Contains(missing, variable.Name) {
					missing = append(missing, variable.Name)
				}
				continue
			}
			kept = append(kept, variable)
		}
		proposal.Mise.EnvironmentVariables = kept
		if err := codingenv.NormalizeMise(proposal.Mise); err != nil {
			return err
		}
	}
	if len(missing) > codingenv.EnvironmentSecretsMax {
		missing = missing[:codingenv.EnvironmentSecretsMax]
	}
	proposal.MissingSecrets = missing
	if proposal.PrepareScript != nil {
		script := strings.ReplaceAll(*proposal.PrepareScript, "\r\n", "\n")
		if len(script) > codingenv.EnvironmentScriptMax {
			return codingenv.Invalid("prepareScript", "must be at most %d bytes", codingenv.EnvironmentScriptMax)
		}
		proposal.PrepareScript = &script
	}
	if proposal.CustomGuidance != nil {
		guidance := strings.TrimSpace(*proposal.CustomGuidance)
		if len(guidance) > codingenv.EnvironmentGuidanceMax {
			return codingenv.Invalid("customGuidance", "must be at most %d bytes", codingenv.EnvironmentGuidanceMax)
		}
		proposal.CustomGuidance = &guidance
	}
	if proposal.DependencyManagement != nil {
		switch *proposal.DependencyManagement {
		case domain.CodingDependenciesCustomOnly, domain.CodingDependenciesCustomAndGenerated:
		default:
			return codingenv.Invalid("dependencyManagement", "must be custom-only or custom-and-generated")
		}
	}
	if proposal.SandboxSize != nil {
		switch *proposal.SandboxSize {
		case domain.CodingSizeSmall, domain.CodingSizeMedium, domain.CodingSizeLarge:
		default:
			return codingenv.Invalid("sandboxSize", "must be small, medium or large")
		}
	}
	proposal.Explanation = strings.TrimSpace(proposal.Explanation)
	if len(proposal.Explanation) > ProposalExplanationMax {
		proposal.Explanation = proposal.Explanation[:ProposalExplanationMax]
	}
	if proposal.Mise == nil && proposal.PrepareScript == nil && proposal.CustomGuidance == nil && proposal.DependencyManagement == nil && proposal.SandboxSize == nil && len(proposal.MissingSecrets) == 0 {
		return codingenv.Invalid("proposal", "proposes no change")
	}
	if proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = now.UTC()
	}
	return nil
}

// ApplyProposal writes the pending proposal's fields into the environment and
// clears it. Fields the proposal leaves nil are kept.
func ApplyProposal(environment *domain.CodingEnvironment) error {
	proposal := environment.PendingProposal
	if proposal == nil {
		return ErrNoProposal
	}
	if proposal.Mise != nil {
		environment.Mise = *proposal.Mise
	}
	if proposal.PrepareScript != nil {
		environment.PrepareScript = *proposal.PrepareScript
	}
	if proposal.CustomGuidance != nil {
		environment.CustomGuidance = *proposal.CustomGuidance
	}
	if proposal.DependencyManagement != nil {
		environment.DependencyManagement = *proposal.DependencyManagement
	}
	if proposal.SandboxSize != nil {
		environment.SandboxSize = *proposal.SandboxSize
	}
	environment.PendingProposal = nil
	return codingenv.NormalizeEnvironment(environment)
}

// ConfigurePrompt is the first turn of "Configure with agent".
func ConfigurePrompt(environment domain.CodingEnvironment, instructions string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Set up the coding environment for %s (%s on %s).\n\n", environment.Repository, environment.CodeHost, environment.HostName)
	builder.WriteString(setupTaskText)
	writeCurrentConfig(&builder, environment)
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		builder.WriteString("\nAdditional instructions from the user:\n")
		builder.WriteString(truncate(instructions, 8<<10))
		builder.WriteString("\n")
	}
	return builder.String()
}

// FixPrompt is the first turn of "Fix with agent", seeded with the failed
// snapshot build's error and the tail of its logs.
func FixPrompt(environment domain.CodingEnvironment, snapshot *domain.CodingEnvironmentSnapshot, logTail string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "The environment setup for %s is failing. Find the cause and fix the setup.\n\n", environment.Repository)
	if snapshot != nil {
		fmt.Fprintf(&builder, "Snapshot %s failed", snapshot.ID)
		if snapshot.ErrorCode != "" {
			fmt.Fprintf(&builder, " (%s)", snapshot.ErrorCode)
		}
		if snapshot.Error != "" {
			fmt.Fprintf(&builder, ": %s", truncate(snapshot.Error, 2000))
		}
		builder.WriteString("\n")
	}
	if logTail = strings.TrimSpace(logTail); logTail != "" {
		builder.WriteString("\nLast lines of the setup log:\n```\n")
		builder.WriteString(logTail)
		builder.WriteString("\n```\n\n")
	}
	builder.WriteString(setupTaskText)
	writeCurrentConfig(&builder, environment)
	return builder.String()
}

const setupTaskText = `Your task:
1. Inspect the repository (lockfiles, README, CI workflows, tool version files) to find the toolchains and the commands needed to install dependencies and build.
2. Choose mise tool versions (for example node, python, go, java) and write the prepare script: the shell commands that install dependencies so the project builds and its tests can run.
3. Run "mise install" and the prepare script in this sandbox and iterate until both succeed.
4. Call the propose_environment tool with the mise tools, any files and plain environment variables, the prepare script, and an explanation. Set verified to true only if the setup succeeded here.
Never put secret values in the proposal: list the names of required secrets in missingSecrets instead. Do not open a pull request and do not push changes; this session only proposes environment settings.
`

func writeCurrentConfig(builder *strings.Builder, environment domain.CodingEnvironment) {
	builder.WriteString("\nCurrent environment configuration:\n")
	if len(environment.Mise.Tools) == 0 {
		builder.WriteString("- mise tools: none\n")
	} else {
		tools := make([]string, 0, len(environment.Mise.Tools))
		for tool, version := range environment.Mise.Tools {
			tools = append(tools, tool+"@"+version)
		}
		slices.Sort(tools)
		fmt.Fprintf(builder, "- mise tools: %s\n", strings.Join(tools, ", "))
	}
	for _, file := range environment.Mise.Files {
		fmt.Fprintf(builder, "- file ~/%s (%d bytes)\n", file.Path, len(file.Content))
	}
	for _, variable := range environment.Mise.EnvironmentVariables {
		fmt.Fprintf(builder, "- variable %s\n", variable.Name)
	}
	for _, secret := range environment.Secrets {
		fmt.Fprintf(builder, "- secret %s (value available during setup)\n", secret.Name)
	}
	fmt.Fprintf(builder, "- dependency management: %s\n", DependencyMode(environment.DependencyManagement))
	if script := strings.TrimSpace(environment.PrepareScript); script != "" {
		builder.WriteString("- prepare script:\n```sh\n")
		builder.WriteString(truncate(script, 8<<10))
		builder.WriteString("\n```\n")
	} else {
		builder.WriteString("- prepare script: none\n")
	}
}

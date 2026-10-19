// Package codingenv holds the coding-environment rules shared by the store
// and the coding layer (docs/coding-sessions-design.md §6, §10, §18):
// validation and normalization, repository keys, secret names, resource ids
// and the snapshot reconciler's row model. Owner: track K.
//
// It imports only domain, so internal/store (which internal/config imports)
// can use it; internal/coding imports config and sandbox and therefore
// cannot be imported by the store.
package codingenv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Field limits.
const (
	EnvironmentNameMax       = 255
	EnvironmentScriptMax     = 64 << 10
	EnvironmentGuidanceMax   = 16 << 10
	EnvironmentToolsMax      = 50
	EnvironmentFilesMax      = 20
	EnvironmentFileMax       = 64 << 10
	EnvironmentVariablesMax  = 200
	EnvironmentOwnersMax     = 50
	EnvironmentDomainsMax    = 100
	EnvironmentSecretsMax    = 100
	EnvironmentSecretNameMax = 191
	EnvironmentSecretMax     = 64 << 10
)

// ErrInvalidEnvironment wraps every validation failure.
var ErrInvalidEnvironment = errors.New("coding: invalid environment")

// ValidationError names the invalid field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalidEnvironment }

// Invalid returns a ValidationError for field.
func Invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

var (
	hostNamePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
	repoPartPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	toolNamePattern  = regexp.MustCompile(`^[A-Za-z0-9@/._:+-]+$`)
	toolValuePattern = regexp.MustCompile(`^[A-Za-z0-9@/._:+~^<>=*-]+( [A-Za-z0-9@/._:+~^<>=*-]+)*$`)
	domainPattern    = regexp.MustCompile(`^\.?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// reservedEnvPrefixes cannot be set by an environment: the runner and the
// sandbox provider own them.
var reservedEnvPrefixes = []string{"FLOW_", "LINEAR_"}
var reservedEnvNames = []string{"HOME", "PATH", "USER", "SHELL", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", "GH_TOKEN", "GITHUB_TOKEN", "GITLAB_TOKEN", "GIT_ASKPASS"}

// ReservedEnvName reports names an environment may not set (variables or secrets).
func ReservedEnvName(name string) bool {
	if slices.Contains(reservedEnvNames, name) {
		return true
	}
	upper := strings.ToUpper(name)
	for _, prefix := range reservedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// DefaultHostName is the public host of a code host.
func DefaultHostName(codeHost string) string {
	if codeHost == domain.CodeHostGitLab {
		return "gitlab.com"
	}
	return "github.com"
}

// ParseRepositoryInput accepts "owner/name", "group/sub/project", a web URL
// (https://github.com/o/r), an https clone URL or an scp-style git URL
// (git@host:o/r.git), and returns the host name (empty when the input does
// not name one) and the repository path without ".git".
func ParseRepositoryInput(raw string) (hostName, repository string) {
	value := strings.TrimSpace(raw)
	switch {
	case strings.Contains(value, "://"):
		if parsed, err := url.Parse(value); err == nil {
			hostName, value = strings.ToLower(parsed.Host), parsed.Path
			if at := strings.LastIndex(hostName, "@"); at >= 0 {
				hostName = hostName[at+1:]
			}
		}
	case strings.HasPrefix(value, "git@") && strings.Contains(value, ":"):
		host, rest, _ := strings.Cut(strings.TrimPrefix(value, "git@"), ":")
		hostName, value = strings.ToLower(host), rest
	}
	value = strings.Trim(value, "/")
	value = strings.TrimSuffix(value, ".git")
	// GitLab web URLs carry "/-/" sections (…/project/-/tree/main).
	if index := strings.Index(value, "/-/"); index >= 0 {
		value = value[:index]
	}
	return hostName, value
}

// InferCodeHost guesses the code host of a legacy repository string: a host
// name containing "gitlab" means GitLab; everything else is GitHub.
func InferCodeHost(hostName string) string {
	if strings.Contains(strings.ToLower(hostName), "gitlab") {
		return domain.CodeHostGitLab
	}
	return domain.CodeHostGitHub
}

// ActiveRepoKey is the coding_environments.active_repo_key of a repository:
// unique per (code host, host, repository), case-insensitive, at most 191
// bytes (long keys are hashed).
func ActiveRepoKey(codeHost, hostName, repository string) string {
	key := strings.ToLower(codeHost + ":" + hostName + "/" + repository)
	if len(key) <= 191 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeEnvironment trims, defaults and validates an environment in place.
// It never touches Secrets, PendingProposal, LatestSnapshot or timestamps.
func NormalizeEnvironment(environment *domain.CodingEnvironment) error {
	environment.CodeHost = strings.ToLower(strings.TrimSpace(environment.CodeHost))
	if environment.CodeHost == "" {
		environment.CodeHost = domain.CodeHostGitHub
	}
	if environment.CodeHost != domain.CodeHostGitHub && environment.CodeHost != domain.CodeHostGitLab {
		return Invalid("codeHost", "must be github or gitlab")
	}
	parsedHost, repository := ParseRepositoryInput(environment.Repository)
	environment.HostName = strings.ToLower(strings.TrimSpace(environment.HostName))
	if environment.HostName == "" {
		environment.HostName = parsedHost
	}
	if environment.HostName == "" {
		environment.HostName = DefaultHostName(environment.CodeHost)
	}
	if !hostNamePattern.MatchString(environment.HostName) || len(environment.HostName) > 191 {
		return Invalid("hostName", "is not a valid host name")
	}
	if parsedHost != "" && parsedHost != environment.HostName {
		return Invalid("repository", "points at %s, not %s", parsedHost, environment.HostName)
	}
	environment.Repository = repository
	if err := validateRepository(environment.CodeHost, environment.Repository); err != nil {
		return err
	}
	environment.Name = strings.TrimSpace(environment.Name)
	if environment.Name == "" {
		environment.Name = environment.Repository[strings.LastIndex(environment.Repository, "/")+1:]
	}
	if len(environment.Name) > EnvironmentNameMax {
		return Invalid("name", "must be at most %d characters", EnvironmentNameMax)
	}
	environment.RepositoryID = strings.TrimSpace(environment.RepositoryID)
	environment.DefaultBranch = strings.TrimSpace(environment.DefaultBranch)
	if len(environment.DefaultBranch) > 255 || strings.ContainsAny(environment.DefaultBranch, " \t\n~^:?*[\\") {
		return Invalid("defaultBranch", "is not a valid branch name")
	}
	owners := make([]string, 0, len(environment.OwnerIDs))
	for _, owner := range environment.OwnerIDs {
		if owner = strings.TrimSpace(owner); owner != "" && !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	if len(owners) > EnvironmentOwnersMax {
		return Invalid("ownerIds", "at most %d owners", EnvironmentOwnersMax)
	}
	environment.OwnerIDs = owners
	switch strings.TrimSpace(environment.Harness) {
	case "", "auto":
		environment.Harness = ""
	case domain.CodingHarnessCodex, domain.CodingHarnessClaudeCode, domain.CodingHarnessOpenSource:
		environment.Harness = strings.TrimSpace(environment.Harness)
	case "claude":
		environment.Harness = domain.CodingHarnessClaudeCode
	default:
		return Invalid("harness", "must be codex, claudeCode or openSource")
	}
	environment.Model = strings.TrimSpace(environment.Model)
	if len(environment.Model) > 191 || strings.ContainsAny(environment.Model, " \t\n") {
		return Invalid("model", "is not a valid model id")
	}
	switch environment.Network {
	case "", domain.CodingNetworkDefault, domain.CodingNetworkExtended, domain.CodingNetworkOpen, domain.CodingNetworkNone:
	default:
		return Invalid("network", "must be default, extended, open or none")
	}
	domains := make([]string, 0, len(environment.AllowedDomains))
	for _, item := range environment.AllowedDomains {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" || slices.Contains(domains, item) {
			continue
		}
		if !domainPattern.MatchString(item) || len(item) > 253 {
			return Invalid("allowedDomains", "%q is not a domain (use .example.com for subdomains)", truncate(item, 64))
		}
		domains = append(domains, item)
	}
	if len(domains) > EnvironmentDomainsMax {
		return Invalid("allowedDomains", "at most %d domains", EnvironmentDomainsMax)
	}
	environment.AllowedDomains = domains
	switch environment.SandboxSize {
	case "", domain.CodingSizeSmall, domain.CodingSizeMedium, domain.CodingSizeLarge:
	default:
		return Invalid("sandboxSize", "must be small, medium or large")
	}
	if err := NormalizeMise(&environment.Mise); err != nil {
		return err
	}
	environment.PrepareScript = strings.ReplaceAll(environment.PrepareScript, "\r\n", "\n")
	if len(environment.PrepareScript) > EnvironmentScriptMax {
		return Invalid("prepareScript", "must be at most %d bytes", EnvironmentScriptMax)
	}
	environment.CustomGuidance = strings.TrimSpace(environment.CustomGuidance)
	if len(environment.CustomGuidance) > EnvironmentGuidanceMax {
		return Invalid("customGuidance", "must be at most %d bytes", EnvironmentGuidanceMax)
	}
	switch environment.DependencyManagement {
	case "", domain.CodingDependenciesCustomOnly, domain.CodingDependenciesCustomAndGenerated:
	default:
		return Invalid("dependencyManagement", "must be custom-only or custom-and-generated")
	}
	return nil
}

func validateRepository(codeHost, repository string) error {
	if repository == "" {
		return Invalid("repository", "is required")
	}
	if len(repository) > 512 {
		return Invalid("repository", "is too long")
	}
	parts := strings.Split(repository, "/")
	if codeHost == domain.CodeHostGitHub && len(parts) != 2 {
		return Invalid("repository", "must be owner/name")
	}
	if len(parts) < 2 {
		return Invalid("repository", "must be group/project")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !repoPartPattern.MatchString(part) {
			return Invalid("repository", "%q is not a valid repository path", truncate(repository, 80))
		}
	}
	return nil
}

func NormalizeMise(mise *domain.CodingMiseConfig) error {
	if len(mise.Tools) > EnvironmentToolsMax {
		return Invalid("mise.tools", "at most %d tools", EnvironmentToolsMax)
	}
	tools := map[string]string{}
	for tool, version := range mise.Tools {
		tool, version = strings.TrimSpace(tool), strings.TrimSpace(version)
		if tool == "" {
			continue
		}
		if len(tool) > 128 || !toolNamePattern.MatchString(tool) {
			return Invalid("mise.tools", "%q is not a valid tool name", truncate(tool, 64))
		}
		if version == "" {
			version = "latest"
		}
		if len(version) > 128 || !toolValuePattern.MatchString(version) {
			return Invalid("mise.tools", "%q is not a valid version for %s", truncate(version, 64), tool)
		}
		tools[tool] = version
	}
	mise.Tools = tools
	if len(mise.Tools) == 0 {
		mise.Tools = nil
	}
	if len(mise.Files) > EnvironmentFilesMax {
		return Invalid("mise.files", "at most %d files", EnvironmentFilesMax)
	}
	seen := map[string]bool{}
	files := make([]domain.CodingMiseFile, 0, len(mise.Files))
	for _, file := range mise.Files {
		name := strings.TrimSpace(file.Path)
		name = strings.TrimPrefix(name, "~/")
		clean := path.Clean(name)
		if name == "" || strings.HasPrefix(name, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\x00") || len(name) > 255 {
			return Invalid("mise.files", "%q must be a path inside the home directory", truncate(file.Path, 64))
		}
		if seen[clean] {
			return Invalid("mise.files", "%s is listed twice", clean)
		}
		if len(file.Content) > EnvironmentFileMax {
			return Invalid("mise.files", "%s is larger than %d bytes", clean, EnvironmentFileMax)
		}
		seen[clean] = true
		files = append(files, domain.CodingMiseFile{Path: clean, Content: file.Content})
	}
	mise.Files = files
	if len(mise.Files) == 0 {
		mise.Files = nil
	}
	if len(mise.EnvironmentVariables) > EnvironmentVariablesMax {
		return Invalid("mise.environmentVariables", "at most %d variables", EnvironmentVariablesMax)
	}
	names := map[string]bool{}
	vars := make([]domain.CodingEnvVar, 0, len(mise.EnvironmentVariables))
	for _, variable := range mise.EnvironmentVariables {
		variable.Name = strings.TrimSpace(variable.Name)
		if !ValidEnvName(variable.Name) {
			return Invalid("mise.environmentVariables", "%q is not a valid variable name", truncate(variable.Name, 64))
		}
		if ReservedEnvName(variable.Name) {
			return Invalid("mise.environmentVariables", "%s is reserved by Flow", variable.Name)
		}
		if names[variable.Name] {
			return Invalid("mise.environmentVariables", "%s is listed twice", variable.Name)
		}
		if len(variable.Value) > EnvValueMaxBytes {
			return Invalid("mise.environmentVariables", "%s is longer than %d bytes", variable.Name, EnvValueMaxBytes)
		}
		names[variable.Name] = true
		vars = append(vars, variable)
	}
	mise.EnvironmentVariables = vars
	if len(mise.EnvironmentVariables) == 0 {
		mise.EnvironmentVariables = nil
	}
	return nil
}

// ValidateSecret checks a secret before it is stored.
func ValidateSecret(name, value string) error {
	if !ValidEnvName(name) || len(name) > EnvironmentSecretNameMax {
		return Invalid("secrets", "%q is not a valid variable name", truncate(name, 64))
	}
	if ReservedEnvName(name) {
		return Invalid("secrets", "%s is reserved by Flow", name)
	}
	if value == "" {
		return Invalid("secrets", "%s has an empty value", name)
	}
	if len(value) > EnvironmentSecretMax {
		return Invalid("secrets", "%s is longer than %d bytes", name, EnvironmentSecretMax)
	}
	return nil
}

// EnvironmentOwnedBy reports whether userID is one of the environment's owners.
func EnvironmentOwnedBy(environment domain.CodingEnvironment, userID string) bool {
	return userID != "" && slices.Contains(environment.OwnerIDs, userID)
}

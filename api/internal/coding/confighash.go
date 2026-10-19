package coding

// Snapshot config hash (docs/coding-sessions-design.md §6, §10). Owner: track K.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

// SnapshotConfigHash is the SHA-256 of everything that makes a prepared
// workspace stale: the image, the mise config, the prepare script, the
// dependency mode, the secret names (not values) and the base ref. Sessions
// restore only a ready snapshot with the same hash.
func SnapshotConfigHash(environment domain.CodingEnvironment, image, baseRef string, secretNames []string) string {
	files := append([]domain.CodingMiseFile(nil), environment.Mise.Files...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	names := make([]string, 0, len(secretNames))
	for _, name := range secretNames {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	tools := map[string]string{}
	for tool, version := range environment.Mise.Tools {
		tools[strings.TrimSpace(tool)] = strings.TrimSpace(version)
	}
	document := struct {
		Version              int                     `json:"v"`
		Image                string                  `json:"image"`
		Repository           string                  `json:"repository"`
		BaseRef              string                  `json:"baseRef"`
		Tools                map[string]string       `json:"tools"`
		Files                []domain.CodingMiseFile `json:"files"`
		Env                  []domain.CodingEnvVar   `json:"env"`
		PrepareScript        string                  `json:"prepareScript"`
		DependencyManagement string                  `json:"dependencyManagement"`
		Secrets              []string                `json:"secrets"`
	}{
		Version:              1,
		Image:                image,
		Repository:           strings.ToLower(environment.CodeHost + "/" + environment.HostName + "/" + environment.Repository),
		BaseRef:              baseRef,
		Tools:                tools,
		Files:                files,
		Env:                  SortEnvVars(environment.Mise.EnvironmentVariables),
		PrepareScript:        strings.TrimSpace(environment.PrepareScript),
		DependencyManagement: DependencyMode(environment.DependencyManagement),
		Secrets:              names,
	}
	encoded, _ := json.Marshal(document)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// DependencyMode normalizes dependency management ("" = custom-and-generated).
func DependencyMode(mode string) string {
	if mode == domain.CodingDependenciesCustomOnly {
		return mode
	}
	return domain.CodingDependenciesCustomAndGenerated
}

// Package docker is the Docker sandbox provider for local development and CI
// (track A): one container per sandbox on FLOW_CODING_DOCKER_NETWORK through
// the Docker Engine API at FLOW_CODING_DOCKER_HOST. Workspaces are named
// volumes; VolumeSnapshots are unsupported (use the archive strategy).
//
// This file is the registration stub; track A replaces New.
package docker

import (
	"fmt"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func init() { sandbox.Register(config.CodingProviderDocker, New) }

// New builds the provider from FLOW_CODING_DOCKER_*.
func New(deps sandbox.Dependencies) (sandbox.Provider, error) {
	return nil, fmt.Errorf("%w: docker", sandbox.ErrNotImplemented)
}

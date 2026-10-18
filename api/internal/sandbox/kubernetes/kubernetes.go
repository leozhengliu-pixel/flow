// Package kubernetes is the Kubernetes sandbox provider (track A): plain Pods
// via client-go, GA APIs only (core/v1, networking.k8s.io/v1, and the
// snapshot.storage.k8s.io/v1 CRD through the dynamic client when
// FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot). See
// docs/coding-sessions-design.md §9 for the objects it creates.
//
// This file is the registration stub; track A adds k8s.io/client-go to go.mod
// and replaces New.
package kubernetes

import (
	"fmt"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func init() { sandbox.Register(config.CodingProviderKubernetes, New) }

// New builds the provider from FLOW_CODING_K8S_*.
func New(deps sandbox.Dependencies) (sandbox.Provider, error) {
	return nil, fmt.Errorf("%w: kubernetes", sandbox.ErrNotImplemented)
}

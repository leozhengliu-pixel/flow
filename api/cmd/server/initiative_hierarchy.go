package main

import (
	"flow/api/internal/domain"
	"fmt"
	"slices"
	"time"
)

func changeInitiativeParent(data *domain.Bootstrap, childID, parentID string, add bool) error {
	child, err := initiativeByID(data, childID)
	if err != nil {
		return err
	}
	parents := domain.InitiativeParents(data)
	if add && domain.InitiativeParentCycle(parents, childID, []string{parentID}) {
		return fmt.Errorf("%w: initiative hierarchy cannot contain a cycle", errInvalid)
	}
	ids := removeString(parents[childID], parentID)
	if add {
		ids = append(ids, parentID)
	}
	child.ParentInitiativeIDs = slices.Clone(ids)
	child.UpdatedAt = time.Now().UTC()
	return nil
}

package main

import (
	"flow/api/internal/domain"
	"fmt"
	"slices"
	"time"
)

// errInitiativeNesting mirrors Linear's "Initiatives cannot be nested deeper than 5 levels".
var errInitiativeNesting = fmt.Errorf("%w: initiatives cannot be nested deeper than %d levels", errInvalid, domain.MaxInitiativeNesting)

func changeInitiativeParent(data *domain.Bootstrap, childID, parentID string, add bool) error {
	child, err := initiativeByID(data, childID)
	if err != nil {
		return err
	}
	parents := domain.InitiativeParents(data)
	if add && domain.InitiativeParentCycle(parents, childID, []string{parentID}) {
		return fmt.Errorf("%w: initiative hierarchy cannot contain a cycle", errInvalid)
	}
	if add && !slices.Contains(parents[childID], parentID) && domain.InitiativeNestingTooDeep(parents, childID, []string{parentID}) {
		return errInitiativeNesting
	}
	ids := removeString(parents[childID], parentID)
	if add {
		ids = append(ids, parentID)
	}
	child.ParentInitiativeIDs = slices.Clone(ids)
	child.UpdatedAt = time.Now().UTC()
	return nil
}

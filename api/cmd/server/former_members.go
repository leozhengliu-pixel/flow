package main

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

// The users collection keeps people after they are removed from or
// suspended in the workspace, so their names stay on the issues, comments and
// history they are part of. A write may keep such a reference, but it may not
// add one: a new assignee, delegate, subscriber, project lead or member,
// initiative owner or triage owner who is no longer a member is refused, and
// former members get no new notifications. Membership comes from the store
// (Bootstrap.FormerMembers, read from workspace_memberships on first use).

// errFormerMember is the 400 a write gets for newly naming a former member.
var errFormerMember error = formerMemberError{}

type formerMemberError struct{}

func (formerMemberError) Error() string {
	return "This person is no longer a member of the workspace"
}
func (formerMemberError) Unwrap() error { return errInvalid }

// formerMember reports whether id names someone removed from or suspended in
// the workspace. Outside a store write (no lookup) nobody is.
func formerMember(data *domain.Bootstrap, id string) bool {
	return id != "" && data != nil && data.FormerMembers != nil && data.FormerMembers()[id]
}

// formerMemberOutsideWrite reports, for a request check that resolves people
// against the viewer's directory (which leaves removed people out), whether
// id names a former member: the check lets it through so the write refuses it
// with errFormerMember instead of a 403 that reads as a permission problem.
func (s *server) formerMemberOutsideWrite(ctx context.Context, workspace, id string) bool {
	if id == "" {
		return false
	}
	former, err := s.store.FormerMembers(ctx, workspace)
	return err == nil && former[id]
}

// refuseNewFormerMembers refuses ids that name a former member, except the
// ones already referenced (kept): a write that leaves an existing reference
// as it is never fails for it.
func refuseNewFormerMembers(data *domain.Bootstrap, kept []string, ids ...string) error {
	for _, id := range ids {
		if id != "" && !slices.Contains(kept, id) && formerMember(data, id) {
			return errFormerMember
		}
	}
	return nil
}

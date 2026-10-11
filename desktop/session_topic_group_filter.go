package main

import (
	"deepzora/desktop/internal/workspacestate"
	"fmt"
)

// Group membership comes from the same immutable organization as the canonical
// rows. A group contains explicit session keys after preference import, so
// sessions sharing a topic must not inherit each other's membership.
func applyOrganizationGroupFilter(req *ProjectTopicPageRequest, org workspacestate.Organization) error {
	req.groupInclude, req.groupExclude = nil, nil
	req.groupIncludeJSON, req.groupExcludeJSON = "", ""
	req.groupAll = organizationSnapshot(org, true).Groups
	req.groupSelected = nil
	if req.GroupFilter == "group" {
		for i := range req.groupAll {
			if req.groupAll[i].ID == req.GroupID {
				req.groupSelected = &req.groupAll[i]
				return nil
			}
		}
		return fmt.Errorf("session group no longer exists")
	}
	return nil
}

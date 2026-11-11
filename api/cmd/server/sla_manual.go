package main

import (
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func manualSLARule(sla domain.IssueSLA) domain.SLARule {
	return domain.SLARule{ID: sla.RuleID, Enabled: true, TargetMinutes: sla.TargetMinutes, BusinessHours: sla.BusinessHours, StartMode: sla.StartMode}
}

func applyManualSLADeadline(data *domain.Bootstrap, issue *domain.Issue, now time.Time) {
	index := -1
	for candidate := range data.IssueSLAs {
		sla := &data.IssueSLAs[candidate]
		if sla.IssueID == issue.ID && (sla.Status != "removed" || sla.Manual) {
			index = candidate
		}
	}
	created := index < 0
	if created {
		ruleID := ""
		if rule := slaWinner(data.SLARules, *issue); rule != nil {
			ruleID = rule.ID
		}
		data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: idgen.New("issue_sla_"), IssueID: issue.ID, RuleID: ruleID, StartedAt: now, StartMode: "ruleMatch"})
		index = len(data.IssueSLAs) - 1
	}
	for other := range data.IssueSLAs {
		if other != index && data.IssueSLAs[other].IssueID == issue.ID {
			data.IssueSLAs[other].Status, data.IssueSLAs[other].Manual = "removed", false
		}
	}
	sla := &data.IssueSLAs[index]
	before := *sla
	sla.Manual = true
	if issue.SLABreachesAt == nil {
		sla.Status = "removed"
		if before.Status != "removed" && !created {
			recordSLAChange(data, *issue, manualSLARule(before), before, "removed", now, nil)
		}
		return
	}
	sla.StartedAt, sla.DueAt, sla.StartMode = now, *issue.SLABreachesAt, "ruleMatch"
	sla.BreachedAt, sla.CompletedAt, sla.PausedAt = nil, nil, nil
	sla.PausedMinutes, sla.Status = 0, "active"
	sla.BusinessHours = issue.SLAType == "onlyBusinessDays"
	sla.TargetMinutes = max(1, int(sla.DueAt.Sub(now)/time.Minute))
	sla.RemainingMinutes = sla.TargetMinutes
	rule := manualSLARule(*sla)
	refreshSLARiskThresholds(data, *issue, rule, sla)
	kind := "changed"
	if created || before.Status == "removed" {
		kind = "started"
	}
	recordSLAChange(data, *issue, rule, *sla, kind, now, &before)
}

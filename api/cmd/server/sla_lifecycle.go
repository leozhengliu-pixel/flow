package main

import (
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func advanceIssueSLA(data *domain.Bootstrap, issue domain.Issue, rule domain.SLARule, sla *domain.IssueSLA, now time.Time) {
	modeChanged := normalizedSLAStartMode(sla.StartMode) != normalizedSLAStartMode(rule.StartMode)
	if modeChanged || sla.TargetMinutes > 0 && (sla.TargetMinutes != rule.TargetMinutes || sla.BusinessHours != rule.BusinessHours) {
		before := *sla
		if modeChanged {
			sla.StartedAt = slaStartedAt(issue, rule, now)
			sla.StartMode = normalizedSLAStartMode(rule.StartMode)
		}
		sla.DueAt = slaDeadline(data, issue, rule, sla.StartedAt, rule.TargetMinutes).Add(time.Duration(sla.PausedMinutes) * time.Minute)
		sla.RemainingMinutes += rule.TargetMinutes - sla.TargetMinutes
		sla.TargetMinutes, sla.BusinessHours = rule.TargetMinutes, rule.BusinessHours
		if now.Before(sla.DueAt) {
			sla.BreachedAt = nil
			if sla.Status == "breached" {
				sla.Status = "active"
			}
		}
		recordSLAChange(data, issue, rule, *sla, "changed", now, &before)
	} else if sla.TargetMinutes == 0 {
		sla.TargetMinutes, sla.BusinessHours = rule.TargetMinutes, rule.BusinessHours
	}
	sla.StartMode = normalizedSLAStartMode(rule.StartMode)
	refreshSLARiskThresholds(data, issue, rule, sla)
	terminal := issue.State.Type == "completed"
	if sla.CompletedAt != nil {
		if terminal {
			if sla.CompletedAt.Before(sla.DueAt) {
				sla.BreachedAt = nil
			} else if sla.BreachedAt == nil {
				sla.BreachedAt = sla.CompletedAt
			}
			return
		}
		sla.CompletedAt, sla.Status = nil, "active"
		if sla.BreachedAt != nil {
			sla.Status = "breached"
		}
		recordSLAEvent(data, issue.ID, sla.ID, "resumed", now)
	}

	paused := slices.Contains(rule.PauseStatuses, issue.State.ID) || slices.Contains(rule.PauseStatuses, issue.State.Type)
	if !paused && sla.PausedAt != nil {
		duration := max(time.Duration(0), now.Sub(*sla.PausedAt))
		sla.PausedMinutes += int(duration / time.Minute)
		if rule.BusinessHours {
			sla.DueAt = slaDeadline(data, issue, rule, now, sla.RemainingMinutes)
		} else {
			sla.DueAt = sla.DueAt.Add(duration)
		}
		sla.PausedAt, sla.Status = nil, "active"
		if sla.BreachedAt != nil {
			sla.Status = "breached"
		}
		recordSLAEvent(data, issue.ID, sla.ID, "resumed", now)
	}
	refreshSLARiskThresholds(data, issue, rule, sla)
	if sla.PausedAt == nil {
		sla.RemainingMinutes = int(sla.DueAt.Sub(now) / time.Minute)
		if rule.BusinessHours {
			sla.RemainingMinutes = businessMinutes(now, sla.DueAt, slaTimezone(data, issue), slaWorkWeek(data))
		}
		if !now.Before(sla.DueAt) && sla.BreachedAt == nil {
			sla.BreachedAt, sla.Status = &now, "breached"
			recordSLAChange(data, issue, rule, *sla, "breached", now, nil)
		} else if !terminal && !paused && sla.BreachedAt == nil && !now.Before(*sla.HighRiskAt) && !slaEventRecorded(data, sla.ID, "high_risk") {
			recordSLAChange(data, issue, rule, *sla, "high_risk", now, nil)
		}
	}
	if terminal {
		sla.CompletedAt, sla.PausedAt, sla.Status = &now, nil, "completed"
		recordSLAEvent(data, issue.ID, sla.ID, "completed", now)
	} else if paused && sla.PausedAt == nil {
		sla.PausedAt, sla.Status = &now, "paused"
		recordSLAEvent(data, issue.ID, sla.ID, "paused", now)
	}
}

func normalizedSLAStartMode(mode string) string {
	if mode == "issueCreation" {
		return mode
	}
	return "ruleMatch"
}

func slaStartedAt(issue domain.Issue, rule domain.SLARule, now time.Time) time.Time {
	if rule.StartMode == "issueCreation" && !issue.CreatedAt.IsZero() && !issue.CreatedAt.After(now) {
		return issue.CreatedAt
	}
	return now
}

func refreshSLARiskThresholds(data *domain.Bootstrap, issue domain.Issue, rule domain.SLARule, sla *domain.IssueSLA) {
	high, medium := sla.DueAt.Add(-24*time.Hour), sla.DueAt.Add(-168*time.Hour)
	if rule.BusinessHours {
		zone, week := slaTimezone(data, issue), slaWorkWeek(data)
		high = businessDayOffset(sla.DueAt, -1, zone, week).UTC()
		medium = businessDayOffset(sla.DueAt, -7, zone, week).UTC()
	}
	sla.HighRiskAt, sla.MediumRiskAt = &high, &medium
}

func retimeBusinessSLAs(data *domain.Bootstrap, issue domain.Issue, now time.Time) {
	for index := range data.IssueSLAs {
		sla := &data.IssueSLAs[index]
		if sla.IssueID != issue.ID || sla.Status == "removed" {
			continue
		}
		ruleIndex := slices.IndexFunc(data.SLARules, func(rule domain.SLARule) bool { return rule.ID == sla.RuleID && rule.BusinessHours })
		if ruleIndex < 0 {
			continue
		}
		rule, before := data.SLARules[ruleIndex], *sla
		sla.DueAt = slaDeadline(data, issue, rule, sla.StartedAt, rule.TargetMinutes).Add(time.Duration(sla.PausedMinutes) * time.Minute)
		if !before.DueAt.Equal(sla.DueAt) {
			recordSLAChange(data, issue, rule, *sla, "changed", now, &before)
		}
	}
}

func slaEventRecorded(data *domain.Bootstrap, slaID, kind string) bool {
	return slices.ContainsFunc(data.SLAEvents, func(event domain.SLAEvent) bool { return event.SLAID == slaID && event.Type == kind })
}

func recordSLAChange(data *domain.Bootstrap, issue domain.Issue, rule domain.SLARule, sla domain.IssueSLA, kind string, now time.Time, before *domain.IssueSLA) {
	recordSLAEvent(data, issue.ID, sla.ID, kind, now)
	metadata := map[string]string{"slaId": sla.ID, "ruleId": rule.ID, "targetMinutes": strconv.Itoa(rule.TargetMinutes), "businessHours": strconv.FormatBool(rule.BusinessHours), "dueAt": sla.DueAt.UTC().Format(time.RFC3339)}
	metadata["manual"] = strconv.FormatBool(sla.Manual)
	if before != nil {
		metadata["previousDueAt"] = before.DueAt.UTC().Format(time.RFC3339)
		metadata["previousTargetMinutes"] = strconv.Itoa(before.TargetMinutes)
	}
	activity := domain.ActivityEvent{ID: idgen.New("activity_"), Type: "issue.sla_" + kind, Actor: domain.User{ID: "sla-automation", Name: "Flow", DisplayName: "Flow"}, Metadata: metadata, CreatedAt: now}
	if data.Activities == nil {
		data.Activities = map[string][]domain.ActivityEvent{}
	}
	data.Activities[issue.ID] = append(data.Activities[issue.ID], activity)
	if kind == "breached" || kind == "high_risk" {
		appendIssueNotifications(data, issue, activity, nil)
	}
}

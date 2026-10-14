package main

import (
	"context"
	"html/template"
	"log"
	"net/url"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// applicationInstalledMessage tells the administrator who installed an OAuth application (actor=app) in a
// workspace what it can now reach, with a link to the workspace's applications.
func (m *smtpMailer) applicationInstalledMessage(to, appName string, workspace domain.Workspace, teamNames []string, allPublicTeams bool) (mailMessage, error) {
	appName, workspaceName := headerText(appName), headerText(workspace.Name)
	subject := `The "` + appName + `" application has been installed in your workspace`
	applicationsURL := m.appURL + "/" + url.PathEscape(workspace.URLKey) + "/settings/applications"
	access, accessHTML := "The application's user has access to all public teams.", emailHTML("The application's user has access to all public teams.")
	if !allPublicTeams {
		names := make([]template.HTML, 0, len(teamNames))
		for _, name := range teamNames {
			names = append(names, emailStrong(headerText(name)))
		}
		switch len(names) {
		case 0:
			access, accessHTML = "The application's user has no team access yet.", emailHTML("The application's user has no team access yet.")
		case 1:
			access, accessHTML = "The application's user has access to the "+headerText(teamNames[0])+" team.", emailHTML("The application's user has access to the %s team.", names[0])
		default:
			joined := make([]string, len(names))
			for index, name := range names {
				joined[index] = string(name)
			}
			plain := make([]string, len(teamNames))
			for index, name := range teamNames {
				plain[index] = headerText(name)
			}
			access, accessHTML = "The application's user has access to these teams: "+strings.Join(plain, ", ")+".", emailHTML("The application's user has access to these teams: %s.", template.HTML(strings.Join(joined, ", ")))
		}
	}
	summary := `The "` + appName + `" application now has access to your ` + workspaceName + ` workspace. ` + access
	text := summary + " \nTo view all applications you have authorized visit your settings at: " + applicationsURL + "\n"
	html, err := m.renderEmail(emailLayout{
		Title:     subject,
		Preheader: summary,
		Heading:   emailHTML("%s", subject),
		Blocks: []emailBlock{
			{Kind: "p", HTML: emailHTML("The %s application now has access to your %s workspace.", emailStrong(appName), workspaceName)},
			{Kind: "p", HTML: accessHTML},
			{Kind: "button", Label: "View authorized applications", URL: applicationsURL},
		},
	})
	if err != nil {
		return mailMessage{}, err
	}
	return mailMessage{To: to, Subject: subject, Text: text, HTML: html}, nil
}

// applicationRevokedMessage tells an OAuth application's creator that the application was deactivated and every
// token it held was revoked.
func (m *smtpMailer) applicationRevokedMessage(to, appName, workspaceName string) (mailMessage, error) {
	appName, workspaceName = headerText(appName), headerText(workspaceName)
	subject := `All OAuth tokens for the application "` + appName + `" were revoked`
	summary := `The OAuth application "` + appName + `" in your ` + workspaceName + ` workspace was deactivated and all tokens were revoked.`
	html, err := m.renderEmail(emailLayout{
		Title:     subject,
		Preheader: summary,
		Heading:   emailHTML("An OAuth application on your account has had its tokens revoked and been archived"),
		Blocks: []emailBlock{
			{Kind: "p", HTML: emailHTML("The OAuth application %s in your %s workspace was deactivated and all tokens were revoked.", emailStrong(appName), workspaceName)},
		},
	})
	if err != nil {
		return mailMessage{}, err
	}
	return mailMessage{To: to, Subject: subject, Text: summary + "\n", HTML: html}, nil
}

// notifyApplicationInstalled emails installer after an application's first (or renewed) installation in workspace.
// Delivery failures are logged: the installation itself has succeeded.
func (s *server) notifyApplicationInstalled(workspace domain.Bootstrap, installer domain.User, appName string, teamIDs []string, allPublicTeams bool) {
	if s.mailer == nil || installer.App || strings.TrimSpace(installer.Email) == "" {
		return
	}
	// The consent request carries only the access check's slice of the workspace; names come from its metadata.
	if metadata, ok := s.store.WorkspaceMetadata(workspace.Workspace.URLKey); ok {
		workspace = metadata
	}
	names := []string{}
	for _, team := range workspace.Teams {
		if slices.Contains(teamIDs, team.ID) {
			names = append(names, team.Name)
		}
	}
	message, err := s.mailer.applicationInstalledMessage(installer.Email, appName, workspace.Workspace, names, allPublicTeams)
	if err == nil {
		err = s.mailer.deliver(message)
	}
	if err != nil {
		log.Printf("Application installed email workspace=%s: %v", workspace.Workspace.URLKey, err)
	}
}

// notifyApplicationRevoked emails the creator of a deleted OAuth application.
func (s *server) notifyApplicationRevoked(ctx context.Context, app domain.OAuthApplication, workspaceName string) {
	if s.mailer == nil || app.CreatorID == "" {
		return
	}
	creator, err := s.store.UserByID(ctx, app.CreatorID)
	if err != nil || creator.App || strings.TrimSpace(creator.Email) == "" {
		return
	}
	message, err := s.mailer.applicationRevokedMessage(creator.Email, app.Name, workspaceName)
	if err == nil {
		err = s.mailer.deliver(message)
	}
	if err != nil {
		log.Printf("Application revoked email client=%s: %v", app.ClientID, err)
	}
}

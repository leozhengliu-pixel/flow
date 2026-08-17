package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
	"os"
	"strings"
)

type smtpMailer struct {
	host, port, username, password, from, appURL string
}

func smtpMailerFromEnv() *smtpMailer {
	host := strings.TrimSpace(os.Getenv("FLOW_SMTP_HOST"))
	from := strings.TrimSpace(os.Getenv("FLOW_SMTP_FROM"))
	if host == "" || from == "" {
		return nil
	}
	port := strings.TrimSpace(os.Getenv("FLOW_SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	appURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), "/")
	if appURL == "" {
		appURL = "http://localhost:5173"
	}
	return &smtpMailer{host: host, port: port, username: os.Getenv("FLOW_SMTP_USERNAME"), password: os.Getenv("FLOW_SMTP_PASSWORD"), from: from, appURL: appURL}
}

func (m *smtpMailer) sendVerification(email, token string) error {
	return m.send(email, "Verify your email", "Verify your email address to finish creating your account.", m.appURL+"/verify-email?token="+url.QueryEscape(token)+"&email="+url.QueryEscape(email))
}

func (m *smtpMailer) sendPasswordReset(email, token string) error {
	return m.send(email, "Reset your password", "Use this link to choose a new password. It expires in one hour.", m.appURL+"/reset-password?token="+url.QueryEscape(token))
}

func (m *smtpMailer) sendInvitation(email, workspace, token string) error {
	return m.send(email, "Join "+workspace, "You have been invited to join "+workspace+".", m.appURL+"/invite/"+url.PathEscape(token))
}

func (m *smtpMailer) sendNotification(email, workspaceKey, identifier, title, body string) error {
	subject := identifier + " " + title
	link := m.appURL + "/" + url.PathEscape(workspaceKey) + "/issue/" + url.PathEscape(identifier)
	return m.send(email, subject, body, link)
}

func (m *smtpMailer) send(to, subject, body, link string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return fmt.Errorf("invalid email header")
	}
	address := net.JoinHostPort(m.host, m.port)
	message := []byte("From: " + m.from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body + "\r\n\r\n" + link + "\r\n")
	var auth smtp.Auth
	if m.username != "" {
		auth = smtp.PlainAuth("", m.username, m.password, m.host)
	}
	if m.port != "465" {
		return smtp.SendMail(address, auth, m.from, []string{to}, message)
	}
	connection, err := tls.Dial("tcp", address, &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, m.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(m.from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

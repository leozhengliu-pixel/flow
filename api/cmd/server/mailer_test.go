package main

import (
	"strings"
	"testing"
)

func TestSMTPMailerConfigurationRequiresHostAndSender(t *testing.T) {
	t.Setenv("FLOW_SMTP_HOST", "")
	t.Setenv("FLOW_SMTP_FROM", "")
	if smtpMailerFromEnv() != nil {
		t.Fatal("mailer configured without host or sender")
	}
	t.Setenv("FLOW_SMTP_HOST", "smtp.example.test")
	t.Setenv("FLOW_SMTP_FROM", "flow@example.test")
	t.Setenv("FLOW_SMTP_PORT", "")
	t.Setenv("FLOW_APP_URL", "https://flow.example.test/")
	mailer := smtpMailerFromEnv()
	if mailer == nil || mailer.port != "587" || mailer.appURL != "https://flow.example.test" {
		t.Fatalf("mailer=%#v", mailer)
	}
}

func TestSMTPMailerRejectsHeaderInjectionBeforeConnecting(t *testing.T) {
	mailer := &smtpMailer{host: "127.0.0.1", port: "1", from: "flow@example.test"}
	if err := mailer.send("victim@example.test\r\nBcc: attacker@example.test", "Subject", "Body", "https://example.test"); err == nil || !strings.Contains(err.Error(), "invalid email header") {
		t.Fatalf("header injection error=%v", err)
	}
	if err := mailer.send("victim@example.test", "Subject\nBcc: attacker@example.test", "Body", "https://example.test"); err == nil {
		t.Fatal("subject header injection was accepted")
	}
}

package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func integrationWebhookCredentials(connection domain.IntegrationConnection) store.IntegrationWebhookCredentials {
	return store.IntegrationWebhookCredentials{
		ID: connection.ID, WebhookSecret: connection.Config["webhookSecret"],
		WebhookSecretEnv: connection.Config["webhookSecretEnv"], SigningSecret: connection.Config["signingSecret"],
		SigningSecretEnv: connection.Config["signingSecretEnv"], Username: connection.Config["username"],
	}
}

func integrationWebhookConnection(credentials *store.IntegrationWebhookCredentials) *domain.IntegrationConnection {
	if credentials == nil {
		return nil
	}
	return &domain.IntegrationConnection{ID: credentials.ID, Config: map[string]string{
		"webhookSecret": credentials.WebhookSecret, "webhookSecretEnv": credentials.WebhookSecretEnv,
		"signingSecret": credentials.SigningSecret, "signingSecretEnv": credentials.SigningSecretEnv,
		"username": credentials.Username,
	}}
}

package main

// Track D's GitHub App webhook (github_app.go) calls codingWebhookDispatch
// once per linked workspace after its CodeReview sync; track L supplies it.
func init() {
	codingWebhookDispatch = (*server).dispatchCodingWebhook
}

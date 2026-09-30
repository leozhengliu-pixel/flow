package main

import (
	"net/url"
	"strings"
)

// resourceBrandNames mirrors BRAND_NAMES in
// web/src/components/project-detail/project-resource-link-name.ts.
var resourceBrandNames = []struct{ domain, name string }{
	{"github.com", "GitHub"},
	{"gitlab.com", "GitLab"},
	{"slack.com", "Slack"},
	{"atlassian.net", "Jira"},
	{"figma.com", "Figma"},
	{"notion.so", "Notion"},
	{"notion.site", "Notion"},
	{"docs.google.com", "Google Docs"},
	{"drive.google.com", "Google Drive"},
	{"youtube.com", "YouTube"},
	{"loom.com", "Loom"},
	{"miro.com", "Miro"},
	{"linear.app", "Linear"},
	{"vercel.com", "Vercel"},
	{"sentry.io", "Sentry"},
}

// resourceLinkName names an untitled link after its site ("GitHub",
// "Example") the way Linear does, instead of storing the whole URL as the
// title. It matches resourceLinkName on the web client.
func resourceLinkName(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return rawURL
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	for _, brand := range resourceBrandNames {
		if host == brand.domain || strings.HasSuffix(host, "."+brand.domain) {
			return brand.name
		}
	}
	parts := strings.Split(host, ".")
	label := host
	if len(parts) >= 2 {
		label = parts[len(parts)-2]
	}
	if label == "" {
		return rawURL
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW48CountingReader struct {
	reader io.Reader
	read   int
}

func (r *scaleP21SW48CountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

type scaleP21SW48FailReader struct{}

func (scaleP21SW48FailReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestScaleP21SW48ChatLanguagePrefixDifferential(t *testing.T) {
	// No non-ASCII rune lowercases to a character in the three classifiers.
	// Thus eight input bytes distinguish the longest six-byte keyword from
	// a longer value without decoding or retaining the rest of the string.
	for r := rune(128); r <= unicode.MaxRune; r++ {
		if strings.ContainsRune("zhsytemauo", unicode.ToLower(r)) {
			t.Fatalf("classifier requires a longer Unicode prefix: %U", r)
		}
	}
	languages := []string{
		"", "system", "SYSTEM", "auto", "Auto", "zh", "ZH-cn", "en", "fr",
		"syst\x00em", "system\x00", "auto\x00", "zh\x00tail", "\x00zh",
		"系统", "İYSTEM", "ſystem", "\xffsystem", "system\xff", "zh\xff",
		"system" + strings.Repeat("x", 3<<20), "ZH" + strings.Repeat("界", 1<<20),
	}
	for _, language := range languages {
		for _, provider := range []string{"feishu", "lark", "unknown", ""} {
			input := &scaleP21SW48CountingReader{reader: strings.NewReader(language)}
			got, err := chatReadLanguagePrefix(input, "native\x00key", provider)
			want := chatLanguage(domain.Bootstrap{UserSettings: map[string]domain.UserSettings{
				"native\x00key": {Language: language},
			}}, "native\x00key", provider)
			if err != nil || got != want || input.read > 8 {
				t.Fatalf("bytes=%d provider=%q got=%q want=%q read=%d err=%v", len(language), provider, got, want, input.read, err)
			}
		}
	}
	if _, err := chatReadLanguagePrefix(scaleP21SW48FailReader{}, "", "feishu"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("reader failure was hidden", err)
	}
}

func TestScaleP21SW48ChatLanguageInstalledGeneration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, nativeKey := f.data.Workspace.URLKey, "sw48-native\x00key"
		seed := func(language string) {
			t.Helper()
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw48.language", "", nil, func(data *domain.Bootstrap) error {
				data.UserSettings[nativeKey] = domain.UserSettings{
					UserID: "different-inner-user-id", Language: language,
					CodingPromptTemplate: strings.Repeat("legal prompt ", 300000),
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		check := func(phase string) {
			t.Helper()
			for _, key := range []string{nativeKey, "different-inner-user-id", "sw48-nativekey", "missing"} {
				for _, provider := range []string{"feishu", "lark"} {
					old, err := (&server{store: repo}).chatReadLanguage(t.Context(), workspace, key, provider)
					if err != nil {
						t.Fatal(err)
					}
					queries := 0
					ctx := store.WithSQLStatementObserver(t.Context(), func(string) { queries++ })
					got, err := (&server{store: f.probe}).chatReadLanguage(ctx, workspace, key, provider)
					if err != nil || got != old || queries != 0 {
						t.Fatalf("%s key=%q provider=%q got=%q old=%q canonicalSQL=%d err=%v", phase, key, provider, got, old, queries, err)
					}
				}
			}
		}
		seed("syst\x00em")
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		check("warm-native-key-and-value")
		warm, err := (&server{store: f.probe}).chatReadLanguage(t.Context(), workspace, nativeKey, "feishu")
		if err != nil || warm != "en" {
			t.Fatal("warm unsanitized value was lost", warm, err)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		check("reloaded")
		seed("zh" + strings.Repeat("large-language-tail", 200000))
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		check("mixed-replica-refreshed")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (&server{store: f.probe}).chatReadLanguage(ctx, workspace, nativeKey, "feishu"); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled lookup was not cancelled", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded language reader decoded full metadata")
		}
	}
}

func TestScaleP21SW48ChatNotificationLanguagePermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer),
			"SW48 foreign", idgen.New("sw48-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := repo.MutateWorkspace(t.Context(), foreign.Workspace.URLKey, "scale_p21_sw48.foreign", "", nil, func(data *domain.Bootstrap) error {
			for _, principal := range f.principals {
				data.UserSettings[principal.user.ID] = domain.UserSettings{Language: "zh"}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw48.languages", "", nil, func(data *domain.Bootstrap) error {
			for _, principal := range f.principals {
				data.UserSettings[principal.user.ID] = domain.UserSettings{
					UserID: principal.user.ID, Language: "syst\x00em",
					CodingPromptTemplate: strings.Repeat("large private prompt ", 15000),
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		check := func(t *testing.T, user domain.User, key *domain.APIKey, workspace string) {
			t.Helper()
			request := f.literalRequest(user, key, nil)
			provider := "feishu"
			if workspace == foreign.Workspace.URLKey {
				provider = "lark"
			}
			var cards []map[string]any
			var counts []int
			for _, current := range []*store.SQLiteStore{repo, f.probe} {
				queries := 0
				ctx := store.WithSQLStatementObserver(request.Context(), func(string) { queries++ })
				card, actions, err := (&server{store: current}).chatPersonalNotificationCard(ctx,
					domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: provider},
					domain.ChatIdentity{UserID: user.ID},
					chatPersonalJob{UserID: user.ID, NotificationID: "sw48-missing-notification"},
					notificationEmailItem{Title: "Notification", Line: "Private references stay upstream", URL: "https://example.test/old-slug"},
					domain.ChatJob{},
				)
				if err != nil || len(actions) != 0 {
					t.Fatal("plain fallback changed", err, actions)
				}
				cards = append(cards, card)
				counts = append(counts, queries)
			}
			if !reflect.DeepEqual(cards[0], cards[1]) || counts[1] > counts[0] {
				t.Fatalf("trusted language projection changed: cards=%v SQL=%v", cards, counts)
			}
			t.Logf("plain notification SQL old=%d bounded=%d", counts[0], counts[1])
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			check(t, user, key, workspace)
			check(t, user, key, foreign.Workspace.URLKey)
			check(t, user, key, "sw48-missing-"+workspace)
		})
		member := f.principals[len(f.principals)-1].user
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued-revocation", "replay-revocation"} {
			t.Run(phase, func(t *testing.T) { check(t, member, nil, workspace) })
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded notification language reader decoded full metadata")
		}
	}
}

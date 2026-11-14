package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW15IntakeRequest(f scaleP21M2Fixture, user domain.User, key *domain.APIKey, team, address string, body any) *http.Request {
	r := f.request(user, key, body)
	r.URL.Path = "/api/teams/" + team + "/email-intake-addresses"
	r.SetPathValue("id", team)
	r.SetPathValue("addressId", address)
	return r
}

func TestScaleP21SW15EmailIntakeLegacySourceMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		addressID := idgen.New("email_intake_sw15_")
		stamp := time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("wire-zone", 3661))
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw15.intake_fixture", "", nil, func(data *domain.Bootstrap) error {
			data.EmailIntakeAddresses = []domain.EmailIntakeAddress{{
				ID: addressID, TeamID: f.public, LocalPart: "help", Domain: "intake.example.test", Address: "help@intake.example.test",
				InboundTokenHash: "private-current-token", VerificationToken: "verification", VerificationState: "verified",
				VerifiedAt: &stamp, Aliases: []domain.EmailIntakeAlias{{Address: "old@example.test", TokenHash: "private-old-token", ExpiresAt: stamp}},
				Enabled: true, Type: "asks", System: true, TemplateID: "hidden-template-reference", SenderName: "Help<&>\x00",
				ForwardingEmailAddress: "help@sender.example.test", ForwardingDomainVerified: &stamp, CustomerRequestsEnabled: true,
				CreatedAt: stamp, UpdatedAt: stamp,
			}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		service := &server{store: repo}
		var expected []byte
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			r := scaleP21SW15IntakeRequest(f, user, key, f.public, addressID, nil)
			r.Method = http.MethodGet
			w := httptest.NewRecorder()
			service.listEmailIntakeAddresses(w, r)
			var result []emailIntakeAddressView
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 1 {
				t.Fatal("legacy active workspace principal lost unfiltered intake directory", w.Code, w.Body.String())
			}
			if result[0].InboundTokenHash != "" || result[0].Aliases[0].TokenHash != "" || result[0].TemplateID != "hidden-template-reference" {
				t.Fatal("legacy token redaction/reference semantics changed")
			}
			if expected == nil {
				expected = append([]byte(nil), w.Body.Bytes()...)
			} else if string(expected) != w.Body.String() {
				t.Fatal("legacy directory unexpectedly intersects key/team content scope")
			}
		})
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := scaleP21SW15IntakeRequest(f, f.data.Viewer, nil, f.public, addressID, nil)
		r.Method = http.MethodGet
		(&server{store: probe, authDisabled: true}).listEmailIntakeAddresses(w, r)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatal("cold installed fixture no longer demonstrates missing email source", w.Code, w.Body.String())
		}
		owner := httptest.NewRecorder()
		(&server{store: repo, authDisabled: true}).listEmailIntakeAddresses(owner, r)
		if strings.TrimSpace(owner.Body.String()) == "[]" {
			t.Fatal("owner fixture unexpectedly dropped installed email source")
		}
		allocations := testing.AllocsPerRun(3, func() {
			response := httptest.NewRecorder()
			service.listEmailIntakeAddresses(response, r.Clone(r.Context()))
			if response.Code != http.StatusOK {
				t.Fatal(response.Code)
			}
		})
		t.Logf("legacy list baseline allocations/op=%.0f with M2 unrelated body/settings fixture; no migrated-path gain claimed", allocations)
	}
}

func TestScaleP21SW15EmailIntakeLegacyAggregateClosure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		addressID := idgen.New("email_intake_sw15_")
		stamp := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
		for _, operation := range []struct {
			name string
			body any
			call func(*server, http.ResponseWriter, *http.Request)
			want int
		}{
			{"update", map[string]any{"senderName": "Changed"}, (*server).updateEmailIntakeAddress, http.StatusOK},
			{"verify", map[string]any{"txtValue": "flow-verification=sw15-token"}, (*server).verifyEmailIntakeAddress, http.StatusOK},
			{"rotate", nil, (*server).rotateEmailIntakeAddress, http.StatusOK},
			{"delete", nil, (*server).deleteEmailIntakeAddress, http.StatusNoContent},
		} {
			t.Run(operation.name, func(t *testing.T) {
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw15.intake_reset", "", nil, func(data *domain.Bootstrap) error {
					data.Documents = []domain.Document{{
						ID: idgen.New("doc_sw15_"), Title: "Earlier aggregate reference", Creator: f.data.Viewer,
						ContentData: map[string]any{"id": addressID, "marker": "earlier-typed-document-map", "hiddenCount": 37},
					}}
					data.EmailIntakeAddresses = []domain.EmailIntakeAddress{{
						ID: addressID, TeamID: f.public, LocalPart: "help", Domain: "intake.example.test", Address: "help@intake.example.test",
						InboundTokenHash: "hash", VerificationToken: "sw15-token", VerificationState: "pending", Aliases: []domain.EmailIntakeAlias{},
						Enabled: true, System: true, SenderName: "Before", ForwardingEmailAddress: "help@sender.example.test", CreatedAt: stamp, UpdatedAt: stamp,
					}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var event domain.DomainEvent
				var realtime domain.RealtimeEvent
				repo.SetWebhookSink(func(_ string, next domain.DomainEvent) { event = next })
				repo.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { realtime = next })
				r := scaleP21SW15IntakeRequest(f, f.data.Viewer, nil, f.public, addressID, operation.body)
				w := httptest.NewRecorder()
				operation.call(&server{store: repo, authDisabled: true}, w, r)
				if w.Code != operation.want {
					t.Fatal("legacy operation fixture failed", w.Code, w.Body.String())
				}
				var payload map[string]any
				if json.Unmarshal(realtime.Payload, &payload) != nil || event.AggregateID != addressID {
					t.Fatal("missing legacy effects", event, string(realtime.Payload))
				}
				want := map[string]any{"id": addressID, "marker": "earlier-typed-document-map", "hiddenCount": float64(37)}
				if !reflect.DeepEqual(payload["entity"], want) || len(event.PreviousValues) != 0 {
					t.Fatal("unknown-prefix aggregate no longer selects earlier nested object", payload, string(event.PreviousValues))
				}
			})
		}
	}
}

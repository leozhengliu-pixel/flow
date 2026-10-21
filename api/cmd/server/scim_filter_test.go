package main

import (
	"errors"
	"testing"
)

func scimFilterFixture() map[string]any {
	return map[string]any{
		"schemas":     []any{scimSchemaUser},
		"id":          "usr_1",
		"externalId":  "Ext-42",
		"userName":    "Ada.Lovelace@Example.com",
		"displayName": "Ada Lovelace",
		"active":      true,
		"name":        map[string]any{"givenName": "Ada", "familyName": "Lovelace"},
		"emails": []any{
			map[string]any{"value": "ada@example.com", "type": "work", "primary": true},
			map[string]any{"value": "ada@home.test", "type": "home"},
		},
		"members": []any{map[string]any{"value": "usr_9"}},
		"meta":    map[string]any{"created": "2026-01-02T03:04:05Z", "lastModified": "2026-03-04T05:06:07Z"},
	}
}

func TestSCIMFilterOperatorsAndCaseRules(t *testing.T) {
	resource := scimFilterFixture()
	cases := map[string]bool{
		`userName eq "ada.lovelace@example.com"`:                                            true, // userName is case-insensitive
		`USERNAME EQ "ADA.LOVELACE@EXAMPLE.COM"`:                                            true, // attribute names and operators too
		`urn:ietf:params:scim:schemas:core:2.0:User:userName eq "ada.lovelace@example.com"`: true,
		`externalId eq "Ext-42"`:                                                            true,
		`externalId eq "ext-42"`:                                                            false, // externalId is caseExact
		`id eq "USR_1"`:                                                                     false,
		`userName ne "someone@example.com"`:                                                 true,
		`displayName co "love"`:                                                             true,
		`displayName sw "ada"`:                                                              true,
		`displayName ew "LACE"`:                                                             true,
		`displayName sw "lace"`:                                                             false,
		`title pr`:                                                                          false,
		`name.givenName pr`:                                                                 true,
		`emails pr`:                                                                         true,
		`active eq true`:                                                                    true,
		`active eq false`:                                                                   false,
		`active ne false`:                                                                   true,
		`emails.value eq "ADA@home.test"`:                                                   true,
		`emails[type eq "work"].value eq "ada@example.com"`:                                 true,
		`emails[type eq "work"].value eq "ada@home.test"`:                                   false,
		`emails[type eq "work" and value co "example"]`:                                     true,
		`emails[type eq "other"]`:                                                           false,
		`emails co "home.test"`:                                                             true,
		`members[value eq "usr_9"]`:                                                         true,
		`members.value eq "usr_9"`:                                                          true,
		`meta.lastModified gt "2026-03-01T00:00:00Z"`:                                       true,
		`meta.lastModified lt "2026-03-01T00:00:00Z"`:                                       false,
		`meta.created ge "2026-01-02T03:04:05Z"`:                                            true,
		`meta.created le "2026-01-02T03:04:04Z"`:                                            false,
		`userName gt "a"`:                                                                   true,
		`userName lt "a"`:                                                                   false,
		`title eq null`:                                                                     true,
		`userName eq "x" or displayName sw "Ada"`:                                           true,
		`userName eq "x" and displayName sw "Ada"`:                                          false,
		`not (userName eq "x")`:                                                             true,
		`not(active eq true)`:                                                               false,
		`(userName eq "x" or active eq true) and (name.familyName eq "lovelace")`: true,
		// and binds tighter than or.
		`userName eq "x" and active eq true or displayName eq "Ada Lovelace"`:              true,
		`userName eq "x" and (active eq true or displayName eq "Ada Lovelace")`:            false,
		`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:employeeNumber eq "1"`: false,
	}
	for expression, want := range cases {
		filter, err := parseSCIMFilter(expression)
		if err != nil {
			t.Fatalf("parse %q: %v", expression, err)
		}
		if got := filter.matches(resource); got != want {
			t.Errorf("%q matched=%v want %v", expression, got, want)
		}
	}
}

func TestSCIMFilterRejectsInvalidFilters(t *testing.T) {
	for _, expression := range []string{
		`userName`,
		`userName eq`,
		`userName equals "a"`,
		`userName eq unquoted`,
		`userName eq "unterminated`,
		`(userName eq "a"`,
		`userName eq "a")`,
		`userName eq "a" and`,
		`not userName eq "a"`,
		`active gt true`,
		`userName co null`,
		`emails[type eq "work"`,
		`emails[type[value eq "a"] eq "b"]`,
		`displayName co 5`,
		`1abc eq "a"`,
	} {
		_, err := parseSCIMFilter(expression)
		var filterErr scimFilterError
		if !errors.As(err, &filterErr) {
			t.Errorf("%q: err=%v, want invalidFilter", expression, err)
		}
	}
	if filter, err := parseSCIMFilter("   "); filter != nil || err != nil {
		t.Fatalf("empty filter = %v, %v", filter, err)
	}
}

func TestSCIMFilterPushdownFindsIndexedEquality(t *testing.T) {
	cases := map[string][2]string{
		`userName eq "a@b.c"`:                            {"username", "a@b.c"},
		`active eq true and externalId eq "e-1"`:         {"externalid", "e-1"},
		`EMAILS.VALUE eq "x@y.z" and displayName co "a"`: {"emails.value", "x@y.z"},
	}
	for expression, want := range cases {
		filter, _ := parseSCIMFilter(expression)
		attribute, value, ok := scimEqualityPushdown(filter, "username", "externalid", "emails.value")
		if !ok || attribute != want[0] || value != want[1] {
			t.Errorf("%q pushdown = %q %q %v, want %v", expression, attribute, value, ok, want)
		}
	}
	for _, expression := range []string{`userName eq "a" or userName eq "b"`, `userName co "a"`, `not (userName eq "a")`} {
		filter, _ := parseSCIMFilter(expression)
		if _, _, ok := scimEqualityPushdown(filter, "username"); ok {
			t.Errorf("%q must not be pushed down", expression)
		}
	}
}

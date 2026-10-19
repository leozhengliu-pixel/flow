package coding

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestParseDotEnv(t *testing.T) {
	type env = domain.CodingEnvVar
	e := func(name, value string) env { return env{Name: name, Value: value} }
	for _, test := range []struct {
		name     string
		input    string
		want     []env
		warnings int
	}{
		{name: "empty", input: "", want: nil},
		{name: "comments and blanks", input: "# comment\n\n   # indented\nA=1\n", want: []env{e("A", "1")}},
		{name: "export prefix", input: "export A=1\nexport\tB = two\nexportC=3", want: []env{e("A", "1"), e("B", "two"), e("exportC", "3")}},
		{name: "spaces around equals", input: "A = hello world  ", want: []env{e("A", "hello world")}},
		{name: "inline comment unquoted", input: "A=value # note\nB=a#b\nC=#start", want: []env{e("A", "value"), e("B", "a#b"), e("C", "")}},
		{name: "empty value", input: "A=\nB=''\nC=\"\"", want: []env{e("A", ""), e("B", ""), e("C", "")}},
		{name: "single quotes literal", input: `A='x\ny $HOME # not comment'`, want: []env{e("A", `x\ny $HOME # not comment`)}},
		{name: "double quotes escapes", input: `A="line1\nline2\t\"q\" \\ \$X \z"`, want: []env{e("A", "line1\nline2\t\"q\" \\ $X \\z")}},
		{name: "double quote with trailing comment", input: `A="v" # c`, want: []env{e("A", "v")}},
		{name: "text after quote warns", input: `A="v" junk`, want: []env{e("A", "v")}, warnings: 1},
		{name: "multiline double", input: "KEY=\"-----BEGIN-----\nabc\n-----END-----\"\nNEXT=1", want: []env{e("KEY", "-----BEGIN-----\nabc\n-----END-----"), e("NEXT", "1")}},
		{name: "multiline single", input: "A='one\ntwo'\nB=2", want: []env{e("A", "one\ntwo"), e("B", "2")}},
		{name: "backticks", input: "A=`x \"y\" 'z'`", want: []env{e("A", `x "y" 'z'`)}},
		{name: "crlf and bom", input: "\uFEFFA=1\r\nB=2\r\n", want: []env{e("A", "1"), e("B", "2")}},
		{name: "unterminated quote skipped", input: "A=\"open\nB=2", want: nil, warnings: 1},
		{name: "invalid name", input: "1A=x\nA-B=y\nOK=z", want: []env{e("OK", "z")}, warnings: 2},
		{name: "missing equals", input: "JUSTTEXT\nA=1", want: []env{e("A", "1")}, warnings: 1},
		{name: "duplicate last wins", input: "A=1\nB=2\nA=3", want: []env{e("B", "2"), e("A", "3")}, warnings: 1},
		{name: "value with equals", input: "URL=postgres://h/db?a=b&c=d", want: []env{e("URL", "postgres://h/db?a=b&c=d")}},
		{name: "variable reference kept", input: "A=${HOME}/x", want: []env{e("A", "${HOME}/x")}},
		{name: "escaped quote does not close", input: `A="a\"b"`, want: []env{e("A", `a"b`)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, warnings, err := ParseDotEnv(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v want %#v", got, test.want)
			}
			if len(warnings) != test.warnings {
				t.Fatalf("warnings %d want %d: %+v", len(warnings), test.warnings, warnings)
			}
		})
	}
}

func TestParseDotEnvLimits(t *testing.T) {
	if _, _, err := ParseDotEnv(strings.Repeat("a", EnvImportMaxBytes+1)); !errors.Is(err, ErrEnvImportTooLarge) {
		t.Fatalf("size limit: %v", err)
	}
	var builder strings.Builder
	for index := 0; index <= EnvImportMaxEntries; index++ {
		fmt.Fprintf(&builder, "K%d=1\n", index)
	}
	if _, _, err := ParseDotEnv(builder.String()); !errors.Is(err, ErrEnvImportTooLarge) {
		t.Fatalf("entry limit: %v", err)
	}
}

func TestIsSecretEnv(t *testing.T) {
	for _, test := range []struct {
		name, value string
		secret      bool
	}{
		{"STRIPE_SECRET_KEY", "sk", true},
		{"API_KEY", "x", true},
		{"GITHUB_TOKEN", "x", true},
		{"DB_PASSWORD", "x", true},
		{"PASSWORD", "x", true},
		{"MYSQL_ROOT_PASSWD", "x", true},
		{"SENTRY_DSN", "https://abc@sentry.io/1", true},
		{"CLIENT_SECRET", "x", true},
		{"DATABASE_URL", "postgres://user:pass@db:5432/app", true},
		{"REDIS_URL", "redis://:pw@localhost:6379", true},
		{"REPO_URL", "https://ghp_token@github.com/o/r", true},
		{"DATABASE_URL", "postgres://db:5432/app", false},
		{"PUBLIC_URL", "https://example.com", false},
		{"NODE_ENV", "production", false},
		{"PORT", "3000", false},
		{"KEYBOARD_LAYOUT", "us", false},
		{"MONKEY", "banana", false},
		{"OTHER", "mysql://root:secret@localhost/db", true},
	} {
		if got := IsSecretEnv(test.name, test.value); got != test.secret {
			t.Errorf("IsSecretEnv(%q,%q)=%t want %t", test.name, test.value, got, test.secret)
		}
	}
}

func TestImportDotEnvClassifies(t *testing.T) {
	result, err := ImportDotEnv("NODE_ENV=development\nAPI_KEY=abc\n# x\nDATABASE_URL=postgres://u:p@h/d\nPORT=3000\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Variables) != 2 || result.Variables[0].Name != "NODE_ENV" || result.Variables[1].Name != "PORT" {
		t.Fatalf("variables: %+v", result.Variables)
	}
	if len(result.Secrets) != 2 || result.Secrets[0].Name != "API_KEY" || result.Secrets[1].Name != "DATABASE_URL" {
		t.Fatalf("secrets: %+v", result.Secrets)
	}
	empty, _ := ImportDotEnv("")
	if empty.Variables == nil || empty.Secrets == nil {
		t.Fatal("lists must be non-nil for JSON")
	}
}

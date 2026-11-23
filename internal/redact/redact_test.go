package redact_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/redact"
)

const (
	ghp      = "ghp_abcdefghijklmnopqrstuvwxyzABCDEF0123"
	ghPAT    = "github_pat_11ABCDEFG0abcdefghijkl_mnopqrstuvwxyz"
	skAnt    = "sk-ant-api03-abcdefghijklmnopqrst_UVWXYZ-0123"
	glpat    = "glpat-abcdefghij0123456789"
	slack    = "xoxb-1234567890-abcdefghij"
	awsKey   = "AKIAIOSFODNN7EXAMPLE"
	pemBlock = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nabcdef\n-----END RSA PRIVATE KEY-----"
)

func TestRedactor_String(t *testing.T) {
	m := redact.Marker
	tests := []struct {
		name   string
		values []string
		in     string
		want   string
		count  int
	}{
		{name: "a ghp token", in: "token " + ghp + " end", want: "token " + m + " end", count: 1},
		{name: "a gho token", in: "gho_" + ghp[4:], want: m, count: 1},
		{name: "a ghp token of 35 characters is not a token", in: ghp[:len(ghp)-1], want: ghp[:len(ghp)-1]},
		{name: "a ghp token of exactly 36 characters", in: ghp[:40], want: m, count: 1},
		{name: "a ghx prefix is not a token", in: "ghx_" + ghp[4:], want: "ghx_" + ghp[4:]},
		{name: "a github_pat token", in: "x=" + ghPAT, want: "x=" + m, count: 1},
		{name: "a github_pat token one short", in: "github_pat_" + strings.Repeat("a", 21), want: "github_pat_" + strings.Repeat("a", 21)},
		{name: "a github_pat token at the minimum", in: "github_pat_" + strings.Repeat("a", 22), want: m, count: 1},
		{name: "an sk-ant key", in: "key: " + skAnt + ".", want: "key: " + m + ".", count: 1},
		{name: "an sk-ant key one short", in: "sk-ant-" + strings.Repeat("a", 19), want: "sk-ant-" + strings.Repeat("a", 19)},
		{name: "an sk-ant key at the minimum", in: "sk-ant-" + strings.Repeat("a", 20), want: m, count: 1},
		{name: "a glpat token", in: glpat, want: m, count: 1},
		{name: "a glpat token one short", in: "glpat-" + strings.Repeat("a", 19), want: "glpat-" + strings.Repeat("a", 19)},
		{name: "a slack token", in: "(" + slack + ")", want: "(" + m + ")", count: 1},
		{name: "a slack token one short", in: "xoxb-123456789", want: "xoxb-123456789"},
		{name: "a slack token with a bad kind", in: "xoxz-1234567890-abcdef", want: "xoxz-1234567890-abcdef"},
		{name: "an AWS key id", in: "id " + awsKey + " x", want: "id " + m + " x", count: 1},
		{name: "an AWS key id one short", in: "AKIAIOSFODNN7EXAMPL", want: "AKIAIOSFODNN7EXAMPL"},
		{name: "an AWS key id with a lowercase letter", in: "AKIAIOSFODNN7EXAMPLEe", want: m + "e", count: 1},
		{name: "a PEM private key across lines", in: "before\n" + pemBlock + "\nafter", want: "before\n" + m + "\nafter", count: 1},
		{name: "a bare PEM private key", in: "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----", want: m, count: 1},
		{name: "an OPENSSH PEM key", in: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", want: m, count: 1},
		{name: "two PEM keys stop at the first END", in: pemBlock + "\nkeep\n" + pemBlock, want: m + "\nkeep\n" + m, count: 2},
		{name: "a PEM public key is untouched", in: "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----", want: "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----"},
		{name: "an Authorization bearer header keeps its prefix", in: "Authorization: Bearer abcdefgh12345678", want: "Authorization: Bearer " + m, count: 1},
		{name: "an Authorization token header is case-insensitive", in: "authorization:   token abc.def~ghi+jk/l=", want: "authorization:   token " + m, count: 1},
		{name: "an Authorization value of 7 characters is kept", in: "Authorization: Bearer abcdefg", want: "Authorization: Bearer abcdefg"},
		{name: "an Authorization value of 8 characters", in: "Authorization: Bearer abcdefgh", want: "Authorization: Bearer " + m, count: 1},
		{name: "two Authorization headers joined by an equals sign both go", in: "Authorization: Bearer aaaaaaaa1=Authorization: Bearer bbbbbbbb2", want: "Authorization: Bearer " + m + "Authorization: Bearer " + m, count: 2},
		{name: "an Authorization Basic header is kept", in: "Authorization: Basic abcdefgh12345678", want: "Authorization: Basic abcdefgh12345678"},
		{name: "a secrets.env value is redacted wherever it appears", values: []string{"hunter2hunter2"}, in: "a hunter2hunter2 b\nhunter2hunter2", want: "a " + m + " b\n" + m, count: 2},
		{name: "a multi-line exact value", values: []string{"line1\nline2-secret"}, in: "x line1\nline2-secret y", want: "x " + m + " y", count: 1},
		{name: "a value shorter than 8 bytes is not redacted", values: []string{"short12"}, in: "short12 short12", want: "short12 short12"},
		{name: "a value of exactly 8 bytes is redacted", values: []string{"eight888"}, in: "eight888", want: m, count: 1},
		{name: "the empty value is ignored", values: []string{""}, in: "abc", want: "abc"},
		{name: "the longer of two overlapping values wins", values: []string{"secretvalue", "secretvalue-long"}, in: "x secretvalue-long y", want: "x " + m + " y", count: 1},
		{name: "partly overlapping values leave no tail", values: []string{"abcdefgh", "defghijk"}, in: "abcdefghijk", want: m, count: 1},
		{name: "a value found inside a token pattern", values: []string{"abcdefghijklmnopqrstuvwxyzABCDEF0123"}, in: "ghp_abcdefghijklmnopqrstuvwxyzABCDEF0123", want: "ghp_" + m, count: 1},
		{name: "a value that is part of the marker does not corrupt it", values: []string{"redacted", "othersecret"}, in: "redacted othersecret", want: m + " " + m, count: 2},
		{name: "an exact value is redacted before patterns", values: []string{ghp}, in: ghp, want: m, count: 1},
		{name: "regexp metacharacters in a value are literal", values: []string{"a.b*c+d(e)[f]"}, in: "a.b*c+d(e)[f] aXb", want: m + " aXb", count: 1},
		{name: "text without secrets is unchanged and counts 0", in: "Reviewed pull request #42: fix sk-learn, ghp_short, AKIA123, Bearer tokens in docs.", want: "Reviewed pull request #42: fix sk-learn, ghp_short, AKIA123, Bearer tokens in docs."},
		{name: "a commit sha and a uuid are unchanged", in: "d9363e0a1b2c3d4e5f60718293a4b5c6d7e8f901 4da7c776-c764-4503-8f7a-fb90ad5d0212", want: "d9363e0a1b2c3d4e5f60718293a4b5c6d7e8f901 4da7c776-c764-4503-8f7a-fb90ad5d0212"},
		{name: "code that mentions the word token is unchanged", in: "func token() { return Authorization }\nauthorization: none", want: "func token() { return Authorization }\nauthorization: none"},
		{name: "a public key block is unchanged", in: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabcdefghijklmnop", want: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabcdefghijklmnop"},
		{name: "an empty string", in: "", want: ""},
		{name: "two tokens are counted twice", in: ghp + " " + awsKey, want: m + " " + m, count: 2},
		{name: "a token wrapped in a line break is not matched", in: "ghp_abcdefghijklmnop\nqrstuvwxyzABCDEF0123", want: "ghp_abcdefghijklmnop\nqrstuvwxyzABCDEF0123"},
		{name: "a token touching letters is still redacted", in: "xAKIAIOSFODNN7EXAMPLE", want: "x" + m, count: 1},
		{name: "invalid utf-8 passes through", in: "\xff\xfe " + awsKey, want: "\xff\xfe " + m, count: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := redact.New(tt.values...).String(tt.in)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.count, count)
		})
	}
}

func TestRedactor_String_adversarial(t *testing.T) {
	secret := "s3cr3t-Value-XYZ-98765"
	r := redact.New(secret)
	pieces := []string{"", " ", "\n", "[", "]", "redacted", "[redacted]", "s3cr3t", "XYZ-98765", "Bearer ", "Authorization: ", "ghp_", "-----BEGIN PRIVATE KEY-----", "é", "\x00", "\\", "\"", "%73"}
	for _, a := range pieces {
		for _, b := range pieces {
			t.Run(fmt.Sprintf("%q around %q", a, b), func(t *testing.T) {
				in := a + secret + b + secret + a
				got, count := r.String(in)
				assert.NotContains(t, got, secret)
				assert.GreaterOrEqual(t, count, 1)
				again, n := r.String(got)
				assert.Equal(t, got, again, "redaction is idempotent")
				assert.Zero(t, n)
			})
		}
	}
}

func TestRedactor_String_adversarialTokens(t *testing.T) {
	r := redact.New()
	tokens := []string{ghp, ghPAT, skAnt, glpat, slack, awsKey, pemBlock, "Authorization: Bearer abcdefgh12345678"}
	seps := []string{" ", "\n", "\t", `"`, "'", "=", ":", "`", "\r\n", "<", ">", " "}
	for _, tok := range tokens {
		for _, sep := range seps {
			t.Run(fmt.Sprintf("%.12q in %q", tok, sep), func(t *testing.T) {
				in := sep + tok + sep + tok + sep
				got, count := r.String(in)
				assert.Equal(t, 2, count)
				assert.Contains(t, got, redact.Marker)
				assert.NotContains(t, got, "abcdefghij")
				assert.NotContains(t, got, "MIIEow")
				assert.NotContains(t, got, "IOSFODNN7")
				assert.NotContains(t, got, "abcdefgh12345678")
			})
		}
	}
}

func FuzzRedactor_String(f *testing.F) {
	secret := "s3cr3t-Value-XYZ-98765"
	r := redact.New(secret)
	f.Add("", "")
	f.Add("[red", "acted]")
	f.Add("s3cr3t-", "Value")
	f.Add("ghp_", ghp)
	f.Fuzz(func(t *testing.T, a, b string) {
		got, _ := r.String(a + secret + b)
		if strings.Contains(got, secret) {
			t.Fatalf("secret survived: %q", got)
		}
		again, n := r.String(got)
		if n != 0 || again != got {
			t.Fatalf("not idempotent: %q then %q (%d)", got, again, n)
		}
	})
}

func TestRedactor_JSON(t *testing.T) {
	m := redact.Marker
	r := redact.New("exactsecret1")
	tests := []struct {
		name  string
		in    string
		want  string
		count int
	}{
		{name: "keys are not redacted", in: `{"` + ghp + `":"` + ghp + `"}`, want: `{"` + ghp + `":"` + m + `"}`, count: 1},
		{name: "nested arrays and objects", in: `{"a":[{"b":["x ` + ghp + `",{"c":"exactsecret1"}]}],"d":{"e":"` + awsKey + `"}}`, want: `{"a":[{"b":["x ` + m + `",{"c":"` + m + `"}]}],"d":{"e":"` + m + `"}}`, count: 3},
		{name: "numbers keep their text", in: `[1.0,1e400,12345678901234567890,-0.10,true,null,"x"]`, want: `[1.0,1e400,12345678901234567890,-0.10,true,null,"x"]`},
		{name: "key order is kept", in: `{"z":1,"a":2,"m":{"y":1,"b":2}}`, want: `{"z":1,"a":2,"m":{"y":1,"b":2}}`},
		{name: "whitespace is compacted", in: " {\n \"a\" : [ 1 , 2 ] \n} ", want: `{"a":[1,2]}`},
		{name: "a PEM key with escaped newlines", in: `{"k":"x\n` + strings.ReplaceAll(pemBlock, "\n", `\n`) + `\ny"}`, want: `{"k":"x\n` + m + `\ny"}`, count: 1},
		{name: "html characters are not escaped", in: `{"a":"<b>&</b>"}`, want: `{"a":"<b>&</b>"}`},
		{name: "a unicode-escaped secret is found after decoding", in: `{"a":"exactsecret1"}`, want: `{"a":"` + m + `"}`, count: 1},
		{name: "a top-level string", in: `"` + awsKey + `"`, want: `"` + m + `"`, count: 1},
		{name: "a top-level number", in: `42`, want: `42`},
		{name: "empty containers", in: `{"a":{},"b":[]}`, want: `{"a":{},"b":[]}`},
		{name: "duplicate keys are kept in order", in: `{"a":"1","a":"2"}`, want: `{"a":"1","a":"2"}`},
		{name: "strings without secrets are unchanged", in: `{"a":"hello \"world\"\n"}`, want: `{"a":"hello \"world\"\n"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count, err := r.JSON([]byte(tt.in))
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.Equal(t, tt.count, count)
		})
	}
	for _, in := range []string{``, `   `, `{`, `{"a":}`, `nope`, `{"a":1} x`, `{"a":1}{"b":2}`, `[1,]`, `{'a':1}`, ghp} {
		t.Run(fmt.Sprintf("invalid JSON %q is ErrInvalid", in), func(t *testing.T) {
			_, _, err := r.JSON([]byte(in))
			require.ErrorIs(t, err, redact.ErrInvalid)
		})
	}
}

func TestSecretValues(t *testing.T) {
	tests := []struct {
		name    string
		secrets map[string]string
		environ []string
		want    []string
	}{
		{name: "every secrets.env value", secrets: map[string]string{"A": "value-one", "B": "value-two"}, want: []string{"value-one", "value-two"}},
		{name: "GH_TOKEN", environ: []string{"GH_TOKEN=tok1"}, want: []string{"tok1"}},
		{name: "GITHUB_TOKEN", environ: []string{"GITHUB_TOKEN=tok1"}, want: []string{"tok1"}},
		{name: "CLAUDE_CODE_OAUTH_TOKEN", environ: []string{"CLAUDE_CODE_OAUTH_TOKEN=tok1"}, want: []string{"tok1"}},
		{name: "WS_FORGEJO_TOKEN_ prefix", environ: []string{"WS_FORGEJO_TOKEN_CODE=tok1"}, want: []string{"tok1"}},
		{name: "WS_ENDPOINT_TOKEN_ prefix", environ: []string{"WS_ENDPOINT_TOKEN_A=tok1"}, want: []string{"tok1"}},
		{name: "_TOKEN suffix", environ: []string{"MY_SERVICE_TOKEN=tok1"}, want: []string{"tok1"}},
		{name: "_SECRET suffix", environ: []string{"MY_SECRET=tok1"}, want: []string{"tok1"}},
		{name: "_API_KEY suffix", environ: []string{"MY_API_KEY=tok1"}, want: []string{"tok1"}},
		{name: "_PASSWORD suffix", environ: []string{"DB_PASSWORD=tok1"}, want: []string{"tok1"}},
		{name: "PATH is not a secret", environ: []string{"PATH=/usr/bin:/bin", "HOME=/home/x", "TOKEN_COUNT=5", "GH_TOKEN_FILE=/x", "my_token=low"}},
		{name: "a value with an equals sign keeps it", environ: []string{"X_TOKEN=a=b=c"}, want: []string{"a=b=c"}},
		{name: "an empty value is dropped", environ: []string{"X_TOKEN="}, secrets: map[string]string{"E": ""}},
		{name: "an entry without an equals sign is ignored", environ: []string{"X_TOKEN"}},
		{name: "duplicates collapse", secrets: map[string]string{"A": "same"}, environ: []string{"X_TOKEN=same"}, want: []string{"same"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.want, redact.SecretValues(tt.secrets, tt.environ))
		})
	}
	t.Run("the values feed New and short ones are ignored", func(t *testing.T) {
		vals := redact.SecretValues(map[string]string{"A": "longenough-value"}, []string{"X_TOKEN=tiny"})
		got, count := redact.New(vals...).String("longenough-value tiny")
		assert.Equal(t, redact.Marker+" tiny", got)
		assert.Equal(t, 1, count)
	})
}

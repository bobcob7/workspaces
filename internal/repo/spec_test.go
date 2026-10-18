package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseSpec(t *testing.T, s string) Spec {
	t.Helper()
	sp, err := ParseSpec(s)
	require.NoError(t, err)
	return sp
}

func TestKind_String(t *testing.T) {
	t.Run("name", func(t *testing.T) {
		assert.Equal(t, "name", KindName.String())
	})
	t.Run("owner/name", func(t *testing.T) {
		assert.Equal(t, "owner/name", KindOwnerName.String())
	})
	t.Run("host/owner/name", func(t *testing.T) {
		assert.Equal(t, "host/owner/name", KindQualified.String())
	})
	t.Run("url", func(t *testing.T) {
		assert.Equal(t, "url", KindURL.String())
	})
	t.Run("unknown", func(t *testing.T) {
		assert.Equal(t, "Kind(0)", Kind(0).String())
	})
}

func TestParseSpec(t *testing.T) {
	t.Run("never repeats a password", func(t *testing.T) {
		_, err := ParseSpec("https://me:secret@h.com/o/n")
		require.ErrorIs(t, err, ErrInvalidSpec)
		assert.NotContains(t, err.Error(), "secret")
	})
	t.Run("rejects a local path", func(t *testing.T) {
		_, err := ParseSpec("/srv/repos/x.git@main")
		require.ErrorIs(t, err, ErrInvalidSpec)
		assert.ErrorIs(t, err, errLocal)
	})
	t.Run("name", func(t *testing.T) {
		assert.Equal(t, Spec{Raw: "buff-bear", Kind: KindName, Name: "buff-bear"}, mustParseSpec(t, "buff-bear"))
	})
	t.Run("owner/name", func(t *testing.T) {
		sp := mustParseSpec(t, "bobcob7/buff-bear")
		assert.Equal(t, KindOwnerName, sp.Kind)
		assert.Equal(t, "bobcob7", sp.Owner)
		assert.Equal(t, "buff-bear", sp.Name)
	})
	t.Run("host/owner/name", func(t *testing.T) {
		sp := mustParseSpec(t, "github.com/bobcob7/buff-bear")
		assert.Equal(t, KindQualified, sp.Kind)
		assert.Equal(t, "github.com", sp.Host)
	})
	t.Run("scp URL", func(t *testing.T) {
		sp := mustParseSpec(t, "git@github.com:bobcob7/buff-bear.git")
		assert.Equal(t, KindURL, sp.Kind)
		assert.Equal(t, "git@github.com:bobcob7/buff-bear.git", sp.URL)
		assert.Equal(t, "buff-bear", sp.Name)
	})
	t.Run("branch with slash", func(t *testing.T) {
		assert.Equal(t, "feat/new-thing", mustParseSpec(t, "buff-bear@feat/new-thing").Ref)
	})
	t.Run("tag with dots", func(t *testing.T) {
		assert.Equal(t, "v1.2.0", mustParseSpec(t, "buff-bear@v1.2.0").Ref)
	})
	t.Run("commit", func(t *testing.T) {
		assert.Equal(t, "3f2a9c1", mustParseSpec(t, "buff-bear@3f2a9c1").Ref)
	})
	t.Run("ref containing @", func(t *testing.T) {
		sp := mustParseSpec(t, "bobcob7/buff-bear@user@fix")
		assert.Equal(t, "buff-bear", sp.Name)
		assert.Equal(t, "user@fix", sp.Ref)
	})
	t.Run("scp URL with ref", func(t *testing.T) {
		sp := mustParseSpec(t, "git@github.com:bobcob7/buff-bear@feat/x")
		assert.Equal(t, "git@github.com:bobcob7/buff-bear", sp.URL)
		assert.Equal(t, "feat/x", sp.Ref)
	})
	t.Run("scheme URL with user and ref", func(t *testing.T) {
		sp := mustParseSpec(t, "ssh://git@github.com/bobcob7/buff-bear.git@main")
		assert.Equal(t, "ssh://git@github.com/bobcob7/buff-bear.git", sp.URL)
		assert.Equal(t, "main", sp.Ref)
	})
	t.Run("keeps the raw spec", func(t *testing.T) {
		assert.Equal(t, "Buff-Bear@Main", mustParseSpec(t, "Buff-Bear@Main").Raw)
	})
	t.Run("lowercases names but not refs", func(t *testing.T) {
		sp := mustParseSpec(t, "BobCob7/Buff-Bear@Feat/X")
		assert.Equal(t, "bobcob7", sp.Owner)
		assert.Equal(t, "buff-bear", sp.Name)
		assert.Equal(t, "Feat/X", sp.Ref)
	})
	t.Run("rejects an empty spec", func(t *testing.T) {
		_, err := ParseSpec("")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects an empty ref", func(t *testing.T) {
		_, err := ParseSpec("buff-bear@")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects a bad repo", func(t *testing.T) {
		_, err := ParseSpec("a//b")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects name@ref:branch, reserved by ADR 0007", func(t *testing.T) {
		_, err := ParseSpec("buff-bear@main:feat/new")
		require.ErrorIs(t, err, ErrInvalidSpec)
		assert.ErrorContains(t, err, `can't contain ":"`)
	})
	t.Run("rejects an insteadOf shorthand", func(t *testing.T) {
		_, err := ParseSpec("gh:bobcob7/buff-bear")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects a ref starting with -", func(t *testing.T) {
		_, err := ParseSpec("buff-bear@--upload-pack=x")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("error names the spec", func(t *testing.T) {
		_, err := ParseSpec("buff-bear@a:b")
		assert.ErrorContains(t, err, `"buff-bear@a:b"`)
	})
}

func TestSpec_ID(t *testing.T) {
	t.Run("qualified", func(t *testing.T) {
		sp := mustParseSpec(t, "gitlab.com/a/b/c")
		id, ok := sp.ID()
		require.True(t, ok)
		assert.Equal(t, ID{Host: "gitlab.com", Owner: "a/b", Name: "c"}, id)
	})
	t.Run("URL", func(t *testing.T) {
		sp := mustParseSpec(t, "https://GitHub.com/BobCob7/Buff-Bear.git")
		id, ok := sp.ID()
		require.True(t, ok)
		assert.Equal(t, "github.com/bobcob7/buff-bear", id.String())
	})
	t.Run("URL with a port", func(t *testing.T) {
		sp := mustParseSpec(t, "ssh://git@h.com:2222/o/n")
		id, ok := sp.ID()
		require.True(t, ok)
		assert.Equal(t, "h.com:2222/o/n", id.String())
	})
	t.Run("owner/name has none", func(t *testing.T) {
		sp := mustParseSpec(t, "bobcob7/buff-bear")
		_, ok := sp.ID()
		assert.False(t, ok)
	})
	t.Run("name has none", func(t *testing.T) {
		sp := mustParseSpec(t, "buff-bear")
		_, ok := sp.ID()
		assert.False(t, ok)
	})
}

func TestSpec_Upstream(t *testing.T) {
	t.Run("URL as given", func(t *testing.T) {
		sp := mustParseSpec(t, "git@github.com:BobCob7/buff-bear.git@main")
		got, ok := sp.Upstream()
		require.True(t, ok)
		assert.Equal(t, "git@github.com:BobCob7/buff-bear.git", got)
	})
	t.Run("qualified builds HTTPS", func(t *testing.T) {
		sp := mustParseSpec(t, "github.com/bobcob7/buff-bear")
		got, ok := sp.Upstream()
		require.True(t, ok)
		assert.Equal(t, "https://github.com/bobcob7/buff-bear.git", got)
	})
	t.Run("short spec has none", func(t *testing.T) {
		sp := mustParseSpec(t, "bobcob7/buff-bear")
		_, ok := sp.Upstream()
		assert.False(t, ok)
	})
}

func TestSplit(t *testing.T) {
	t.Run("no ref", func(t *testing.T) {
		repo, ref, hasRef := split("buff-bear")
		assert.Equal(t, "buff-bear", repo)
		assert.Empty(t, ref)
		assert.False(t, hasRef)
	})
	t.Run("first @ wins", func(t *testing.T) {
		repo, ref, hasRef := split("o/n@a@b")
		assert.Equal(t, "o/n", repo)
		assert.Equal(t, "a@b", ref)
		assert.True(t, hasRef)
	})
	t.Run("scp user stays in the repo", func(t *testing.T) {
		repo, ref, _ := split("git@host.com:o/n@main")
		assert.Equal(t, "git@host.com:o/n", repo)
		assert.Equal(t, "main", ref)
	})
	t.Run("URL user stays in the repo", func(t *testing.T) {
		repo, ref, hasRef := split("ssh://git@host.com/o/n")
		assert.Equal(t, "ssh://git@host.com/o/n", repo)
		assert.Empty(t, ref)
		assert.False(t, hasRef)
	})
	t.Run("empty ref is still a ref", func(t *testing.T) {
		_, ref, hasRef := split("n@")
		assert.Empty(t, ref)
		assert.True(t, hasRef)
	})
}

func TestPathStart(t *testing.T) {
	t.Run("plain path", func(t *testing.T) {
		assert.Equal(t, 0, pathStart("o/n"))
	})
	t.Run("scheme URL", func(t *testing.T) {
		assert.Equal(t, len("https://h.com"), pathStart("https://h.com/o/n"))
	})
	t.Run("scheme URL without a path", func(t *testing.T) {
		assert.Equal(t, len("https://u@h.com"), pathStart("https://u@h.com"))
	})
	t.Run("scp URL", func(t *testing.T) {
		assert.Equal(t, len("git@h.com:"), pathStart("git@h.com:o/n"))
	})
}

func TestSchemeEnd(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		end, ok := schemeEnd("https://h.com/o/n")
		require.True(t, ok)
		assert.Equal(t, len("https://"), end)
	})
	t.Run("scheme with + . -", func(t *testing.T) {
		_, ok := schemeEnd("git+ssh://h.com/o/n")
		assert.True(t, ok)
	})
	t.Run("no scheme", func(t *testing.T) {
		_, ok := schemeEnd("o/n")
		assert.False(t, ok)
	})
	t.Run(":// at the start", func(t *testing.T) {
		_, ok := schemeEnd("://h.com")
		assert.False(t, ok)
	})
	t.Run(":// inside a ref", func(t *testing.T) {
		_, ok := schemeEnd("o/n@x://y")
		assert.False(t, ok)
	})
	t.Run("scheme starting with a digit", func(t *testing.T) {
		_, ok := schemeEnd("1http://h.com")
		assert.False(t, ok)
	})
}

func TestScpColon(t *testing.T) {
	t.Run("user@host:path", func(t *testing.T) {
		colon, ok := scpColon("git@h.com:o/n")
		require.True(t, ok)
		assert.Equal(t, len("git@h.com"), colon)
	})
	t.Run("host:path", func(t *testing.T) {
		_, ok := scpColon("h.com:o/n")
		assert.True(t, ok)
	})
	t.Run("colon after a slash", func(t *testing.T) {
		_, ok := scpColon("o/n@a:b")
		assert.False(t, ok)
	})
	t.Run("no colon", func(t *testing.T) {
		_, ok := scpColon("o/n")
		assert.False(t, ok)
	})
	t.Run("leading colon", func(t *testing.T) {
		_, ok := scpColon(":o/n")
		assert.False(t, ok)
	})
	t.Run("host without a dot", func(t *testing.T) {
		_, ok := scpColon("gh:o/n")
		assert.False(t, ok)
	})
	t.Run("name@ref:path is not a URL", func(t *testing.T) {
		_, ok := scpColon("buff-bear@main:feat/new")
		assert.False(t, ok)
	})
}

func TestValidateRef(t *testing.T) {
	t.Run("accepts a branch", func(t *testing.T) {
		assert.NoError(t, validateRef("feat/x.y"))
	})
	t.Run("accepts @ inside", func(t *testing.T) {
		assert.NoError(t, validateRef("user@fix"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.Error(t, validateRef(""))
	})
	t.Run("rejects bare @", func(t *testing.T) {
		assert.Error(t, validateRef("@"))
	})
	t.Run("rejects @{", func(t *testing.T) {
		assert.Error(t, validateRef("main@{1}"))
	})
	t.Run("rejects colons", func(t *testing.T) {
		assert.ErrorContains(t, validateRef("main:feat"), `"main:feat"`)
	})
	t.Run("rejects a leading dash", func(t *testing.T) {
		assert.Error(t, validateRef("-x"))
	})
	t.Run("accepts a dash inside", func(t *testing.T) {
		assert.NoError(t, validateRef("feat-x"))
	})
}

func TestParseRepo(t *testing.T) {
	t.Run("scheme URL", func(t *testing.T) {
		sp, err := parseRepo("https://h.com/o/n")
		require.NoError(t, err)
		assert.Equal(t, KindURL, sp.Kind)
	})
	t.Run("scp URL", func(t *testing.T) {
		sp, err := parseRepo("h.com:o/n")
		require.NoError(t, err)
		assert.Equal(t, KindURL, sp.Kind)
	})
	t.Run("rejects a local path", func(t *testing.T) {
		_, err := parseRepo("/srv/o/n.git")
		assert.ErrorIs(t, err, errLocal)
	})
	t.Run("path", func(t *testing.T) {
		sp, err := parseRepo("o/n")
		require.NoError(t, err)
		assert.Equal(t, KindOwnerName, sp.Kind)
	})
}

func TestDefaultPorts(t *testing.T) {
	assert.Equal(t, map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}, defaultPorts())
}

func TestParseSchemeURL(t *testing.T) {
	t.Run("https", func(t *testing.T) {
		sp, err := parseSchemeURL("https://GitHub.com/BobCob7/buff-bear.git")
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindURL, Host: "github.com", Owner: "bobcob7", Name: "buff-bear", URL: "https://GitHub.com/BobCob7/buff-bear.git"}, sp)
	})
	t.Run("ssh with user and port", func(t *testing.T) {
		sp, err := parseSchemeURL("ssh://git@h.com:2222/a/b/c")
		require.NoError(t, err)
		assert.Equal(t, "h.com:2222", sp.Host)
		assert.Equal(t, "a/b", sp.Owner)
		assert.Equal(t, "c", sp.Name)
	})
	t.Run("drops the scheme's default port", func(t *testing.T) {
		sp, err := parseSchemeURL("ssh://git@h.com:22/o/n")
		require.NoError(t, err)
		assert.Equal(t, "h.com", sp.Host)
	})
	t.Run("keeps a port that isn't the scheme's default", func(t *testing.T) {
		sp, err := parseSchemeURL("https://h.com:22/o/n")
		require.NoError(t, err)
		assert.Equal(t, "h.com:22", sp.Host)
	})
	t.Run("rejects a password", func(t *testing.T) {
		_, err := parseSchemeURL("https://me:secret@h.com/o/n")
		assert.ErrorContains(t, err, "credential helper")
	})
	t.Run("allows a user without a password", func(t *testing.T) {
		sp, err := parseSchemeURL("https://me@h.com/o/n")
		require.NoError(t, err)
		assert.Equal(t, "https://me@h.com/o/n", sp.URL)
	})
	t.Run("uppercase scheme", func(t *testing.T) {
		_, err := parseSchemeURL("HTTPS://h.com/o/n")
		assert.NoError(t, err)
	})
	t.Run("rejects a file URL", func(t *testing.T) {
		_, err := parseSchemeURL("file:///srv/git/o/n.git")
		assert.ErrorIs(t, err, errLocal)
	})
	t.Run("rejects other schemes", func(t *testing.T) {
		_, err := parseSchemeURL("ftp://h.com/o/n")
		assert.ErrorContains(t, err, "unsupported URL scheme")
	})
	t.Run("rejects a query", func(t *testing.T) {
		_, err := parseSchemeURL("https://h.com/o/n?x=1")
		assert.Error(t, err)
	})
	t.Run("rejects a fragment", func(t *testing.T) {
		_, err := parseSchemeURL("https://h.com/o/n#x")
		assert.Error(t, err)
	})
	t.Run("rejects a bad URL", func(t *testing.T) {
		_, err := parseSchemeURL("https://h.com:bad/o/n")
		assert.ErrorContains(t, err, "parsing URL")
	})
	t.Run("rejects a missing host", func(t *testing.T) {
		_, err := parseSchemeURL("https:///o/n")
		assert.Error(t, err)
	})
}

func TestParseSCP(t *testing.T) {
	t.Run("with user", func(t *testing.T) {
		sp, err := parseSCP("git@h.com:o/n.git", len("git@h.com"))
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindURL, Host: "h.com", Owner: "o", Name: "n", URL: "git@h.com:o/n.git"}, sp)
	})
	t.Run("without user", func(t *testing.T) {
		sp, err := parseSCP("h.com:o/n", len("h.com"))
		require.NoError(t, err)
		assert.Equal(t, "h.com", sp.Host)
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		_, err := parseSCP("git@:o/n", len("git@"))
		assert.Error(t, err)
	})
}

func TestUrlSpec(t *testing.T) {
	t.Run("nested groups", func(t *testing.T) {
		sp, err := urlSpec("u", "H.com", "/a/b/c.git")
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindURL, Host: "h.com", Owner: "a/b", Name: "c", URL: "u"}, sp)
	})
	t.Run("rejects one segment", func(t *testing.T) {
		_, err := urlSpec("u", "h.com", "/n")
		assert.ErrorContains(t, err, "owner and a name")
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		_, err := urlSpec("u", "", "/o/n")
		assert.Error(t, err)
	})
	t.Run("rejects a bad path", func(t *testing.T) {
		_, err := urlSpec("u", "h.com", "/o//n")
		assert.Error(t, err)
	})
}

func TestParsePath(t *testing.T) {
	t.Run("name", func(t *testing.T) {
		sp, err := parsePath("buff-bear.git")
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindName, Name: "buff-bear"}, sp)
	})
	t.Run("owner/name", func(t *testing.T) {
		sp, err := parsePath("o/n")
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindOwnerName, Owner: "o", Name: "n"}, sp)
	})
	t.Run("host/owner/name", func(t *testing.T) {
		sp, err := parsePath("h.com/o/n")
		require.NoError(t, err)
		assert.Equal(t, Spec{Kind: KindQualified, Host: "h.com", Owner: "o", Name: "n"}, sp)
	})
	t.Run("nested owner groups", func(t *testing.T) {
		sp, err := parsePath("gitlab.com/a/b/c")
		require.NoError(t, err)
		assert.Equal(t, "a/b", sp.Owner)
	})
	t.Run("rejects three segments without a host", func(t *testing.T) {
		_, err := parsePath("a/b/c")
		assert.ErrorContains(t, err, `"a" isn't a host name`)
	})
	t.Run("rejects a trailing slash", func(t *testing.T) {
		_, err := parsePath("o/n/")
		assert.Error(t, err)
	})
	t.Run("rejects a bad segment", func(t *testing.T) {
		_, err := parsePath("o/n!")
		assert.Error(t, err)
	})
	t.Run("rejects a host with a bad character", func(t *testing.T) {
		_, err := parsePath("my_host.com/o/n")
		assert.Error(t, err)
	})
}

func TestSegments(t *testing.T) {
	t.Run("splits and lowercases", func(t *testing.T) {
		segs, err := segments("A/B")
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, segs)
	})
	t.Run("drops one leading slash and .git", func(t *testing.T) {
		segs, err := segments("/o/n.git")
		require.NoError(t, err)
		assert.Equal(t, []string{"o", "n"}, segs)
	})
	t.Run("drops .git in any case", func(t *testing.T) {
		segs, err := segments("o/N.GIT")
		require.NoError(t, err)
		assert.Equal(t, []string{"o", "n"}, segs)
	})
	t.Run("drops .git only once", func(t *testing.T) {
		segs, err := segments("o/n.git.git")
		require.NoError(t, err)
		assert.Equal(t, []string{"o", "n.git"}, segs)
	})
	t.Run("rejects empty", func(t *testing.T) {
		_, err := segments(".git")
		assert.Error(t, err)
	})
	t.Run("rejects a bad segment", func(t *testing.T) {
		_, err := segments("o/../n")
		assert.Error(t, err)
	})
}

func TestRedact(t *testing.T) {
	t.Run("masks a URL password", func(t *testing.T) {
		assert.Equal(t, "https://me:xxxxx@h.com/o/n", redact("https://me:secret@h.com/o/n"))
	})
	t.Run("leaves other specs alone", func(t *testing.T) {
		assert.Equal(t, "git@h.com:o/n", redact("git@h.com:o/n"))
	})
	t.Run("leaves unparsable URLs alone", func(t *testing.T) {
		assert.Equal(t, "https://h.com/%zz", redact("https://h.com/%zz"))
	})
}

func TestIsLocalPath(t *testing.T) {
	t.Run("absolute path", func(t *testing.T) {
		assert.True(t, isLocalPath("/srv/o/n"))
	})
	t.Run("relative path", func(t *testing.T) {
		assert.True(t, isLocalPath("./o/n"))
	})
	t.Run("parent-relative path", func(t *testing.T) {
		assert.True(t, isLocalPath("../o/n"))
	})
	t.Run("home-relative path", func(t *testing.T) {
		assert.True(t, isLocalPath("~/o/n"))
	})
	t.Run("dot", func(t *testing.T) {
		assert.True(t, isLocalPath("."))
	})
	t.Run("dot-dot", func(t *testing.T) {
		assert.True(t, isLocalPath(".."))
	})
	t.Run("owner/name", func(t *testing.T) {
		assert.False(t, isLocalPath("o/n"))
	})
	t.Run("dotted name", func(t *testing.T) {
		assert.False(t, isLocalPath(".github"))
	})
}

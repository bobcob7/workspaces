package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	t.Run("host is github.com", func(t *testing.T) {
		assert.Equal(t, "github.com", Default().Defaults.Host)
	})
	t.Run("owners is empty, not nil", func(t *testing.T) {
		assert.NotNil(t, Default().Defaults.Owners)
		assert.Empty(t, Default().Defaults.Owners)
	})
	t.Run("matches the generated file", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
}

func TestParse(t *testing.T) {
	t.Run("empty file gives the defaults", func(t *testing.T) {
		cfg, err := parse(nil)
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
	t.Run("reads defaults", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nhost = \"gitlab.com\"\nowners = [\"team\", \"me\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, "gitlab.com", cfg.Defaults.Host)
		assert.Equal(t, []string{"team", "me"}, cfg.Defaults.Owners)
	})
	t.Run("keeps defaults for missing keys", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nowners = [\"me\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, "github.com", cfg.Defaults.Host)
	})
	t.Run("normalizes values", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nhost = \"GitHub.com\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "github.com", cfg.Defaults.Host)
	})
	t.Run("rejects bad TOML", func(t *testing.T) {
		_, err := parse([]byte("[defaults\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects the wrong type", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nowners = \"me\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects unknown keys and names them", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nprotocol = \"ssh\"\n[extra]\nx = 1\n"))
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "defaults.protocol")
		assert.ErrorContains(t, err, "extra.x")
	})
	t.Run("rejects invalid values", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nhost = \"\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("reads review", func(t *testing.T) {
		cfg, err := parse([]byte("[review]\ntimeout = \"90m\"\n[review.profiles.go]\nwhen = \"always\"\nmodel = \"opus\"\n"))
		require.NoError(t, err)
		assert.Equal(t, 90*time.Minute, cfg.Review.Timeout)
		assert.Equal(t, map[string]ReviewProfile{"go": {When: "always", Model: "opus"}}, cfg.Review.Profiles)
	})
	t.Run("rejects a bad review when", func(t *testing.T) {
		_, err := parse([]byte("[review.profiles.go]\nwhen = \"sometimes\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, "review.profiles.go.when")
	})
	t.Run("rejects a negative review timeout", func(t *testing.T) {
		_, err := parse([]byte("[review]\ntimeout = \"-1m\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("hosts in order", func(t *testing.T) {
		cfg, err := parse([]byte("[[hosts]]\nname = \"github.com\"\nkind = \"github\"\n\n[[hosts]]\nname = \"git.example.com\"\nkind = \"forgejo\"\n"))
		require.NoError(t, err)
		assert.Equal(t, []Host{{Name: "github.com", Kind: KindGithub}, {Name: "git.example.com", Kind: KindForgejo}}, cfg.Hosts)
	})
	t.Run("kind defaults to auto", func(t *testing.T) {
		cfg, err := parse([]byte("[[hosts]]\nname = \"github.com\"\n"))
		require.NoError(t, err)
		assert.Equal(t, []Host{{Name: "github.com", Kind: KindAuto}}, cfg.Hosts)
	})
	t.Run("names are lowercased", func(t *testing.T) {
		cfg, err := parse([]byte("[[hosts]]\nname = \"Git.Example.COM\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "git.example.com", cfg.Hosts[0].Name)
	})
	t.Run("a port is allowed", func(t *testing.T) {
		cfg, err := parse([]byte("[[hosts]]\nname = \"git.example.com:3000\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "git.example.com:3000", cfg.Hosts[0].Name)
	})
	t.Run("empty name is invalid", func(t *testing.T) {
		_, err := parse([]byte("[[hosts]]\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, "hosts entry 1 has no name")
	})
	t.Run("bad name is invalid", func(t *testing.T) {
		_, err := parse([]byte("[[hosts]]\nname = \"https://x\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("unknown kind is invalid", func(t *testing.T) {
		_, err := parse([]byte("[[hosts]]\nname = \"gitlab.com\"\nkind = \"gitlab\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("repeated name is invalid", func(t *testing.T) {
		_, err := parse([]byte("[[hosts]]\nname = \"GitHub.com\"\n\n[[hosts]]\nname = \"github.com\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, `hosts lists "github.com" twice`)
	})
	t.Run("token is an unknown key with a hint", func(t *testing.T) {
		_, err := parse([]byte("[[hosts]]\nname = \"git.example.com\"\ntoken = \"x\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "hosts.token")
		assert.ErrorContains(t, err, "WS_FORGEJO_TOKEN_<HOST>")
	})
	t.Run("defaults.host and hosts together are invalid", func(t *testing.T) {
		_, err := parse([]byte("[defaults]\nhost = \"gitlab.example.com\"\n\n[[hosts]]\nname = \"github.com\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, `move "gitlab.example.com" into [[hosts]]`)
	})
	t.Run("defaults.host alone still works", func(t *testing.T) {
		cfg, err := parse([]byte("[defaults]\nhost = \"gitlab.example.com\"\n"))
		require.NoError(t, err)
		assert.Equal(t, []Host{{Name: "gitlab.example.com", Kind: KindAuto}}, cfg.EffectiveHosts())
	})
	t.Run("the default file parses to Default()", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
}

func TestConfig_EffectiveHosts(t *testing.T) {
	t.Run("is the hosts list when it is set", func(t *testing.T) {
		cfg := Config{Hosts: []Host{{Name: "git.example.com", Kind: KindForgejo}}}
		assert.Equal(t, []Host{{Name: "git.example.com", Kind: KindForgejo}}, cfg.EffectiveHosts())
	})
	t.Run("is defaults.host as auto when only it is set", func(t *testing.T) {
		cfg := Config{Defaults: Defaults{Host: "gitlab.example.com"}}
		assert.Equal(t, []Host{{Name: "gitlab.example.com", Kind: KindAuto}}, cfg.EffectiveHosts())
	})
	t.Run("is github.com when nothing is set", func(t *testing.T) {
		assert.Equal(t, []Host{{Name: "github.com", Kind: KindAuto}}, Config{}.EffectiveHosts())
	})
}

func TestConfig_SpecDefaults(t *testing.T) {
	t.Run("lists the names in order, with the owners", func(t *testing.T) {
		cfg := Config{
			Defaults: Defaults{Owners: []string{"me"}},
			Hosts:    []Host{{Name: "github.com", Kind: KindGithub}, {Name: "git.example.com", Kind: KindForgejo}},
		}
		assert.Equal(t, SpecDefaults{Hosts: []string{"github.com", "git.example.com"}, Owners: []string{"me"}}, cfg.SpecDefaults())
	})
	t.Run("is github.com when nothing is set", func(t *testing.T) {
		assert.Equal(t, []string{"github.com"}, Config{}.SpecDefaults().Hosts)
	})
}

func TestNormalizeHosts(t *testing.T) {
	t.Run("nil is nil", func(t *testing.T) {
		hosts, err := normalizeHosts(nil)
		require.NoError(t, err)
		assert.Nil(t, hosts)
	})
	tests := []struct {
		name  string
		hosts []Host
		want  string
	}{
		{name: "empty name", hosts: []Host{{}}, want: "hosts entry 1 has no name"},
		{name: "second entry's name", hosts: []Host{{Name: "github.com"}, {Name: "a/b"}}, want: `hosts entry 2: name "a/b"`},
		{name: "unknown kind", hosts: []Host{{Name: "gitlab.com", Kind: "gitlab"}}, want: `hosts entry 1 (gitlab.com): kind must be auto, github, or forgejo, not "gitlab"`},
		{name: "repeated name", hosts: []Host{{Name: "github.com"}, {Name: "GitHub.com"}}, want: `hosts lists "github.com" twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeHosts(tt.hosts)
			require.ErrorIs(t, err, ErrInvalid)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestReview_WaitTimeout(t *testing.T) {
	t.Run("defaults to 60 minutes", func(t *testing.T) {
		assert.Equal(t, 60*time.Minute, (&Review{}).WaitTimeout())
	})
	t.Run("is the configured timeout", func(t *testing.T) {
		assert.Equal(t, 2*time.Hour, (&Review{Timeout: 2 * time.Hour}).WaitTimeout())
	})
}

func TestConfig_normalize(t *testing.T) {
	newConfig := func(host string, owners ...string) Config {
		return Config{Defaults: Defaults{Host: host, Owners: owners}}
	}
	t.Run("lowercases host and owners", func(t *testing.T) {
		cfg, err := newConfig("GitLab.COM", "MyOrg").normalize(false)
		require.NoError(t, err)
		assert.Equal(t, newConfig("gitlab.com", "myorg"), cfg)
	})
	t.Run("drops duplicate owners, keeping order", func(t *testing.T) {
		cfg, err := newConfig("github.com", "b", "A", "a", "b").normalize(false)
		require.NoError(t, err)
		assert.Equal(t, []string{"b", "a"}, cfg.Defaults.Owners)
	})
	t.Run("nil owners become empty", func(t *testing.T) {
		cfg, err := newConfig("github.com").normalize(false)
		require.NoError(t, err)
		assert.NotNil(t, cfg.Defaults.Owners)
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		_, err := newConfig("").normalize(false)
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("rejects a bad owner", func(t *testing.T) {
		_, err := newConfig("github.com", "ok", "").normalize(false)
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestValidateHost(t *testing.T) {
	t.Run("accepts a host name", func(t *testing.T) {
		assert.NoError(t, validateHost("gitlab.example.com"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.ErrorIs(t, validateHost(""), ErrInvalid)
	})
	t.Run("rejects a URL", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("https://github.com"), ErrInvalid)
	})
	t.Run("rejects a path", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("github.com/bobcob7"), ErrInvalid)
	})
	t.Run("rejects a user", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("git@github.com"), ErrInvalid)
	})
	t.Run("rejects spaces", func(t *testing.T) {
		assert.ErrorIs(t, validateHost("git hub.com"), ErrInvalid)
	})
}

func TestValidateOwner(t *testing.T) {
	t.Run("accepts a name", func(t *testing.T) {
		assert.NoError(t, validateOwner("bobcob7"))
	})
	t.Run("accepts nested groups", func(t *testing.T) {
		assert.NoError(t, validateOwner("gitlab-org/api"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner(""), ErrInvalid)
	})
	t.Run("rejects @", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("me@main"), ErrInvalid)
	})
	t.Run("rejects colons", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("git:me"), ErrInvalid)
	})
	t.Run("rejects spaces", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("my org"), ErrInvalid)
	})
	t.Run("rejects a leading slash", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("/me"), ErrInvalid)
	})
	t.Run("rejects a trailing slash", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("me/"), ErrInvalid)
	})
	t.Run("rejects an empty segment", func(t *testing.T) {
		assert.ErrorIs(t, validateOwner("a//b"), ErrInvalid)
	})
}

func TestEnvironments_addresses(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		var env Environments
		assert.Equal(t, "0.0.0.0:8883", env.ListenAddr())
		assert.Equal(t, "0.0.0.0:8884", env.EnrollAddr())
		assert.Equal(t, 30, env.RenewDays())
	})
	t.Run("reads what is set", func(t *testing.T) {
		days := 7
		env := Environments{Listen: "127.0.0.1:9001", EnrollListen: "127.0.0.1:9002", RenewBeforeDays: &days}
		assert.Equal(t, "127.0.0.1:9001", env.ListenAddr())
		assert.Equal(t, "127.0.0.1:9002", env.EnrollAddr())
		assert.Equal(t, 7, env.RenewDays())
	})
	t.Run("hosts join public_name with each port", func(t *testing.T) {
		env := Environments{PublicName: "ws.example.com", Listen: "0.0.0.0:9001"}
		enroll, err := env.EnrollHost()
		require.NoError(t, err)
		assert.Equal(t, "ws.example.com:8884", enroll)
		broker, err := env.BrokerHost()
		require.NoError(t, err)
		assert.Equal(t, "ws.example.com:9001", broker)
	})
	t.Run("hosts keep IPv6 brackets", func(t *testing.T) {
		env := Environments{PublicName: "::1"}
		broker, err := env.BrokerHost()
		require.NoError(t, err)
		assert.Equal(t, "[::1]:8883", broker)
	})
	t.Run("hosts need public_name", func(t *testing.T) {
		var env Environments
		_, err := env.EnrollHost()
		require.ErrorIs(t, err, ErrNoPublicName)
		_, err = env.BrokerHost()
		require.ErrorIs(t, err, ErrNoPublicName)
	})
}

func TestConfig_environments(t *testing.T) {
	t.Run("agent_loop_scripts defaults to allowed", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.True(t, cfg.Environments.LoopScriptsAllowed())
	})
	t.Run("false forbids", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\nagent_loop_scripts = false\n"))
		require.NoError(t, err)
		assert.False(t, cfg.Environments.LoopScriptsAllowed())
	})
	t.Run("true allows", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\nagent_loop_scripts = true\n"))
		require.NoError(t, err)
		assert.True(t, cfg.Environments.LoopScriptsAllowed())
	})
}

func TestParse_environments(t *testing.T) {
	t.Run("absent table gives the defaults", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, Environments{}, cfg.Environments)
	})
	t.Run("reads every key", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\nlisten = \"0.0.0.0:9001\"\nenroll_listen = \"0.0.0.0:9002\"\npublic_name = \"ws.example.com\"\nrenew_before_days = 14\n"))
		require.NoError(t, err)
		require.NotNil(t, cfg.Environments.RenewBeforeDays)
		assert.Equal(t, Environments{Listen: "0.0.0.0:9001", EnrollListen: "0.0.0.0:9002", PublicName: "ws.example.com", RenewBeforeDays: cfg.Environments.RenewBeforeDays}, cfg.Environments)
		assert.Equal(t, 14, cfg.Environments.RenewDays())
	})
	t.Run("reads api_listen", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\napi_listen = \"0.0.0.0:9003\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "0.0.0.0:9003", cfg.Environments.APIAddr())
	})
	t.Run("api_listen defaults when unset and turns off with off or empty", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\npublic_name = \"ws.example.com\"\n"))
		require.NoError(t, err)
		assert.Equal(t, "0.0.0.0:8885", cfg.Environments.APIAddr())
		for _, v := range []string{"off", ""} {
			cfg, err = parse([]byte("[environments]\napi_listen = \"" + v + "\"\n"))
			require.NoError(t, err)
			assert.Empty(t, cfg.Environments.APIAddr(), v)
		}
	})
	t.Run("accepts an IP public_name", func(t *testing.T) {
		_, err := parse([]byte("[environments]\npublic_name = \"192.0.2.10\"\n"))
		require.NoError(t, err)
	})
	t.Run("reads a valid default_scope", func(t *testing.T) {
		cfg, err := parse([]byte("[environments]\ndefault_scope = [\"local\", \"linux-server\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, []string{"local", "linux-server"}, cfg.Environments.DefaultScope)
	})
	bad := []struct {
		name string
		line string
		key  string
	}{
		{"listen without a port", `listen = "8883"`, "environments.listen"},
		{"listen port 0", `listen = "0.0.0.0:0"`, "environments.listen"},
		{"listen port 70000", `listen = "0.0.0.0:70000"`, "environments.listen"},
		{"listen port with a sign", `listen = "0.0.0.0:+80"`, "environments.listen"},
		{"enroll without a port", `enroll_listen = "0.0.0.0"`, "environments.enroll_listen"},
		{"api without a port", `api_listen = "0.0.0.0"`, "environments.api_listen"},
		{"api port 70000", `api_listen = "0.0.0.0:70000"`, "environments.api_listen"},
		{"public_name with a port", `public_name = "ws.example.com:8884"`, "environments.public_name"},
		{"public_name with a leading hyphen", `public_name = "-ws.example.com"`, "environments.public_name"},
		{"public_name with an empty label", `public_name = "ws..example.com"`, "environments.public_name"},
		{"renew_before_days 0", `renew_before_days = 0`, "environments.renew_before_days"},
		{"renew_before_days 61", `renew_before_days = 61`, "environments.renew_before_days"},
		{"default_scope with an empty entry", `default_scope = [""]`, "environments.default_scope"},
		{"default_scope with a space", `default_scope = ["win rig"]`, "environments.default_scope"},
		{"default_scope with a comma", `default_scope = ["a,b"]`, "environments.default_scope"},
		{"default_scope with a control character", `default_scope = ["a\tb"]`, "environments.default_scope"},
		{"default_scope with a duplicate", `default_scope = ["local", "local"]`, "environments.default_scope"},
		{"default_scope with a 65-byte entry", `default_scope = ["` + strings.Repeat("a", 65) + `"]`, "environments.default_scope"},
	}
	for _, tt := range bad {
		t.Run(tt.name+" is ErrInvalid", func(t *testing.T) {
			_, err := parse([]byte("[environments]\n" + tt.line + "\n"))
			require.ErrorIs(t, err, ErrInvalid)
			assert.ErrorContains(t, err, tt.key)
		})
	}
	t.Run("unknown key is ErrInvalid", func(t *testing.T) {
		_, err := parse([]byte("[environments]\nlisten_addr = \"0.0.0.0:1\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, "environments.listen_addr")
	})
}

func TestConfig_Claude(t *testing.T) {
	t.Run("defaults to auto", func(t *testing.T) {
		cfg, err := parse([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, ClaudeConfigAuto, cfg.Claude.Mode())
	})
	for _, mode := range []string{ClaudeConfigAuto, ClaudeConfigWorkspace, ClaudeConfigShared} {
		t.Run("reads "+mode, func(t *testing.T) {
			cfg, err := parse([]byte("[claude]\nconfig_dir = \"" + mode + "\"\n"))
			require.NoError(t, err)
			assert.Equal(t, mode, cfg.Claude.Mode())
		})
	}
	t.Run("an invalid mode is ErrInvalid", func(t *testing.T) {
		_, err := parse([]byte("[claude]\nconfig_dir = \"bogus\"\n"))
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, `claude.config_dir must be auto, workspace or shared, not "bogus"`)
	})
}

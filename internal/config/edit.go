package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
)

// ErrKeyUnknown marks a key that isn't one of ws config's keys.
var ErrKeyUnknown = errors.New("unknown config key")

// ErrValueInvalid marks a value that breaks its key's rule.
var ErrValueInvalid = errors.New("invalid config value")

// ErrUneditable marks a file that sets the key in a form Set and Unset
// don't edit, such as a dotted key or an inline table.
var ErrUneditable = errors.New("config key can't be edited in place")

// Type is a setting's value type, as ws config --json prints it.
type Type string

// The value types ws config --json prints.
const (
	TypeString   Type = "string"
	TypeStrings  Type = "strings"
	TypeDuration Type = "duration"
	TypeBool     Type = "bool"
	TypeHosts    Type = "hosts"
)

// Setting is one key of config.toml, as ws config lists it. Value is the
// effective value: the file's, or Default. Value and Default are string,
// []string, bool, or []Host; a duration is its short string ("1h30m").
type Setting struct {
	Key     string `json:"key"`
	Type    Type   `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Set     bool   `json:"set"`
}

// keyRule is one of ws config's keys: the table that holds it and its leaf.
type keyRule struct {
	key   string
	typ   Type
	table string
	leaf  string
}

// Settings parses data as config.toml, with the same checks as Load, and
// returns every key in docs/tools/ws-config.md order: defaults.host,
// defaults.owners, hosts, review.timeout, review.remove_finished, then .when
// and .model for each [review.profiles.<name>] table, sorted by name.
func Settings(data []byte) ([]Setting, error) {
	cfg, meta, err := load(data)
	if err != nil {
		return nil, err
	}
	var settings []Setting
	for _, rule := range fixedRules() {
		settings = append(settings, settingFor(rule, &cfg, meta))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Review.Profiles)) {
		for _, leaf := range []string{"when", "model"} {
			settings = append(settings, settingFor(profileRule(name, leaf), &cfg, meta))
		}
	}
	return settings, nil
}

// Lookup returns key's Setting in data. A well-formed profile key whose
// table isn't in data has Set false and the default. An unknown key
// wraps ErrKeyUnknown.
func Lookup(data []byte, key string) (Setting, error) {
	rule, err := parseKey(key)
	if err != nil {
		return Setting{}, err
	}
	cfg, meta, err := load(data)
	if err != nil {
		return Setting{}, err
	}
	return settingFor(rule, &cfg, meta), nil
}

// Set returns data with key set to values, plus whether the text changed.
// Values are checked and normalized by the key's rule (ErrValueInvalid).
// Only key's lines change (see docs/tools/ws-config.md), except that hosts
// rewrites its whole list (rewriteHosts). The result is parsed again and
// must decode to the normalized value, or Set returns ErrUneditable.
func Set(data []byte, key string, values []string) ([]byte, bool, error) {
	rule, err := parseKey(key)
	if err != nil {
		return nil, false, err
	}
	if rule.typ == TypeHosts {
		return replaceHosts(data, values)
	}
	want, err := normalize(rule, values)
	if err != nil {
		return nil, false, err
	}
	if rule.key == "defaults.host" && hostsListSet(data) {
		return nil, false, fmt.Errorf("%w: defaults.host can't be set while [[hosts]] is; use ws config set hosts", ErrValueInvalid)
	}
	lines, err := setLines(splitLines(data), rule, rule.leaf+" = "+encode(want))
	if err != nil {
		return nil, false, err
	}
	out := []byte(strings.Join(lines, ""))
	if err := verifySet(out, rule, want); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// Unset returns data without key, plus whether the text changed. A key
// that isn't set leaves data as it is. A [review.profiles.<name>] table
// left with no keys loses its header line too. Unset of hosts removes
// every [[hosts]] table.
func Unset(data []byte, key string) ([]byte, bool, error) {
	rule, err := parseUnsetKey(key)
	if err != nil {
		return nil, false, err
	}
	if rule.typ == TypeHosts {
		return replaceHosts(data, nil)
	}
	lines := splitLines(data)
	sp, found, err := findSpan(lines, rule)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return data, false, nil
	}
	lines = dropEmptyProfile(slices.Delete(lines, sp.first, sp.last+1), rule.table)
	out := []byte(strings.Join(lines, ""))
	cfg, meta, err := load(out)
	if err != nil || settingFor(rule, &cfg, meta).Set {
		return nil, false, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
	}
	return out, !bytes.Equal(data, out), nil
}

// WriteFile replaces path with data atomically. It follows a symlink at
// path, writes a temp file in the target's directory, syncs it, gives it
// the old file's mode (0o644 if there is none), and renames it into place.
func WriteFile(path string, data []byte) error {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	mode, err := fileMode(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := writeSynced(tmp, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// FormatDuration is d in short form: "1h30m", "1h", "45s", "0s".
func FormatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// fixedRules are the keys that always exist in ws config.
func fixedRules() []keyRule {
	return []keyRule{
		{key: "defaults.host", typ: TypeString, table: "defaults", leaf: "host"},
		{key: "defaults.owners", typ: TypeStrings, table: "defaults", leaf: "owners"},
		{key: "hosts", typ: TypeHosts, table: "", leaf: "hosts"},
		{key: "review.timeout", typ: TypeDuration, table: "review", leaf: "timeout"},
		{key: "review.remove_finished", typ: TypeBool, table: "review", leaf: "remove_finished"},
	}
}

// profileRule is review.profiles.<name>.<leaf>.
func profileRule(name, leaf string) keyRule {
	table := "review.profiles." + name
	return keyRule{key: table + "." + leaf, typ: TypeString, table: table, leaf: leaf}
}

// parseKey returns the rule for key, or an error wrapping ErrKeyUnknown.
func parseKey(key string) (keyRule, error) {
	return parseKeyWith(key, profileNameValid)
}

// parseUnsetKey is parseKey, but accepts any profile name, so unset can
// clean up a hand-written profile whose name Set refuses.
func parseUnsetKey(key string) (keyRule, error) {
	return parseKeyWith(key, func(name string) bool { return name != "" })
}

// parseKeyWith is parseKey with the rule for profile names passed in.
func parseKeyWith(key string, nameOK func(string) bool) (keyRule, error) {
	for _, rule := range fixedRules() {
		if rule.key == key {
			return rule, nil
		}
	}
	if rest, ok := strings.CutPrefix(key, "review.profiles."); ok {
		name, leaf, found := strings.Cut(rest, ".")
		if found && nameOK(name) && (leaf == "when" || leaf == "model") {
			return profileRule(name, leaf), nil
		}
	}
	return keyRule{}, fmt.Errorf("%w: %s", ErrKeyUnknown, key)
}

// profileNameValid reports whether name matches ^[a-z0-9-]+$.
func profileNameValid(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool {
		return r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

// load parses data the way Load does and also returns its MetaData, which
// tells which keys the file sets.
func load(data []byte) (Config, *toml.MetaData, error) {
	cfg, err := parse(data)
	if err != nil {
		return Config{}, nil, err
	}
	meta, err := toml.Decode(string(data), &Config{})
	if err != nil {
		return Config{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return cfg, &meta, nil
}

// settingFor is rule's Setting for a parsed file.
func settingFor(rule keyRule, cfg *Config, meta *toml.MetaData) Setting {
	set := meta.IsDefined(strings.Split(rule.key, ".")...)
	if rule.typ == TypeHosts {
		set = set && len(cfg.Hosts) > 0
	}
	return Setting{
		Key:     rule.key,
		Type:    rule.typ,
		Value:   valueOf(rule, cfg),
		Default: defaultOf(rule),
		Set:     set,
	}
}

// valueOf is rule's effective value in cfg.
func valueOf(rule keyRule, cfg *Config) any {
	switch rule.key {
	case "defaults.host":
		return cfg.EffectiveHosts()[0].Name
	case "defaults.owners":
		return cfg.Defaults.Owners
	case "hosts":
		return cfg.EffectiveHosts()
	case "review.timeout":
		return FormatDuration(cfg.Review.WaitTimeout())
	case "review.remove_finished":
		return cfg.Review.RemovesFinished()
	}
	profile := cfg.Review.Profiles[strings.TrimPrefix(rule.table, "review.profiles.")]
	if rule.leaf == "when" {
		return profile.When
	}
	return profile.Model
}

// defaultOf is rule's value when the file doesn't set it.
func defaultOf(rule keyRule) any {
	switch rule.key {
	case "defaults.host":
		return defaultHost
	case "defaults.owners":
		return []string{}
	case "hosts":
		return []Host{{Name: defaultHost, Kind: KindAuto}}
	case "review.timeout":
		return FormatDuration(defaultReviewTimeout)
	case "review.remove_finished":
		return true
	}
	return ""
}

// verifySet checks that out sets rule's key to want.
func verifySet(out []byte, rule keyRule, want any) error {
	s, err := Lookup(out, rule.key)
	if err != nil || !s.Set || !reflect.DeepEqual(s.Value, want) {
		return fmt.Errorf("%w: %s", ErrUneditable, rule.key)
	}
	return nil
}

// normalize checks values against rule and returns the value Settings
// reads back for them.
func normalize(rule keyRule, values []string) (any, error) {
	for _, value := range values {
		if err := checkText(value); err != nil {
			return nil, err
		}
	}
	if rule.key == "defaults.owners" {
		return normalizeOwners(values)
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: %s takes one value, not %d", ErrValueInvalid, rule.key, len(values))
	}
	return normalizeOne(rule, values[0])
}

// checkText rejects text that can't be written inside a TOML basic string.
func checkText(value string) error {
	if strings.ContainsAny(value, `"\`) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %q has a quote, backslash, or control character", ErrValueInvalid, value)
	}
	return nil
}

// normalizeOwners lower-cases owners and drops repeats, keeping the first.
func normalizeOwners(values []string) ([]string, error) {
	owners := []string{}
	for _, value := range values {
		owner := strings.ToLower(value)
		if err := validateOwner(owner); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrValueInvalid, err)
		}
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	return owners, nil
}

// normalizeOne checks the single value of a key other than owners.
func normalizeOne(rule keyRule, value string) (any, error) {
	switch rule.key {
	case "defaults.host":
		return normalizeHost(value)
	case "review.timeout":
		return normalizeTimeout(value)
	case "review.remove_finished":
		return normalizeBool(value)
	}
	if rule.leaf == "when" {
		return normalizeWhen(value)
	}
	return normalizeModel(value)
}

func normalizeHost(value string) (string, error) {
	host := strings.ToLower(value)
	if err := validateHost(host); err != nil {
		return "", fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return host, nil
}

func normalizeTimeout(value string) (string, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("%w: review.timeout %q: %w", ErrValueInvalid, value, err)
	}
	if d <= 0 {
		return "", fmt.Errorf("%w: review.timeout %q must be greater than zero", ErrValueInvalid, value)
	}
	return FormatDuration(d), nil
}

func normalizeBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%w: review.remove_finished must be true or false, not %q", ErrValueInvalid, value)
}

func normalizeWhen(value string) (string, error) {
	switch value {
	case "always", "auto", "off":
		return value, nil
	}
	return "", fmt.Errorf("%w: when must be always, auto, or off, not %q", ErrValueInvalid, value)
}

func normalizeModel(value string) (string, error) {
	if strings.ContainsFunc(value, unicode.IsSpace) {
		return "", fmt.Errorf("%w: model %q must not contain spaces", ErrValueInvalid, value)
	}
	return value, nil
}

// parseHostValues reads the NAME or NAME=KIND values of ws config set hosts.
func parseHostValues(values []string) ([]Host, error) {
	hosts := make([]Host, 0, len(values))
	for _, value := range values {
		if err := checkText(value); err != nil {
			return nil, err
		}
		name, kind, found := strings.Cut(value, "=")
		if !found {
			kind = KindAuto
		}
		hosts = append(hosts, Host{Name: name, Kind: kind})
	}
	normalized, err := normalizeHosts(hosts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValueInvalid, err)
	}
	return normalized, nil
}

// replaceHosts is Set and Unset for hosts: values become the whole list, and
// none removes it. A file that doesn't set hosts stays as it is for none.
func replaceHosts(data []byte, values []string) ([]byte, bool, error) {
	want, err := parseHostValues(values)
	if err != nil {
		return nil, false, err
	}
	_, meta, err := load(data)
	if err != nil {
		return nil, false, err
	}
	if len(want) == 0 && !meta.IsDefined("hosts") {
		return data, false, nil
	}
	lines, err := rewriteHosts(splitLines(data), want)
	if err != nil {
		return nil, false, err
	}
	out := []byte(strings.Join(lines, ""))
	if err := verifyHosts(out, want); err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(data, out), nil
}

// hostsListSet reports whether data sets [[hosts]] with at least one entry.
func hostsListSet(data []byte) bool {
	cfg, _, err := load(data)
	return err == nil && len(cfg.Hosts) > 0
}

// rewriteHosts returns lines with every [[hosts]] table replaced by hosts,
// written as one block where the first table was. defaults.host is removed
// too, and comment lines are kept (reference-hosts.md).
func rewriteHosts(lines []string, hosts []Host) ([]string, error) {
	if err := checkHostsForm(lines); err != nil {
		return nil, err
	}
	drop, err := dropMarks(lines)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(lines))
	at := -1
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
			continue
		}
		if at < 0 && isHostsHeader(lineText(line)) {
			at = len(kept)
		}
	}
	if len(hosts) == 0 {
		return kept, nil
	}
	eol := newline(lines)
	block := hostsBlock(hosts, eol)
	if at < 0 {
		return appendBlock(kept, block, eol), nil
	}
	return insertBlock(kept, at, block, eol), nil
}

// checkHostsForm rejects hosts set in a form rewriteHosts can't edit, such
// as an inline hosts = [...].
func checkHostsForm(lines []string) error {
	meta, err := toml.Decode(strings.Join(lines, ""), &Config{})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !meta.IsDefined("hosts") || slices.ContainsFunc(lines, func(line string) bool { return isHostsHeader(lineText(line)) }) {
		return nil
	}
	return fmt.Errorf("%w: hosts", ErrUneditable)
}

// dropMarks marks the lines rewriteHosts removes: each [[hosts]] header,
// each key of a hosts table with all the lines of its value, and the
// defaults.host line.
func dropMarks(lines []string) ([]bool, error) {
	names := sectionNames(lines)
	drop := make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		text := lineText(lines[i])
		if isHostsHeader(text) {
			drop[i] = true
			continue
		}
		if names[i] != "[hosts" || keyName(text) == "" {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return nil, fmt.Errorf("%w: hosts", ErrUneditable)
		}
		markRange(drop, sp.first, sp.last)
		i = sp.last
	}
	sp, found, err := findSpan(lines, keyRule{key: "defaults.host", table: "defaults", leaf: "host"})
	if err != nil {
		return nil, err
	}
	if found {
		markRange(drop, sp.first, sp.last)
	}
	return drop, nil
}

// markRange sets drop from first through last.
func markRange(drop []bool, first, last int) {
	for i := first; i <= last; i++ {
		drop[i] = true
	}
}

// isHostsHeader reports whether text is a [[hosts]] header, spaces and tabs
// aside.
func isHostsHeader(text string) bool {
	return strings.HasPrefix(withoutBlanks(text), "[[hosts]]")
}

// hostsBlock is hosts as [[hosts]] tables, with a blank line between them.
func hostsBlock(hosts []Host, eol string) []string {
	var block []string
	for i, host := range hosts {
		if i > 0 {
			block = append(block, eol)
		}
		block = append(block, "[[hosts]]"+eol, "name = "+quote(host.Name)+eol, "kind = "+quote(host.Kind)+eol)
	}
	return block
}

// insertBlock puts block at index at of kept, with a blank line after it
// when a non-blank line follows.
func insertBlock(kept []string, at int, block []string, eol string) []string {
	if at > 0 && lineEnding(kept[at-1]) == "" {
		kept[at-1] += eol
	}
	out := slices.Insert(kept, at, block...)
	after := at + len(block)
	if after < len(out) && strings.TrimSpace(lineText(out[after])) != "" {
		out = slices.Insert(out, after, eol)
	}
	return out
}

// appendBlock adds block at the end of kept, after one blank line, the way
// appendTable adds a table.
func appendBlock(kept, block []string, eol string) []string {
	if len(kept) == 0 {
		return block
	}
	if lineEnding(kept[len(kept)-1]) == "" {
		kept[len(kept)-1] += eol
	}
	kept = append(kept, eol)
	return append(kept, block...)
}

// verifyHosts checks that out sets hosts to want and doesn't set defaults.host.
func verifyHosts(out []byte, want []Host) error {
	hosts, err := Lookup(out, "hosts")
	if err != nil || hosts.Set != (len(want) > 0) || (hosts.Set && !reflect.DeepEqual(hosts.Value, want)) {
		return fmt.Errorf("%w: hosts", ErrUneditable)
	}
	host, err := Lookup(out, "defaults.host")
	if err != nil || host.Set {
		return fmt.Errorf("%w: hosts", ErrUneditable)
	}
	return nil
}

// encode is value as TOML text.
func encode(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case []string:
		quoted := make([]string, 0, len(v))
		for _, s := range v {
			quoted = append(quoted, quote(s))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	return quote(fmt.Sprint(value))
}

func quote(s string) string {
	return `"` + s + `"`
}

// splitLines splits data after each newline, keeping the line endings.
func splitLines(data []byte) []string {
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineText is line without its line ending.
func lineText(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// lineEnding is line's line ending, or "" for a last line without one.
func lineEnding(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	}
	return ""
}

// newline is "\r\n" when lines use CRLF, and "\n" otherwise.
func newline(lines []string) string {
	if slices.ContainsFunc(lines, func(line string) bool { return strings.HasSuffix(line, "\r\n") }) {
		return "\r\n"
	}
	return "\n"
}

// headerName returns the table a header line names, with spaces and tabs
// removed, and whether text is a header at all.
func headerName(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	inner, _, _ := strings.Cut(trimmed[1:], "]")
	return withoutBlanks(inner), true
}

// withoutBlanks is text with spaces and tabs removed.
func withoutBlanks(text string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
}

// keyName is the key a "key = value" line sets, dotted names included, or ""
// when text isn't such a line.
func keyName(text string) string {
	name, _, found := strings.Cut(strings.TrimLeft(text, " \t"), "=")
	name = strings.TrimRight(name, " \t")
	if !found || name == "" || strings.ContainsFunc(name, notBareKeyRune) {
		return ""
	}
	return name
}

func notBareKeyRune(r rune) bool {
	return r != '_' && r != '-' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// sectionNames is the table each line belongs to. A header line belongs to
// the table it names, and lines before the first header to "".
func sectionNames(lines []string) []string {
	names := make([]string, len(lines))
	current := ""
	for i, line := range lines {
		if name, ok := headerName(lineText(line)); ok {
			current = name
		}
		names[i] = current
	}
	return names
}

// lastHeader is the index of the last header line for table, or -1.
func lastHeader(lines []string, table string) int {
	found := -1
	for i, line := range lines {
		if name, ok := headerName(lineText(line)); ok && name == table {
			found = i
		}
	}
	return found
}

// span is the lines of one key, and the comment after its value.
type span struct {
	first   int
	last    int
	comment string
}

// findSpan returns the lines of rule's key, and whether the file sets it.
func findSpan(lines []string, rule keyRule) (span, bool, error) {
	names := sectionNames(lines)
	for i, line := range lines {
		if names[i] != rule.table || keyName(lineText(line)) != rule.leaf {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return span{}, false, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
		}
		return sp, true, nil
	}
	return span{}, false, nil
}

// spanAt returns the key on line i through the end of its value. It
// reports false for a multi-line string or an array that never closes.
func spanAt(lines []string, i int) (span, bool) {
	value := lineText(lines[i])
	value = value[strings.IndexByte(value, '=')+1:]
	trimmed := strings.TrimLeft(value, " \t")
	if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, "'''") {
		return span{}, false
	}
	isArray := strings.HasPrefix(trimmed, "[")
	depth := 0
	for last := i; last < len(lines); last++ {
		text := lineText(lines[last])
		if last != i {
			value = text
		}
		var comment string
		depth, comment = scanValueLine(value, depth)
		if !isArray || depth == 0 {
			return span{first: i, last: last, comment: comment}, true
		}
	}
	return span{}, false
}

// scanValueLine returns the bracket depth after text, skipping strings, and
// the comment that ends text, if any.
func scanValueLine(text string, depth int) (int, string) {
	for j := 0; j < len(text); j++ {
		switch text[j] {
		case '"', '\'':
			j = closingQuote(text, j)
		case '#':
			return depth, text[j:]
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return depth, ""
}

// closingQuote is the index of the quote that closes the string opened at
// open. Only basic strings, with double quotes, have escapes.
func closingQuote(text string, open int) int {
	for j := open + 1; j < len(text); j++ {
		switch {
		case text[open] == '"' && text[j] == '\\':
			j++
		case text[j] == text[open]:
			return j
		}
	}
	return len(text)
}

// setLines returns lines with rule's key set to assignment, which is
// "leaf = value".
func setLines(lines []string, rule keyRule, assignment string) ([]string, error) {
	sp, found, err := findSpan(lines, rule)
	if err != nil {
		return nil, err
	}
	if found {
		return replaceSpan(lines, sp, assignment), nil
	}
	if lastHeader(lines, rule.table) >= 0 {
		return insertInTable(lines, rule, assignment)
	}
	return appendTable(lines, rule, assignment), nil
}

// replaceSpan replaces sp's lines with one line of assignment, keeping the
// indent and the trailing comment.
func replaceSpan(lines []string, sp span, assignment string) []string {
	first := lineText(lines[sp.first])
	indent := first[:len(first)-len(strings.TrimLeft(first, " \t"))]
	text := indent + assignment
	if sp.comment != "" {
		text += " " + sp.comment
	}
	return slices.Replace(lines, sp.first, sp.last+1, text+lineEnding(lines[sp.last]))
}

// insertInTable adds assignment after the last key of rule's table, or
// after its header when the table has no keys.
func insertInTable(lines []string, rule keyRule, assignment string) ([]string, error) {
	names := sectionNames(lines)
	header := lastHeader(lines, rule.table)
	at := header + 1
	for i := header + 1; i < len(lines) && names[i] == rule.table; i++ {
		if keyName(lineText(lines[i])) == "" {
			continue
		}
		sp, ok := spanAt(lines, i)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUneditable, rule.key)
		}
		at = sp.last + 1
		i = sp.last
	}
	return insertLine(lines, at, assignment), nil
}

// insertLine inserts text as a line before index at.
func insertLine(lines []string, at int, text string) []string {
	eol := newline(lines)
	if lineEnding(lines[at-1]) == "" {
		lines[at-1] += eol
	}
	return slices.Insert(lines, at, text+eol)
}

// appendTable adds rule's table at the end of the file, with assignment.
func appendTable(lines []string, rule keyRule, assignment string) []string {
	eol := newline(lines)
	if n := len(lines); n > 0 {
		if lineEnding(lines[n-1]) == "" {
			lines[n-1] += eol
		}
		lines = append(lines, eol)
	}
	return append(lines, "["+rule.table+"]"+eol, assignment+eol)
}

// dropEmptyProfile removes the headers of a review profile table that has
// no keys left, with one blank line before each. Comments are kept.
func dropEmptyProfile(lines []string, table string) []string {
	if !strings.HasPrefix(table, "review.profiles.") || tableHasKeys(lines, table) {
		return lines
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if name, ok := headerName(lineText(line)); ok && name == table {
			if n := len(kept); n > 0 && strings.TrimSpace(lineText(kept[n-1])) == "" {
				kept = kept[:n-1]
			}
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// tableHasKeys reports whether any key line belongs to table.
func tableHasKeys(lines []string, table string) bool {
	names := sectionNames(lines)
	for i, line := range lines {
		if names[i] == table && keyName(lineText(line)) != "" {
			return true
		}
	}
	return false
}

// resolveTarget is path with any symlinks followed. A path that doesn't
// exist is its own target.
func resolveTarget(path string) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return target, nil
}

// fileMode is the permission bits of the file at path, or 0o644 when there
// is no file.
func fileMode(path string) (fs.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0o644, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

// writeSynced writes data to f with mode, syncs it, and closes it.
func writeSynced(f *os.File, data []byte, mode fs.FileMode) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

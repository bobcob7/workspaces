package claude

import (
	"cmp"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Overlay is a workspace's own Claude Code config directory (ADR 0085).
type Overlay struct {
	ConfigDir    string // CONFIG_DIR/state/<id>/claude
	WorkspaceDir string // CONFIG_DIR/workspaces/<id>
}

// maxAgentLists is how many `claude agents` run at once across the overlays.
const maxAgentLists = 8

// Pool is a Client for the base config directory and one per overlay. Each
// call goes to the config directory that owns its session or directory
// (ADR 0085).
type Pool struct {
	base     *Client
	overlays func() ([]Overlay, error)
	mu       sync.Mutex
	// clients is the overlay Client for each config directory, made once.
	clients map[string]*Client
	// routes is the config directory ("" for the base) that lists each
	// session id.
	routes map[string]string
}

// NewPool returns a Pool that routes to base and to the overlays overlays
// lists.
func NewPool(base *Client, overlays func() ([]Overlay, error)) *Pool {
	return &Pool{base: base, overlays: overlays, clients: map[string]*Client{}, routes: map[string]string{}}
}

// client is the Client for config directory dir, or the base when dir is "".
func (p *Pool) client(dir string) *Client {
	if dir == "" {
		return p.base
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.clients[dir]
	if !ok {
		c = p.base.WithConfigDir(dir, nil)
		p.clients[dir] = c
	}
	return c
}

func (p *Pool) setRoute(id, dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[id] = dir
}

func (p *Pool) route(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dir, ok := p.routes[id]
	return dir, ok
}

// overlayListed reports whether config directory dir is the base or an
// overlay that still exists. A route to one that is gone is evicted, so the
// id is looked up again and falls to the base. When the overlays can't be
// listed, the route is kept.
func (p *Pool) overlayListed(dir string) bool {
	if dir == "" {
		return true
	}
	overlays, err := p.overlays()
	if err != nil {
		return true
	}
	if slices.ContainsFunc(overlays, func(o Overlay) bool { return o.ConfigDir == dir }) {
		return true
	}
	p.dropRoutesTo(dir)
	return false
}

// dropRoutesTo forgets the routes to config directory dir.
func (p *Pool) dropRoutesTo(dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, d := range p.routes {
		if d == dir {
			delete(p.routes, id)
		}
	}
}

// clientFor is the Client that runs session id. An unknown id, or one routed
// to an overlay that is gone, is looked up once by listing every session;
// one still unknown is the base's.
func (p *Pool) clientFor(ctx context.Context, id string) *Client {
	if dir, ok := p.route(id); ok && p.overlayListed(dir) {
		return p.client(dir)
	}
	if _, err := p.Agents(ctx, ListOptions{All: true}); err != nil {
		p.base.log.DebugContext(ctx, "listing sessions to route a call", slog.String("id", id), slog.Any("error", err))
		return p.base
	}
	if dir, ok := p.route(id); ok {
		return p.client(dir)
	}
	return p.base
}

// clientForWorkspace is the Client whose config owns dir: the overlay whose
// workspace directory contains dir, else the base.
func (p *Pool) clientForWorkspace(dir string) (*Client, error) {
	overlays, err := p.sortedOverlays()
	if err != nil {
		return nil, err
	}
	want := resolved(dir)
	for _, o := range overlays {
		if withinDir(want, resolved(o.WorkspaceDir)) {
			return p.client(o.ConfigDir), nil
		}
	}
	return p.base, nil
}

// sortedOverlays is the overlays in ConfigDir order.
func (p *Pool) sortedOverlays() ([]Overlay, error) {
	overlays, err := p.overlays()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(overlays, func(a, b Overlay) int {
		return cmp.Compare(a.ConfigDir, b.ConfigDir)
	})
	return overlays, nil
}

// withinDir reports whether dir is root or below it.
func withinDir(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// Agents lists the base's sessions, then each overlay's, at most
// maxAgentLists overlays at a time. A base error is returned; an overlay's
// error is logged and its sessions are left out.
func (p *Pool) Agents(ctx context.Context, opts ListOptions) ([]Session, error) {
	sessions, err := p.base.Agents(ctx, opts)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		p.setRoute(sessions[i].ID, "")
	}
	overlays, err := p.sortedOverlays()
	if err != nil {
		return nil, err
	}
	p.dropRoutesExcept(overlays)
	listed := make([][]Session, len(overlays))
	runLimited(len(overlays), maxAgentLists, func(i int) {
		listed[i] = p.listOverlay(ctx, overlays[i].ConfigDir, opts)
	})
	for _, group := range listed {
		sessions = append(sessions, group...)
	}
	return sessions, nil
}

// dropRoutesExcept forgets the routes to overlays missing from overlays.
func (p *Pool) dropRoutesExcept(overlays []Overlay) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, dir := range p.routes {
		if dir != "" && !slices.ContainsFunc(overlays, func(o Overlay) bool { return o.ConfigDir == dir }) {
			delete(p.routes, id)
		}
	}
}

// listOverlay lists the sessions of the config directory dir, each marked
// with it. An error is logged and gives no sessions.
func (p *Pool) listOverlay(ctx context.Context, dir string, opts ListOptions) []Session {
	sessions, err := p.client(dir).Agents(ctx, opts)
	if err != nil {
		p.base.log.DebugContext(ctx, "listing an overlay's sessions", slog.String("config_dir", dir), slog.Any("error", err))
		return nil
	}
	for i := range sessions {
		sessions[i].ConfigDir = dir
		p.setRoute(sessions[i].ID, dir)
	}
	return sessions
}

// runLimited calls fn for each index in [0, n), at most limit at a time,
// and returns once all have returned.
func runLimited(n, limit int, fn func(i int)) {
	slots := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range n {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			fn(i)
		})
	}
	wg.Wait()
}

// Start starts a session in opts.ConfigDir's config, or the base's when it
// is empty, and routes the new session there.
//
//nolint:gocritic // hugeParam: claudeClient.Start takes StartOptions by value.
func (p *Pool) Start(ctx context.Context, opts StartOptions) (Started, error) {
	started, err := p.client(opts.ConfigDir).Start(ctx, opts)
	if err == nil {
		p.setRoute(started.ID, opts.ConfigDir)
	}
	return started, err
}

// Attach runs `claude attach` for session id in the config that lists it.
func (p *Pool) Attach(ctx context.Context, id string) error {
	return p.clientFor(ctx, id).Attach(ctx, id)
}

// Stop stops session id in the config that lists it.
func (p *Pool) Stop(ctx context.Context, id string) error {
	return p.clientFor(ctx, id).Stop(ctx, id)
}

// Remove removes session id in the config that lists it.
func (p *Pool) Remove(ctx context.Context, id string) error {
	return p.clientFor(ctx, id).Remove(ctx, id)
}

// Respawn respawns session id in the config that lists it.
func (p *Pool) Respawn(ctx context.Context, id string) error {
	return p.clientFor(ctx, id).Respawn(ctx, id)
}

// Logs returns session id's log from the config that lists it.
func (p *Pool) Logs(ctx context.Context, id string) (string, error) {
	return p.clientFor(ctx, id).Logs(ctx, id)
}

// Send sends a message to s in s.ConfigDir's config when it is set, else in
// the config that lists s.ID.
func (p *Pool) Send(ctx context.Context, s *Session, message string) error {
	if s.ConfigDir != "" {
		return p.client(s.ConfigDir).Send(ctx, s, message)
	}
	return p.clientFor(ctx, s.ID).Send(ctx, s, message)
}

// Trust trusts dir in the config of the overlay whose workspace contains
// it, else in the base's.
func (p *Pool) Trust(ctx context.Context, dir, parent string, record func(dir string, created bool) error) error {
	c, err := p.clientForWorkspace(dir)
	if err != nil {
		return err
	}
	return c.Trust(ctx, dir, parent, record)
}

// Untrust undoes Trust for dir in the same config Trust used.
func (p *Pool) Untrust(dir string, created bool) error {
	c, err := p.clientForWorkspace(dir)
	if err != nil {
		return err
	}
	return c.Untrust(dir, created)
}

// StopDaemon stops the base's background service, then each overlay's. It
// tries all of them and returns the first error.
func (p *Pool) StopDaemon(ctx context.Context) error {
	first := p.base.StopDaemon(ctx)
	overlays, err := p.sortedOverlays()
	if err != nil {
		return keepFirst(first, err)
	}
	for _, o := range overlays {
		first = keepFirst(first, p.client(o.ConfigDir).StopDaemon(ctx))
	}
	return first
}

// keepFirst is first when it is set, else err.
func keepFirst(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// Check checks the base's claude (ADR 0024).
func (p *Pool) Check(ctx context.Context) error {
	return p.base.Check(ctx)
}

// SignedIn reports whether claude is signed in with dir and extra (see
// Client.SignedIn). It always uses the base.
func (p *Pool) SignedIn(ctx context.Context, dir string, extra []string) (bool, string, error) {
	return p.base.SignedIn(ctx, dir, extra)
}

// RunDaemon runs the base's background service.
func (p *Pool) RunDaemon(ctx context.Context) (int, error) {
	return p.base.RunDaemon(ctx)
}

// DaemonStatus reports the base's background service.
func (p *Pool) DaemonStatus(ctx context.Context) (DaemonStatus, error) {
	return p.base.DaemonStatus(ctx)
}

// JobExists reports whether the base has session id's directory.
func (p *Pool) JobExists(id string) (bool, error) {
	return p.base.JobExists(id)
}

// UsageLimit is the base's UsageLimit.
func (p *Pool) UsageLimit(s *Session, message string) (*Limit, error) {
	return p.base.UsageLimit(s, message)
}

// Model is the base's Model.
func (p *Pool) Model(s *Session) (string, error) {
	return p.base.Model(s)
}

// Recorded is the base's Recorded.
func (p *Pool) Recorded(s *Session, message string, since time.Time) (bool, error) {
	return p.base.Recorded(s, message, since)
}

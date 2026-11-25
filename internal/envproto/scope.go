package envproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Local is the id of the controller's own environment in a scope.
const Local = "local"

// Operations for environment scopes and machine.record (ADR 0082).
const (
	OpMachineRecord = "machine.record" // controller → agent, Since 4, 30 s
	OpScopeEnvs     = "scope.envs"     // agent → controller (up), 30 s
	OpScopeSpawn    = "scope.spawn"    // up, 10 min
	OpScopeChildren = "scope.children" // up, 30 s
	OpScopeChild    = "scope.child"    // up, 2 min
)

// Size limits of scope arguments (ADR 0082).
const (
	MaxScopeEntries      = 64
	MaxDescendants       = 1024
	MaxRecordReportBytes = 256 << 10
	maxSpawnNames        = 16
	maxSpawnNameBytes    = 32
	maxSpawnVars         = 64
	maxRoleBytes         = 64
	maxEnvNameBytes      = 64
)

// EnvRef names an environment in a scope.
type EnvRef struct {
	ID   string `json:"id"` // "local" or ValidID
	Name string `json:"name"`
}

// WorkspaceAt names a workspace on an environment.
type WorkspaceAt struct {
	Environment string `json:"environment"` // "local" or ValidID
	Workspace   string `json:"workspace"`
}

// ScopeGrant is the scope a child is created with.
type ScopeGrant struct {
	Environments []EnvRef     `json:"environments"` // 1..64, contains the target
	Parent       *WorkspaceAt `json:"parent"`
}

// SpawnInfo is the role a child was spawned with.
type SpawnInfo struct {
	Role  string            `json:"role"`
	Model string            `json:"model"`
	Pass  []string          `json:"pass"`
	Fail  []string          `json:"fail"`
	Vars  map[string]string `json:"vars"`
}

// MachineRecordResult is the result of machine.record.
type MachineRecordResult struct {
	Workspace       string          `json:"workspace"`
	Role            *string         `json:"role"`
	Result          *RecordResult   `json:"result"`
	Report          *string         `json:"report"` // ≤ MaxRecordReportBytes
	ReportTruncated bool            `json:"report_truncated"`
	Progress        json.RawMessage `json:"progress"` // ws machine progress JSON or null
	Rollup          json.RawMessage `json:"rollup"`   // or null
}

// RecordResult is the verdict a workspace recorded.
type RecordResult struct {
	Verdict    string    `json:"verdict"`
	Outcome    string    `json:"outcome"`
	RecordedAt time.Time `json:"recorded_at"`
}

// ScopeCallerArgs are the fields every scope.* request shares. Workspace is
// the caller; nil only for a person grant on the daemon socket.
type ScopeCallerArgs struct {
	Workspace *string  `json:"workspace"`
	Ancestors []string `json:"ancestors"` // nearest first, ≤ 64
	Scope     []string `json:"scope"`     // claimed ids, ≤ 64
}

// ScopeEnvsArgs are the arguments of scope.envs.
type ScopeEnvsArgs struct{ ScopeCallerArgs }

// ScopeSpawnArgs are the arguments of scope.spawn.
type ScopeSpawnArgs struct {
	ScopeCallerArgs
	Target string     `json:"target"` // environment name
	Narrow []string   `json:"narrow"` // names, or null for "same as caller"
	Create CreateArgs `json:"create"` // Scope must be nil; Parent must be nil
	Spawn  *SpawnInfo `json:"spawn"`
}

// ScopeChildrenArgs are the arguments of scope.children.
type ScopeChildrenArgs struct {
	ScopeCallerArgs
	Descendants []string `json:"descendants"` // caller's descendants on its machine, ≤ 1024
}

// ScopeChildArgs are the arguments of scope.child.
type ScopeChildArgs struct {
	ScopeCallerArgs
	Environment  string `json:"environment"` // the child's, a name
	Child        string `json:"child"`
	Parent       string `json:"parent"` // the child's ledger parent (caller or a descendant)
	Op           string `json:"op"`     // "status" | "logs" | "rm" | "record"
	Tail         int    `json:"tail"`
	KeepChildren bool   `json:"keep_children"`
}

// ScopeEnvsResult is the result of scope.envs.
type ScopeEnvsResult struct {
	Environments []ScopeEnv `json:"environments"`
}

// ScopeEnv is one environment in a scope.
type ScopeEnv struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	State   string `json:"state"` // online | offline | removed
	Current bool   `json:"current"`
}

// ScopeChildrenResult is the result of scope.children.
type ScopeChildrenResult struct {
	Children []ScopeChild `json:"children"`
}

// ScopeChild is one cross-environment child in a scope.children result.
type ScopeChild struct {
	Environment string          `json:"environment"` // name
	Workspace   string          `json:"workspace"`
	Parent      string          `json:"parent"`
	Role        *string         `json:"role"`
	Entry       json.RawMessage `json:"entry"` // ws list JSON object from the snapshot, or null
}

// scopeOpSpec returns the rule of a scope operation or machine.record.
// A false second result means name is not one of them.
func scopeOpSpec(name string) (OpSpec, bool) {
	switch name {
	case OpMachineRecord:
		return OpSpec{Name: name, Caller: CallerController, Since: ProtocolScopes, Timeout: 30 * time.Second}, true
	case OpScopeEnvs, OpScopeChildren:
		return OpSpec{Name: name, Caller: CallerAgent, Timeout: 30 * time.Second}, true
	case OpScopeChild:
		return OpSpec{Name: name, Caller: CallerAgent, Timeout: 2 * time.Minute}, true
	case OpScopeSpawn:
		return OpSpec{Name: name, Caller: CallerAgent, Timeout: 10 * time.Minute}, true
	}
	return OpSpec{}, false
}

// newScopeArgs returns an empty argument struct for a scope operation or machine.record.
func newScopeArgs(op string) (any, bool) {
	switch op {
	case OpMachineRecord:
		return &WorkspaceArgs{}, true
	case OpScopeEnvs:
		return &ScopeEnvsArgs{}, true
	case OpScopeSpawn:
		return &ScopeSpawnArgs{}, true
	case OpScopeChildren:
		return &ScopeChildrenArgs{}, true
	case OpScopeChild:
		return &ScopeChildArgs{}, true
	}
	return nil, false
}

// validateScopeArgs checks the arguments of a scope operation, and refuses any other type.
func validateScopeArgs(args any) error {
	switch a := args.(type) {
	case *ScopeEnvsArgs:
		return validateScopeCaller(&a.ScopeCallerArgs)
	case *ScopeSpawnArgs:
		return validateScopeSpawn(a)
	case *ScopeChildrenArgs:
		return validateScopeChildren(a)
	case *ScopeChildArgs:
		return validateScopeChild(a)
	}
	return usageError("not a scope operation")
}

func validateScopeCaller(a *ScopeCallerArgs) error {
	if a.Workspace != nil {
		if err := validateWorkspace(*a.Workspace); err != nil {
			return err
		}
	}
	if err := validatePathElements(a.Ancestors, MaxScopeEntries); err != nil {
		return err
	}
	return validateEnvIDs(a.Scope)
}

func validateScopeSpawn(a *ScopeSpawnArgs) error {
	if err := validateScopeCaller(&a.ScopeCallerArgs); err != nil {
		return err
	}
	if err := validateEnvName(a.Target); err != nil {
		return err
	}
	if len(a.Narrow) > MaxScopeEntries {
		return usageError(fmt.Sprintf("narrow lists at most %d names", MaxScopeEntries))
	}
	for _, name := range a.Narrow {
		if err := validateEnvName(name); err != nil {
			return err
		}
	}
	if a.Create.Scope != nil || a.Create.Parent != nil {
		return usageError("a child's scope and parent are set by ws, not by its create")
	}
	if err := validateCreate(&a.Create); err != nil {
		return err
	}
	if a.Spawn == nil {
		return nil
	}
	return validateSpawnInfo(a.Spawn)
}

func validateScopeChildren(a *ScopeChildrenArgs) error {
	if err := validateScopeCaller(&a.ScopeCallerArgs); err != nil {
		return err
	}
	return validatePathElements(a.Descendants, MaxDescendants)
}

func validateScopeChild(a *ScopeChildArgs) error {
	if err := validateScopeCaller(&a.ScopeCallerArgs); err != nil {
		return err
	}
	if err := validateEnvName(a.Environment); err != nil {
		return err
	}
	if err := validatePathElement(a.Child); err != nil {
		return err
	}
	if err := validatePathElement(a.Parent); err != nil {
		return err
	}
	switch a.Op {
	case "status", "logs", "rm", "record":
	default:
		return usageError("op must be status, logs, rm or record")
	}
	if a.Tail < 0 || a.Tail > MaxTail {
		return usageError(fmt.Sprintf("tail must be 0 to %d", MaxTail))
	}
	return nil
}

// validateScopeGrant checks the scope a child is created with: 1 to 64 refs, each with a valid id and a name.
func validateScopeGrant(grant *ScopeGrant) error {
	if len(grant.Environments) < 1 || len(grant.Environments) > MaxScopeEntries {
		return usageError(fmt.Sprintf("a scope has 1 to %d environments", MaxScopeEntries))
	}
	for _, ref := range grant.Environments {
		if !validEnvID(ref.ID) {
			return usageError("a scope environment id is local or a valid environment id")
		}
		if ref.Name == "" {
			return usageError("a scope environment needs a name")
		}
	}
	if grant.Parent == nil {
		return nil
	}
	if !validEnvID(grant.Parent.Environment) {
		return usageError("a scope parent environment is local or a valid environment id")
	}
	return validatePathElement(grant.Parent.Workspace)
}

// validateSpawnInfo checks a child's role: a role of 1 to 64 bytes, up to 16 pass and fail entries of at most 32 bytes, up to 64 vars, and a model that is valid if given.
func validateSpawnInfo(s *SpawnInfo) error {
	if s.Role == "" || len(s.Role) > maxRoleBytes {
		return usageError(fmt.Sprintf("a role is 1 to %d bytes", maxRoleBytes))
	}
	if err := validateSpawnNames(s.Pass); err != nil {
		return err
	}
	if err := validateSpawnNames(s.Fail); err != nil {
		return err
	}
	if len(s.Vars) > maxSpawnVars {
		return usageError(fmt.Sprintf("at most %d vars", maxSpawnVars))
	}
	if s.Model != "" && !modelPattern.MatchString(s.Model) {
		return usageError("model " + s.Model + " is not a valid model name")
	}
	return nil
}

func validateSpawnNames(names []string) error {
	if len(names) > maxSpawnNames {
		return usageError(fmt.Sprintf("at most %d entries", maxSpawnNames))
	}
	for _, name := range names {
		if len(name) > maxSpawnNameBytes {
			return usageError(fmt.Sprintf("an entry is at most %d bytes", maxSpawnNameBytes))
		}
	}
	return nil
}

// validateEnvIDs checks a list of claimed ids: at most 64, each local or a valid environment id.
func validateEnvIDs(ids []string) error {
	if len(ids) > MaxScopeEntries {
		return usageError(fmt.Sprintf("a scope lists at most %d environments", MaxScopeEntries))
	}
	for _, id := range ids {
		if !validEnvID(id) {
			return usageError("a scope id is local or a valid environment id")
		}
	}
	return nil
}

func validEnvID(id string) bool {
	return id == Local || ValidID(id)
}

// validateEnvName checks an environment name: 1 to 64 bytes, no control characters.
func validateEnvName(name string) error {
	if name == "" || len(name) > maxEnvNameBytes || strings.ContainsFunc(name, unicode.IsControl) {
		return usageError(fmt.Sprintf("an environment name is 1 to %d bytes without control characters", maxEnvNameBytes))
	}
	return nil
}

// validatePathElements checks a list of workspace ids, at most limit of them.
func validatePathElements(refs []string, limit int) error {
	if len(refs) > limit {
		return usageError(fmt.Sprintf("at most %d workspace ids", limit))
	}
	for _, ref := range refs {
		if err := validatePathElement(ref); err != nil {
			return err
		}
	}
	return nil
}

// validatePathElement checks a workspace id that becomes a path element: no
// slash, no backslash and no leading dot.
func validatePathElement(ref string) error {
	if err := validateWorkspace(ref); err != nil {
		return err
	}
	if strings.ContainsAny(ref, `/\`) || strings.HasPrefix(ref, ".") {
		return usageError("a workspace id is one path element: no slash, no backslash, no leading dot")
	}
	return nil
}

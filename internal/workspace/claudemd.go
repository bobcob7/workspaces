package workspace

import (
	_ "embed"
	"path/filepath"
)

// claudeMDName is the file under workspaces/ that points agents to ws llm.
// Claude Code loads CLAUDE.md from the directories above where it starts,
// so every session in a workspace sees it (ADR 0014).
const claudeMDName = "CLAUDE.md"

// claudeMD is workspaces/CLAUDE.md's contents. ws writes it once and never
// overwrites it, so a person can edit it. The source isn't named CLAUDE.md,
// so Claude Code doesn't load it while working on ws.
//
//go:embed claudemd.md
var claudeMD []byte

// writeClaudeMD writes workspaces/CLAUDE.md if it's missing. A failure is
// a warning: workspaces work without it.
func (s *Store) writeClaudeMD() {
	path := filepath.Join(s.root, claudeMDName)
	if err := s.writeOnce(path, claudeMD); err != nil {
		s.warn("couldn't write %s, which tells agents to run `ws llm`: %v", path, err)
	}
}

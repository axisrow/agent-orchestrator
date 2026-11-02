package opencode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

const (
	// opencodePluginDirName is opencode's workspace config directory. OpenCode
	// discovers workspace skills under `.opencode/skills/<name>/SKILL.md`; the
	// v2 adapter's plugin file lives under the same tree.
	opencodePluginDirName = ".opencode"

	// opencodeSkillSubDir is where opencode discovers project skills
	// (`.opencode/skills/<name>/SKILL.md`). AO materializes the using-ao skill
	// here so opencode's native `skill` tool can see it — the data-dir install
	// alone is invisible to that discovery path.
	opencodeSkillSubDir = "skills"

	// opencodeSkillMarkerFile lives beside the skill directory (not inside it) so
	// Materialize's RemoveAll of using-ao/ cannot erase ownership mid-install.
	// Install overwrites and uninstall deletes only when this marker is present.
	opencodeSkillMarkerFile = ".using-ao.ao-managed"

	// opencodeSkillSentinel is written into the marker file. Keep it distinct
	// from any plugin sentinel so ownership checks stay file-specific.
	opencodeSkillSentinel = "agent-orchestrator: managed opencode using-ao skill"
)

func opencodeSkillDir(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodeSkillSubDir, skillassets.SkillName)
}

func opencodeSkillsDir(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodeSkillSubDir)
}

func opencodeSkillMarkerPath(workspacePath string) string {
	return filepath.Join(opencodeSkillsDir(workspacePath), opencodeSkillMarkerFile)
}

// InstallUsingAOSkill materializes the embedded shared using-ao skill into
// .opencode/skills/using-ao/. It refuses to overwrite a same-named directory
// that is not AO-managed.
func InstallUsingAOSkill(workspacePath string) error {
	skillDir := opencodeSkillDir(workspacePath)
	if info, err := os.Stat(skillDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("refusing to overwrite non-directory at %s — move it so AO can install using-ao", skillDir)
		}
		managed, err := isAOManagedSkill(workspacePath)
		if err != nil {
			return err
		}
		if !managed {
			return fmt.Errorf("refusing to overwrite non-AO skill at %s — move it so AO can install using-ao", skillDir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat skill dir: %w", err)
	}

	skillsParent := opencodeSkillsDir(workspacePath)
	if err := os.MkdirAll(skillsParent, 0o750); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	// Write ownership before Materialize clobbers using-ao/, so a crash mid-tree
	// write leaves a marker that allows the next install attempt to recover.
	if err := hookutil.AtomicWriteFile(opencodeSkillMarkerPath(workspacePath), []byte(opencodeSkillSentinel+"\n"), 0o600); err != nil {
		return fmt.Errorf("write skill marker: %w", err)
	}
	if err := skillassets.Materialize(skillDir); err != nil {
		return fmt.Errorf("materialize using-ao skill: %w", err)
	}
	if err := ensureSkillTreeGitignored(skillDir); err != nil {
		return fmt.Errorf("skill gitignore: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(skillsParent, opencodeSkillMarkerFile); err != nil {
		return fmt.Errorf("skill marker gitignore: %w", err)
	}
	return nil
}

// ensureSkillTreeGitignored writes AO-managed .gitignore files beside every
// file in the skill tree so registry's hook-footprint contract holds: each
// installed path must be ignorable for git worktree teardown.
func ensureSkillTreeGitignored(skillRoot string) error {
	byDir := map[string][]string{}
	err := filepath.WalkDir(skillRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dir := filepath.Dir(path)
		byDir[dir] = append(byDir[dir], filepath.Base(path))
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk skill tree: %w", err)
	}
	for dir, names := range byDir {
		if err := hookutil.EnsureWorkspaceGitignore(dir, names...); err != nil {
			return err
		}
	}
	return nil
}

// UninstallUsingAOSkill removes the shared skill only when its AO marker owns
// it. A missing directory or marker is a no-op.
func UninstallUsingAOSkill(workspacePath string) error {
	managed, err := isAOManagedSkill(workspacePath)
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	if err := os.RemoveAll(opencodeSkillDir(workspacePath)); err != nil {
		return fmt.Errorf("remove skill dir: %w", err)
	}
	markerPath := opencodeSkillMarkerPath(workspacePath)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove skill marker: %w", err)
	}
	return nil
}

// isAOManagedSkill reports whether the AO ownership marker beside the skill
// directory exists. A missing marker yields (false, nil).
func isAOManagedSkill(workspacePath string) (bool, error) {
	data, err := os.ReadFile(opencodeSkillMarkerPath(workspacePath)) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read skill marker: %w", err)
	}
	return strings.Contains(string(data), opencodeSkillSentinel), nil
}

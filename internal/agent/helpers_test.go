package agent

import (
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// testCfg is the settings default with overrides, as a run's snapshot.
func testCfg(mut ...func(*config.AgentConfig)) config.AgentConfig {
	c := config.DefaultAgent()
	for _, m := range mut {
		m(&c)
	}
	return c
}

// runCfg is the snapshot the wire and brain tests run with.
func runCfg(c *config.AgentConfig) {
	c.MaxStep, c.ChatMaxStep, c.MaxRetries = 20, 20, 1
}

func setTargetUnique(r *db.Repository, sessionID uint, originalPath, newPath, fileType string) (string, int64, error) {
	role := db.RoleFile
	if fileType == "directory" {
		role = db.RoleUnit
	}
	if newPath == "" {
		role = db.RoleUndecided
	}
	res, err := r.Decide(sessionID, db.Decision{Path: originalPath, Role: role, NewPath: newPath, Override: true})
	if err != nil {
		return "", 0, err
	}
	return res.NewPath, res.OverriddenCount, nil
}

func ToolNames(kind ToolsetKind) []string {
	names := append([]string(nil), taggingToolNames...)
	if kind == ToolsetMain {
		names = append(names, mainOnlyToolNames...)
	}
	return names
}

func BuildSharedContext(repo *db.Repository, session *model.ScanSession) (string, error) {
	c, err := LoadSharedContext(repo, session)
	if err != nil {
		return "", err
	}
	return RenderSharedContext(c), nil
}

func listChatTurns(t *testing.T, repo *db.Repository, sessionID uint) []model.ChatTurn {
	t.Helper()
	var turns []model.ChatTurn
	err := repo.DB().Where("session_id = ? AND role IN ?", sessionID, []string{db.ChatRoleUser, db.ChatRoleAssistant}).Order("id ASC").Find(&turns).Error
	if err != nil {
		t.Fatal(err)
	}
	return turns
}

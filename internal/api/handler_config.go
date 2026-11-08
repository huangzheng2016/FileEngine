package api

import (
	"encoding/json"
	"net/http"

	"FileEngine/internal/config"

	"github.com/gin-gonic/gin"
)

// configView is the API shape of the config: server and database are
// informational only (no token, no DSN); agent is the editable section.
type configView struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	Database struct {
		Driver string `json:"driver"`
	} `json:"database"`
	Agent config.AgentConfig `json:"agent"`
}

func toConfigView(cfg *config.Config) configView {
	var v configView
	v.Server.Host, v.Server.Port = cfg.Server.Host, cfg.Server.Port
	v.Database.Driver = cfg.Database.Driver
	v.Agent = cfg.Agent
	return v
}

func (s *Server) getConfig(c *gin.Context) {
	cfg := config.Get()
	if cfg == nil {
		internalError(c, errConfigNotLoaded)
		return
	}
	c.JSON(http.StatusOK, toConfigView(cfg))
}

// PUT /config merges a partial {"agent": {...}} into the agent section;
// absent fields keep their values, server/database are ignored (read-only).
func (s *Server) updateConfig(c *gin.Context) {
	var req struct {
		Agent json.RawMessage `json:"agent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请求格式错误")
		return
	}
	if len(req.Agent) == 0 {
		badRequest(c, "仅支持修改 agent 配置")
		return
	}
	cfg, err := config.UpdateAgentJSON(req.Agent)
	if err != nil {
		badRequest(c, "agent 配置格式错误："+err.Error())
		return
	}
	if err := config.Save(); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, toConfigView(cfg))
}

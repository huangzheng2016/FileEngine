package api

import (
	"net/http"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *Server) getPrompt(c *gin.Context) {
	cfg := config.Get()
	prompt := cfg.Agent.SystemPrompt
	if prompt == "" {
		prompt = agent.DefaultSystemPrompt
	}
	c.JSON(http.StatusOK, gin.H{
		"prompt":         prompt,
		"default_prompt": agent.DefaultSystemPrompt,
		"is_custom":      cfg.Agent.SystemPrompt != "",
	})
}

func (s *Server) updatePrompt(c *gin.Context) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := config.Get()
	updated := *cfg
	// If the prompt equals the default, store empty to indicate "use default"
	if req.Prompt == agent.DefaultSystemPrompt {
		updated.Agent.SystemPrompt = ""
	} else {
		updated.Agent.SystemPrompt = req.Prompt
	}
	config.Update(&updated)

	if err := config.Save("config.yaml"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prompt":    req.Prompt,
		"is_custom": updated.Agent.SystemPrompt != "",
	})
}

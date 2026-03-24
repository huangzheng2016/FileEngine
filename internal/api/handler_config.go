package api

import (
	"context"
	"net/http"

	"FileEngine/internal/config"
	modelfactory "FileEngine/internal/model"

	"github.com/gin-gonic/gin"
)

func (s *Server) getConfig(c *gin.Context) {
	cfg := config.Get()
	c.JSON(http.StatusOK, cfg.Sanitized())
}

func (s *Server) updateConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Preserve sensitive fields if masked
	current := config.Get()
	if cfg.Model.APIKey == "****" || cfg.Model.APIKey == "" {
		cfg.Model.APIKey = current.Model.APIKey
	}

	config.Update(&cfg)

	if err := config.Save("config.yaml"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, cfg.Sanitized())
}

func (s *Server) testModel(c *gin.Context) {
	var modelCfg config.ModelConfig
	if err := c.ShouldBindJSON(&modelCfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if modelCfg.APIKey == "****" {
		modelCfg.APIKey = config.Get().Model.APIKey
	}

	_, err := modelfactory.NewChatModel(context.Background(), modelCfg)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

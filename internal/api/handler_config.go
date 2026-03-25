package api

import (
	"net/http"

	"FileEngine/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *Server) getConfig(c *gin.Context) {
	c.JSON(http.StatusOK, config.Get())
}

func (s *Server) updateConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	config.Update(&cfg)

	if err := config.Save("config.yaml"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, config.Get())
}

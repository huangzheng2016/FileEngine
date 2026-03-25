package api

import (
	"context"
	"net/http"
	"strconv"

	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"

	"github.com/gin-gonic/gin"
)

type ModelProviderResponse struct {
	ID          uint    `json:"id"`
	Name        string  `json:"name"`
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	BaseURL     string  `json:"base_url"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func toModelProviderResponse(m *db.ModelProvider) ModelProviderResponse {
	return ModelProviderResponse{
		ID: m.ID, Name: m.Name, Provider: m.Provider, Model: m.Model,
		BaseURL: m.BaseURL, Temperature: m.Temperature, MaxTokens: m.MaxTokens,
		CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: m.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listModelProviders(c *gin.Context) {
	providers, err := s.repo.ListModelProviders()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result := make([]ModelProviderResponse, len(providers))
	for i := range providers {
		result[i] = toModelProviderResponse(&providers[i])
	}
	c.JSON(http.StatusOK, result)
}

type ModelProviderRequest struct {
	Name        string  `json:"name" binding:"required"`
	Provider    string  `json:"provider" binding:"required"`
	APIKey      string  `json:"api_key"`
	Model       string  `json:"model" binding:"required"`
	BaseURL     string  `json:"base_url"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

func (s *Server) createModelProvider(c *gin.Context) {
	var req ModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m := &db.ModelProvider{
		Name: req.Name, Provider: req.Provider, APIKey: req.APIKey,
		Model: req.Model, BaseURL: req.BaseURL,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
	}
	if err := s.repo.CreateModelProvider(m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toModelProviderResponse(m))
}

func (s *Server) getModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	m, err := s.repo.GetModelProvider(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, toModelProviderResponse(m))
}

func (s *Server) updateModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	m, err := s.repo.GetModelProvider(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req ModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m.Name = req.Name
	m.Provider = req.Provider
	m.Model = req.Model
	m.BaseURL = req.BaseURL
	m.Temperature = req.Temperature
	m.MaxTokens = req.MaxTokens
	if req.APIKey != "" && req.APIKey != "****" {
		m.APIKey = req.APIKey
	}
	if err := s.repo.UpdateModelProvider(m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toModelProviderResponse(m))
}

func (s *Server) deleteModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.repo.DeleteModelProvider(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *Server) testModelProvider(c *gin.Context) {
	var req ModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	apiKey := req.APIKey
	if apiKey == "****" && req.Name != "" {
		providers, _ := s.repo.ListModelProviders()
		for _, p := range providers {
			if p.Name == req.Name {
				apiKey = p.APIKey
				break
			}
		}
	}
	testProvider := &db.ModelProvider{
		Provider: req.Provider, APIKey: apiKey, Model: req.Model,
		BaseURL: req.BaseURL, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
	}
	_, err := modelfactory.NewChatModelFromProvider(context.Background(), testProvider)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"

	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

type ModelProviderResponse struct {
	VisionEnabled   bool    `json:"vision_enabled"`
	ThinkingEnabled bool    `json:"thinking_enabled"`
	ID              uint    `json:"id"`
	Name            string  `json:"name"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	BaseURL         string  `json:"base_url"`
	Temperature     float64 `json:"temperature"`
	MaxTokens       int     `json:"max_tokens"`
	ContextWindow   int     `json:"context_window"`
	ReasoningEffort string  `json:"reasoning_effort"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func toModelProviderResponse(m *db.ModelProvider) ModelProviderResponse {
	return ModelProviderResponse{
		ID: m.ID, Name: m.Name, Provider: m.Provider, Model: m.Model,
		BaseURL: m.BaseURL, Temperature: m.Temperature, MaxTokens: m.MaxTokens,
		ContextWindow:   m.EffectiveContextWindow(),
		VisionEnabled:   m.VisionEnabled,
		ThinkingEnabled: m.ThinkingEnabled,
		ReasoningEffort: m.ReasoningEffort,
		CreatedAt:       m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       m.UpdatedAt.Format("2006-01-02T15:04:05Z"),
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
	// The optional fields are pointers: absent (nil) means "not provided" —
	// create applies the default, update keeps the stored value.
	ContextWindow   *int    `json:"context_window"` // tokens; tiers are UI suggestions, any positive value is accepted
	VisionEnabled   *bool   `json:"vision_enabled"`
	ThinkingEnabled *bool   `json:"thinking_enabled"`
	ReasoningEffort *string `json:"reasoning_effort"` // "" = model default; free-form level (low/medium/high/max/...)
}

// reasoningEffortPattern bounds the free-form thinking strength: providers
// keep inventing levels, so only charset/length are checked. "" means "use
// the model default".
var reasoningEffortPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

func resolveReasoningEffort(v *string, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	if *v != "" && !reasoningEffortPattern.MatchString(*v) {
		return "", fmt.Errorf("思考强度只能为空或 1-32 个字母、数字、_、-（如 low、medium、high、max）")
	}
	return *v, nil
}

// resolveContextWindow validates an optional context_window value.
func resolveContextWindow(v *int, fallback int) (int, error) {
	if v == nil {
		return fallback, nil
	}
	if *v <= 0 {
		return 0, fmt.Errorf("上下文长度必须是正整数（token 数）")
	}
	return *v, nil
}

func resolveBool(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func (s *Server) createModelProvider(c *gin.Context) {
	var req ModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	contextWindow, err := resolveContextWindow(req.ContextWindow, db.DefaultContextWindow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m := &db.ModelProvider{
		Name: req.Name, Provider: req.Provider, APIKey: req.APIKey,
		Model: req.Model, BaseURL: req.BaseURL,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		ContextWindow: contextWindow, ReasoningEffort: reasoningEffort,
		VisionEnabled:   resolveBool(req.VisionEnabled, false),
		ThinkingEnabled: resolveBool(req.ThinkingEnabled, false),
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
	contextWindow, err := resolveContextWindow(req.ContextWindow, m.ContextWindow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, m.ReasoningEffort)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m.ContextWindow = contextWindow
	m.ReasoningEffort = reasoningEffort
	m.VisionEnabled = resolveBool(req.VisionEnabled, m.VisionEnabled)
	m.ThinkingEnabled = resolveBool(req.ThinkingEnabled, m.ThinkingEnabled)
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
	if _, err := resolveContextWindow(req.ContextWindow, db.DefaultContextWindow); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	testProvider := &db.ModelProvider{
		Provider: req.Provider, APIKey: apiKey, Model: req.Model,
		BaseURL: req.BaseURL, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		ThinkingEnabled: resolveBool(req.ThinkingEnabled, false), ReasoningEffort: reasoningEffort,
	}
	chatModel, err := modelfactory.NewChatModelFromProvider(c.Request.Context(), testProvider)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	// Send a test message to verify the model actually works
	result, err := chatModel.Generate(c.Request.Context(), []*schema.Message{
		{Role: schema.User, Content: "Hi"},
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	reply := ""
	if result != nil {
		reply = result.Content
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "reply": reply})
}

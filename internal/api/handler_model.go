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
	APIKeySet       bool    `json:"api_key_set"`
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
		APIKeySet:       m.APIKey != "",
		CreatedAt:       m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       m.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listModelProviders(c *gin.Context) {
	providers, err := s.repo.ListModelProviders()
	if err != nil {
		internalError(c, err)
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
	APIKey      *string `json:"api_key"`
	Model       string  `json:"model" binding:"required"`
	BaseURL     string  `json:"base_url"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	// Optional fields are pointers: nil means create applies the default and
	// update keeps the stored value.
	ContextWindow   *int    `json:"context_window"` // tokens; tiers are UI suggestions, any positive value is accepted
	VisionEnabled   *bool   `json:"vision_enabled"`
	ThinkingEnabled *bool   `json:"thinking_enabled"`
	ReasoningEffort *string `json:"reasoning_effort"` // "" = model default; free-form level (low/medium/high/max/...)
}

// Providers keep inventing levels, so only charset and length are checked.
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

func resolveContextWindow(v *int, fallback int) (int, error) {
	if v == nil {
		return fallback, nil
	}
	if *v <= 0 {
		return 0, fmt.Errorf("上下文长度需为正整数")
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
		badBody(c, err)
		return
	}
	contextWindow, err := resolveContextWindow(req.ContextWindow, db.DefaultContextWindow)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, "")
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	m := &db.ModelProvider{
		Name: req.Name, Provider: req.Provider, APIKey: deref(req.APIKey),
		Model: req.Model, BaseURL: req.BaseURL,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		ContextWindow: contextWindow, ReasoningEffort: reasoningEffort,
		VisionEnabled:   resolveBool(req.VisionEnabled, false),
		ThinkingEnabled: resolveBool(req.ThinkingEnabled, false),
	}
	if err := s.repo.CreateModelProvider(m); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toModelProviderResponse(m))
}

func (s *Server) getModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	m, err := s.repo.GetModelProvider(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoModel)
		return
	}
	c.JSON(http.StatusOK, toModelProviderResponse(m))
}

func (s *Server) updateModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	m, err := s.repo.GetModelProvider(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoModel)
		return
	}
	var req ModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}
	if req.APIKey == nil && m.APIKey != "" && !sameModelEndpoint(m, &req) {
		badRequest(c, "服务地址已变化，请重新输入 API Key")
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
		badRequest(c, err.Error())
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, m.ReasoningEffort)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	m.ContextWindow = contextWindow
	m.ReasoningEffort = reasoningEffort
	m.VisionEnabled = resolveBool(req.VisionEnabled, m.VisionEnabled)
	m.ThinkingEnabled = resolveBool(req.ThinkingEnabled, m.ThinkingEnabled)
	if req.APIKey != nil {
		m.APIKey = *req.APIKey
	}
	if err := s.repo.UpdateModelProvider(m); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, toModelProviderResponse(m))
}

func (s *Server) deleteModelProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	n, err := s.repo.CountSessionsUsingModel(uint(id))
	if err != nil {
		internalError(c, err)
		return
	}
	if n > 0 {
		conflict(c, fmt.Sprintf("有 %d 个整理任务在用这个模型，先删除这些任务", n))
		return
	}
	if err := s.repo.DeleteModelProvider(uint(id)); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

type ModelTestRequest struct {
	ModelProviderRequest
	ID uint `json:"id"`
}

// sameModelEndpoint reports whether req still talks to the stored provider's
// endpoint; otherwise a stored key could be sent to an arbitrary base_url.
func sameModelEndpoint(m *db.ModelProvider, req *ModelProviderRequest) bool {
	return m.Provider == req.Provider && m.BaseURL == req.BaseURL
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) testModelProvider(c *gin.Context) {
	var treq ModelTestRequest
	if err := c.ShouldBindJSON(&treq); err != nil {
		badBody(c, err)
		return
	}
	req := treq.ModelProviderRequest
	apiKey := deref(req.APIKey)
	if req.APIKey == nil && treq.ID != 0 {
		stored, err := s.repo.GetModelProvider(treq.ID)
		if err != nil {
			notFound(c, "模型不存在")
			return
		}
		if stored.APIKey != "" && !sameModelEndpoint(stored, &req) {
			badRequest(c, "服务地址已变化，请重新输入 API Key")
			return
		}
		apiKey = stored.APIKey
	}
	if _, err := resolveContextWindow(req.ContextWindow, db.DefaultContextWindow); err != nil {
		badRequest(c, err.Error())
		return
	}
	reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, "")
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	testProvider := &db.ModelProvider{
		Provider: req.Provider, APIKey: apiKey, Model: req.Model,
		BaseURL: req.BaseURL, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		ThinkingEnabled: resolveBool(req.ThinkingEnabled, false), ReasoningEffort: reasoningEffort,
	}
	chatModel, err := modelfactory.NewChatModelFromProvider(c.Request.Context(), testProvider)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": "模型配置无效：" + err.Error()})
		return
	}
	result, err := chatModel.Generate(c.Request.Context(), []*schema.Message{
		{Role: schema.User, Content: "Hi"},
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": "模型调用失败：" + err.Error()})
		return
	}
	reply := ""
	if result != nil {
		reply = result.Content
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "reply": reply})
}

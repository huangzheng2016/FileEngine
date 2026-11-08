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
	// APIKey omitted keeps the stored key on update.
	APIKey      *string `json:"api_key"`
	Model       string  `json:"model" binding:"required"`
	BaseURL     string  `json:"base_url"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	// ContextWindow is optional and additive: old clients omit it (nil),
	// new clients may pass a tier (200K/256K/500K/900K/1M) or a custom value.
	ContextWindow *int `json:"context_window"`
	// VisionEnabled is additive like context_window: absent (nil) means
	// "not provided" — create defaults to false, update keeps the stored value.
	VisionEnabled *bool `json:"vision_enabled"`
	// ReasoningEffort is additive too: free-form pass-through ("" = model
	// default; suggested levels none/low/medium/high/max); absent (nil) keeps
	// the stored value on update, means unset on create.
	ReasoningEffort *string `json:"reasoning_effort"`
}

// reasoningEffortPattern bounds the free-form pass-through: any value an
// OpenAI-compatible backend might accept (suggested levels are
// none/low/medium/high/max, but providers keep inventing new ones — the API
// must not hard-reject values it does not know). "" means "not set — use the
// model default".
var reasoningEffortPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

func resolveReasoningEffort(v *string, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	if *v != "" && !reasoningEffortPattern.MatchString(*v) {
		return "", fmt.Errorf("reasoning_effort must be empty or 1-32 chars of [a-zA-Z0-9_-] (suggested: none, low, medium, high, max)")
	}
	return *v, nil
}

// resolveContextWindow validates an optional context_window value. Lenient by
// design: any positive token count is accepted (tiers are UI suggestions).
func resolveContextWindow(v *int, fallback int) (int, error) {
	if v == nil {
		return fallback, nil
	}
	if *v <= 0 {
		return 0, fmt.Errorf("context_window must be a positive token count")
	}
	return *v, nil
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
		Name: req.Name, Provider: req.Provider, APIKey: deref(req.APIKey),
		Model: req.Model, BaseURL: req.BaseURL,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
		ContextWindow: contextWindow, ReasoningEffort: reasoningEffort,
	}
	if req.VisionEnabled != nil {
		m.VisionEnabled = *req.VisionEnabled
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
	// context_window is additive: absent (nil) keeps the stored value so old
	// clients doing full-form updates never clobber it.
	if req.ContextWindow != nil {
		contextWindow, err := resolveContextWindow(req.ContextWindow, m.ContextWindow)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		m.ContextWindow = contextWindow
	}
	if req.VisionEnabled != nil {
		m.VisionEnabled = *req.VisionEnabled
	}
	if req.ReasoningEffort != nil {
		reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, m.ReasoningEffort)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		m.ReasoningEffort = reasoningEffort
	}
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.repo.DeleteModelProvider(uint(id)); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ModelTestRequest tests a provider. ID names a stored provider whose API key
// may be reused — only when provider and base_url are unchanged.
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
		badRequest(c, "请求格式错误："+err.Error())
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
	testProvider := &db.ModelProvider{
		Provider: req.Provider, APIKey: apiKey, Model: req.Model,
		BaseURL: req.BaseURL, Temperature: req.Temperature, MaxTokens: req.MaxTokens,
	}
	if req.ReasoningEffort != nil {
		reasoningEffort, err := resolveReasoningEffort(req.ReasoningEffort, "")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		testProvider.ReasoningEffort = reasoningEffort
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

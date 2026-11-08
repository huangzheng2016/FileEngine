package api

import (
	"io/fs"
	"net/http"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

type Server struct {
	router   *gin.Engine
	repo     *db.Repository
	frontend fs.FS
}

func NewServer(repo *db.Repository, frontend fs.FS) *Server {
	s := &Server{
		repo:     repo,
		frontend: frontend,
	}
	s.setupRouter()
	return s
}

func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(gin.Logger())

	api := r.Group("/api/v1")
	{
		// Sessions
		api.POST("/sessions", s.createSession)
		api.GET("/sessions", s.listSessions)
		api.GET("/sessions/:id", s.getSession)
		api.DELETE("/sessions/:id", s.deleteSession)
		api.PATCH("/sessions/:id", s.updateSessionConfig)
		api.POST("/sessions/:id/rescan", s.rescanSession)
		api.GET("/sessions/:id/stats", s.getSessionStats)

		// Agent tasks
		api.POST("/sessions/:id/tag", s.startTagging)
		api.POST("/sessions/:id/tag/stop", s.stopTagging)
		api.GET("/sessions/:id/tag/status", s.tagStatus)
		api.POST("/sessions/:id/instruct", s.instructAgent)
		api.POST("/sessions/:id/instruct/stop", s.stopInstruct)
		api.POST("/sessions/:id/execute", s.startExecute)
		api.POST("/sessions/:id/execute/stop", s.stopExecute)
		api.GET("/sessions/:id/execute/status", s.executeStatus)
		api.GET("/sessions/:id/execute/validate", s.validatePlans)
		api.GET("/sessions/:id/plans", s.getPlans)
		api.GET("/sessions/:id/plans/export", s.exportPlans)
		api.POST("/sessions/:id/plans/reset", s.resetPlans)
		api.GET("/sessions/:id/preview/tree", s.previewTree)

		// Session plan (整理方案)
		api.GET("/sessions/:id/plan", s.getPlan)
		api.PUT("/sessions/:id/plan", s.updatePlan)
		api.POST("/sessions/:id/plan/approve", s.approvePlan)

		// Memory
		api.GET("/memory", s.listMemory)
		api.POST("/memory", s.createMemory)
		api.PUT("/memory/:id", s.updateMemory)
		api.DELETE("/memory/:id", s.deleteMemory)

		// Files
		api.GET("/files", s.listFiles)
		api.GET("/files/tree", s.getFileTree)
		api.GET("/files/:id", s.getFile)
		api.PATCH("/files/:id", s.updateFile)
		api.GET("/files/:id/content", s.getFileContent)

		// Categories
		api.GET("/categories", s.listCategories)
		api.POST("/categories", s.createCategory)
		api.PUT("/categories/:id", s.updateCategory)
		api.DELETE("/categories/:id", s.deleteCategory)
		api.GET("/categories/export", s.exportCategories)
		api.POST("/categories/import", s.importCategories)

		// Filesystems
		api.GET("/filesystems", s.listFilesystems)
		api.POST("/filesystems", s.createFilesystem)
		api.GET("/filesystems/:id", s.getFilesystem)
		api.PUT("/filesystems/:id", s.updateFilesystem)
		api.DELETE("/filesystems/:id", s.deleteFilesystem)
		api.POST("/filesystems/test", s.testFilesystemConnection)

		// Model Providers
		api.GET("/models", s.listModelProviders)
		api.POST("/models", s.createModelProvider)
		api.GET("/models/:id", s.getModelProvider)
		api.PUT("/models/:id", s.updateModelProvider)
		api.DELETE("/models/:id", s.deleteModelProvider)
		api.POST("/models/test", s.testModelProvider)

		// Config
		api.GET("/config", s.getConfig)
		api.PUT("/config", s.updateConfig)

		// Prompt
		api.GET("/prompt", s.getPrompt)
		api.PUT("/prompt", s.updatePrompt)

		// Logs
		api.GET("/logs", s.listLogs)

		// Session event stream (SSE): live agent run events
		api.GET("/sessions/:id/events/stream", s.streamSessionEvents)
	}

	// Serve frontend static files
	if s.frontend != nil {
		r.NoRoute(func(c *gin.Context) {
			f, err := http.FS(s.frontend).Open(c.Request.URL.Path)
			if err == nil {
				f.Close()
				http.FileServer(http.FS(s.frontend)).ServeHTTP(c.Writer, c.Request)
				return
			}
			c.FileFromFS("index.html", http.FS(s.frontend))
		})
	}

	s.router = r
}

// Handler returns the complete HTTP surface (REST API, SSE stream and the
// SPA fallback) for an http.Server owned by the caller (internal/app).
func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

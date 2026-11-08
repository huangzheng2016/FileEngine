package api

import (
	"io/fs"
	"net/http"
	"time"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

type Server struct {
	router   *gin.Engine
	repo     *db.Repository
	frontend fs.FS
	opts     Options
	limiter  *rateLimiter
}

func NewServer(repo *db.Repository, frontend fs.FS, opts ...Options) *Server {
	s := &Server{
		repo:     repo,
		frontend: frontend,
		opts:     resolveOptions(opts),
		limiter:  newRateLimiter(10, time.Minute),
	}
	s.setupRouter()
	installRuleHook(repo)
	return s
}

func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())
	r.Use(securityHeaders())
	r.Use(s.hostGuard())

	r.GET("/healthz", s.healthz)

	api := r.Group("/api/v1", s.apiGuard(), bodyLimit())
	{
		api.POST("/auth/login", s.login)
		api.POST("/auth/logout", s.logout)
		api.GET("/auth/me", s.me)
		api.GET("/auth/bootstrap", s.bootstrap)
		api.POST("/admin/backup", s.backup)

		api.POST("/sessions", s.createSession)
		api.GET("/sessions", s.listSessions)
		api.GET("/sessions/:id", s.getSession)
		api.DELETE("/sessions/:id", s.deleteSession)
		api.PATCH("/sessions/:id", s.updateSessionConfig)
		api.POST("/sessions/:id/rescan", s.rescanSession)
		api.GET("/sessions/:id/stats", s.getSessionStats)

		api.POST("/sessions/:id/tag", s.startTagging)
		api.POST("/sessions/:id/chat", s.postChat)
		api.GET("/sessions/:id/chat", s.listChat)
		api.POST("/sessions/:id/execute", s.startExecute)
		api.GET("/sessions/:id/runs", s.listRuns)
		api.GET("/runs/:run_id", s.getRun)
		api.POST("/runs/:run_id/stop", s.stopRun)

		api.GET("/sessions/:id/events", s.listSessionEvents)
		api.GET("/sessions/:id/events/stream", s.streamSessionEvents)

		api.GET("/sessions/:id/execute/check", s.checkExecute)
		api.POST("/runs/:run_id/revert", s.revertRun)
		api.GET("/sessions/:id/locate", s.locateEntries)
		api.GET("/sessions/:id/plans", s.getPlans)
		api.GET("/sessions/:id/plans/export", s.exportPlans)
		api.POST("/sessions/:id/plans/reset", s.resetPlans)
		api.GET("/sessions/:id/preview/tree", s.previewTree)
		api.POST("/sessions/:id/review/confirm", s.confirmReview)

		api.GET("/sessions/:id/rules", s.listRules)
		api.POST("/sessions/:id/rules", s.createRule)
		api.POST("/sessions/:id/rules/apply", s.applyRules)
		api.PUT("/rules/:id", s.updateRule)
		api.DELETE("/rules/:id", s.deleteRule)

		api.GET("/sessions/:id/plan", s.getPlan)
		api.PUT("/sessions/:id/plan", s.updatePlan)
		api.POST("/sessions/:id/plan/approve", s.approvePlan)
		api.POST("/sessions/:id/plan/draft", s.draftPlan)

		api.GET("/memory", s.listMemory)
		api.POST("/memory", s.createMemory)
		api.PUT("/memory/:id", s.updateMemory)
		api.DELETE("/memory/:id", s.deleteMemory)

		api.GET("/files", s.listFiles)
		api.GET("/files/tree", s.getFileTree)
		api.GET("/files/:id", s.getFile)
		api.PATCH("/files/:id", s.updateFile)
		api.GET("/files/:id/content", s.getFileContent)

		api.GET("/categories", s.listCategories)
		api.POST("/categories", s.createCategory)
		api.PUT("/categories/:id", s.updateCategory)
		api.DELETE("/categories/:id", s.deleteCategory)
		api.GET("/categories/export", s.exportCategories)
		api.POST("/categories/import", s.importCategories)

		api.GET("/filesystems", s.listFilesystems)
		api.POST("/filesystems", s.createFilesystem)
		api.GET("/filesystems/:id", s.getFilesystem)
		api.PUT("/filesystems/:id", s.updateFilesystem)
		api.DELETE("/filesystems/:id", s.deleteFilesystem)
		api.POST("/filesystems/test", s.rateLimited("filesystems/test"), s.testFilesystemConnection)

		api.GET("/models", s.listModelProviders)
		api.POST("/models", s.createModelProvider)
		api.GET("/models/:id", s.getModelProvider)
		api.PUT("/models/:id", s.updateModelProvider)
		api.DELETE("/models/:id", s.deleteModelProvider)
		api.POST("/models/test", s.rateLimited("models/test"), s.testModelProvider)

		api.GET("/config", s.getConfig)
		api.PUT("/config", s.updateConfig)

		api.GET("/prompt", s.getPrompt)
		api.PUT("/prompt", s.updatePrompt)
	}

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

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

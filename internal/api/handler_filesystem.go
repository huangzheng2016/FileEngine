package api

import (
	"context"
	"net/http"
	"strconv"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
)

type FilesystemResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Protocol    string `json:"protocol"`
	BasePath    string `json:"base_path"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	KeyPath     string `json:"key_path"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toFilesystemResponse(fs *db.Filesystem) FilesystemResponse {
	return FilesystemResponse{
		ID:          fs.ID,
		Name:        fs.Name,
		Description: fs.Description,
		Protocol:    fs.Protocol,
		BasePath:    fs.BasePath,
		Host:        fs.Host,
		Port:        fs.Port,
		Username:    fs.Username,
		HasPassword: fs.Password != "",
		KeyPath:     fs.KeyPath,
		CreatedAt:   fs.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   fs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listFilesystems(c *gin.Context) {
	fss, err := s.repo.ListFilesystems()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result := make([]FilesystemResponse, len(fss))
	for i := range fss {
		result[i] = toFilesystemResponse(&fss[i])
	}
	c.JSON(http.StatusOK, result)
}

type FilesystemRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Protocol    string `json:"protocol" binding:"required"`
	BasePath    string `json:"base_path" binding:"required"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	KeyPath     string `json:"key_path"`
}

func (s *Server) createFilesystem(c *gin.Context) {
	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fs := &db.Filesystem{
		Name:        req.Name,
		Description: req.Description,
		Protocol:    req.Protocol,
		BasePath:    req.BasePath,
		Host:        req.Host,
		Port:        req.Port,
		Username:    req.Username,
		Password:    req.Password,
		KeyPath:     req.KeyPath,
	}
	if err := s.repo.CreateFilesystem(fs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Auto-create default "未分类" category
	_ = s.repo.CreateCategory(&db.Category{
		FilesystemID: fs.ID,
		Name:         "未分类",
		Path:         "/uncategorized",
		Description:  "Files that don't fit any other category",
	})

	c.JSON(http.StatusCreated, toFilesystemResponse(fs))
}

func (s *Server) getFilesystem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	fs, err := s.repo.GetFilesystem(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "filesystem not found"})
		return
	}
	c.JSON(http.StatusOK, toFilesystemResponse(fs))
}

func (s *Server) updateFilesystem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	fs, err := s.repo.GetFilesystem(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "filesystem not found"})
		return
	}

	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fs.Name = req.Name
	fs.Description = req.Description
	fs.Protocol = req.Protocol
	fs.BasePath = req.BasePath
	fs.Host = req.Host
	fs.Port = req.Port
	fs.Username = req.Username
	fs.KeyPath = req.KeyPath

	// Preserve existing password if client sends "****" or empty
	if req.Password != "" && req.Password != "****" {
		fs.Password = req.Password
	}

	if err := s.repo.UpdateFilesystem(fs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toFilesystemResponse(fs))
}

func (s *Server) deleteFilesystem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.repo.DeleteFilesystem(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *Server) testFilesystemConnection(c *gin.Context) {
	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fsCfg := config.RemoteFSConfig{
		Protocol: req.Protocol,
		BasePath: req.BasePath,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		KeyPath:  req.KeyPath,
	}

	// If password is masked, try to get it from existing filesystem
	if req.Password == "****" {
		// Check if this is an existing filesystem by name
		fss, _ := s.repo.ListFilesystems()
		for _, existing := range fss {
			if existing.Name == req.Name {
				fsCfg.Password = existing.Password
				break
			}
		}
	}

	rfs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rfs.Close()

	if _, err := rfs.List(context.Background(), "."); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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
	PasswordSet bool   `json:"password_set"`
	KeyPath     string `json:"key_path"`
	HostKey     string `json:"host_key"`
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
		PasswordSet: fs.Password != "",
		KeyPath:     fs.KeyPath,
		HostKey:     fs.HostKey,
		CreatedAt:   fs.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   fs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// FilesystemRequest is the create/update/test body. Password and HostKey are
// pointers: omitted means "keep the stored value" on update.
type FilesystemRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Protocol    string  `json:"protocol" binding:"required"`
	BasePath    string  `json:"base_path" binding:"required"`
	Host        string  `json:"host"`
	Port        int     `json:"port"`
	Username    string  `json:"username"`
	Password    *string `json:"password"`
	KeyPath     string  `json:"key_path"`
	HostKey     *string `json:"host_key"`
}

// sameEndpoint reports whether a request targets the same server identity as
// a stored record — the condition for reusing its stored password. Without
// it, editing host/port/username would redirect the stored secret to an
// arbitrary server.
func sameEndpoint(stored *db.Filesystem, req *FilesystemRequest) bool {
	return stored.Protocol == req.Protocol && stored.Host == req.Host &&
		stored.Port == req.Port && stored.Username == req.Username
}

var validProtocols = map[string]bool{"local": true, "sftp": true, "ftp": true, "smb": true, "nfs": true}

// checkHostPaths validates the host-local paths a request names (LOCAL base
// path, SFTP key file) against the LocalPolicy.
func checkHostPaths(req *FilesystemRequest) error {
	if req.Protocol == "local" {
		if err := remotefs.CheckLocalPath(req.BasePath); err != nil {
			return err
		}
	}
	if req.Protocol == "sftp" && req.KeyPath != "" {
		if err := remotefs.CheckLocalPath(req.KeyPath); err != nil {
			return err
		}
	}
	return nil
}

func validateFilesystemRequest(c *gin.Context, req *FilesystemRequest) bool {
	if !validProtocols[req.Protocol] {
		badRequest(c, "不支持的协议："+req.Protocol)
		return false
	}
	if err := checkHostPaths(req); err != nil {
		badRequest(c, err.Error())
		return false
	}
	return true
}

func (s *Server) listFilesystems(c *gin.Context) {
	fss, err := s.repo.ListFilesystems()
	if err != nil {
		internalError(c, err)
		return
	}
	result := make([]FilesystemResponse, len(fss))
	for i := range fss {
		result[i] = toFilesystemResponse(&fss[i])
	}
	c.JSON(http.StatusOK, result)
}

// pinHostKey sets fs.HostKey for SFTP: an explicit request value wins,
// otherwise the server's current key is fetched (trust on first use).
func pinHostKey(fs *db.Filesystem, explicit *string) error {
	if fs.Protocol != "sftp" {
		fs.HostKey = ""
		return nil
	}
	if explicit != nil && *explicit != "" {
		fs.HostKey = *explicit
		return nil
	}
	fp, err := remotefs.FetchSSHHostKey(fs.Host, fs.Port)
	if err != nil {
		return err
	}
	fs.HostKey = fp
	return nil
}

func (s *Server) createFilesystem(c *gin.Context) {
	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请求格式错误："+err.Error())
		return
	}
	if !validateFilesystemRequest(c, &req) {
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
		KeyPath:     req.KeyPath,
	}
	if req.Password != nil {
		fs.Password = *req.Password
	}
	if err := pinHostKey(fs, req.HostKey); err != nil {
		slog.Warn("create filesystem: fetch host key", "host", fs.Host, "err", err)
		badRequest(c, "无法获取 SFTP 服务器指纹，请检查主机和端口")
		return
	}
	if err := s.repo.CreateFilesystem(fs); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			badRequest(c, "名称已存在")
			return
		}
		internalError(c, err)
		return
	}

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
		badRequest(c, "无效的 ID")
		return
	}
	fs, err := s.repo.GetFilesystem(uint(id))
	if err != nil {
		notFound(c, "文件系统不存在")
		return
	}
	c.JSON(http.StatusOK, toFilesystemResponse(fs))
}

func (s *Server) updateFilesystem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的 ID")
		return
	}
	fs, err := s.repo.GetFilesystem(uint(id))
	if err != nil {
		notFound(c, "文件系统不存在")
		return
	}
	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请求格式错误："+err.Error())
		return
	}
	if !validateFilesystemRequest(c, &req) {
		return
	}
	endpointChanged := !sameEndpoint(fs, &req)
	if endpointChanged && req.Password == nil && fs.Password != "" {
		badRequest(c, "连接目标已变化，请重新输入密码")
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
	if req.Password != nil {
		fs.Password = *req.Password
	}
	if req.HostKey != nil || endpointChanged || (fs.Protocol == "sftp" && fs.HostKey == "") {
		if err := pinHostKey(fs, req.HostKey); err != nil {
			slog.Warn("update filesystem: fetch host key", "host", fs.Host, "err", err)
			badRequest(c, "无法获取 SFTP 服务器指纹，请检查主机和端口")
			return
		}
	}

	if err := s.repo.UpdateFilesystem(fs); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, toFilesystemResponse(fs))
}

func (s *Server) deleteFilesystem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的 ID")
		return
	}
	if err := s.repo.DeleteFilesystem(uint(id)); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// FilesystemTestRequest tests a connection. ID names a stored filesystem whose
// password may be reused — only when the request targets the same endpoint.
type FilesystemTestRequest struct {
	FilesystemRequest
	ID uint `json:"id"`
}

// testFilesystemConnection answers {success, error?, host_key?,
// host_key_changed?}. For SFTP the server fingerprint is always returned so
// the user can verify it before saving.
func (s *Server) testFilesystemConnection(c *gin.Context) {
	var req FilesystemTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请求格式错误："+err.Error())
		return
	}
	if !validateFilesystemRequest(c, &req.FilesystemRequest) {
		return
	}

	fsCfg := config.RemoteFSConfig{
		Protocol: req.Protocol,
		BasePath: req.BasePath,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		KeyPath:  req.KeyPath,
	}
	var stored *db.Filesystem
	if req.ID != 0 {
		fs, err := s.repo.GetFilesystem(req.ID)
		if err != nil {
			notFound(c, "文件系统不存在")
			return
		}
		stored = fs
	}
	switch {
	case req.Password != nil:
		fsCfg.Password = *req.Password
	case stored != nil && stored.Password != "":
		if !sameEndpoint(stored, &req.FilesystemRequest) {
			badRequest(c, "连接目标已变化，请重新输入密码")
			return
		}
		fsCfg.Password = stored.Password
	}

	resp := gin.H{"success": false}
	if req.Protocol == "sftp" {
		fp, err := remotefs.FetchSSHHostKey(req.Host, req.Port)
		if err != nil {
			resp["error"] = "无法获取 SFTP 服务器指纹：" + err.Error()
			c.JSON(http.StatusOK, resp)
			return
		}
		resp["host_key"] = fp
		expected := fp
		switch {
		case req.HostKey != nil && *req.HostKey != "":
			expected = *req.HostKey
		case stored != nil && stored.HostKey != "" && stored.Host == req.Host && stored.Port == req.Port:
			expected = stored.HostKey
		}
		if expected != fp {
			resp["host_key_changed"] = true
			resp["error"] = (&remotefs.HostKeyMismatchError{Expected: expected, Got: fp}).Error()
			c.JSON(http.StatusOK, resp)
			return
		}
		fsCfg.HostKey = expected
	}

	rfs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		resp["error"] = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	defer rfs.Close()
	if _, err := rfs.List(c.Request.Context(), "."); err != nil {
		resp["error"] = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp["success"] = true
	c.JSON(http.StatusOK, resp)
}

package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type FilesystemResponse struct {
	ID               uint   `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Protocol         string `json:"protocol"`
	BasePath         string `json:"base_path"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Username         string `json:"username"`
	PasswordSet      bool   `json:"password_set"`
	PrivateKeySet    bool   `json:"private_key_set"`
	KeyPassphraseSet bool   `json:"key_passphrase_set"`
	HostKey          string `json:"host_key"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func toFilesystemResponse(fs *db.Filesystem) FilesystemResponse {
	return FilesystemResponse{
		ID:               fs.ID,
		Name:             fs.Name,
		Description:      fs.Description,
		Protocol:         fs.Protocol,
		BasePath:         fs.BasePath,
		Host:             fs.Host,
		Port:             fs.Port,
		Username:         fs.Username,
		PasswordSet:      fs.Password != "",
		PrivateKeySet:    fs.PrivateKey != "",
		KeyPassphraseSet: fs.KeyPassphrase != "",
		HostKey:          fs.HostKey,
		CreatedAt:        fs.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:        fs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// FilesystemConn is what a connection needs. Secrets and HostKey are
// pointers: omitted keeps the stored value, "" clears it.
type FilesystemConn struct {
	Protocol      string  `json:"protocol" binding:"required"`
	BasePath      string  `json:"base_path"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Username      string  `json:"username"`
	Password      *string `json:"password"`
	PrivateKey    *string `json:"private_key"`
	KeyPassphrase *string `json:"key_passphrase"`
	HostKey       *string `json:"host_key"`
}

type FilesystemRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	FilesystemConn
}

// Without this check, editing host/port/username would send the stored
// password to an arbitrary server. A stored private key may follow the
// endpoint: public-key authentication never reveals the key to the server.
func sameEndpoint(stored *db.Filesystem, req *FilesystemConn) bool {
	return stored.Protocol == req.Protocol && stored.Host == req.Host &&
		stored.Port == req.Port && stored.Username == req.Username
}

var validProtocols = map[string]bool{"local": true, "sftp": true, "ftp": true, "smb": true, "nfs": true}

var basePathMissing = map[string]string{
	"local": "请选择本机文件夹",
	"smb":   "请选择共享文件夹",
	"nfs":   "请输入 NFS 导出路径",
	"sftp":  "请选择根文件夹",
	"ftp":   "请选择根文件夹",
}

const msgReenterPassword = "连接目标已变化，请重新输入密码"

func validateConn(req *FilesystemConn) string {
	if !validProtocols[req.Protocol] {
		return "不支持的协议：" + req.Protocol
	}
	if req.Protocol == "local" && req.BasePath != "" {
		if err := remotefs.CheckLocalPath(req.BasePath); err != nil {
			return err.Error()
		}
	}
	if req.Protocol != "local" && strings.TrimSpace(req.Host) == "" {
		return "请输入地址"
	}
	if req.Port < 0 || req.Port > 65535 {
		return "端口需在 1 到 65535 之间"
	}
	if req.Protocol == "sftp" && req.PrivateKey != nil && *req.PrivateKey != "" {
		if _, err := remotefs.ParsePrivateKey(*req.PrivateKey, deref(req.KeyPassphrase)); err != nil {
			return err.Error()
		}
	}
	return ""
}

func validateFilesystemRequest(c *gin.Context, req *FilesystemRequest) bool {
	if msg := validateConn(&req.FilesystemConn); msg != "" {
		badRequest(c, msg)
		return false
	}
	if strings.TrimSpace(req.BasePath) == "" {
		badRequest(c, basePathMissing[req.Protocol])
		return false
	}
	return true
}

type connSecrets struct{ password, privateKey, passphrase string }

// resolveSecrets merges typed secrets with the stored ones; ok is false when
// a stored password would follow a changed endpoint.
func resolveSecrets(stored *db.Filesystem, req *FilesystemConn) (connSecrets, bool) {
	var out connSecrets
	if stored != nil {
		out = connSecrets{stored.Password, stored.PrivateKey, stored.KeyPassphrase}
	}
	if req.Password != nil {
		out.password = *req.Password
	} else if out.password != "" && !sameEndpoint(stored, req) {
		return out, false
	}
	if req.PrivateKey != nil {
		out.privateKey = *req.PrivateKey
		// a passphrase belongs to the key it was typed for
		out.passphrase = ""
	}
	if req.KeyPassphrase != nil {
		out.passphrase = *req.KeyPassphrase
	}
	if req.Protocol != "sftp" {
		out.privateKey, out.passphrase = "", ""
	}
	return out, true
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

// Without an explicit key the server's current key is pinned (trust on first
// use).
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
		badBody(c, err)
		return
	}
	if !validateFilesystemRequest(c, &req) {
		return
	}
	sec, _ := resolveSecrets(nil, &req.FilesystemConn)
	fs := &db.Filesystem{
		Name:          req.Name,
		Description:   req.Description,
		Protocol:      req.Protocol,
		BasePath:      req.BasePath,
		Host:          req.Host,
		Port:          req.Port,
		Username:      req.Username,
		Password:      sec.password,
		PrivateKey:    sec.privateKey,
		KeyPassphrase: sec.passphrase,
	}
	if err := pinHostKey(fs, req.HostKey); err != nil {
		slog.Warn("create filesystem: fetch host key", "host", fs.Host, "err", err)
		badRequest(c, "无法获取 SFTP 服务器指纹："+remotefs.Explain(err))
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
		Path:         "/未分类",
		Description:  "不属于其他分类的文件",
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
		notFound(c, msgNoFilesystem)
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
		notFound(c, msgNoFilesystem)
		return
	}
	var req FilesystemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}
	if !validateFilesystemRequest(c, &req) {
		return
	}
	endpointChanged := !sameEndpoint(fs, &req.FilesystemConn)
	sec, ok := resolveSecrets(fs, &req.FilesystemConn)
	if !ok {
		badRequest(c, msgReenterPassword)
		return
	}

	fs.Name = req.Name
	fs.Description = req.Description
	fs.Protocol = req.Protocol
	fs.BasePath = req.BasePath
	fs.Host = req.Host
	fs.Port = req.Port
	fs.Username = req.Username
	fs.Password = sec.password
	fs.PrivateKey = sec.privateKey
	fs.KeyPassphrase = sec.passphrase
	if req.HostKey != nil || endpointChanged || (fs.Protocol == "sftp" && fs.HostKey == "") {
		if err := pinHostKey(fs, req.HostKey); err != nil {
			slog.Warn("update filesystem: fetch host key", "host", fs.Host, "err", err)
			badRequest(c, "无法获取 SFTP 服务器指纹："+remotefs.Explain(err))
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
	n, err := s.repo.CountSessionsUsingFilesystem(uint(id))
	if err != nil {
		internalError(c, err)
		return
	}
	if n > 0 {
		conflict(c, fmt.Sprintf("有 %d 个整理任务在用这个存储，先删除这些任务", n))
		return
	}
	if err := s.repo.DeleteFilesystem(uint(id)); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// FilesystemProbeRequest tests a connection and lists the folders at
// base_path, so the same call both checks the settings and drives the folder
// picker. ID names the saved storage whose secrets may be reused.
type FilesystemProbeRequest struct {
	FilesystemConn
	ID uint `json:"id"`
}

type probeDir struct {
	Name string `json:"name"`
	// The base_path value that selects (or opens) this folder.
	Path string `json:"path"`
}

type probeResult struct {
	Success        bool       `json:"success"`
	Error          string     `json:"error,omitempty"`
	Detail         string     `json:"detail,omitempty"`
	HostKey        string     `json:"host_key,omitempty"`
	HostKeyChanged bool       `json:"host_key_changed,omitempty"`
	Dirs           []probeDir `json:"dirs"`
}

const maxProbeDirs = 2000

func (r *probeResult) fail(err error) {
	r.Error = remotefs.Explain(err)
	if detail := err.Error(); detail != r.Error {
		r.Detail = detail
	}
}

func childBasePath(protocol, base, name string) string {
	switch protocol {
	case "local":
		return filepath.Join(base, name)
	case "smb":
		return strings.Trim(strings.Trim(base, "/")+"/"+name, "/")
	default:
		return path.Join(base, name)
	}
}

// Failures are answered with 200 and success:false so the form shows them
// inline; only a request that cannot be understood is a 400.
func (s *Server) testFilesystemConnection(c *gin.Context) {
	var req FilesystemProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}
	res := probeResult{Dirs: []probeDir{}}
	if !validProtocols[req.Protocol] {
		badRequest(c, "不支持的协议："+req.Protocol)
		return
	}
	if msg := validateConn(&req.FilesystemConn); msg != "" {
		res.Error = msg
		c.JSON(http.StatusOK, res)
		return
	}

	if req.Protocol == "local" && req.BasePath == "" {
		roots := remotefs.LocalRoots()
		if len(roots) == 0 {
			res.fail(remotefs.ErrLocalDisabled)
		} else {
			res.Success = true
			for _, r := range roots {
				res.Dirs = append(res.Dirs, probeDir{Name: r, Path: r})
			}
		}
		c.JSON(http.StatusOK, res)
		return
	}

	var stored *db.Filesystem
	if req.ID != 0 {
		fs, err := s.repo.GetFilesystem(req.ID)
		if err != nil {
			notFound(c, msgNoFilesystem)
			return
		}
		stored = fs
	}
	sec, ok := resolveSecrets(stored, &req.FilesystemConn)
	if !ok {
		badRequest(c, msgReenterPassword)
		return
	}
	fsCfg := config.RemoteFSConfig{
		Protocol:      req.Protocol,
		BasePath:      req.BasePath,
		Host:          req.Host,
		Port:          req.Port,
		Username:      req.Username,
		Password:      sec.password,
		PrivateKey:    sec.privateKey,
		KeyPassphrase: sec.passphrase,
	}

	if req.Protocol == "sftp" {
		fp, err := remotefs.FetchSSHHostKey(req.Host, req.Port)
		if err != nil {
			res.fail(err)
			c.JSON(http.StatusOK, res)
			return
		}
		res.HostKey = fp
		expected := fp
		switch {
		case req.HostKey != nil && *req.HostKey != "":
			expected = *req.HostKey
		case stored != nil && stored.HostKey != "" && stored.Host == req.Host && stored.Port == req.Port:
			expected = stored.HostKey
		}
		if expected != fp {
			res.HostKeyChanged = true
			res.fail(&remotefs.HostKeyMismatchError{Expected: expected, Got: fp})
			c.JSON(http.StatusOK, res)
			return
		}
		fsCfg.HostKey = expected
	}

	if req.Protocol == "smb" && strings.Trim(req.BasePath, "/\\") == "" {
		shares, err := remotefs.ListSMBShares(c.Request.Context(), fsCfg)
		if err != nil {
			res.fail(err)
			c.JSON(http.StatusOK, res)
			return
		}
		sort.Strings(shares)
		for _, n := range shares {
			res.Dirs = append(res.Dirs, probeDir{Name: n, Path: n})
		}
		res.Success = true
		c.JSON(http.StatusOK, res)
		return
	}

	rfs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		res.fail(err)
		c.JSON(http.StatusOK, res)
		return
	}
	defer rfs.Close()
	entries, err := rfs.List(c.Request.Context(), ".")
	if err != nil {
		res.fail(err)
		c.JSON(http.StatusOK, res)
		return
	}
	for _, e := range entries {
		if !e.IsDir || strings.HasPrefix(e.Name, ".") {
			continue
		}
		res.Dirs = append(res.Dirs, probeDir{Name: e.Name, Path: childBasePath(req.Protocol, req.BasePath, e.Name)})
	}
	sort.Slice(res.Dirs, func(i, j int) bool { return strings.ToLower(res.Dirs[i].Name) < strings.ToLower(res.Dirs[j].Name) })
	if len(res.Dirs) > maxProbeDirs {
		res.Dirs = res.Dirs[:maxProbeDirs]
	}
	res.Success = true
	c.JSON(http.StatusOK, res)
}

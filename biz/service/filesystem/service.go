// Package filesystem manages storage connections: CRUD, the in-use check on
// delete, and the connection probe that doubles as the folder picker.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/internal/remotefs"

	"gorm.io/gorm"
)

const (
	// Without this check, editing host/port/username would send the stored
	// password to an arbitrary server.
	msgReenterPassword = "连接目标已变化，请重新输入密码"
	msgNoFilesystem    = "存储不存在"
)

var errNoFilesystem = errs.NotFound(msgNoFilesystem)

var validProtocols = map[string]bool{"local": true, "sftp": true, "ftp": true, "smb": true, "nfs": true}

var basePathMissing = map[string]string{
	"local": "请选择本机文件夹",
	"smb":   "请选择共享文件夹",
	"nfs":   "请输入 NFS 导出路径",
	"sftp":  "请选择根文件夹",
	"ftp":   "请选择根文件夹",
}

type Service struct {
	q  *query.Query
	rq *dal.Reader
}

func NewService(q *query.Query, rq *dal.Reader) *Service { return &Service{q: q, rq: rq} }

// Conn is what a connection needs. Secrets and HostKey are pointers: nil keeps
// the stored value, "" clears it.
type Conn struct {
	Protocol      string
	BasePath      string
	Host          string
	Port          int
	Username      string
	Password      *string
	PrivateKey    *string
	KeyPassphrase *string
	HostKey       *string
}

type SaveInput struct {
	// 0 creates
	ID          uint
	Name        string
	Description string
	Conn
}

func (s *Service) List(ctx context.Context) ([]*model.Filesystem, error) {
	f := s.rq.Filesystem
	return f.WithContext(ctx).Order(f.Name).Find()
}

func (s *Service) Get(ctx context.Context, id uint) (*model.Filesystem, error) {
	f := s.q.Filesystem
	row, err := f.WithContext(ctx).Where(f.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNoFilesystem
	}
	return row, err
}

// storedFor loads the saved storage a probe may borrow secrets from (nil for 0).
func (s *Service) storedFor(ctx context.Context, id uint) (*model.Filesystem, error) {
	if id == 0 {
		return nil, nil
	}
	return s.Get(ctx, id)
}

func (s *Service) Create(ctx context.Context, in SaveInput) (*model.Filesystem, error) {
	if err := validateSave(&in); err != nil {
		return nil, err
	}
	sec, _ := resolveSecrets(nil, &in.Conn)
	row := &model.Filesystem{
		Name: in.Name, Description: in.Description, Protocol: in.Protocol, BasePath: in.BasePath,
		Host: in.Host, Port: in.Port, Username: in.Username,
		Password: sec.password, PrivateKey: sec.privateKey, KeyPassphrase: sec.passphrase,
	}
	if err := pinHostKey(row, in.HostKey); err != nil {
		return nil, hostKeyErr(err)
	}
	err := s.q.Transaction(func(tx *query.Query) error {
		if err := tx.Filesystem.WithContext(ctx).Create(row); err != nil {
			return err
		}
		return tx.Category.WithContext(ctx).Create(&model.Category{
			FilesystemID: row.ID, Name: "未分类", Path: "/未分类", Description: "不属于其他分类的文件",
		})
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, errs.Conflict("名称已存在")
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (s *Service) Update(ctx context.Context, in SaveInput) (*model.Filesystem, error) {
	stored, err := s.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := validateSave(&in); err != nil {
		return nil, err
	}
	endpointChanged := !sameEndpoint(stored, &in.Conn)
	sec, ok := resolveSecrets(stored, &in.Conn)
	if !ok {
		return nil, errs.Invalid(msgReenterPassword)
	}
	next := &model.Filesystem{
		ID: stored.ID, Name: in.Name, Description: in.Description, Protocol: in.Protocol, BasePath: in.BasePath,
		Host: in.Host, Port: in.Port, Username: in.Username,
		Password: sec.password, PrivateKey: sec.privateKey, KeyPassphrase: sec.passphrase,
		HostKey: stored.HostKey, CreatedAt: stored.CreatedAt,
	}
	if in.HostKey != nil || endpointChanged || (next.Protocol == "sftp" && next.HostKey == "") {
		if err := pinHostKey(next, in.HostKey); err != nil {
			return nil, hostKeyErr(err)
		}
	}
	f := s.q.Filesystem
	_, err = f.WithContext(ctx).Where(f.ID.Eq(stored.ID)).
		Select(f.Name, f.Description, f.Protocol, f.BasePath, f.Host, f.Port, f.Username,
			f.Password, f.PrivateKey, f.KeyPassphrase, f.HostKey).
		Updates(next)
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, errs.Conflict("名称已存在")
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, stored.ID)
}

// Delete refuses while tasks use the storage; deleting a missing one succeeds.
func (s *Service) Delete(ctx context.Context, id uint) error {
	return s.q.Transaction(func(tx *query.Query) error {
		ss := tx.ScanSession
		n, err := ss.WithContext(ctx).Where(ss.FilesystemID.Eq(id)).Count()
		if err != nil {
			return err
		}
		if n > 0 {
			return errs.Conflict(fmt.Sprintf("有 %d 个整理任务在用这个存储，先删除这些任务", n))
		}
		c, m := tx.Category, tx.MemoryItem
		if _, err := c.WithContext(ctx).Where(c.FilesystemID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := m.WithContext(ctx).Where(m.Scope.Eq("filesystem"), m.ScopeID.Eq(id)).Delete(); err != nil {
			return err
		}
		_, err = tx.Filesystem.WithContext(ctx).Where(tx.Filesystem.ID.Eq(id)).Delete()
		return err
	})
}

func hostKeyErr(err error) error {
	slog.Warn("fetch sftp host key", "err", err)
	return errs.Invalid("无法获取 SFTP 服务器指纹：" + remotefs.Explain(err))
}

func sameEndpoint(stored *model.Filesystem, req *Conn) bool {
	return stored.Protocol == req.Protocol && stored.Host == req.Host &&
		stored.Port == req.Port && stored.Username == req.Username
}

func validateConn(c *Conn) error {
	if !validProtocols[c.Protocol] {
		return errs.Invalid("不支持的协议：" + c.Protocol)
	}
	if c.Protocol == "local" && c.BasePath != "" {
		if err := remotefs.CheckLocalPath(c.BasePath); err != nil {
			return errs.Invalid(remotefs.Explain(err))
		}
	}
	if c.Protocol != "local" && strings.TrimSpace(c.Host) == "" {
		return errs.Invalid("请输入地址")
	}
	if c.Port < 0 || c.Port > 65535 {
		return errs.Invalid("端口需在 1 到 65535 之间")
	}
	if c.Protocol == "sftp" && c.PrivateKey != nil && *c.PrivateKey != "" {
		if _, err := remotefs.ParsePrivateKey(*c.PrivateKey, deref(c.KeyPassphrase)); err != nil {
			return errs.Invalid(remotefs.Explain(err))
		}
	}
	return nil
}

func validateSave(in *SaveInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errs.Invalid("请输入名称")
	}
	if err := validateConn(&in.Conn); err != nil {
		return err
	}
	if strings.TrimSpace(in.BasePath) == "" {
		return errs.Invalid(basePathMissing[in.Protocol])
	}
	return nil
}

type secrets struct{ password, privateKey, passphrase string }

// resolveSecrets merges typed secrets with the stored ones; ok is false when a
// stored password would follow a changed endpoint. A stored private key may
// follow the endpoint: public-key authentication never reveals it.
func resolveSecrets(stored *model.Filesystem, req *Conn) (secrets, bool) {
	var out secrets
	if stored != nil {
		out = secrets{stored.Password, stored.PrivateKey, stored.KeyPassphrase}
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

// Without an explicit key the server's current key is pinned (trust on first use).
func pinHostKey(fs *model.Filesystem, explicit *string) error {
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

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

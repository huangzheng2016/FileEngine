// Package file lists, reads and edits the entries of a session's scan.
package file

import (
	"context"
	"errors"
	"io"
	"log/slog"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/run"
	"FileEngine/internal/agent"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"

	"gorm.io/gorm"
)

const maxPageSize = 200

var errNoFile = errs.NotFound("文件不存在")

type Service struct {
	q    *query.Query
	rq   *dal.Reader
	repo *db.Repository
	// reads is repo on the read pool: list endpoints never wait for the writer
	reads *db.Repository
	runs  *run.Service
}

func NewService(q *query.Query, rq *dal.Reader, repo *db.Repository, runs *run.Service) *Service {
	return &Service{q: q, rq: rq, repo: repo, reads: repo.WithDB(rq.DB), runs: runs}
}

type ListParams struct {
	SessionID uint
	// nil = no folder filter; "" = the children of the scan root
	ParentPath   *string
	FileType     string
	Tagged       *bool
	Categorized  *bool
	CategoryPath *string
	Search       string
	NeedsReview  *bool
	Sensitive    *bool
	Role         *string
	Page         int32
	PageSize     int32
}

func (s *Service) List(ctx context.Context, p ListParams) ([]model.FileEntry, int64, error) {
	if _, err := s.runs.ReadSession(ctx, p.SessionID); err != nil {
		return nil, 0, err
	}
	size := int(p.PageSize)
	if size > maxPageSize {
		size = maxPageSize
	}
	return s.reads.ListFiles(db.FileQuery{
		SessionID: p.SessionID, ParentPath: p.ParentPath, FileType: p.FileType, Tagged: p.Tagged,
		Categorized: p.Categorized, CategoryPath: p.CategoryPath, Search: p.Search,
		NeedsReview: p.NeedsReview, Sensitive: p.Sensitive, Role: p.Role,
		Page: int(p.Page), PageSize: size,
	})
}

func (s *Service) Get(ctx context.Context, id uint) (*model.FileEntry, error) {
	f := s.q.FileEntry
	row, err := f.WithContext(ctx).Where(f.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNoFile
	}
	return row, err
}

// Update changes the description and/or sets the user's target. A target equal
// to the entry's own path keeps it in place and learns nothing.
func (s *Service) Update(ctx context.Context, id uint, description, newPath *string) (*model.FileEntry, error) {
	file, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if newPath != nil {
		if err := pathutil.ValidateFSPath(*newPath); err != nil {
			return nil, errs.Invalid("目标位置无效：" + err.Error())
		}
	}
	if description != nil {
		f := s.q.FileEntry
		if _, err := f.WithContext(ctx).Where(f.ID.Eq(id)).UpdateSimple(f.Description.Value(*description)); err != nil {
			return nil, err
		}
	}
	if newPath != nil {
		oldTarget := file.NewPath
		res, err := s.repo.Decide(file.ScanSessionID, db.UserTargetDecision(file, *newPath))
		if db.IsRejection(err) {
			return nil, errs.Conflict(err.Error())
		}
		if err != nil {
			return nil, err
		}
		if err := s.learnCorrection(ctx, file, oldTarget, res.NewPath); err != nil {
			slog.Warn("record correction memory", "file", file.ID, "err", err)
		}
	}
	return s.Get(ctx, id)
}

// learnCorrection remembers where the user wants this kind of file, so the
// next run classifies it that way.
func (s *Service) learnCorrection(ctx context.Context, f *model.FileEntry, oldTarget, newTarget string) error {
	session, err := s.runs.Session(ctx, f.ScanSessionID)
	if err != nil {
		return err
	}
	m := agent.CorrectionMemory(*f, oldTarget, newTarget, session.FilesystemID)
	if m == nil {
		return nil
	}
	if _, err := s.repo.UpsertMemory(m); err != nil {
		return err
	}
	db.Emit(session.ID, 0, db.EvMemoryUpdated, db.MemoryUpdatedData{Scope: m.Scope, ID: m.ID})
	return nil
}

// Tree lists the directories directly under parent ("" = the scan root).
func (s *Service) Tree(ctx context.Context, sessionID uint, parent string) ([]*model.FileEntry, error) {
	if _, err := s.runs.ReadSession(ctx, sessionID); err != nil {
		return nil, err
	}
	f := s.rq.FileEntry
	return f.WithContext(ctx).
		Where(f.ScanSessionID.Eq(sessionID), f.ParentPath.Eq(parent), f.FileType.Eq("directory")).
		Order(f.Name, f.ID).Find()
}

type Content struct {
	Name string
	io.ReadCloser
}

// Open streams the file's bytes from its storage. Closing the result also
// closes the storage connection.
func (s *Service) Open(ctx context.Context, id uint) (*Content, error) {
	file, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	src, err := pathutil.RootRelative(file.OriginalPath)
	if err != nil {
		return nil, errs.Invalid("文件路径非法")
	}
	session, err := s.runs.Session(ctx, file.ScanSessionID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.runs.ResolveFSConfig(ctx, session)
	if err != nil {
		return nil, err
	}
	rfs, err := remotefs.NewFromConfig(cfg)
	if err != nil {
		slog.Warn("file content: connect filesystem", "file", file.ID, "err", err)
		return nil, errs.Unavailable("无法连接存储")
	}
	reader, err := rfs.ReadFile(ctx, src)
	if err != nil {
		_ = rfs.Close()
		slog.Warn("file content: read", "file", file.ID, "err", err)
		return nil, &errs.Error{Status: 502, Code: errs.CodeUnavailable, Msg: "读取文件失败"}
	}
	return &Content{Name: file.Name, ReadCloser: &closeBoth{reader, rfs}}, nil
}

type closeBoth struct {
	io.ReadCloser
	fs interface{ Close() error }
}

func (c *closeBoth) Close() error {
	err := c.ReadCloser.Close()
	_ = c.fs.Close()
	return err
}

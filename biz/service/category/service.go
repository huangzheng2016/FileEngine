// Package category manages the target folders a session's files are sorted into.
package category

import (
	"context"
	"errors"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"

	"gorm.io/gorm"
)

var (
	errNoCategory   = errs.NotFound("分类不存在")
	errNoFilesystem = errs.NotFound("存储不存在")
	errDuplicate    = errs.Conflict("分类名称或路径已存在")
)

type Service struct {
	q    *query.Query
	repo *db.Repository
}

func NewService(q *query.Query, repo *db.Repository) *Service { return &Service{q: q, repo: repo} }

type Input struct {
	FilesystemID uint
	Name         string
	Path         string
	Structure    string
	Description  string
}

func (in *Input) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errs.Invalid("分类名称不能为空")
	}
	// Category paths become new_path prefixes, so they get the same root
	// confinement as the agent's tools.
	if err := pathutil.ValidateFSPath(in.Path); err != nil {
		return errs.Invalid("分类路径非法：" + err.Error())
	}
	return nil
}

func (s *Service) List(ctx context.Context, filesystemID uint) ([]*model.Category, error) {
	c := s.q.Category
	return c.WithContext(ctx).Where(c.FilesystemID.Eq(filesystemID)).Order(c.Name, c.ID).Find()
}

func (s *Service) Get(ctx context.Context, id uint) (*model.Category, error) {
	c := s.q.Category
	row, err := c.WithContext(ctx).Where(c.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNoCategory
	}
	return row, err
}

func (s *Service) filesystemExists(ctx context.Context, id uint) error {
	f := s.q.Filesystem
	n, err := f.WithContext(ctx).Where(f.ID.Eq(id)).Count()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNoFilesystem
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in Input) (*model.Category, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if err := s.filesystemExists(ctx, in.FilesystemID); err != nil {
		return nil, err
	}
	row := &model.Category{FilesystemID: in.FilesystemID, Name: in.Name, Path: in.Path,
		Structure: in.Structure, Description: in.Description}
	if err := s.q.Category.WithContext(ctx).Create(row); err != nil {
		return nil, conflict(err)
	}
	return row, nil
}

// Update never moves a category to another filesystem. A new path rewrites the
// planned targets of this filesystem's sessions in the same transaction.
func (s *Service) Update(ctx context.Context, id uint, in Input) (*model.Category, error) {
	cat, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.FilesystemID != cat.FilesystemID {
		return nil, errs.Invalid("不能更换分类所在的存储")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	oldPath := cat.Path
	cat.Name, cat.Path, cat.Structure, cat.Description = in.Name, in.Path, in.Structure, in.Description
	if err := s.repo.UpdateCategoryPath(cat, oldPath); err != nil {
		return nil, conflict(err)
	}
	return s.Get(ctx, id)
}

// Delete releases the targets planned into the category, then removes it.
// Deleting a category that is already gone succeeds.
func (s *Service) Delete(ctx context.Context, id uint) error {
	cat, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, errNoCategory) {
			return nil
		}
		return err
	}
	if err := s.repo.ClearPlannedByTargetPath(cat.FilesystemID, cat.Path); err != nil {
		return err
	}
	c := s.q.Category
	_, err = c.WithContext(ctx).Where(c.ID.Eq(id)).Delete()
	return err
}

type Template struct {
	Name, Path, Structure, Description string
}

// Import adds the valid items whose name and path are free; everything else
// (invalid, duplicate, lost to a concurrent writer) counts as skipped.
func (s *Service) Import(ctx context.Context, filesystemID uint, items []Template) (created, skipped int, err error) {
	if err := s.filesystemExists(ctx, filesystemID); err != nil {
		return 0, 0, err
	}
	existing, err := s.List(ctx, filesystemID)
	if err != nil {
		return 0, 0, err
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, c := range existing {
		names[c.Name], paths[c.Path] = true, true
	}
	var fresh []*model.Category
	for _, it := range items {
		in := Input{FilesystemID: filesystemID, Name: it.Name, Path: it.Path, Structure: it.Structure, Description: it.Description}
		if in.validate() != nil || names[in.Name] || paths[in.Path] {
			skipped++
			continue
		}
		names[in.Name], paths[in.Path] = true, true
		fresh = append(fresh, &model.Category{FilesystemID: filesystemID, Name: in.Name, Path: in.Path,
			Structure: in.Structure, Description: in.Description})
	}
	if len(fresh) == 0 {
		return 0, skipped, nil
	}
	err = s.q.Transaction(func(tx *query.Query) error {
		return tx.Category.WithContext(ctx).CreateInBatches(fresh, 200)
	})
	if err == nil {
		return len(fresh), skipped, nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return 0, 0, err
	}
	// A concurrent writer took a name or path: keep what still fits.
	for _, c := range fresh {
		c.ID = 0
		if err := s.q.Category.WithContext(ctx).Create(c); err != nil {
			if !errors.Is(err, gorm.ErrDuplicatedKey) {
				return created, skipped, err
			}
			skipped++
			continue
		}
		created++
	}
	return created, skipped, nil
}

func conflict(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return errDuplicate
	}
	return err
}

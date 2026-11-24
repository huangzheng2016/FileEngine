package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
)

type CreateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"分类名：2–6 个字的中文名词，如 照片、软件"`
	Path        string `json:"path" jsonschema_description:"分类路径，以 / 开头，如 /照片、/文档/合同"`
	Description string `json:"description" jsonschema_description:"放什么：一句话说明哪些文件属于这个分类"`
}

type CreateCategoryOutput struct {
	Envelope
	Name              string                `json:"name,omitempty"`
	Path              string                `json:"path,omitempty"`
	SimilarCategories []SimilarCategoryItem `json:"similar_categories,omitempty"`
}

type SimilarCategoryItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int64  `json:"file_count"`
	Reason    string `json:"reason"`
}

type UpdateCategoryInput struct {
	Name        string `json:"name" jsonschema_description:"要修改的分类的当前名称"`
	NewName     string `json:"new_name,omitempty" jsonschema_description:"新名称"`
	NewPath     string `json:"new_path,omitempty" jsonschema_description:"新路径，以 / 开头；旧路径下已规划的目标随之更新"`
	Description string `json:"description,omitempty" jsonschema_description:"新的放什么说明"`
	Structure   string `json:"structure,omitempty" jsonschema_description:"分类下的子目录结构说明，如 按 年/月 分目录"`
}

type UpdateCategoryOutput struct {
	Envelope
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type DeleteCategoryInput struct {
	Name string `json:"name" jsonschema_description:"要删除的分类名称"`
}

type DeleteCategoryOutput struct {
	Envelope
	Reverted int64 `json:"reverted"`
}

func (tb *ToolBuilder) createCategory(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {
	return tb.createCategoryCore(ctx, input)
}

// Shared by the tool handler and the run_script builtin so a script can never
// bypass the switch or the guard rails. A model-fixable rejection comes back
// as a rejectError.
func (tb *ToolBuilder) createCategoryCore(ctx context.Context, input *CreateCategoryInput) (*CreateCategoryOutput, error) {
	if !tb.session.AllowAutoCategory {
		return nil, rejectHint("归入现有分类；现有分类放不下时在 note 或回复里说明", "本任务不能新建分类")
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return nil, rejectf("分类名不能为空")
	}
	// Category paths become new_path prefixes, so they follow the same root
	// confinement as decisions.
	if err := pathutil.ValidateTargetPath(input.Path); err != nil || input.Path == "" {
		if err == nil {
			err = errors.New("路径不能为空")
		}
		return nil, rejectf("分类路径非法：%v", err)
	}

	usages, err := tb.repo.GetCategoryUsageSummary(tb.filesystemID, tb.sessionID)
	if err != nil {
		return nil, fmt.Errorf("create category: read current categories: %w", err)
	}
	names := make([]string, len(usages))
	for i, u := range usages {
		names[i] = u.Name
	}

	// The rejection doubles as the agent's re-read, so it is recorded as the new
	// snapshot: a retry decided from that list passes unless the set changed
	// again.
	if lastFP, seen := tb.guard.last(); !seen || lastFP != categoryFingerprint(names) {
		tb.guard.note(names)
		list := strings.TrimSpace(buildCategorySummary(usages))
		if list == "" {
			list = "（当前没有任何分类）"
		}
		return nil, rejectHint("基于这份列表重新决定：优先用 decide 把条目归入现有分类；确认没有合适分类后再调用 create_category",
			"分类列表自你上次看到后已变化（或你尚未看到），本次创建已拒绝。最新完整分类列表：\n%s", list)
	}

	hard, soft := findSimilarCategories(usages, input.Name, input.Path)
	if len(hard) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("已存在相同分类（名称或路径重复），拒绝创建 %q (%s)：\n", SanitizeUntrustedName(input.Name), SanitizeUntrustedName(input.Path)))
		for _, s := range hard {
			sb.WriteString(fmt.Sprintf("- %s (%s): 已归入 %d 项（%s）\n", SanitizeUntrustedName(s.usage.Name), SanitizeUntrustedName(s.usage.Path), s.usage.FileCount, s.reason))
		}
		return nil, rejectHint("用 decide 把条目归入上述现有分类；确需新分类时换用不同的名称和路径", "%s", strings.TrimSpace(sb.String()))
	}

	cat := &model.Category{
		FilesystemID:  tb.filesystemID,
		Name:          input.Name,
		Path:          input.Path,
		Description:   input.Description,
		AgentCreated:  true,
		AgentEditable: true,
	}
	created, err := tb.repo.CreateCategoryWithConflict(cat)
	if err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}
	if !created {
		return nil, rejectHint("用 decide 把条目归入现有分类", "名称 %q 或路径 %q 已被其他分类使用，未创建", SanitizeUntrustedName(input.Name), SanitizeUntrustedName(input.Path))
	}

	// The agent knows the post-create set, so back-to-back creations are not
	// re-challenged; anyone else's change still forces a re-read.
	tb.guard.note(append(names, cat.Name))

	out := succeeded[CreateCategoryOutput]()
	out.Name, out.Path = cat.Name, cat.Path
	for _, s := range soft {
		out.SimilarCategories = append(out.SimilarCategories, SimilarCategoryItem{
			Name:      SanitizeUntrustedName(s.usage.Name),
			Path:      SanitizeUntrustedName(s.usage.Path),
			FileCount: s.usage.FileCount,
			Reason:    s.reason,
		})
	}
	if len(out.SimilarCategories) > 0 {
		out.Hint = "已创建，但存在路径重叠的相似分类（见 similar_categories）。职责重复时用 decide 把条目归入现有分类；确有必要保留则无需处理"
	}
	return out, nil
}

func (tb *ToolBuilder) updateCategory(ctx context.Context, input *UpdateCategoryInput) (*UpdateCategoryOutput, error) {
	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return nil, rejectf("分类不存在：%s", SanitizeUntrustedName(input.Name))
	}
	if !cat.AgentEditable {
		return nil, rejectf("分类「%s」由用户维护，不能修改", SanitizeUntrustedName(input.Name))
	}
	if input.NewName == "" && input.NewPath == "" && input.Description == "" && input.Structure == "" {
		return nil, rejectf("没有要修改的字段：至少给出 new_name、new_path、description、structure 之一")
	}
	// A changed category path cascades into planned targets, so it follows the
	// same root confinement as decisions.
	if input.NewPath != "" {
		if err := pathutil.ValidateTargetPath(input.NewPath); err != nil {
			return nil, rejectf("new_path 非法：%v", err)
		}
	}
	if msg, err := tb.categoryClash(cat.ID, input.NewName, input.NewPath); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return nil, rejectf("%s", msg)
	}

	oldPath := cat.Path
	if input.NewName != "" {
		cat.Name = input.NewName
	}
	if input.NewPath != "" {
		cat.Path = input.NewPath
	}
	if input.Description != "" {
		cat.Description = input.Description
	}
	if input.Structure != "" {
		cat.Structure = input.Structure
	}
	if err := tb.repo.UpdateCategoryPath(cat, oldPath); err != nil {
		return nil, fmt.Errorf("update category: %w", err)
	}
	out := succeeded[UpdateCategoryOutput]()
	out.Name, out.Path = cat.Name, cat.Path
	return out, nil
}

// Name and path are unique per filesystem; a clash is the agent's to fix,
// not a database error.
func (tb *ToolBuilder) categoryClash(id uint, newName, newPath string) (string, error) {
	if newName == "" && newPath == "" {
		return "", nil
	}
	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return "", err
	}
	for _, c := range cats {
		if c.ID == id {
			continue
		}
		if newName != "" && normalizeCategoryName(c.Name) == normalizeCategoryName(newName) {
			return fmt.Sprintf("名称「%s」已被分类「%s」使用", SanitizeUntrustedName(newName), SanitizeUntrustedName(c.Name)), nil
		}
		if newPath != "" && normalizeCategoryPath(c.Path) == normalizeCategoryPath(newPath) {
			return fmt.Sprintf("路径 %s 已被分类「%s」使用；合并分类时先把规划改到保留的分类，再删除多余的分类", db.NormalizeTargetPath(newPath), SanitizeUntrustedName(c.Name)), nil
		}
	}
	return "", nil
}

func (tb *ToolBuilder) deleteCategory(ctx context.Context, input *DeleteCategoryInput) (*DeleteCategoryOutput, error) {
	cat, err := tb.repo.GetCategoryByName(tb.filesystemID, input.Name)
	if err != nil {
		return nil, rejectf("分类不存在：%s", SanitizeUntrustedName(input.Name))
	}
	if !cat.AgentEditable {
		return nil, rejectf("分类「%s」由用户维护，不能删除", SanitizeUntrustedName(input.Name))
	}
	reverted, err := tb.repo.CountPlannedByTargetPath(tb.filesystemID, cat.Path)
	if err != nil {
		return nil, fmt.Errorf("count planned files: %w", err)
	}
	if err := tb.repo.ClearPlannedByTargetPath(tb.filesystemID, cat.Path); err != nil {
		return nil, fmt.Errorf("failed to clear planned files: %w", err)
	}
	if err := tb.repo.DeleteCategory(cat.ID); err != nil {
		return nil, fmt.Errorf("delete category: %w", err)
	}
	out := succeeded[DeleteCategoryOutput]()
	out.Reverted = reverted
	return out, nil
}

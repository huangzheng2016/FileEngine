package api

import (
	"net/http"
	"strconv"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

func (s *Server) listCategories(c *gin.Context) {
	fsID, err := strconv.ParseUint(c.Query("filesystem_id"), 10, 32)
	if err != nil || fsID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filesystem_id required"})
		return
	}
	cats, err := s.repo.ListCategories(uint(fsID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cats)
}

type CategoryRequest struct {
	FilesystemID uint   `json:"filesystem_id" binding:"required"`
	Name         string `json:"name" binding:"required"`
	Path         string `json:"path" binding:"required"`
	Structure    string `json:"structure"`
	Description  string `json:"description"`
}

func (s *Server) createCategory(c *gin.Context) {
	var req CategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cat := &db.Category{
		FilesystemID: req.FilesystemID,
		Name:         req.Name,
		Path:         req.Path,
		Structure:    req.Structure,
		Description:  req.Description,
	}
	if err := s.repo.CreateCategory(cat); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cat)
}

func (s *Server) updateCategory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	cat, err := s.repo.GetCategory(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		return
	}

	var req CategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldPath := cat.Path
	cat.FilesystemID = req.FilesystemID
	cat.Name = req.Name
	cat.Path = req.Path
	cat.Structure = req.Structure
	cat.Description = req.Description

	if err := s.repo.UpdateCategoryPath(cat, oldPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cat)
}

func (s *Server) deleteCategory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cat, err := s.repo.GetCategory(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		return
	}
	// Clear planned files under this category and reset tagged for re-classification
	_ = s.repo.ClearPlannedByTargetPath(cat.FilesystemID, cat.Path)
	if err := s.repo.DeleteCategory(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

type CategoryExportItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Structure   string `json:"structure,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *Server) exportCategories(c *gin.Context) {
	fsID, err := strconv.ParseUint(c.Query("filesystem_id"), 10, 32)
	if err != nil || fsID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filesystem_id required"})
		return
	}
	cats, err := s.repo.ListCategories(uint(fsID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]CategoryExportItem, len(cats))
	for i, c := range cats {
		items[i] = CategoryExportItem{Name: c.Name, Path: c.Path, Structure: c.Structure, Description: c.Description}
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) importCategories(c *gin.Context) {
	fsID, err := strconv.ParseUint(c.Query("filesystem_id"), 10, 32)
	if err != nil || fsID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filesystem_id required"})
		return
	}

	var items []CategoryExportItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	created := 0
	skipped := 0
	for _, item := range items {
		if _, err := s.repo.GetCategoryByName(uint(fsID), item.Name); err == nil {
			skipped++
			continue
		}
		cat := &db.Category{
			FilesystemID: uint(fsID),
			Name:         item.Name,
			Path:         item.Path,
			Structure:    item.Structure,
			Description:  item.Description,
		}
		if err := s.repo.CreateCategory(cat); err != nil {
			continue
		}
		created++
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "skipped": skipped})
}

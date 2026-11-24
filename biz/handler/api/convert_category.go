package api

import (
	"FileEngine/biz/dal/db/model"
	category "FileEngine/biz/model/api/category"
	svc "FileEngine/biz/service/category"
)

func toCategory(c *model.Category) *category.Category {
	return &category.Category{
		ID: int64(c.ID), FilesystemID: int64(c.FilesystemID), Name: c.Name, Path: c.Path,
		Structure: c.Structure, Description: c.Description, AgentCreated: c.AgentCreated,
		AgentEditable: c.AgentEditable, CreatedAt: unix(c.CreatedAt), UpdatedAt: unix(c.UpdatedAt),
	}
}

func categoryInput(req *category.SaveCategoryReq) (svc.Input, error) {
	fsID, err := id(req.FilesystemID)
	if err != nil {
		return svc.Input{}, err
	}
	return svc.Input{FilesystemID: fsID, Name: req.Name, Path: req.Path, Structure: req.Structure, Description: req.Description}, nil
}

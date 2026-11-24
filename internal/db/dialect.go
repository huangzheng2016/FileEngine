package db

import (
	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

func nameSearchCondition(tx *gorm.DB) string { return dal.CILikeCondition(tx, "name") }

func entriesUnder(tx *gorm.DB, sessionID uint, dir string) *gorm.DB {
	cond, args := dal.SubtreeCond(tx, "original_path", dir)
	return tx.Model(&model.FileEntry{}).Where("scan_session_id = ?", sessionID).Where(cond, args...)
}

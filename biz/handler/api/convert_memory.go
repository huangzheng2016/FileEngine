package api

import (
	"FileEngine/biz/dal/db/model"
	memory "FileEngine/biz/model/api/memory"
)

func toMemoryItem(m *model.MemoryItem) *memory.MemoryItem {
	return &memory.MemoryItem{
		ID: int64(m.ID), Scope: m.Scope, ScopeID: int64(m.ScopeID), Kind: m.Kind, Key: m.Key, Content: m.Content,
		Path: m.Path, Source: m.Source, Pinned: m.Pinned, CreatedAt: unix(m.CreatedAt), UpdatedAt: unix(m.UpdatedAt),
	}
}

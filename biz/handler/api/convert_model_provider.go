package api

import (
	"FileEngine/biz/dal/db/model"
	mp "FileEngine/biz/model/api/model_provider"
	mpsvc "FileEngine/biz/service/modelprovider"
)

func toModelProvider(m *model.ModelProvider) *mp.ModelProvider {
	return &mp.ModelProvider{
		ID: int64(m.ID), Name: m.Name, Provider: m.Provider, Model: m.Model, BaseURL: m.BaseURL,
		Temperature: m.Temperature, MaxTokens: int32(m.MaxTokens), ContextWindow: int32(m.EffectiveContextWindow()),
		VisionEnabled: m.VisionEnabled, ThinkingEnabled: m.ThinkingEnabled, ReasoningEffort: m.ReasoningEffort,
		APIKeySet: m.APIKey != "", CreatedAt: unix(m.CreatedAt), UpdatedAt: unix(m.UpdatedAt),
	}
}

func optInt(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func mpSaveInput(req *mp.SaveModelProviderReq, mid uint) mpsvc.SaveInput {
	return mpsvc.SaveInput{
		ID: mid, Name: req.Name, Provider: req.Provider, Model: req.Model, BaseURL: req.BaseURL,
		Temperature: req.Temperature, MaxTokens: int(req.MaxTokens), APIKey: req.APIKey,
		ContextWindow: optInt(req.ContextWindow), VisionEnabled: req.VisionEnabled,
		ThinkingEnabled: req.ThinkingEnabled, ReasoningEffort: req.ReasoningEffort,
	}
}

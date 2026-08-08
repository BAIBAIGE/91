package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// settingsDTO 是 GET/PUT /admin/api/settings 的入参/出参。
//
// 注意：早期的全局 previewEnabled 字段已经下沉为每盘 teaser_enabled，
// 不再出现在这里；前端要切换某个盘的预览视频生成请用 POST /admin/api/drives 上传
// teaserEnabled 字段。config.yaml 中的应用配置由 /admin/api/config.yaml
// 管理；这里仅保留已有的数据库型偏好设置。
type settingsDTO struct {
	Theme                   string `json:"theme"`
	AutoGenerateTagsEnabled bool   `json:"autoGenerateTagsEnabled"`
}

func (a *AdminServer) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	theme := "dark"
	if a.GetTheme != nil {
		if v := a.GetTheme(); v != "" {
			theme = v
		}
	}
	autoGenerateTagsEnabled := false
	if a.Catalog != nil {
		enabled, err := a.Catalog.AutoGenerateTagsEnabled(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		autoGenerateTagsEnabled = enabled
	}
	writeJSON(w, http.StatusOK, settingsDTO{
		Theme:                   theme,
		AutoGenerateTagsEnabled: autoGenerateTagsEnabled,
	})
}

func (a *AdminServer) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := raw["builtinTagsEnabled"]; ok {
		writeErr(w, http.StatusBadRequest, errors.New("builtinTagsEnabled is managed by config.yaml"))
		return
	}

	if v, ok := raw["theme"]; ok && a.SetTheme != nil {
		var theme string
		if err := json.Unmarshal(v, &theme); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if theme != "" {
			if err := a.SetTheme(theme); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
		}
	}
	if v, ok := raw["autoGenerateTagsEnabled"]; ok && a.Catalog != nil {
		var enabled bool
		if err := json.Unmarshal(v, &enabled); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Catalog.SetAutoGenerateTagsEnabled(r.Context(), enabled); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	// 回显当前值
	resp := settingsDTO{
		AutoGenerateTagsEnabled: false,
	}
	if a.GetTheme != nil {
		resp.Theme = a.GetTheme()
	}
	if a.Catalog != nil {
		enabled, err := a.Catalog.AutoGenerateTagsEnabled(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		resp.AutoGenerateTagsEnabled = enabled
	}
	writeJSON(w, http.StatusOK, resp)
}

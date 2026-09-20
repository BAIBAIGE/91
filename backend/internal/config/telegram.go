package config

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/video-site/backend/internal/scopedproxy"
)

type Telegram struct {
	BotToken            string  `yaml:"bot_token" json:"-"`
	Enabled             bool    `yaml:"enabled" json:"enabled"`
	AllowedUserIDs      []int64 `yaml:"allowed_user_ids" json:"allowedUserIds"`
	SiteBaseURL         string  `yaml:"site_base_url" json:"siteBaseUrl"`
	MaxFileSizeBytes    int64   `yaml:"max_file_size_bytes" json:"maxFileSizeBytes"`
	MaxPendingJobs      int     `yaml:"max_pending_jobs" json:"maxPendingJobs"`
	FetchTimeoutSeconds int     `yaml:"fetch_timeout_seconds" json:"fetchTimeoutSeconds"`
	UploadDriveID       string  `yaml:"upload_drive_id" json:"uploadDriveId"`
	UploadDirectory     string  `yaml:"upload_directory" json:"uploadDirectory"`
	UploadProxy         string  `yaml:"upload_proxy" json:"-"`

	// The runtime endpoint and paths come exclusively from the deployment's Compose file.
	APIBaseURL     string `yaml:"-" json:"apiBaseUrl"`
	APIFilesRoot   string `yaml:"-" json:"-"`
	LocalFilesRoot string `yaml:"-" json:"-"`
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func (t *Telegram) Validate() error {
	t.BotToken = strings.TrimSpace(t.BotToken)
	if t.BotToken != "" && !telegramTokenPattern.MatchString(t.BotToken) {
		return errors.New("telegram.bot_token 格式无效")
	}
	if t.Enabled && t.BotToken == "" {
		return errors.New("启用 Telegram 前请填写 bot_token")
	}

	if t.AllowedUserIDs == nil {
		t.AllowedUserIDs = []int64{}
	}
	t.UploadDriveID = strings.TrimSpace(t.UploadDriveID)
	proxy, err := scopedproxy.Normalize(t.UploadProxy)
	if err != nil {
		return errors.New("telegram.upload_proxy 必须为有效的 HTTP、HTTPS、SOCKS5 或 SOCKS5H 代理地址")
	}
	t.UploadProxy = proxy
	t.UploadDirectory = strings.TrimSpace(t.UploadDirectory)
	if t.UploadDirectory == "" {
		t.UploadDirectory = "Telegram"
	}
	for _, part := range strings.Split(t.UploadDirectory, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00\r\n") {
			return errors.New("telegram.upload_directory 必须为网盘根目录下的相对目录，如 Telegram/videos")
		}
	}
	if t.UploadDriveID == "local-upload" || t.UploadDriveID == "telegram-local" {
		return errors.New("telegram.upload_drive_id 必须选择支持上传的网盘")
	}
	if t.MaxFileSizeBytes == 0 {
		t.MaxFileSizeBytes = 4 << 30
	}
	if t.MaxPendingJobs == 0 {
		t.MaxPendingJobs = 100
	}
	if t.FetchTimeoutSeconds == 0 {
		t.FetchTimeoutSeconds = 1800
	}
	if t.MaxFileSizeBytes < 1 || t.MaxFileSizeBytes > 16<<30 {
		return errors.New("telegram.max_file_size_bytes 必须在 1 字节到 16 GiB 之间")
	}
	if t.MaxPendingJobs < 1 || t.MaxPendingJobs > 10000 {
		return errors.New("telegram.max_pending_jobs 必须在 1 到 10000 之间")
	}
	if t.FetchTimeoutSeconds < 30 || t.FetchTimeoutSeconds > 86400 {
		return errors.New("telegram.fetch_timeout_seconds 必须在 30 到 86400 之间")
	}
	if t.SiteBaseURL != "" {
		u, err := url.Parse(t.SiteBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("telegram.site_base_url 必须为有效的站点 HTTP(S) 地址")
		}
	}
	for _, id := range t.AllowedUserIDs {
		if id <= 0 {
			return errors.New("telegram.allowed_user_ids 必须为正整数用户 ID")
		}
	}
	return nil
}

// TelegramSettings returns an isolated copy of the validated live configuration.
func (m *Manager) TelegramSettings() Telegram {
	if m == nil {
		cfg := Telegram{}
		_ = cfg.Validate()
		return cfg
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.current.Telegram
	cfg.APIBaseURL = m.telegramDeployment.apiBaseURL
	cfg.APIFilesRoot = m.telegramDeployment.apiRoot
	cfg.LocalFilesRoot = m.telegramDeployment.localRoot
	cfg.AllowedUserIDs = append([]int64{}, cfg.AllowedUserIDs...)
	return cfg
}

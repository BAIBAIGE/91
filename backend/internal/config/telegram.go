package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/video-site/backend/internal/scopedproxy"
)

type Telegram struct {
	BotToken            string  `yaml:"bot_token" json:"-"`
	APIID               int64   `yaml:"api_id" json:"-"`
	APIHash             string  `yaml:"api_hash" json:"-"`
	Enabled             bool    `yaml:"enabled" json:"enabled"`
	APIBaseURL          string  `yaml:"api_base_url" json:"apiBaseUrl"`
	LocalFilesRoot      string  `yaml:"local_files_root" json:"localFilesRoot"`
	AllowedUserIDs      []int64 `yaml:"allowed_user_ids" json:"allowedUserIds"`
	SiteBaseURL         string  `yaml:"site_base_url" json:"siteBaseUrl"`
	MaxFileSizeBytes    int64   `yaml:"max_file_size_bytes" json:"maxFileSizeBytes"`
	MaxPendingJobs      int     `yaml:"max_pending_jobs" json:"maxPendingJobs"`
	FetchTimeoutSeconds int     `yaml:"fetch_timeout_seconds" json:"fetchTimeoutSeconds"`
	UploadDriveID       string  `yaml:"upload_drive_id" json:"uploadDriveId"`
	UploadDirectory     string  `yaml:"upload_directory" json:"uploadDirectory"`
	UploadProxy         string  `yaml:"upload_proxy" json:"-"`
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramAPIHashPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

func (t *Telegram) Validate() error {
	t.BotToken = strings.TrimSpace(t.BotToken)
	t.APIHash = strings.TrimSpace(t.APIHash)
	if t.BotToken != "" && !telegramTokenPattern.MatchString(t.BotToken) {
		return errors.New("telegram.bot_token 格式无效")
	}
	if t.APIHash != "" && !telegramAPIHashPattern.MatchString(t.APIHash) {
		return errors.New("telegram.api_hash 必须为 32 位十六进制字符")
	}
	if t.APIID < 0 || t.APIID > 2147483647 {
		return errors.New("telegram.api_id 必须为有效的正整数")
	}
	if t.Enabled && (t.BotToken == "" || t.APIHash == "" || t.APIID == 0) {
		return errors.New("启用 Telegram 前请填写 bot_token、api_id 和 api_hash")
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
	if t.APIBaseURL == "" {
		t.APIBaseURL = "http://telegram-bot-api:7878"
	}
	if t.LocalFilesRoot == "" {
		t.LocalFilesRoot = "/var/lib/telegram-bot-api"
		if runtime.GOOS == "windows" {
			base := os.Getenv("ProgramData")
			if base == "" {
				base = `C:\ProgramData`
			}
			t.LocalFilesRoot = filepath.Join(base, "telegram-bot-api")
		}
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
	if !filepath.IsAbs(t.LocalFilesRoot) {
		return errors.New("telegram.local_files_root 必须为绝对路径")
	}
	u, err := url.Parse(t.APIBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return errors.New("telegram.api_base_url 必须为不带路径、凭据或查询参数的 HTTP(S) 地址")
	}
	if strings.EqualFold(u.Hostname(), "api.telegram.org") {
		return errors.New("Telegram 视频导入需要自建 Local Bot API 地址")
	}
	if t.SiteBaseURL != "" {
		u, err = url.Parse(t.SiteBaseURL)
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
	cfg.AllowedUserIDs = append([]int64{}, cfg.AllowedUserIDs...)
	return cfg
}

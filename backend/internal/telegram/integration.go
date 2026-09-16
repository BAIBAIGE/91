package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/drives/telegramstorage"
	"github.com/video-site/backend/internal/mediaimport"
)

// Integration consumes the validated YAML snapshot and replaces receiver sessions.
// The Bot API supervisor consumes only a derived API credential file; it never
// receives the project's Bot Token or opens the project database.
type Integration struct {
	cat                   *catalog.Catalog
	configManager         *config.Manager
	uploadDir, controlDir string
	reserve               int64
	mu                    sync.RWMutex
	active                *Service
	status                Status
	version               string
	wake                  func()
	cancel                context.CancelFunc
	done                  chan struct{}
}

func NewIntegration(cat *catalog.Catalog, manager *config.Manager, uploadDir string, reserve int64, controlDir string) *Integration {
	cfg := manager.TelegramSettings()
	return &Integration{cat: cat, configManager: manager, uploadDir: uploadDir, reserve: reserve, controlDir: controlDir,
		status: Status{State: "disabled", Config: cfg}}
}
func (i *Integration) SetWake(wake func()) { i.mu.Lock(); i.wake = wake; i.mu.Unlock() }
func (i *Integration) Start(ctx context.Context) {
	ctx, i.cancel = context.WithCancel(ctx)
	i.done = make(chan struct{})
	go func() {
		defer close(i.done)
		defer i.stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			i.reconcile(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (i *Integration) Shutdown(ctx context.Context) error {
	if i.cancel == nil {
		return nil
	}
	i.cancel()
	select {
	case <-i.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (i *Integration) session() *Service { i.mu.RLock(); defer i.mu.RUnlock(); return i.active }
func (i *Integration) Status() Status {
	i.mu.RLock()
	s, status := i.active, i.status
	i.mu.RUnlock()
	if s != nil {
		return s.Status()
	}
	if n, err := mediaimport.AvailableBytes(status.Config.LocalFilesRoot); err == nil {
		status.CacheAvailableBytes = n
	}
	return status
}
func (i *Integration) Available() bool { s := i.session(); return s != nil && s.Available() }
func (i *Integration) BotID() int64 {
	if s := i.session(); s != nil {
		return s.BotID()
	}
	return 0
}
func (i *Integration) Allowed(id int64) bool { s := i.session(); return s != nil && s.Allowed(id) }
func (i *Integration) Resume(ctx context.Context) error {
	if s := i.session(); s != nil {
		return s.Resume(ctx)
	}
	return errors.New("请先保存配置并等待 Bot API 服务启动")
}
func (i *Integration) PreparePolling(ctx context.Context) error {
	if s := i.session(); s != nil {
		return s.PreparePolling(ctx)
	}
	return errors.New("请先保存配置并等待 Bot API 服务启动")
}
func (i *Integration) Test(ctx context.Context) (string, error) {
	if s := i.session(); s != nil {
		return TestConnection(ctx, s.cfg, s.token)
	}
	return "", errors.New("请先保存配置并等待 Bot API 服务启动")
}
func (i *Integration) Fetch(ctx context.Context, j *catalog.RemoteUploadJob, progress func(string, int64, int64) error) (mediaimport.SourceFile, error) {
	if s := i.session(); s != nil {
		file, err := s.Fetch(ctx, j, progress)
		if ctx.Err() == nil && (i.session() != s || (s.runCtx != nil && s.runCtx.Err() != nil)) {
			return mediaimport.SourceFile{}, &mediaimport.SourceError{Message: "Telegram 配置已变化，等待重新连接", WaitForAvailability: true, RetryAfter: time.Second}
		}
		return file, err
	}
	return mediaimport.SourceFile{}, &mediaimport.SourceError{Message: "Telegram 正在应用配置，请稍候", WaitForAvailability: true, RetryAfter: time.Second}
}
func (i *Integration) stop() {
	i.mu.Lock()
	s := i.active
	i.active = nil
	i.mu.Unlock()
	if s != nil {
		// Cancellation interrupts polling, notifications and outstanding downloads.
		// Wait for the old receiver before exposing a new bot identity.
		_ = s.Shutdown(context.Background())
	}
}
func (i *Integration) setStatus(cfg config.Telegram, state, message string) {
	i.mu.Lock()
	i.status = Status{Enabled: cfg.Enabled, Config: cfg, State: state, Error: message}
	i.mu.Unlock()
}
func (i *Integration) reconcile(ctx context.Context) {
	cfg := i.configManager.TelegramSettings()
	// Only Telegram edits rebuild a session; unrelated YAML edits leave it running.
	raw, _ := yaml.Marshal(cfg)
	digest := sha256.Sum256(raw)
	version := hex.EncodeToString(digest[:])
	if version != i.version {
		i.stop()
		i.version = version
	}
	ready := cfg.Enabled && cfg.BotToken != "" && cfg.APIID > 0 && cfg.APIHash != ""
	revision, err := i.publishBotAPI(cfg, ready)
	if err != nil {
		i.stop()
		i.setStatus(cfg, "error", "无法写入 Bot API 配置，请检查共享配置目录权限")
		return
	}
	if !cfg.Enabled {
		i.stop()
		i.setStatus(cfg, "disabled", "")
		return
	}
	if !ready {
		i.stop()
		i.setStatus(cfg, "waiting_config", "请在面板填写 Bot Token、API ID 和 API Hash")
		return
	}
	var process struct {
		Revision  string `json:"revision"`
		State     string `json:"state"`
		UpdatedAt int64  `json:"updatedAt"`
	}
	data, err := os.ReadFile(filepath.Join(cfg.LocalFilesRoot, "panel-status.json"))
	if err != nil || json.Unmarshal(data, &process) != nil || process.Revision != revision || process.State != "running" || time.Since(time.Unix(process.UpdatedAt, 0)) > 10*time.Second {
		i.stop()
		i.setStatus(cfg, "waiting_service", "配置已保存，等待配套 Bot API 服务启动；请确认服务及共享目录已部署")
		return
	}
	if i.session() == nil {
		s := New(cfg, cfg.BotToken, i.cat, i.uploadDir, i.reserve)
		i.mu.RLock()
		wake := i.wake
		i.mu.RUnlock()
		s.SetWake(wake)
		s.setState("connecting", nil)
		s.Start(ctx)
		i.mu.Lock()
		i.active = s
		i.mu.Unlock()
	}
}

// Publication is atomic, contains no Bot Token, and only changes when the
// process's API credentials or enabled state change. Read permission for group
// 0 lets the unprivileged companion container consume the dedicated mount.
func (i *Integration) publishBotAPI(s config.Telegram, enabled bool) (string, error) {
	payload := struct {
		Enabled bool   `json:"enabled"`
		APIID   int64  `json:"apiId"`
		APIHash string `json:"apiHash"`
	}{Enabled: enabled}
	if enabled {
		payload.APIID = s.APIID
		payload.APIHash = s.APIHash
	}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	revision := hex.EncodeToString(digest[:])
	output := struct {
		Revision string `json:"revision"`
		Config   any    `json:"config"`
	}{revision, payload}
	data, _ := json.Marshal(output)
	name := filepath.Join(i.controlDir, "config.json")
	if existing, err := os.ReadFile(name); err == nil && bytes.Equal(existing, data) {
		return revision, nil
	}
	if err := os.MkdirAll(i.controlDir, 0750); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(i.controlDir, ".config-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0640); err != nil {
		return "", err
	}
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return "", err
	}
	return revision, nil
}

func (i *Integration) Discard(ctx context.Context, j *catalog.RemoteUploadJob) error {
	return telegramstorage.New(i.cat).Remove(ctx, j.ID+".media")
}

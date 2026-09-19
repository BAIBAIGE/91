package telegram

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/drives/telegramstorage"
	"github.com/video-site/backend/internal/mediaimport"
	"github.com/video-site/backend/internal/videoname"
)

type Status struct {
	Enabled              bool            `json:"enabled"`
	State                string          `json:"state"`
	BotID                string          `json:"botId"`
	Username             string          `json:"username"`
	Error                string          `json:"error"`
	LastPoll             time.Time       `json:"lastPoll"`
	LastMessage          time.Time       `json:"lastMessage"`
	CacheAvailableBytes  int64           `json:"cacheAvailableBytes"`
	NotificationFailures int             `json:"notificationFailures"`
	Config               config.Telegram `json:"config"`
}
type Service struct {
	wakeImports func()
	cfg         config.Telegram
	token       string
	runCtx      context.Context
	cat         *catalog.Catalog
	uploadDir   string
	reserve     int64
	mu          sync.RWMutex
	control     sync.Mutex
	status      Status
	client      *client
	botID       int64
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

func New(cfg config.Telegram, token string, cat *catalog.Catalog, uploadDir string, reserve int64) *Service {
	return &Service{cfg: cfg, token: token, cat: cat, uploadDir: uploadDir, reserve: reserve, status: Status{Enabled: cfg.Enabled, State: "disabled", Config: cfg}}
}
func (s *Service) SetWake(wake func()) { s.mu.Lock(); s.wakeImports = wake; s.mu.Unlock() }

func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.runCtx = ctx
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.receive(ctx) }()
	go func() { defer s.wg.Done(); s.notify(ctx) }()
}
func (s *Service) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Status() Status {
	s.mu.RLock()
	out := s.status
	s.mu.RUnlock()
	if n, err := mediaimport.AvailableBytes(s.cfg.LocalFilesRoot); err == nil {
		out.CacheAvailableBytes = n
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if out.BotID != "" {
		id, _ := strconv.ParseInt(out.BotID, 10, 64)
		out.NotificationFailures = s.cat.TelegramNotificationFailures(ctx, id)
	}
	return out
}
func (s *Service) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.State == "connected"
}
func (s *Service) setState(state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = state
	s.status.Error = ""
	if err != nil {
		s.status.Error = err.Error()
	}
}
func (s *Service) Allowed(id int64) bool {
	for _, allowed := range s.cfg.AllowedUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}
func (s *Service) BotID() int64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.botID }
func (s *Service) Resume(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if !s.cfg.Enabled {
		return errors.New("请先在面板启用 Telegram")
	}
	c, u, err := checkConnection(ctx, s.cfg, s.token)
	if err != nil {
		return err
	}
	if _, _, err = s.cat.TelegramOffset(ctx, u.ID); err != nil {
		return err
	}
	if err = s.cat.ResumeTelegram(ctx, u.ID); err != nil {
		return err
	}
	s.mu.Lock()
	s.client = c
	s.botID = u.ID
	s.mu.Unlock()
	s.setState("connecting", nil)
	return nil
}
func (s *Service) PreparePolling(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.Available() {
		return errors.New("机器人正在接收消息，无需切换")
	}
	c, err := newClient(s.cfg.APIBaseURL, s.token)
	if err != nil {
		return err
	}
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}
func TestConnection(ctx context.Context, cfg config.Telegram, token string) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	_, u, err := checkConnection(ctx, cfg, token)
	return u.Username, err
}
func checkConnection(ctx context.Context, cfg config.Telegram, token string) (*client, user, error) {
	c, err := newClient(cfg.APIBaseURL, token)
	if err != nil {
		return nil, user{}, err
	}
	var u user
	if err = c.call(ctx, "getMe", struct{}{}, &u); err != nil {
		return nil, u, err
	}
	if u.ID <= 0 {
		return nil, u, errors.New("机器人身份无效")
	}
	var hook struct {
		URL string `json:"url"`
	}
	if err = c.call(ctx, "getWebhookInfo", struct{}{}, &hook); err != nil {
		return nil, u, err
	}
	if hook.URL != "" {
		return nil, u, errors.New("机器人已有 webhook，请先切换为轮询接收")
	}
	dir, err := os.Open(cfg.LocalFilesRoot)
	if err != nil {
		return nil, u, errors.New("无法读取共享视频目录")
	}
	defer dir.Close()
	if info, err := dir.Stat(); err != nil || !info.IsDir() {
		return nil, u, errors.New("共享视频路径不是目录")
	}
	return c, u, nil
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (s *Service) receive(ctx context.Context) {
	for ctx.Err() == nil {
		s.control.Lock()
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		c, u, err := checkConnection(checkCtx, s.cfg, s.token)
		cancel()
		if err == nil {
			s.mu.Lock()
			s.client = c
			s.botID = u.ID
			s.status.BotID = strconv.FormatInt(u.ID, 10)
			s.status.Username = u.Username
			s.mu.Unlock()
		}
		s.control.Unlock()
		if err != nil {
			s.setState("error", err)
			if !wait(ctx, 10*time.Second) {
				return
			}
			continue
		}
		offset, paused, err := s.cat.TelegramOffset(ctx, u.ID)
		if err != nil {
			s.setState("error", errors.New("无法读取 Telegram 接收状态"))
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if paused {
			s.setState("needs_reconnect", errors.New("备份已恢复，请检查连接后恢复接收"))
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		s.setState("connected", nil)
		for ctx.Err() == nil {
			pollCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
			var updates []update
			err = c.call(pollCtx, "getUpdates", map[string]any{"offset": offset, "timeout": 30, "allowed_updates": []string{"message"}}, &updates)
			cancel()
			if err != nil {
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.Code == 409 {
					s.setState("conflict", errors.New("同一机器人有其他接收实例，请停止其他实例后重启"))
					return
				}
				s.setState("error", err)
				delay := 10 * time.Second
				if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
					delay = apiErr.RetryAfter
				}
				if !wait(ctx, delay) {
					return
				}
				break
			}
			s.mu.Lock()
			s.status.LastPoll = time.Now()
			s.mu.Unlock()
			for _, u := range updates {
				if err = s.accept(ctx, u); err != nil {
					s.setState("error", errors.New("保存 TG 消息失败，稍后重试"))
					break
				}
				offset = u.ID + 1
				s.mu.Lock()
				s.status.LastMessage = time.Now()
				s.mu.Unlock()
			}
			if err != nil {
				if !wait(ctx, 5*time.Second) {
					return
				}
				break
			}
		}
	}
}
func (s *Service) accept(ctx context.Context, u update) error {
	botID := s.BotID()
	r := catalog.TelegramReceipt{BotID: botID, UpdateID: u.ID, MessageID: -u.ID - 1, Response: "__ignore__"}
	var source *catalog.TelegramSource
	title := ""
	if m := u.Message; m != nil {
		r.ChatID = m.Chat.ID
		r.MessageID = m.ID
		r.SenderID = m.From.ID
		if m.Chat.Type == "private" {
			command := strings.Fields(m.Text)
			if len(command) > 0 && strings.Split(command[0], "@")[0] == "/id" {
				r.Response = "你的 Telegram 用户 ID：" + strconv.FormatInt(m.From.ID, 10)
			} else if s.Allowed(m.From.ID) {
				r.Response = "请转发视频或发送视频文件；消息链接、图片和动画暂不支持。"
				file := m.Video
				if file == nil && m.Document != nil && isVideoDocument(*m.Document) {
					file = m.Document
				}
				if file != nil {
					r.Response = ""
					if file.FileID == "" || file.UniqueID == "" {
						r.Response = "视频文件信息不完整，请重新发送"
					} else if file.Size < 0 || file.Size > s.cfg.MaxFileSizeBytes {
						r.Response = "视频超过配置的单文件大小限制"
					} else {
						source = &catalog.TelegramSource{BotID: botID, SenderID: m.From.ID, FileID: file.FileID, UniqueID: file.UniqueID, FileName: file.Name, Size: file.Size, MIME: file.MIME}
						title = videoTitle(m.Caption, file.Name, m.ID)
						if err := s.invalidateMissingVideo(ctx, botID, file.UniqueID); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	err := s.cat.AcceptTelegramUpdate(ctx, r, source, "tg-"+hex.EncodeToString(random[:]), title, s.cfg.MaxPendingJobs)
	if err == nil {
		s.mu.RLock()
		wake := s.wakeImports
		s.mu.RUnlock()
		if wake != nil {
			wake()
		}
	}
	return err
}
func (s *Service) invalidateMissingVideo(ctx context.Context, botID int64, unique string) error {
	id, err := s.cat.TelegramVideoID(ctx, botID, unique)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	v, err := s.cat.GetVideo(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && v != nil && v.DriveID == telegramstorage.DriveID {
		info, e := telegramstorage.New(s.cat, func() string { return s.cfg.LocalFilesRoot }).Stat(ctx, v.FileID)
		if e == nil && info.Size > 0 {
			return nil
		}
		return s.cat.InvalidateMissingTelegramVideo(ctx, botID, unique)
	}
	if err == nil && v != nil && v.DriveID != "local-upload" {
		return nil
	}
	if err == nil && v != nil && filepath.Base(v.FileID) == v.FileID {
		info, e := os.Stat(filepath.Join(s.uploadDir, v.FileID))
		if e == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return nil
		}
	}
	return s.cat.InvalidateMissingTelegramVideo(ctx, botID, unique)
}
func isVideoDocument(m media) bool {
	if strings.HasPrefix(strings.ToLower(m.MIME), "video/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(m.Name)) {
	case ".mp4", ".mkv", ".webm", ".mov", ".avi":
		return true
	}
	return false
}
func videoTitle(caption, name string, id int64) string {
	title := ""
	for _, line := range strings.Split(caption, "\n") {
		if strings.TrimSpace(line) != "" {
			title = strings.TrimSpace(line)
			break
		}
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(strings.ReplaceAll(name, `\`, "/")), filepath.Ext(name))
	}
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return ' '
		}
		return r
	}, title)
	title = strings.Trim(strings.Join(strings.Fields(title), " "), ". ")
	for len(title) > 120 {
		rs := []rune(title)
		title = string(rs[:len(rs)-1])
	}
	if title == "" || title == "." {
		title = fmt.Sprintf("TG 视频 %d", id)
	}
	if videoname.ValidateUploadTitle(title, ".webm") != nil {
		title = fmt.Sprintf("TG 视频 %d", id)
	}
	return title
}

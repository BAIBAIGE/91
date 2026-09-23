package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/video-site/backend/internal/applog"
	"github.com/video-site/backend/internal/schedule"
)

const (
	commandStart  = "start"
	commandHelp   = "help"
	commandID     = "id"
	commandStatus = "status"
)

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Menu configuration belongs to the receiver session, not the connection probe.
// Run it independently so a slow or failed registration cannot delay imports.
func (s *Service) syncCommandMenu(ctx context.Context) {
	backoff := 10 * time.Second
	for ctx.Err() == nil {
		s.mu.RLock()
		c, connected := s.client, s.status.State == "connected"
		s.mu.RUnlock()
		if c == nil || !connected {
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}

		requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := registerCommandMenu(requestCtx, c)
		cancel()
		if err == nil || ctx.Err() != nil {
			return
		}
		applog.Warn(ctx, "同步 TG 命令菜单失败，稍后重试", err, applog.Fields{Component: "telegram", Stage: "command_menu"})
		delay := backoff
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
			delay = apiErr.RetryAfter
		}
		if !wait(ctx, delay) {
			return
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

func registerCommandMenu(ctx context.Context, c *client) error {
	// Commands are handled only in private chats. An omitted language_code
	// supplies this list to every language without a more specific override.
	if err := c.call(ctx, "setMyCommands", map[string]any{
		"commands": []botCommand{
			{Command: commandStart, Description: "开始"},
			{Command: commandHelp, Description: "使用说明"},
			{Command: commandStatus, Description: "转存统计"},
			{Command: commandID, Description: "我的 Telegram ID"},
		},
		"scope": map[string]string{"type": "all_private_chats"},
	}, nil); err != nil {
		return fmt.Errorf("注册 TG 指令列表失败: %w", err)
	}
	if err := c.call(ctx, "setChatMenuButton", map[string]any{
		"menu_button": map[string]string{"type": "commands"},
	}, nil); err != nil {
		return fmt.Errorf("设置 TG 菜单按钮失败: %w", err)
	}
	return nil
}

func (s *Service) statusMessage(ctx context.Context, now time.Time) (botMessage, error) {
	timezone := schedule.DefaultTimezone
	if s.statusTimezone != nil {
		timezone = s.statusTimezone()
	}
	_, location, err := schedule.LoadTimezone(timezone)
	if err != nil {
		return botMessage{}, err
	}
	stats, err := s.cat.CountTelegramImports(ctx, now.In(location))
	if err != nil {
		return botMessage{}, err
	}
	return renderStatusMessage(stats), nil
}

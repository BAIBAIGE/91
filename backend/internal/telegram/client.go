package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type APIError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("Telegram API 请求失败（%d）", e.Code) }

type client struct {
	base, token string
	http        *http.Client
}

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func newClient(baseURL, token string) (*client, error) {
	token = strings.TrimSpace(token)
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("请在 Telegram 面板填写有效的 Bot Token")
	}
	return &client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *client) call(ctx context.Context, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return errors.New("无法编码 Telegram 请求")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("Telegram API 地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &APIError{Code: 503, RetryAfter: 10 * time.Second}
	}
	defer resp.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		ErrorCode  int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return errors.New("Telegram API 响应无效")
	}
	if !envelope.OK {
		code := envelope.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		retry := time.Duration(envelope.Parameters.RetryAfter) * time.Second
		if code >= 500 && retry <= 0 {
			retry = 10 * time.Second
		}
		return &APIError{Code: code, RetryAfter: retry}
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Result, output); err != nil {
			return errors.New("Telegram API 数据无效")
		}
	}
	return nil
}

type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}
type media struct {
	FileID   string `json:"file_id"`
	UniqueID string `json:"file_unique_id"`
	Name     string `json:"file_name"`
	MIME     string `json:"mime_type"`
	Size     int64  `json:"file_size"`
}
type message struct {
	ID   int64 `json:"message_id"`
	From user  `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text     string `json:"text"`
	Caption  string `json:"caption"`
	Video    *media `json:"video"`
	Document *media `json:"document"`
}
type update struct {
	ID      int64    `json:"update_id"`
	Message *message `json:"message"`
}

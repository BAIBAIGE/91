package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SaveShortsFeed 持久化一个洗牌后的短视频 feed 快照（同 token 覆盖写）。
func (c *Catalog) SaveShortsFeed(ctx context.Context, token string, videoIDs []string, lastAccess time.Time) error {
	if token == "" {
		return errors.New("empty shorts feed token")
	}
	encoded, err := json.Marshal(videoIDs)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO shorts_feed_sessions (token, video_ids, last_access)
		 VALUES (?, ?, ?)
		 ON CONFLICT(token) DO UPDATE SET
		   video_ids = excluded.video_ids,
		   last_access = excluded.last_access`,
		token, string(encoded), lastAccess.UnixMilli(),
	)
	return err
}

// LoadShortsFeed 读取快照；token 不存在时返回 (nil, 零值, nil)。
// 是否过期由调用方按自己的 TTL 判断。
func (c *Catalog) LoadShortsFeed(ctx context.Context, token string) ([]string, time.Time, error) {
	var encoded string
	var lastAccess int64
	err := c.db.QueryRowContext(ctx,
		`SELECT video_ids, last_access FROM shorts_feed_sessions WHERE token = ?`,
		token,
	).Scan(&encoded, &lastAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var videoIDs []string
	if err := json.Unmarshal([]byte(encoded), &videoIDs); err != nil {
		return nil, time.Time{}, err
	}
	return videoIDs, time.UnixMilli(lastAccess), nil
}

// TouchShortsFeed 刷新快照的最近访问时间，维持活跃会话不过期。
func (c *Catalog) TouchShortsFeed(ctx context.Context, token string, lastAccess time.Time) error {
	_, err := c.db.ExecContext(ctx,
		`UPDATE shorts_feed_sessions SET last_access = ? WHERE token = ?`,
		lastAccess.UnixMilli(), token,
	)
	return err
}

// DeleteShortsFeed 删除单个快照（读取到过期或损坏数据时清理）。
func (c *Catalog) DeleteShortsFeed(ctx context.Context, token string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM shorts_feed_sessions WHERE token = ?`, token,
	)
	return err
}

// PruneShortsFeeds 删除最近访问早于 activeSince 的快照，并把总数收敛到
// maxSessions 以内（超出部分按最近访问从旧到新淘汰）。
func (c *Catalog) PruneShortsFeeds(ctx context.Context, activeSince time.Time, maxSessions int) error {
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM shorts_feed_sessions WHERE last_access < ?`,
		activeSince.UnixMilli(),
	); err != nil {
		return err
	}
	if maxSessions <= 0 {
		return nil
	}
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM shorts_feed_sessions WHERE token NOT IN (
		   SELECT token FROM shorts_feed_sessions
		    ORDER BY last_access DESC, token DESC
		    LIMIT ?
		 )`, maxSessions,
	)
	return err
}

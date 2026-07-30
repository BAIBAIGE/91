package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"-"`
	Role      string `json:"role"`
	Banned    bool   `json:"banned"`
	CreatedAt int64  `json:"createdAt"`
}

func (c *Catalog) CreateUser(ctx context.Context, username, hashedPassword, role string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, fmt.Errorf("username is required")
	}
	if role == "" {
		role = "user"
	}
	now := time.Now().UnixMilli()
	res, err := c.db.ExecContext(ctx,
		`INSERT INTO users (username, password, role, banned, created_at) VALUES (?, ?, ?, 0, ?)`,
		username, hashedPassword, role, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (c *Catalog) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := c.db.QueryRowContext(ctx,
		`SELECT id, username, password, role, banned, created_at FROM users WHERE username = ? COLLATE NOCASE`,
		username).Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.Banned, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (c *Catalog) GetUserByID(ctx context.Context, id int64) (*User, error) {
	u := &User{}
	err := c.db.QueryRowContext(ctx,
		`SELECT id, username, password, role, banned, created_at FROM users WHERE id = ?`,
		id).Scan(&u.ID, &u.Username, &u.Password, &u.Role, &u.Banned, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (c *Catalog) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, username, role, banned, created_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Banned, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListAdmins returns every administrator, including the stored password hash,
// for internal operations that must preserve target-environment credentials.
func (c *Catalog) ListAdmins(ctx context.Context) ([]*User, error) {
	rows, err := c.db.QueryContext(ctx, `
SELECT id, username, password, role, banned, created_at
  FROM users
 WHERE role = 'admin'
 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.Password,
			&u.Role,
			&u.Banned,
			&u.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (c *Catalog) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (c *Catalog) CountAdmins(ctx context.Context) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count)
	return count, err
}

func (c *Catalog) CountActiveAdmins(ctx context.Context) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND banned = 0`).Scan(&count)
	return count, err
}

func (c *Catalog) SetUserBanned(ctx context.Context, id int64, banned bool) error {
	val := 0
	if banned {
		val = 1
	}
	res, err := c.db.ExecContext(ctx,
		`UPDATE users SET banned = ? WHERE id = ?`, val, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (c *Catalog) DeleteUser(ctx context.Context, id int64) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (c *Catalog) UpdateUserPassword(ctx context.Context, id int64, hashedPassword string) error {
	res, err := c.db.ExecContext(ctx,
		`UPDATE users SET password = ? WHERE id = ?`, hashedPassword, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return err
}

type BannedIP struct {
	IP        string `json:"ip"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"createdAt"`
}

func (c *Catalog) ListBannedLoginIPs(ctx context.Context) ([]BannedIP, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT ip, reason, created_at FROM banned_login_ips ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BannedIP
	for rows.Next() {
		var item BannedIP
		if err := rows.Scan(&item.IP, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *Catalog) UnbanLoginIP(ctx context.Context, ip string) error {
	res, err := c.db.ExecContext(ctx, `DELETE FROM banned_login_ips WHERE ip = ?`, ip)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

package catalog

import (
	"context"
	"errors"
	"time"
)

// TelegramMediaGroupUpdate is a durable inbox entry. The Telegram receiver owns
// the payload format and title policy; the catalog owns delivery and admission.
type TelegramMediaGroupUpdate struct {
	BotID, ChatID, MessageID, UpdateID int64
	MediaGroupID, Payload              string
	ReceivedAt                         int64
}

type TelegramMediaGroup struct {
	BotID, ChatID int64
	ID            string
	Updates       []TelegramMediaGroupUpdate
}

type TelegramImport struct {
	Receipt   TelegramReceipt
	Source    *TelegramSource
	ID, Title string
}

// StageTelegramMediaGroupUpdate persists the message before acknowledging its
// update offset. Redelivery must not extend the group's collection window.
func (c *Catalog) StageTelegramMediaGroupUpdate(ctx context.Context, u TelegramMediaGroupUpdate) error {
	if u.MediaGroupID == "" || u.Payload == "" {
		return errors.New("missing Telegram media group or payload")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO telegram_media_group_updates
 (bot_id,chat_id,media_group_id,message_id,update_id,payload,received_at)
 SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (
  SELECT 1 FROM telegram_receipts WHERE bot_id=? AND (update_id=? OR (chat_id=? AND message_id=?))
 ) ON CONFLICT DO NOTHING`, u.BotID, u.ChatID, u.MediaGroupID, u.MessageID, u.UpdateID, u.Payload, time.Now().UnixMilli(), u.BotID, u.UpdateID, u.ChatID, u.MessageID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted > 0 {
		if err := advanceTelegramOffset(ctx, tx, u.BotID, u.UpdateID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PendingTelegramMediaGroups returns groups with no new members since before.
// One query takes a consistent snapshot, including members split across polls.
func (c *Catalog) PendingTelegramMediaGroups(ctx context.Context, botID int64, before time.Time) ([]TelegramMediaGroup, error) {
	rows, err := c.db.QueryContext(ctx, `WITH ready AS (
 SELECT bot_id,chat_id,media_group_id,MIN(update_id) AS first_update
 FROM telegram_media_group_updates WHERE bot_id=?
 GROUP BY bot_id,chat_id,media_group_id HAVING MAX(received_at)<=?
 ORDER BY first_update LIMIT 30
 ) SELECT u.bot_id,u.chat_id,u.media_group_id,u.message_id,u.update_id,u.payload,u.received_at
 FROM telegram_media_group_updates u JOIN ready r
 ON u.bot_id=r.bot_id AND u.chat_id=r.chat_id AND u.media_group_id=r.media_group_id
 ORDER BY r.first_update,u.message_id`, botID, before.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []TelegramMediaGroup
	for rows.Next() {
		var u TelegramMediaGroupUpdate
		if err := rows.Scan(&u.BotID, &u.ChatID, &u.MediaGroupID, &u.MessageID, &u.UpdateID, &u.Payload, &u.ReceivedAt); err != nil {
			return nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].ChatID != u.ChatID || groups[len(groups)-1].ID != u.MediaGroupID {
			groups = append(groups, TelegramMediaGroup{BotID: u.BotID, ChatID: u.ChatID, ID: u.MediaGroupID})
		}
		group := &groups[len(groups)-1]
		group.Updates = append(group.Updates, u)
	}
	return groups, rows.Err()
}

// AcceptTelegramMediaGroup atomically replaces buffered updates with receipts
// and import jobs. If another member arrived after the snapshot, collect again
// before publishing any titles. The polling offset was committed at staging.
func (c *Catalog) AcceptTelegramMediaGroup(ctx context.Context, group TelegramMediaGroup, imports []TelegramImport, limit int) (bool, error) {
	if len(imports) == 0 || len(imports) != len(group.Updates) {
		return false, errors.New("incomplete Telegram media group admission")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT update_id FROM telegram_media_group_updates
 WHERE bot_id=? AND chat_id=? AND media_group_id=? ORDER BY message_id`, group.BotID, group.ChatID, group.ID)
	if err != nil {
		return false, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(ids) != len(group.Updates) {
		return false, nil
	}
	for i, id := range ids {
		if id != group.Updates[i].UpdateID {
			return false, nil
		}
		r := imports[i].Receipt
		if r.BotID != group.BotID || r.ChatID != group.ChatID || r.UpdateID != id || r.MessageID != group.Updates[i].MessageID {
			return false, errors.New("Telegram media group receipt does not match buffered update")
		}
	}
	for _, input := range imports {
		if err := acceptTelegramUpdate(ctx, tx, input.Receipt, input.Source, input.ID, input.Title, limit); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_media_group_updates WHERE bot_id=? AND chat_id=? AND media_group_id=?`, group.BotID, group.ChatID, group.ID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

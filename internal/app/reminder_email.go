package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/providers"
)

type reminderEmailPayload struct {
	ReminderID string `json:"reminder_id"`
	DueAt      string `json:"due_at"`
}

func (a *App) processReminderEmailJob(ctx context.Context, userID string, payload string) error {
	var body reminderEmailPayload
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return err
	}
	if body.ReminderID == "" || body.DueAt == "" || userID == "" {
		return nil
	}
	reminder, email, ok, err := a.emailReminder(ctx, userID, body.ReminderID, body.DueAt)
	if err != nil || !ok {
		return err
	}
	settings, err := a.runtime.Effective(ctx)
	if err != nil {
		return err
	}
	client := providers.ResendClient{APIKey: settings.ResendAPIKey, From: settings.ResendFromEmail, HTTP: a.resendHTTP}
	subject := "Reminder: " + reminder.ItemTitle
	if err := client.Send(ctx, email, subject, reminderEmailHTML(reminder)); err != nil {
		if errors.Is(err, providers.ErrNotConfigured) {
			return nil
		}
		return err
	}
	_, err = a.db.ExecContext(ctx, `UPDATE reminders SET last_notified_at=? WHERE id=? AND user_id=? AND due_at=? AND status='pending'`, time.Now().UTC().Format(time.RFC3339), body.ReminderID, userID, body.DueAt)
	return err
}

type emailReminder struct {
	ItemTitle string
	ItemType  string
	ItemID    string
	DueAt     string
	Note      string
}

func (a *App) emailReminder(ctx context.Context, userID, reminderID, dueAt string) (emailReminder, string, bool, error) {
	row := a.db.QueryRowContext(ctx, `SELECT r.item_type,r.item_id,r.due_at,r.note,u.email FROM reminders r JOIN users u ON u.id=r.user_id WHERE r.id=? AND r.user_id=? AND r.due_at=? AND r.status='pending' AND r.notification_channel='email' AND r.last_notified_at IS NULL`, reminderID, userID, dueAt)
	var reminder emailReminder
	var email string
	if err := row.Scan(&reminder.ItemType, &reminder.ItemID, &reminder.DueAt, &reminder.Note, &email); err != nil {
		if err == sql.ErrNoRows {
			return emailReminder{}, "", false, nil
		}
		return emailReminder{}, "", false, err
	}
	title, err := a.reminderItemTitle(ctx, userID, reminder.ItemType, reminder.ItemID)
	if err != nil {
		return emailReminder{}, "", false, nil
	}
	reminder.ItemTitle = title
	return reminder, email, true, nil
}

func (a *App) reminderItemTitle(ctx context.Context, userID, itemType, itemID string) (string, error) {
	switch itemType {
	case "bookmark":
		var title, rawURL string
		if err := a.db.QueryRowContext(ctx, `SELECT title,url FROM bookmarks WHERE id=? AND user_id=?`, itemID, userID).Scan(&title, &rawURL); err != nil {
			return "", err
		}
		return firstNonEmpty(title, rawURL, itemID), nil
	case "note":
		var title string
		if err := a.db.QueryRowContext(ctx, `SELECT title FROM notes WHERE id=? AND user_id=?`, itemID, userID).Scan(&title); err != nil {
			return "", err
		}
		return firstNonEmpty(title, "Untitled note"), nil
	default:
		return "", fmt.Errorf("unknown reminder item type %s", itemType)
	}
}

func reminderEmailHTML(reminder emailReminder) string {
	var body strings.Builder
	body.WriteString("<p>")
	body.WriteString(html.EscapeString(reminder.ItemTitle))
	body.WriteString(" is due.</p>")
	if reminder.Note != "" {
		body.WriteString("<p>")
		body.WriteString(html.EscapeString(reminder.Note))
		body.WriteString("</p>")
	}
	body.WriteString("<p>Due: ")
	body.WriteString(html.EscapeString(reminder.DueAt))
	body.WriteString("</p>")
	return body.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

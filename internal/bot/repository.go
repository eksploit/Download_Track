package bot

import (
	"database/sql"
	"time"
)


// PendingEmailChangeRow — одна строка из списка заявок на смену email со статусом pending.
type PendingEmailChangeRow struct {
	ID        int64
	UserID    int
	OldEmail  string
	NewEmail  string
	Status    string
	CreatedAt time.Time
}

// repo хранит подключение к БД и реализует доступ к данным для бота.
type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo {
	return &repo{db: db}
}

func (r *repo) telegramExists(telegramID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow("SELECT EXISTS(SELECT 1 FROM telegram_users WHERE telegram_id=$1)", telegramID).Scan(&exists)
	return exists, err
}

func (r *repo) usernameByTelegramID(telegramID int64) (string, error) {
	var username string
	err := r.db.QueryRow("SELECT t.username FROM telegram_users t WHERE t.telegram_id=$1", telegramID).Scan(&username)
	return username, err
}

func (r *repo) createUser(email, apiKey string) (userID int, err error) {
	err = r.db.QueryRow(
		"INSERT INTO users (email, api_key) VALUES ($1,$2) RETURNING id",
		email, apiKey,
	).Scan(&userID)
	return userID, err
}

func (r *repo) linkTelegramUser(telegramID int64, username string, userID int) error {
	_, err := r.db.Exec(
		"INSERT INTO telegram_users (telegram_id, username, user_id) VALUES ($1,$2,$3)",
		telegramID, username, userID,
	)
	return err
}

func (r *repo) emailByTelegramID(telegramID int64) (string, error) {
	var email string
	err := r.db.QueryRow(
		`SELECT u.email
         FROM telegram_users t
         JOIN users u ON u.id = t.user_id
         WHERE t.telegram_id = $1`,
		telegramID,
	).Scan(&email)
	if err != nil {
		return "", err
	}
	return email, nil
}

func (r *repo) userIDByTelegramID(telegramID int64) (int, error) {
	var userID int
	err := r.db.QueryRow("SELECT user_id FROM telegram_users WHERE telegram_id=$1", telegramID).Scan(&userID)
	return userID, err
}

func (r *repo) apiKeyByUserID(userID int) (string, error) {
	var apiKey string
	err := r.db.QueryRow("SELECT api_key FROM users WHERE id=$1", userID).Scan(&apiKey)
	return apiKey, err
}

func (r *repo) listPendingEmailChangeRequests() ([]PendingEmailChangeRow, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, old_email, new_email, status, created_at
         FROM email_change_requests
         WHERE status = 'pending'
         ORDER BY created_at DESC, id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PendingEmailChangeRow
	for rows.Next() {
		var row PendingEmailChangeRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.OldEmail, &row.NewEmail, &row.Status, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// emailChangeRequestByID возвращает user_id, telegram_id, old_email, new_email, status. Возвращает sql.ErrNoRows, если заявка не найдена.
func (r *repo) emailChangeRequestByID(id int) (userID int, telegramID int64, oldEmail, newEmail, status string, err error) {
	err = r.db.QueryRow(
		`SELECT user_id, telegram_id, old_email, new_email, status
         FROM email_change_requests
         WHERE id = $1`,
		id,
	).Scan(&userID, &telegramID, &oldEmail, &newEmail, &status)
	return userID, telegramID, oldEmail, newEmail, status, err
}

func (r *repo) updateUserEmail(userID int, newEmail string) error {
	_, err := r.db.Exec(
		`UPDATE users SET email = $1 WHERE id = $2`,
		newEmail, userID,
	)
	return err
}

func (r *repo) updateEmailChangeRequestStatus(id int, status string) error {
	_, err := r.db.Exec(
		`UPDATE email_change_requests
         SET status = $1, processed_at = now()
         WHERE id = $2`,
		status, id,
	)
	return err
}

func (r *repo) userIDAndEmailByTelegramID(telegramID int64) (userID int, email string, err error) {
	err = r.db.QueryRow(
		`SELECT u.id, u.email
         FROM telegram_users t
         JOIN users u ON u.id = t.user_id
         WHERE t.telegram_id = $1`,
		telegramID,
	).Scan(&userID, &email)
	return userID, email, err
}

func (r *repo) createEmailChangeRequest(userID int, telegramID int64, oldEmail, newEmail string) (requestID int64, err error) {
	err = r.db.QueryRow(
		`INSERT INTO email_change_requests (user_id, telegram_id, old_email, new_email, status)
         VALUES ($1, $2, $3, $4, 'pending')
         RETURNING id`,
		userID, telegramID, oldEmail, newEmail,
	).Scan(&requestID)
	return requestID, err
}

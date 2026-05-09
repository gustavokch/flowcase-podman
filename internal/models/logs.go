package models

import (
	"github.com/jmoiron/sqlx"
)

// LogsRepo wraps DB access for the `log` table. The orchestrator writes
// every log line through here so admins can inspect history via the UI;
// see legacy utils/logger.py.
type LogsRepo struct{ DB *sqlx.DB }

func NewLogsRepo(db *sqlx.DB) *LogsRepo { return &LogsRepo{DB: db} }

// Create inserts one row. CreatedAt is filled by the schema default.
// Returns the newly assigned ID.
func (r *LogsRepo) Create(level, message string) (int64, error) {
	res, err := r.DB.Exec(`INSERT INTO log(level, message) VALUES(?, ?)`, level, message)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns the most recent `limit` log entries, newest first.
// Use 0 for "no limit".
func (r *LogsRepo) List(limit int) ([]LogEntry, error) {
	var xs []LogEntry
	q := `SELECT * FROM log ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	if err := r.DB.Select(&xs, q, args...); err != nil {
		return nil, err
	}
	return xs, nil
}

// DeleteAll truncates the log table. Used by the admin "clear logs"
// button in the legacy UI.
func (r *LogsRepo) DeleteAll() error {
	_, err := r.DB.Exec(`DELETE FROM log`)
	return err
}

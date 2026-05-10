package models

import (
	"strings"

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

// Paginate returns one page of log rows ordered by created_at DESC,
// plus the total row count matching the same filters.
//
// Filters:
//   - `level`: when non-empty, restricts to rows where level matches
//     exactly (case-sensitive — caller must upper-case if needed).
//   - `messageLike`: when non-empty, used as a SQL LIKE pattern on
//     the message column. Caller supplies wildcards (admin.py:853
//     uses '%Docker image%').
//
// `page` is 1-indexed. Pages or per_page < 1 are clamped to 1.
// `perPage` is capped at 1000 to keep one bad query from dumping the
// whole table.
//
// Mirrors the SQLAlchemy paginate(error_out=False) behavior at
// admin.py:706 / 860 — out-of-range pages return an empty slice
// rather than a 4xx.
func (r *LogsRepo) Paginate(level, messageLike string, page, perPage int) ([]LogEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 1000 {
		perPage = 1000
	}

	var (
		whereClauses []string
		args         []any
	)
	if level != "" {
		whereClauses = append(whereClauses, "level = ?")
		args = append(args, level)
	}
	if messageLike != "" {
		whereClauses = append(whereClauses, "message LIKE ?")
		args = append(args, messageLike)
	}
	where := ""
	if len(whereClauses) > 0 {
		where = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	if err := r.DB.Get(&total, `SELECT COUNT(*) FROM log`+where, args...); err != nil {
		return nil, 0, err
	}

	q := `SELECT * FROM log` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	pagedArgs := append(append([]any{}, args...), perPage, (page-1)*perPage)
	var xs []LogEntry
	if err := r.DB.Select(&xs, q, pagedArgs...); err != nil {
		return nil, 0, err
	}
	return xs, total, nil
}

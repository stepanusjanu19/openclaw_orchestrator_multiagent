package security

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type auditIndex struct {
	db *sql.DB
}

func newAuditIndex(storeDir string) (*auditIndex, error) {
	db, err := sql.Open("sqlite3", filepath.Join(storeDir, "audit-index.db"))
	if err != nil {
		return nil, err
	}
	idx := &auditIndex{db: db}
	if err := idx.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

func (idx *auditIndex) init() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp_ns INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			level TEXT,
			agent_id TEXT,
			channel TEXT,
			status TEXT,
			request_id TEXT,
			model TEXT,
			prompt_hash TEXT,
			output_hash TEXT,
			masking_mode TEXT,
			error_class TEXT,
			message TEXT,
			payload TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_time ON audit_events(timestamp_ns)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_agent ON audit_events(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_level ON audit_events(level)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_channel ON audit_events(channel)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_status ON audit_events(status)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS audit_events_fts USING fts5(search, content='')`,
	}
	for _, stmt := range stmts {
		if _, err := idx.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (idx *auditIndex) reset() error {
	tx, err := idx.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS audit_events_fts`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM audit_events`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE audit_events_fts USING fts5(search, content='')`); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (idx *auditIndex) add(ev AuditEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	search := strings.Join([]string{
		ev.RequestID,
		ev.Level,
		ev.AgentID,
		ev.Channel,
		ev.Model,
		ev.PromptHash,
		ev.OutputHash,
		ev.MaskingMode,
		ev.Status,
		ev.ErrorClass,
		ev.Message,
		metadataSearchText(ev.Metadata),
	}, " ")

	tx, err := idx.db.Begin()
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO audit_events (
		timestamp_ns, timestamp, level, agent_id, channel, status, request_id, model,
		prompt_hash, output_hash, masking_mode, error_class, message, payload
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Timestamp.UnixNano(),
		ev.Timestamp.Format(time.RFC3339Nano),
		strings.ToUpper(ev.Level),
		ev.AgentID,
		strings.ToLower(ev.Channel),
		strings.ToLower(ev.Status),
		ev.RequestID,
		ev.Model,
		ev.PromptHash,
		ev.OutputHash,
		ev.MaskingMode,
		ev.ErrorClass,
		ev.Message,
		string(payload),
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT INTO audit_events_fts(rowid, search) VALUES (?, ?)`, id, search); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (idx *auditIndex) query(q AuditQuery) ([]AuditEvent, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > maxAuditQueryLimit {
		limit = maxAuditQueryLimit
	}

	clauses := []string{"1=1"}
	args := []interface{}{}
	if q.AgentID != "" {
		clauses = append(clauses, "e.agent_id = ?")
		args = append(args, q.AgentID)
	}
	if q.Level != "" {
		clauses = append(clauses, "e.level = ?")
		args = append(args, strings.ToUpper(q.Level))
	}
	if q.Channel != "" {
		clauses = append(clauses, "e.channel = ?")
		args = append(args, strings.ToLower(q.Channel))
	}
	if q.Status != "" {
		clauses = append(clauses, "e.status = ?")
		args = append(args, strings.ToLower(q.Status))
	}
	if !q.From.IsZero() {
		clauses = append(clauses, "e.timestamp_ns >= ?")
		args = append(args, q.From.UnixNano())
	}
	if !q.To.IsZero() {
		clauses = append(clauses, "e.timestamp_ns <= ?")
		args = append(args, q.To.UnixNano())
	}

	query := `SELECT e.payload FROM audit_events e`
	if strings.TrimSpace(q.Query) != "" {
		query += ` JOIN audit_events_fts f ON f.rowid = e.id AND f.search MATCH ?`
		args = append([]interface{}{ftsQuery(q.Query)}, args...)
	}
	query += ` WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY e.timestamp_ns DESC LIMIT ?`
	args = append(args, limit)

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditEvent{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var ev AuditEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func metadataSearchText(meta map[string]interface{}) string {
	if len(meta) == 0 {
		return ""
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(b)
}

func ftsQuery(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return `""`
	}
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, `"`)
		part = strings.ReplaceAll(part, `"`, `""`)
		if part == "" {
			continue
		}
		terms = append(terms, fmt.Sprintf(`"%s"*`, part))
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " AND ")
}

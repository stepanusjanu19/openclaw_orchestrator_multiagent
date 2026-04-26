package visitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/config"

	"github.com/lib/pq"
)

type Store struct {
	db            *sql.DB
	enabled       bool
	retentionDays int
	cleanupMu     sync.Mutex
	lastCleanup   time.Time
}

const (
	MaxQueryLen = 200
	MaxOffset   = 10000
)

type Record struct {
	OccurredAt      time.Time              `json:"occurred_at"`
	RequestID       string                 `json:"request_id"`
	GatewayID       string                 `json:"gateway_id"`
	AgentID         string                 `json:"agent_id"`
	Channel         string                 `json:"channel"`
	Status          string                 `json:"status"`
	Decision        string                 `json:"decision"`
	RiskScore       int                    `json:"risk_score"`
	RiskReasons     []string               `json:"risk_reasons"`
	VisitorIP       string                 `json:"visitor_ip"`
	VisitorHash     string                 `json:"visitor_hash"`
	IPHash          string                 `json:"ip_hash"`
	IPClass         string                 `json:"ip_class"`
	Subnet          string                 `json:"subnet"`
	CIDR            string                 `json:"cidr"`
	ProxyChainDepth int                    `json:"proxy_chain_depth"`
	ASN             string                 `json:"asn"`
	Org             string                 `json:"org"`
	Country         string                 `json:"country"`
	Region          string                 `json:"region"`
	City            string                 `json:"city"`
	Latitude        *float64               `json:"latitude,omitempty"`
	Longitude       *float64               `json:"longitude,omitempty"`
	AccuracyKM      *float64               `json:"accuracy_km,omitempty"`
	Timezone        string                 `json:"timezone"`
	GeoSource       string                 `json:"geo_source"`
	DNSProvider     string                 `json:"dns_provider"`
	ReverseDNS      string                 `json:"reverse_dns"`
	UserAgentFamily string                 `json:"user_agent_family"`
	UserAgentHash   string                 `json:"user_agent_hash"`
	HostHash        string                 `json:"host_hash"`
	OriginHash      string                 `json:"origin_hash"`
	Proto           string                 `json:"proto"`
	Method          string                 `json:"method"`
	ContentType     string                 `json:"content_type"`
	BodyBytes       int                    `json:"body_bytes"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

type Query struct {
	Q      string
	Limit  int
	Offset int
}

type QueryResult struct {
	Rows   []Record `json:"rows"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

func NewStore(cfg *config.Config) *Store {
	if cfg == nil || !cfg.GatewayVisitorStore || strings.TrimSpace(cfg.PgVectorURL) == "" {
		return &Store{}
	}
	db, err := sql.Open("postgres", normalizePostgresDSN(cfg.PgVectorURL))
	if err != nil {
		log.Printf("[visitor] postgres disabled: %v", err)
		return &Store{}
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		log.Printf("[visitor] postgres unavailable, visitor DB disabled: %v", err)
		return &Store{}
	}
	s := &Store{db: db, enabled: true, retentionDays: cfg.GatewayVisitorRetentionDays}
	if err := s.ensureSchema(ctx); err != nil {
		_ = db.Close()
		log.Printf("[visitor] schema init failed, visitor DB disabled: %v", err)
		return &Store{}
	}
	if err := s.cleanupRetention(ctx, true); err != nil {
		log.Printf("[visitor] retention cleanup failed: %v", err)
	}
	return s
}

func normalizePostgresDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" || strings.Contains(strings.ToLower(dsn), "sslmode=") {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return dsn
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	ip := net.ParseIP(host)
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || (ip != nil && ip.IsLoopback()) {
		q := u.Query()
		q.Set("sslmode", "disable")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn
}

func (s *Store) Enabled() bool {
	return s != nil && s.enabled && s.db != nil
}

func (s *Store) Close() error {
	if !s.Enabled() {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ensureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS gateway_visitors (
			id BIGSERIAL PRIMARY KEY,
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			request_id TEXT NOT NULL,
			gateway_id TEXT,
			agent_id TEXT,
			channel TEXT,
			status TEXT,
			decision TEXT,
			risk_score INTEGER DEFAULT 0,
			risk_reasons TEXT[] DEFAULT '{}',
			visitor_ip INET,
			visitor_hash TEXT,
			ip_hash TEXT,
			ip_class TEXT,
			subnet CIDR,
			cidr CIDR,
			proxy_chain_depth INTEGER DEFAULT 0,
			asn TEXT,
			org TEXT,
			country TEXT,
			region TEXT,
			city TEXT,
			latitude DOUBLE PRECISION,
			longitude DOUBLE PRECISION,
			accuracy_km DOUBLE PRECISION,
			timezone TEXT,
			geo_source TEXT,
			dns_provider TEXT,
			reverse_dns TEXT,
			user_agent_family TEXT,
			user_agent_hash TEXT,
			host_hash TEXT,
			origin_hash TEXT,
			proto TEXT,
			method TEXT,
			content_type TEXT,
			body_bytes INTEGER DEFAULT 0,
			metadata JSONB DEFAULT '{}'::jsonb,
			search_text TEXT,
			search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', coalesce(search_text, ''))) STORED
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_visitors_time ON gateway_visitors (occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_visitors_gateway ON gateway_visitors (gateway_id)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_visitors_status ON gateway_visitors (status)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_visitors_subnet ON gateway_visitors (subnet)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_visitors_search ON gateway_visitors USING gin (search_vector)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Insert(ctx context.Context, rec Record) error {
	if !s.Enabled() {
		return nil
	}
	if rec.OccurredAt.IsZero() {
		rec.OccurredAt = time.Now()
	}
	meta := rec.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	metaBytes, _ := json.Marshal(meta)
	search := buildSearchText(rec)
	insertCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(insertCtx, `INSERT INTO gateway_visitors (
		occurred_at, request_id, gateway_id, agent_id, channel, status, decision, risk_score, risk_reasons,
		visitor_ip, visitor_hash, ip_hash, ip_class, subnet, cidr, proxy_chain_depth,
		asn, org, country, region, city, latitude, longitude, accuracy_km, timezone, geo_source,
		dns_provider, reverse_dns, user_agent_family, user_agent_hash, host_hash, origin_hash,
		proto, method, content_type, body_bytes, metadata, search_text
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,
		NULLIF($10,'')::inet,$11,$12,$13,NULLIF($14,'')::cidr,NULLIF($15,'')::cidr,$16,
		$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,
		$27,$28,$29,$30,$31,$32,
		$33,$34,$35,$36,$37::jsonb,$38
	)`,
		rec.OccurredAt, rec.RequestID, rec.GatewayID, rec.AgentID, rec.Channel, rec.Status, rec.Decision, rec.RiskScore, pq.Array(rec.RiskReasons),
		rec.VisitorIP, rec.VisitorHash, rec.IPHash, rec.IPClass, rec.Subnet, rec.CIDR, rec.ProxyChainDepth,
		rec.ASN, rec.Org, rec.Country, rec.Region, rec.City, rec.Latitude, rec.Longitude, rec.AccuracyKM, rec.Timezone, rec.GeoSource,
		rec.DNSProvider, rec.ReverseDNS, rec.UserAgentFamily, rec.UserAgentHash, rec.HostHash, rec.OriginHash,
		rec.Proto, rec.Method, rec.ContentType, rec.BodyBytes, string(metaBytes), search,
	)
	if err == nil {
		if cleanupErr := s.cleanupRetention(ctx, false); cleanupErr != nil {
			log.Printf("[visitor] retention cleanup failed: %v", cleanupErr)
		}
	}
	return err
}

func (s *Store) Query(ctx context.Context, q Query) (QueryResult, error) {
	out := QueryResult{Limit: normalizeLimit(q.Limit), Offset: q.Offset}
	if !s.Enabled() {
		return out, nil
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	if out.Offset > MaxOffset {
		out.Offset = MaxOffset
	}
	queryText := strings.TrimSpace(q.Q)
	if len(queryText) > MaxQueryLen {
		return out, fmt.Errorf("query visitor maksimal %d karakter", MaxQueryLen)
	}
	where := ""
	args := []interface{}{}
	if queryText != "" {
		where = `WHERE search_vector @@ plainto_tsquery('simple', $1)`
		args = append(args, queryText)
	}
	countSQL := `SELECT count(*) FROM gateway_visitors ` + where
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.db.QueryRowContext(queryCtx, countSQL, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, out.Limit, out.Offset)
	limitParam := len(args) - 1
	offsetParam := len(args)
	rowsSQL := fmt.Sprintf(`SELECT
		occurred_at, request_id, gateway_id, agent_id, channel, status, decision, risk_score, risk_reasons,
		coalesce(visitor_ip::text,''), visitor_hash, ip_hash, ip_class, coalesce(subnet::text,''), coalesce(cidr::text,''), proxy_chain_depth,
		asn, org, country, region, city, latitude, longitude, accuracy_km, timezone, geo_source,
		dns_provider, reverse_dns, user_agent_family, user_agent_hash, host_hash, origin_hash,
		proto, method, content_type, body_bytes, metadata
		FROM gateway_visitors %s
		ORDER BY occurred_at DESC
		LIMIT $%d OFFSET $%d`, where, limitParam, offsetParam)
	rows, err := s.db.QueryContext(queryCtx, rowsSQL, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var rec Record
		var metaRaw []byte
		var lat, lon, acc sql.NullFloat64
		if err := rows.Scan(
			&rec.OccurredAt, &rec.RequestID, &rec.GatewayID, &rec.AgentID, &rec.Channel, &rec.Status, &rec.Decision, &rec.RiskScore, pq.Array(&rec.RiskReasons),
			&rec.VisitorIP, &rec.VisitorHash, &rec.IPHash, &rec.IPClass, &rec.Subnet, &rec.CIDR, &rec.ProxyChainDepth,
			&rec.ASN, &rec.Org, &rec.Country, &rec.Region, &rec.City, &lat, &lon, &acc, &rec.Timezone, &rec.GeoSource,
			&rec.DNSProvider, &rec.ReverseDNS, &rec.UserAgentFamily, &rec.UserAgentHash, &rec.HostHash, &rec.OriginHash,
			&rec.Proto, &rec.Method, &rec.ContentType, &rec.BodyBytes, &metaRaw,
		); err != nil {
			return out, err
		}
		if lat.Valid {
			rec.Latitude = &lat.Float64
		}
		if lon.Valid {
			rec.Longitude = &lon.Float64
		}
		if acc.Valid {
			rec.AccuracyKM = &acc.Float64
		}
		_ = json.Unmarshal(metaRaw, &rec.Metadata)
		out.Rows = append(out.Rows, rec)
	}
	return out, rows.Err()
}

func (s *Store) cleanupRetention(ctx context.Context, force bool) error {
	if !s.Enabled() || s.retentionDays <= 0 {
		return nil
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if !force && time.Since(s.lastCleanup) < time.Hour {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(cleanupCtx, `DELETE FROM gateway_visitors WHERE occurred_at < now() - ($1::int * interval '1 day')`, s.retentionDays)
	if err == nil {
		s.lastCleanup = time.Now()
	}
	return err
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func buildSearchText(rec Record) string {
	lat := ""
	lon := ""
	acc := ""
	if rec.Latitude != nil {
		lat = strconv.FormatFloat(*rec.Latitude, 'f', -1, 64)
	}
	if rec.Longitude != nil {
		lon = strconv.FormatFloat(*rec.Longitude, 'f', -1, 64)
	}
	if rec.AccuracyKM != nil {
		acc = strconv.FormatFloat(*rec.AccuracyKM, 'f', -1, 64)
	}
	parts := []string{
		rec.RequestID, rec.GatewayID, rec.AgentID, rec.Channel, rec.Status, rec.Decision,
		strconv.Itoa(rec.RiskScore), strings.Join(rec.RiskReasons, " "),
		rec.VisitorIP, rec.VisitorHash, rec.IPHash, rec.IPClass, rec.Subnet, rec.CIDR,
		rec.ASN, rec.Org, rec.Country, rec.Region, rec.City, rec.Timezone, rec.GeoSource,
		lat, lon, acc, rec.DNSProvider, rec.ReverseDNS, rec.UserAgentFamily,
		rec.UserAgentHash, rec.HostHash, rec.OriginHash, rec.Proto, rec.Method, rec.ContentType,
	}
	return strings.Join(parts, " ")
}

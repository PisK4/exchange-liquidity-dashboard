// Package liquidity owns the pure compute + state-machine + render
// helpers for the Dashboard liquidity-lag (#10) and worst-depth (#11)
// alert cards. See architecture/方案设计/EdgeX运营/Listing/
// 2026-05-29-Listing-Agent-Dashboard-Liquidity-Alerts-#10-#11.md and
// docs/feat/listing-agent-liquidity-alert.md for the authoritative contract.
//
// The package has no MySQL / HTTP / config import edges: callers
// (listing.ProduceLiquidityAlertPush, the preview CLI) project their
// own datasource into the input types defined here. This mirrors the
// internal/divergence layering so unit tests stay deterministic.
package liquidity

import "time"

// AlertKind discriminates which of the two liquidity alerts a
// candidate row belongs to. The string form is also persisted into
// t_listing_alert_state.alert_kind and used as the
// t_listing_delivery_outbox.event_type.
type AlertKind string

const (
	// KindLiquidityLag fires when edgeX's depth at the configured
	// tier drops below `LagThreshold * competitor median`. PRD §3.7
	// default LagThreshold = 0.5 (i.e. edgeX < half the median).
	KindLiquidityLag AlertKind = "liquidity_lag"
	// KindWorstDepth fires when edgeX's depth is the second-lowest
	// (rank == N-1, 0-indexed; N == total platforms including
	// edgeX). PRD §3.7: "edgeX 在所有可对比平台中深度排名倒数第二".
	KindWorstDepth AlertKind = "worst_depth"
)

// PlatformDepthRow is a single (platform, canonical, tier) row at
// the freshest available snapshot. Stale / error / unsupported rows
// MUST be filtered upstream so Compute can assume every row is a
// real, successful observation.
type PlatformDepthRow struct {
	Platform      string
	DisplaySymbol string
	Tier          string // e.g. "0.001"
	DepthUSD      float64
	BidUSD        float64
	AskUSD        float64
	SnapshotTS    time.Time
}

// AlertCandidate is the Compute output. One canonical may produce
// up to two candidates (one liquidity_lag + one worst_depth) in the
// same tick.
type AlertCandidate struct {
	Kind           AlertKind
	Canonical      string
	DisplaySymbol  string  // a human-friendly label, e.g. "BTC-USDT (perp)"
	Tier           string  // mirrors Config.DepthTierPct rendered as e.g. "0.1%"
	EdgexDepth     float64 // edgeX's depth at this tier
	MedianDepth    float64 // competitor median (excludes edgeX)
	Ratio          float64 // EdgexDepth / MedianDepth (lag_threshold context)
	Comparators    int     // number of non-edgeX platforms with fresh data
	TotalPlatforms int     // Comparators + 1 (edgeX itself)
	EdgexRank      int     // 1-based; 1 == strongest, TotalPlatforms == weakest
	Platforms      []AlertPlatformRow
	EvaluatedAt    time.Time
}

// AlertPlatformRow is one row of the per-platform depth list rendered
// into the Lark card. Sorted by depth DESC so the card naturally
// reads top→bottom from strongest to weakest.
type AlertPlatformRow struct {
	Platform   string
	DepthUSD   float64
	Rank       int // 1-based
	IsEdgex    bool
	IsMedian   bool
	SnapshotTS time.Time
}

// CanonicalResolver folds (platform, displaySymbol) into a canonical
// symbol. Mirrors divergence.CanonicalResolver but with an extra
// PlatformExclusive lookup the alert path needs to exclude specialty
// markets where N-1 ranking is meaningless.
type CanonicalResolver interface {
	ResolveCanonical(platform, base string) string
	IsPlatformExclusive(canonical string) bool
}

// ListedLookup answers the "is canonical X currently listed on edgeX?"
// question. Liquidity alerts only apply to listed markets; for
// unlisted symbols the Top30 #1-#5 cards do the heavy lifting.
type ListedLookup interface {
	IsListed(platform, base string) bool
}

// Config is the runtime tuning passed into Compute / DecideAction.
// Mirrors config.LiquidityAlertConfig 1-to-1 so callers can copy
// fields without importing the config package.
type Config struct {
	DepthTierPct     float64
	LagThreshold     float64
	MinComparators   int
	ReissueInterval  time.Duration
	ClearConsecutive int
}

// AlertState mirrors one row of t_listing_alert_state. Lifecycle:
//
//	(no row) ── trigger ──▶ active(seq=1) ── reissue×N ──┐
//	                                                     │
//	cleared ◀── clear_streak ≥ N ─── active              │
//	   │                                                 │
//	   └── trigger ──▶ active(seq+=1) ◀───────────────────┘
type AlertState struct {
	Kind             AlertKind
	Canonical        string
	Status           AlertStatus
	SeveritySeq      int
	ReissueCount     int
	ClearStreak      int
	FirstTriggeredAt time.Time
	LastPushedAt     time.Time
	LastEvaluatedAt  time.Time
}

// AlertStatus is the row-level lifecycle marker.
type AlertStatus string

const (
	StatusActive  AlertStatus = "active"
	StatusCleared AlertStatus = "cleared"
)

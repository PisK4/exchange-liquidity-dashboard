package fetcher

import (
	"fmt"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing"
	"edgex-ops-intelligence/backend/internal/listing/announcement"
)

// BuiltListingSources is what BuildListingSources hands back: the
// concrete listing.InstrumentSource / listing.AnnouncementSource
// slices the engine threads into EngineDeps. We return a struct
// (rather than two raw slices) so callers cannot accidentally swap
// the argument order at the call site.
type BuiltListingSources struct {
	Instrument   []listing.InstrumentSource
	Announcement []listing.AnnouncementSource
}

// BuildListingSources composes the live HTTP-backed listing sources
// for the engine. It walks the config-declared roster
// (cfg.Sources.InstrumentDiff.Polls and cfg.Sources.Announcement.Polls),
// dispatches each (platform, market_type) tuple to the matching
// fetcher constructor, and returns the assembled InstrumentSource /
// AnnouncementSource slices.
//
// Contract:
//   - Subsystem-level Enabled=false short-circuits the whole branch
//     (e.g. InstrumentDiff.Enabled=false yields zero instrument
//     sources regardless of per-poll Enabled values).
//   - Per-poll Enabled=false is silently skipped (operator opts a
//     single source out without removing the entry).
//   - Unknown (platform, market_type) combos or unknown announcement
//     platforms return an error rather than skipping silently —
//     misconfiguration should fail loud, not produce a half-armed
//     engine.
//   - The function does not perform any I/O; it just wires closures.
//     Source health and baseline state are the engine's concern.
func BuildListingSources(cfg config.ListingSourcesConfig, deps HTTPDeps) (BuiltListingSources, error) {
	out := BuiltListingSources{}
	if cfg.InstrumentDiff.Enabled {
		for _, poll := range cfg.InstrumentDiff.Polls {
			if !poll.Enabled {
				continue
			}
			src, err := buildInstrumentSource(poll, deps)
			if err != nil {
				return BuiltListingSources{}, err
			}
			out.Instrument = append(out.Instrument, src)
		}
	}
	if cfg.Announcement.Enabled {
		for _, poll := range cfg.Announcement.Polls {
			if !poll.Enabled {
				continue
			}
			src, err := buildAnnouncementSource(poll, deps)
			if err != nil {
				return BuiltListingSources{}, err
			}
			out.Announcement = append(out.Announcement, src)
		}
	}
	return out, nil
}

func buildInstrumentSource(poll config.ListingSourcePollConfig, deps HTTPDeps) (listing.InstrumentSource, error) {
	key := poll.Platform + "/" + poll.MarketType
	src := listing.InstrumentSource{
		Platform:   poll.Platform,
		MarketType: poll.MarketType,
		SourceKey:  key,
	}
	switch key {
	case "binance/usdm_futures":
		src.SourceURL = BinanceUSDMExchangeInfoURL
		src.Fetch = FetchBinanceUSDM(deps, BinanceUSDMExchangeInfoURL)
	case "bybit/linear":
		// Bybit's config/source-health key remains bybit/linear for
		// compatibility, but the normalized snapshot table stores this
		// surface as linear_futures. Keep the poll baseline key aligned
		// with snapshots so warm-path instrument_diff can fire.
		src.MarketType = "linear_futures"
		src.SourceURL = BybitLinearInstrumentsURL
		src.Fetch = FetchBybitLinear(deps, BybitLinearInstrumentsURL)
	case "okx/swap":
		src.SourceURL = OKXPublicInstrumentsURL
		src.Fetch = FetchOKXSwap(deps, OKXPublicInstrumentsURL)
	case "bitget/usdt_futures":
		src.SourceURL = BitgetMixContractsURL
		src.Fetch = FetchBitgetUSDTFutures(deps, BitgetMixContractsURL)
	case "mexc/contract":
		src.SourceURL = MEXCContractDetailURL
		src.Fetch = FetchMEXCContract(deps, MEXCContractDetailURL)
	case "hyperliquid/perp":
		src.SourceURL = HyperliquidInfoURL
		src.Fetch = FetchHyperliquidPerp(deps, HyperliquidInfoURL)
	case "edgeX/perp_v1":
		src.SourceURL = EdgeXPerpV1MetaURL
		src.Fetch = FetchEdgeXPerpV1(deps, EdgeXPerpV1MetaURL)
	case "edgeX/perp_v2":
		src.SourceURL = EdgeXPerpV2MetaURL
		src.Fetch = FetchEdgeXPerpV2(deps, EdgeXPerpV2MetaURL)
	case "edgeX/spot":
		src.SourceURL = EdgeXSpotMetaURL
		src.Fetch = FetchEdgeXSpot(deps, EdgeXSpotMetaURL)
	case "bingx/spot":
		src.SourceURL = BingXSpotSymbolsURL
		src.Fetch = FetchBingXSpot(deps, BingXSpotSymbolsURL)
	case "bingx/swap":
		src.SourceURL = BingXSwapContractsURL
		src.Fetch = FetchBingXSwap(deps, BingXSwapContractsURL)
	case "gate/spot":
		src.SourceURL = GateSpotCurrencyPairsURL
		src.Fetch = FetchGateSpot(deps, GateSpotCurrencyPairsURL)
	case "gate/usdt_futures":
		src.SourceURL = GateUSDTFuturesContractsURL
		src.Fetch = FetchGateUSDTFutures(deps, GateUSDTFuturesContractsURL)
	case "lighter/perp":
		src.SourceURL = LighterOrderBookDetailsURL
		src.Fetch = FetchLighterPerp(deps, LighterOrderBookDetailsURL)
	case "lighter/spot":
		src.SourceURL = LighterOrderBookDetailsURL
		src.Fetch = FetchLighterSpot(deps, LighterOrderBookDetailsURL)
	default:
		return listing.InstrumentSource{}, fmt.Errorf("listing: no instrument fetcher registered for platform=%q market_type=%q", poll.Platform, poll.MarketType)
	}
	src.SignalingMode = listing.SignalingMode(poll.SignalingMode)
	return src, nil
}

func buildAnnouncementSource(poll config.ListingSourcePollConfig, deps HTTPDeps) (listing.AnnouncementSource, error) {
	src := listing.AnnouncementSource{
		Platform:  poll.Platform,
		SourceKey: poll.Platform + "/announcement",
	}
	switch poll.Platform {
	case "binance":
		src.SourceURL = BinanceCMSArticleListURL
		src.Fetch = FetchBinanceCMSAnnouncements(deps, BinanceCMSArticleListURL, DefaultBinanceCMSCatalogID)
		src.Parse = announcement.ParseBinanceCMSAnnouncement
	case "bybit":
		src.SourceURL = BybitAnnouncementsURL
		src.Fetch = FetchBybitAnnouncements(deps, BybitAnnouncementsURL)
		src.Parse = announcement.ParseBybitAnnouncement
	case "bitget":
		src.SourceURL = BitgetAnnouncementsURL
		src.Fetch = FetchBitgetAnnouncements(deps, BitgetAnnouncementsURL)
		src.Parse = announcement.ParseBitgetAnnouncement
	case "hyperliquid":
		src.SourceURL = HyperliquidEntriesURL
		src.Fetch = FetchHyperliquidAnnouncements(deps, HyperliquidEntriesURL)
		src.Parse = announcement.ParseHyperliquidAnnouncement
	default:
		return listing.AnnouncementSource{}, fmt.Errorf("listing: no announcement fetcher registered for platform=%q", poll.Platform)
	}
	return src, nil
}

package adapter

import (
	"sort"

	"edgex-dashboard/backend/internal/domain"
)

func finalizeBook(book domain.OrderBookSnapshot, sourceID, depthSource, sourceEndpoint string) domain.OrderBookSnapshot {
	if sourceID == "" {
		sourceID = "raw"
	}
	if depthSource == "" {
		depthSource = domain.SourceRawOrderbook
	}
	if sourceEndpoint == "" {
		sourceEndpoint = book.SourceEndpoint
	}
	book.SourceID = sourceID
	book.DepthSource = depthSource
	book.SourceEndpoint = sourceEndpoint
	book.BidLevelsReturned = len(book.Bids)
	book.AskLevelsReturned = len(book.Asks)
	book.LevelsReturned = book.BidLevelsReturned + book.AskLevelsReturned
	book.FarthestBidPct, book.FarthestAskPct = farthestSideDistancePct(book.Bids, book.Asks)
	book.FarthestDistancePC = maxFloat(book.FarthestBidPct, book.FarthestAskPct)
	book.DepthStatus, book.PartialReason = classifyDepth(book)
	if book.SourceBooks == nil {
		book.SourceBooks = map[string]domain.BookView{}
	}
	view := domain.BookView{
		SourceID:       sourceID,
		Source:         depthSource,
		SourceEndpoint: sourceEndpoint,
		Bids:           book.Bids,
		Asks:           book.Asks,
		SnapshotTS:     book.SnapshotTS,
		APILevelCap:    book.APILevelCap,
	}
	book.SourceBooks[sourceID] = enrichBookViewMetrics(view, midPrice(book.Bids, book.Asks))
	return book
}

func TierDepthMetrics(book domain.OrderBookSnapshot, tier float64) domain.TierDepthMetrics {
	view := selectBookView(book, tier)
	mid := midPrice(book.Bids, book.Asks)
	if mid <= 0 {
		mid = midPrice(view.Bids, view.Asks)
	}
	bidFloor := mid * (1 - tier)
	askCeil := mid * (1 + tier)
	var bidUSD, askUSD float64
	for _, level := range view.Bids {
		if level.Price >= bidFloor {
			bidUSD += level.Price * level.Size
		}
	}
	for _, level := range view.Asks {
		if level.Price <= askCeil {
			askUSD += level.Price * level.Size
		}
	}
	bidLevels := len(view.Bids)
	askLevels := len(view.Asks)
	levels := bidLevels + askLevels
	view = enrichBookViewMetrics(view, mid)
	farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
	status, reason := classifyDepthView(view.Source, farBid, farAsk, tier*100, levels, view.APILevelCap)
	resolutionOK := viewResolutionOK(view, tier, mid)
	if farBid >= tier*100 && farAsk >= tier*100 && !resolutionOK {
		status = domain.StatusPartial
		reason = domain.ReasonFeedTruncation
	}
	metric := domain.TierDepthMetrics{
		BidUSD:               bidUSD,
		AskUSD:               askUSD,
		TotalUSD:             bidUSD + askUSD,
		DepthStatus:          status,
		PartialReason:        reason,
		DepthSource:          view.Source,
		SourceID:             view.SourceID,
		SourceEndpoint:       view.SourceEndpoint,
		LevelsReturned:       levels,
		BidLevelsReturned:    bidLevels,
		AskLevelsReturned:    askLevels,
		APILevelCap:          view.APILevelCap,
		FarthestBidPct:       farBid,
		FarthestAskPct:       farAsk,
		FarthestDistancePct:  maxFloat(farBid, farAsk),
		AggregationParams:    view.AggregationParams,
		PolicyAcceptance:     policyAcceptanceForView(view.Source, status),
		UnofficialUIEndpoint: view.UnofficialUIEndpoint,
	}
	if view.PolicyAcceptance == domain.PolicyLooseGroupedApprox || view.PolicyAcceptance == domain.PolicyLooseLowerBound {
		metric.DepthStatus = domain.StatusPartial
		metric.StrictComplete = false
		metric.PolicyAcceptance = view.PolicyAcceptance
		if metric.PartialReason == "" {
			metric.PartialReason = domain.ReasonFeedTruncation
		}
	}
	domain.DeriveDepthMetricsDefaults(book.DepthStatus, &metric)
	return metric
}

func selectBookView(book domain.OrderBookSnapshot, tier float64) domain.BookView {
	mid := midPrice(book.Bids, book.Asks)
	candidates := make([]domain.BookView, 0, len(book.SourceBooks)+1)
	if book.SourceBooks != nil {
		for _, view := range book.SourceBooks {
			candidates = append(candidates, enrichBookViewMetrics(view, mid))
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, enrichBookViewMetrics(domain.BookView{
			SourceID:       book.SourceID,
			Source:         book.DepthSource,
			SourceEndpoint: book.SourceEndpoint,
			Bids:           book.Bids,
			Asks:           book.Asks,
			SnapshotTS:     book.SnapshotTS,
			APILevelCap:    book.APILevelCap,
		}, mid))
	}
	if mid <= 0 && len(candidates) > 0 {
		mid = midPrice(candidates[0].Bids, candidates[0].Asks)
		for i := range candidates {
			candidates[i] = enrichBookViewMetrics(candidates[i], mid)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return viewStepForSort(candidates[i]) < viewStepForSort(candidates[j])
	})
	targetPct := tier * 100
	maxStep := tier * mid / 4
	for _, view := range candidates {
		farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
		if farBid >= targetPct && farAsk >= targetPct && (view.StepUSD <= 0 || view.StepUSD <= maxStep || view.Source != domain.SourceAggregatedOrderbook) && view.PolicyAcceptance == "" {
			return view
		}
	}
	best := candidates[0]
	bestCoverage := -1.0
	for _, view := range candidates {
		farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
		coverage := minFloat(farBid, farAsk)
		if coverage > bestCoverage {
			best = view
			bestCoverage = coverage
		}
	}
	return best
}

func viewStepForSort(view domain.BookView) float64 {
	if view.StepUSD <= 0 {
		return 0
	}
	return view.StepUSD
}

func viewResolutionOK(view domain.BookView, tier, mid float64) bool {
	if view.Source != domain.SourceAggregatedOrderbook {
		return true
	}
	if view.StepUSD <= 0 || mid <= 0 {
		return true
	}
	return view.StepUSD <= tier*mid/4
}

func classifyDepth(book domain.OrderBookSnapshot) (string, string) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	levels := book.LevelsReturned
	if levels == 0 {
		levels = len(book.Bids) + len(book.Asks)
	}
	farBid, farAsk := farthestSideDistancePct(book.Bids, book.Asks)
	return classifyDepthView(book.DepthSource, farBid, farAsk, 2, levels, book.APILevelCap)
}

func classifyDepthView(source string, farBid, farAsk, targetPct float64, levels, apiMax int) (string, string) {
	if levels == 0 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	covered := farBid >= targetPct && farAsk >= targetPct
	if covered {
		if source == domain.SourceAggregatedOrderbook {
			return domain.StatusAggregatedOrderbook, ""
		}
		if source == domain.SourceWSLimitedDepth {
			return domain.StatusWSLimitedDepth, ""
		}
		return domain.StatusComplete, ""
	}
	if apiMax > 0 && levels >= apiMax {
		return domain.StatusPartial, domain.ReasonAPILevelCap
	}
	if apiMax > 0 && float64(levels) < float64(apiMax)*0.8 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	return domain.StatusPartial, domain.ReasonUnknown
}

func policyAcceptanceForView(source, status string) string {
	switch status {
	case domain.StatusComplete:
		return domain.PolicyRawStrict
	case domain.StatusAggregatedOrderbook, domain.StatusWSLimitedDepth:
		return domain.PolicyAggregatedStrict
	case domain.StatusPartial:
		if source == domain.SourceAggregatedOrderbook {
			return domain.PolicyLooseGroupedApprox
		}
		return domain.PolicyLooseLowerBound
	default:
		return ""
	}
}

func enrichBookViewMetrics(view domain.BookView, mid float64) domain.BookView {
	if view.StepUSD <= 0 {
		view.StepUSD = medianAdjacentStepUSD(view.Bids, view.Asks)
	}
	if view.ResolutionPct <= 0 && view.StepUSD > 0 && mid > 0 {
		view.ResolutionPct = view.StepUSD / mid * 100
	}
	if view.PolicyAcceptance == "" {
		view.PolicyAcceptance = policyAcceptanceForView(view.Source, "")
	}
	return view
}

func medianAdjacentStepUSD(bids, asks []domain.Level) float64 {
	diffs := make([]float64, 0, maxInt(len(bids)-1, 0)+maxInt(len(asks)-1, 0))
	appendDiffs := func(levels []domain.Level) {
		for i := 1; i < len(levels); i++ {
			diff := mathAbs(levels[i].Price - levels[i-1].Price)
			if diff > 0 {
				diffs = append(diffs, diff)
			}
		}
	}
	appendDiffs(bids)
	appendDiffs(asks)
	if len(diffs) == 0 {
		return 0
	}
	sort.Float64s(diffs)
	mid := len(diffs) / 2
	if len(diffs)%2 == 1 {
		return diffs[mid]
	}
	return (diffs[mid-1] + diffs[mid]) / 2
}

func apiLevelCap(platform string) int {
	switch platform {
	case "binance":
		return 2000
	case "okx":
		return 10000
	case "bybit":
		return 2000
	case "bitget":
		return 200
	case "bingx":
		return 2000
	case "mexc":
		return 2000
	case "gate":
		return 400
	case "hyperliquid":
		return 40
	case "edgeX":
		return 400
	case "lighter":
		return 0
	default:
		return 0
	}
}

func farthestDistancePct(book domain.OrderBookSnapshot) float64 {
	farBid, farAsk := farthestSideDistancePct(book.Bids, book.Asks)
	return maxFloat(farBid, farAsk)
}

func farthestSideDistancePct(bids, asks []domain.Level) (float64, float64) {
	return farthestSideDistancePctFromMid(bids, asks, midPrice(bids, asks))
}

func farthestSideDistancePctFromMid(bids, asks []domain.Level, mid float64) (float64, float64) {
	if len(bids) == 0 || len(asks) == 0 || mid <= 0 {
		return 0, 0
	}
	farBid := mathAbs(bids[len(bids)-1].Price-mid) / mid * 100
	farAsk := mathAbs(asks[len(asks)-1].Price-mid) / mid * 100
	return farBid, farAsk
}

func midPrice(bids, asks []domain.Level) float64 {
	if len(bids) == 0 || len(asks) == 0 {
		return 0
	}
	return (bids[0].Price + asks[0].Price) / 2
}

func defaultSourceID(platform string) string {
	switch platform {
	case "okx":
		return "okx_books_full"
	case "bybit":
		return "bybit_raw_1000"
	case "lighter":
		return "lighter_ws"
	default:
		return "raw"
	}
}

func defaultDepthSource(platform string) string {
	if platform == "lighter" {
		return domain.SourceWSLocalBook
	}
	return domain.SourceRawOrderbook
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

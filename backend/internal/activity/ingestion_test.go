package activity

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type fakeIngestionStore struct {
	existingStates map[string]SourceState
	loadCalls      []string
	sourceStates   []SourceState
	rawEvidence    []RawEvidence
	events         []ActivityEvent
	nextRawID      int64
	nextEventID    int64
}

func (f *fakeIngestionStore) LoadActivitySourceState(ctx context.Context, sourceKey string) (SourceState, bool, error) {
	f.loadCalls = append(f.loadCalls, sourceKey)
	if f.existingStates == nil {
		return SourceState{}, false, nil
	}
	state, ok := f.existingStates[sourceKey]
	return state, ok, nil
}

func (f *fakeIngestionStore) UpsertActivitySourceState(ctx context.Context, state SourceState) error {
	f.sourceStates = append(f.sourceStates, state)
	return nil
}

func (f *fakeIngestionStore) UpsertRawEvidence(ctx context.Context, row RawEvidence) (int64, error) {
	if f.nextRawID == 0 {
		f.nextRawID = 100
	}
	f.nextRawID++
	row.ID = f.nextRawID
	f.rawEvidence = append(f.rawEvidence, row)
	return row.ID, nil
}

func (f *fakeIngestionStore) UpsertActivityEvent(ctx context.Context, event ActivityEvent) (int64, bool, error) {
	if f.nextEventID == 0 {
		f.nextEventID = 200
	}
	f.nextEventID++
	event.ID = f.nextEventID
	f.events = append(f.events, event)
	return event.ID, true, nil
}

func TestIngestSourcesFetchesRawEvidenceParsesAndPersistsEvents(t *testing.T) {
	now := time.Date(2026, 6, 6, 8, 30, 0, 0, time.UTC)
	store := &fakeIngestionStore{}
	fetchCalls := 0
	parseCalls := 0
	payload := []byte(`{"id":"launchpool-abc","title":"Binance Launchpool ABC","summary":"Stake BNB to earn ABC","url":"https://binance.example/abc","activity_type":"launchpool","symbols":["ABC"]}`)

	res, err := IngestSources(context.Background(), store, IngestionDeps{
		Sources: []SourceConfig{{
			Platform:        "binance",
			SourceGroup:     "cms_article_list",
			SourceURL:       "https://binance.example/list",
			FetchMode:       "http_direct",
			Enabled:         true,
			AutoPushEnabled: true,
			PollInterval:    10 * time.Minute,
		}},
		Fetch: func(ctx context.Context, req FetchRequest) (FetchResult, error) {
			fetchCalls++
			if req.URL != "https://binance.example/list" {
				t.Fatalf("unexpected fetch URL %q", req.URL)
			}
			return FetchResult{
				Platform:    req.Platform,
				SourceGroup: req.SourceGroup,
				SourceURL:   req.URL,
				FetchMode:   req.FetchMode,
				Payload:     payload,
				PayloadHash: "payload-hash",
				ContentHash: "content-hash",
				HTTPStatus:  200,
				ContentType: "application/json",
				FetchedAt:   now,
			}, nil
		},
		Parse: func(ctx context.Context, doc RawDocument) ([]ActivityEvent, error) {
			parseCalls++
			if string(doc.Payload) != string(payload) {
				t.Fatalf("parser payload mismatch")
			}
			return []ActivityEvent{{
				Platform:         doc.Platform,
				SourceGroup:      doc.SourceGroup,
				SourceExternalID: "launchpool-abc",
				SourceURL:        "https://binance.example/abc",
				Title:            "Binance Launchpool ABC",
				ActivityType:     "launchpool",
				ContentText:      "Stake BNB to earn ABC",
				ContentHash:      "content-hash",
				DedupeKey:        BuildEventDedupeKey(doc.Platform, doc.SourceGroup, "launchpool-abc", ""),
				AutoPushAllowed:  true,
				ReviewStatus:     ReviewPending,
				EventVersion:     1,
				ParserVersion:    "test-parser",
				TargetSymbols: []ActivityEventSymbol{{
					CanonicalSymbol: "ABC", DisplaySymbol: "ABC-USDT", MarketSurface: "perp", Role: "target",
				}},
			}}, nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("IngestSources err=%v", err)
	}
	if res.Sources != 1 || res.Fetched != 1 || res.RawEvidence != 1 || res.Events != 1 || res.UnchangedSources != 0 {
		t.Fatalf("result=%+v", res)
	}
	if fetchCalls != 1 || parseCalls != 1 {
		t.Fatalf("fetchCalls=%d parseCalls=%d", fetchCalls, parseCalls)
	}
	if len(store.rawEvidence) != 1 || store.rawEvidence[0].PayloadText != string(payload) {
		t.Fatalf("rawEvidence=%+v", store.rawEvidence)
	}
	if len(store.events) != 1 || store.events[0].RawEvidenceID != store.rawEvidence[0].ID {
		t.Fatalf("events=%+v raw=%+v", store.events, store.rawEvidence)
	}
	if len(store.sourceStates) != 1 || store.sourceStates[0].SourceStatus != SourceStatusOK {
		t.Fatalf("sourceStates=%+v", store.sourceStates)
	}
	if store.sourceStates[0].LastHTTPStatus == nil || *store.sourceStates[0].LastHTTPStatus != 200 {
		b, _ := json.Marshal(store.sourceStates[0])
		t.Fatalf("source state http status missing: %s", b)
	}
}

func TestIngestSourcesSkipsRawEvidenceAndEventsWhenContentUnchanged(t *testing.T) {
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	src := SourceConfig{
		Platform:        "binance",
		SourceGroup:     "cms_article_list",
		SourceURL:       "https://binance.example/list",
		FetchMode:       "http_direct",
		Enabled:         true,
		AutoPushEnabled: true,
		PollInterval:    30 * time.Minute,
	}
	store := &fakeIngestionStore{existingStates: map[string]SourceState{
		BuildSourceKey(src.Platform, src.SourceGroup, src.FetchMode): {LastContentHash: "same-content-hash"},
	}}
	parseCalls := 0

	res, err := IngestSources(context.Background(), store, IngestionDeps{
		Sources: []SourceConfig{src},
		Fetch: func(ctx context.Context, req FetchRequest) (FetchResult, error) {
			return FetchResult{
				Platform:     req.Platform,
				SourceGroup:  req.SourceGroup,
				SourceURL:    req.URL,
				FetchMode:    req.FetchMode,
				Payload:      []byte(`{"title":"same"}`),
				PayloadHash:  "same-payload-hash",
				ContentHash:  "same-content-hash",
				HTTPStatus:   200,
				ContentType:  "application/json",
				FetchedAt:    now,
				ElapsedMS:    23,
				AttemptCount: 2,
				ProxyUsed:    true,
			}, nil
		},
		Parse: func(ctx context.Context, doc RawDocument) ([]ActivityEvent, error) {
			parseCalls++
			return nil, nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("IngestSources err=%v", err)
	}
	if res.Sources != 1 || res.Fetched != 1 || res.RawEvidence != 0 || res.Events != 0 || res.UnchangedSources != 1 {
		t.Fatalf("result=%+v", res)
	}
	if parseCalls != 0 || len(store.rawEvidence) != 0 || len(store.events) != 0 {
		t.Fatalf("parseCalls=%d raw=%+v events=%+v", parseCalls, store.rawEvidence, store.events)
	}
	if len(store.sourceStates) != 1 || store.sourceStates[0].SourceStatus != SourceStatusOK || store.sourceStates[0].SampleCount != 0 {
		t.Fatalf("sourceStates=%+v", store.sourceStates)
	}
	if store.sourceStates[0].LastSuccessAt == nil {
		t.Fatalf("LastSuccessAt not set: %+v", store.sourceStates[0])
	}
	var sourceContext map[string]any
	if err := json.Unmarshal(store.sourceStates[0].SourceContextJSON, &sourceContext); err != nil {
		t.Fatalf("source_context_json err=%v json=%s", err, store.sourceStates[0].SourceContextJSON)
	}
	if sourceContext["attempt_count"] != float64(2) || sourceContext["proxy_used"] != true || sourceContext["elapsed_ms"] != float64(23) {
		t.Fatalf("source_context_json=%+v", sourceContext)
	}
}

func TestIngestSourcesPersistsWhenExistingContentChanges(t *testing.T) {
	now := time.Date(2026, 6, 6, 9, 15, 0, 0, time.UTC)
	src := SourceConfig{
		Platform:        "gate",
		SourceGroup:     "launchpool_project_list",
		SourceURL:       "https://gate.example/list",
		FetchMode:       "utls_proxy_json",
		Enabled:         true,
		AutoPushEnabled: true,
		PollInterval:    30 * time.Minute,
	}
	store := &fakeIngestionStore{existingStates: map[string]SourceState{
		BuildSourceKey(src.Platform, src.SourceGroup, src.FetchMode): {LastContentHash: "old-content-hash"},
	}}

	res, err := IngestSources(context.Background(), store, IngestionDeps{
		Sources: []SourceConfig{src},
		Fetch: func(ctx context.Context, req FetchRequest) (FetchResult, error) {
			return FetchResult{
				Platform:    req.Platform,
				SourceGroup: req.SourceGroup,
				SourceURL:   req.URL,
				FetchMode:   req.FetchMode,
				Payload:     []byte(`{"id":"new","title":"New launchpool"}`),
				ContentHash: "new-content-hash",
				HTTPStatus:  200,
				FetchedAt:   now,
			}, nil
		},
		Parse: func(ctx context.Context, doc RawDocument) ([]ActivityEvent, error) {
			return []ActivityEvent{{
				Platform:         doc.Platform,
				SourceGroup:      doc.SourceGroup,
				SourceExternalID: "new",
				Title:            "New launchpool",
				ActivityType:     "launchpool",
				ContentHash:      "new-content-hash",
			}}, nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("IngestSources err=%v", err)
	}
	if res.RawEvidence != 1 || res.Events != 1 || res.UnchangedSources != 0 {
		t.Fatalf("result=%+v", res)
	}
	if len(store.rawEvidence) != 1 || len(store.events) != 1 {
		t.Fatalf("raw=%+v events=%+v", store.rawEvidence, store.events)
	}
}

func TestIngestSourcesKeepsRawEvidenceForNon2xxEvenWhenContentUnchanged(t *testing.T) {
	now := time.Date(2026, 6, 6, 9, 30, 0, 0, time.UTC)
	src := SourceConfig{
		Platform:        "bingx",
		SourceGroup:     "openapi_notice",
		SourceURL:       "https://bingx.example/notices",
		FetchMode:       "http_direct_json",
		Enabled:         true,
		AutoPushEnabled: true,
		PollInterval:    30 * time.Minute,
	}
	store := &fakeIngestionStore{existingStates: map[string]SourceState{
		BuildSourceKey(src.Platform, src.SourceGroup, src.FetchMode): {LastContentHash: "same-content-hash"},
	}}
	parseCalls := 0

	res, err := IngestSources(context.Background(), store, IngestionDeps{
		Sources: []SourceConfig{src},
		Fetch: func(ctx context.Context, req FetchRequest) (FetchResult, error) {
			return FetchResult{
				Platform:     req.Platform,
				SourceGroup:  req.SourceGroup,
				SourceURL:    req.URL,
				FetchMode:    req.FetchMode,
				Payload:      []byte(`service unavailable`),
				PayloadHash:  "same-payload-hash",
				ContentHash:  "same-content-hash",
				HTTPStatus:   503,
				ContentType:  "text/plain",
				FetchedAt:    now,
				AttemptCount: 3,
			}, nil
		},
		Parse: func(ctx context.Context, doc RawDocument) ([]ActivityEvent, error) {
			parseCalls++
			return nil, nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("IngestSources err=%v", err)
	}
	if res.RawEvidence != 1 || res.SourceErrors != 1 || res.UnchangedSources != 0 {
		t.Fatalf("result=%+v", res)
	}
	if parseCalls != 0 || len(store.rawEvidence) != 1 || len(store.events) != 0 {
		t.Fatalf("parseCalls=%d raw=%+v events=%+v", parseCalls, store.rawEvidence, store.events)
	}
	var meta map[string]any
	if err := json.Unmarshal(store.rawEvidence[0].ResponseMeta, &meta); err != nil {
		t.Fatalf("raw meta err=%v json=%s", err, store.rawEvidence[0].ResponseMeta)
	}
	if meta["http_status"] != float64(503) || meta["attempt_count"] != float64(3) || meta["source_url"] != src.SourceURL {
		t.Fatalf("raw meta=%+v", meta)
	}
}

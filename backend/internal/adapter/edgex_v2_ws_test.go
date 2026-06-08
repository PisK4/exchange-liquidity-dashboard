package adapter

import (
	"strings"
	"testing"
	"time"
)

func TestEdgeXPerpV2WSProviderSnapshotUpdateAndDelete(t *testing.T) {
	provider := NewEdgeXPerpV2WSProvider("", time.Minute)
	snapshot := []byte(`{"channel":"depth.30000001.200","dataType":"Snapshot","timestamp":1893456000000,"data":[{"contractId":"30000001","startVersion":"10","endVersion":"10","bids":[{"price":"100","size":"1"}],"asks":[{"price":"101","size":"2"}]}]}`)
	if err := provider.handleMessage(snapshot); err != nil {
		t.Fatalf("snapshot err=%v", err)
	}
	update := []byte(`{"channel":"depth.30000001.200","dataType":"changed","timestamp":1893456001000,"data":[{"contractId":"30000001","startVersion":"11","endVersion":"11","bids":[{"price":"99","size":"3"},{"price":"100","size":"0"}],"asks":[{"price":"102","size":"4"}]}]}`)
	if err := provider.handleMessage(update); err != nil {
		t.Fatalf("update err=%v", err)
	}

	bids, asks, _, err := provider.Snapshot("30000001")
	if err != nil {
		t.Fatalf("Snapshot err=%v", err)
	}
	if len(bids) != 1 || bids[0].Price != 99 || bids[0].Size != 3 {
		t.Fatalf("unexpected bids after update/delete: %+v", bids)
	}
	if len(asks) != 2 || asks[0].Price != 101 || asks[1].Price != 102 {
		t.Fatalf("unexpected asks after update: %+v", asks)
	}
}

func TestEdgeXPerpV2WSProviderDetectsVersionGapAndRebuildsOnSnapshot(t *testing.T) {
	provider := NewEdgeXPerpV2WSProvider("", time.Minute)
	if err := provider.handleMessage([]byte(`{"channel":"depth.30000001.200","dataType":"Snapshot","data":[{"contractId":"30000001","startVersion":"10","endVersion":"10","bids":[{"price":"100","size":"1"}],"asks":[{"price":"101","size":"1"}]}]}`)); err != nil {
		t.Fatalf("snapshot err=%v", err)
	}
	err := provider.handleMessage([]byte(`{"channel":"depth.30000001.200","dataType":"changed","data":[{"contractId":"30000001","startVersion":"12","endVersion":"12","bids":[{"price":"99","size":"1"}],"asks":[{"price":"102","size":"1"}]}]}`))
	if err == nil || !strings.Contains(err.Error(), "version gap") {
		t.Fatalf("expected version gap error, got %v", err)
	}
	if _, _, _, snapErr := provider.Snapshot("30000001"); snapErr == nil || !strings.Contains(snapErr.Error(), "version gap") {
		t.Fatalf("Snapshot should surface gap error, got %v", snapErr)
	}

	if err := provider.handleMessage([]byte(`{"channel":"depth.30000001.200","dataType":"Snapshot","data":[{"contractId":"30000001","startVersion":"20","endVersion":"20","bids":[{"price":"98","size":"2"}],"asks":[{"price":"103","size":"2"}]}]}`)); err != nil {
		t.Fatalf("rebuild snapshot err=%v", err)
	}
	bids, asks, _, err := provider.Snapshot("30000001")
	if err != nil {
		t.Fatalf("Snapshot after rebuild err=%v", err)
	}
	if len(bids) != 1 || bids[0].Price != 98 || len(asks) != 1 || asks[0].Price != 103 {
		t.Fatalf("unexpected rebuilt book: %+v/%+v", bids, asks)
	}
}

func TestEdgeXPerpV2WSProviderRejectsStaleSnapshots(t *testing.T) {
	provider := NewEdgeXPerpV2WSProvider("", time.Nanosecond)
	provider.applyEdgeXPerpV2Snapshot("30000001", edgeXPerpV2WSDepth{
		EndVersion: "1",
		Bids:       []edgeXPerpV2WSLevel{{Price: "100", Size: "1"}},
		Asks:       []edgeXPerpV2WSLevel{{Price: "101", Size: "1"}},
	}, 0, 0)
	time.Sleep(time.Millisecond)
	if _, _, _, err := provider.Snapshot("30000001"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale snapshot error, got %v", err)
	}
}

func TestEdgeXPerpV2ContractIDFromChannel(t *testing.T) {
	id, ok := edgeXPerpV2ContractIDFromChannel("depth.30000001.200")
	if !ok || id != "30000001" {
		t.Fatalf("contract id parse = %q/%v", id, ok)
	}
	if _, ok := edgeXPerpV2ContractIDFromChannel("ticker.all.1s"); ok {
		t.Fatalf("ticker channel must not parse as depth contract")
	}
}

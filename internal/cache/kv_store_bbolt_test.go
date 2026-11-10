package cache

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/kvstore"
)

func TestKVStoreBBoltPersistenceAndRouting(t *testing.T) {
	_ = kvstore.ShutdownLocal()
	ClearSignatureCache("")
	ClearCodexReasoningReplayCache()
	ClearClaudeWebSearchResultCache()
	t.Cleanup(func() {
		_ = kvstore.ShutdownLocal()
		ClearSignatureCache("")
		ClearCodexReasoningReplayCache()
		ClearClaudeWebSearchResultCache()
	})

	dbPath := filepath.Join(t.TempDir(), "kv.db")
	if errCfg := kvstore.ConfigureLocal(true, dbPath); errCfg != nil {
		t.Fatalf("kvstore.ConfigureLocal(true, %q) error = %v", dbPath, errCfg)
	}

	// 1. Signature cache persists to bbolt when in-memory cache is cleared.
	sigText := "thinking text persisted in bbolt"
	sigVal := "bboltValidSignature1234567890123456789012345678901234567890"
	CacheSignature(testModelName, sigText, sigVal)
	ClearSignatureCache("")
	if gotSig := GetCachedSignature(testModelName, sigText); gotSig != sigVal {
		t.Fatalf("GetCachedSignature() after ClearSignatureCache = %q, want %q", gotSig, sigVal)
	}

	// 2. Codex reasoning replay items persist to bbolt (including CAS append) when in-memory map is cleared.
	turn1 := [][]byte{
		[]byte(`{"type":"` + CodexReasoningReplayTurnType + `","id":"turn-1","assistant_fingerprint":"answer-1"}`),
		validCodexReasoningReplayItemForTest(21),
	}
	turn2 := [][]byte{
		[]byte(`{"type":"` + CodexReasoningReplayTurnType + `","id":"turn-2","call_ids":["call-2"]}`),
		validCodexReasoningReplayItemForTest(22),
	}
	if !CacheCodexReasoningReplayItems("gpt-5.4", "session-bbolt", turn1) {
		t.Fatalf("CacheCodexReasoningReplayItems() = false, want true")
	}
	if !AppendCodexReasoningReplayItemsBestEffort(context.Background(), "gpt-5.4", "session-bbolt", turn2) {
		t.Fatalf("AppendCodexReasoningReplayItemsBestEffort() = false, want true")
	}
	ClearCodexReasoningReplayCache()
	replayItems, foundReplay := GetCodexReasoningReplayItems("gpt-5.4", "session-bbolt")
	if !foundReplay || len(replayItems) != 4 {
		t.Fatalf("GetCodexReasoningReplayItems() after ClearCodexReasoningReplayCache = (len=%d, found=%v), want (len=4, found=true)", len(replayItems), foundReplay)
	}
	if gotID1 := ojson.GetBytes(replayItems[0], "id").String(); gotID1 != "turn-1" {
		t.Fatalf("replayItems[0] id = %q, want turn-1", gotID1)
	}
	if gotID2 := ojson.GetBytes(replayItems[2], "id").String(); gotID2 != "turn-2" {
		t.Fatalf("replayItems[2] id = %q, want turn-2", gotID2)
	}

	// 3. Claude web search result cache persists to bbolt when in-memory cache is cleared.
	webContent := []byte(`[{"type":"web_search_result","url":"https://example.com/bbolt"}]`)
	CacheClaudeWebSearchResult("session-bbolt", "srvtoolu_bbolt", webContent, 30*time.Minute, 16)
	ClearClaudeWebSearchResultCache()
	gotWeb, foundWeb := GetClaudeWebSearchResult("session-bbolt", "srvtoolu_bbolt", 30*time.Minute)
	if !foundWeb || !bytes.Equal(gotWeb, webContent) {
		t.Fatalf("GetClaudeWebSearchResult() after ClearClaudeWebSearchResultCache = (%q, %v), want (%q, true)", gotWeb, foundWeb, webContent)
	}

	// 4. Disabling local bbolt KV restores standard in-memory cache behavior.
	if errDisable := kvstore.ConfigureLocal(false, ""); errDisable != nil {
		t.Fatalf("kvstore.ConfigureLocal(false, \"\") error = %v", errDisable)
	}
	if gotAfterDisable := GetCachedSignature(testModelName, sigText); gotAfterDisable != "" {
		t.Fatalf("GetCachedSignature() after disabling bbolt = %q, want empty in-memory cache", gotAfterDisable)
	}
	memText := "thinking text in memory only"
	CacheSignature(testModelName, memText, sigVal)
	if gotMem := GetCachedSignature(testModelName, memText); gotMem != sigVal {
		t.Fatalf("GetCachedSignature() in memory mode = %q, want %q", gotMem, sigVal)
	}
	ClearSignatureCache("")
	if gotCleared := GetCachedSignature(testModelName, memText); gotCleared != "" {
		t.Fatalf("GetCachedSignature() after ClearSignatureCache in memory mode = %q, want empty", gotCleared)
	}
}

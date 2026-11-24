package agent

import (
	"testing"

	"FileEngine/internal/db"
)

func TestChatEpochSnapshotIsStoredAndRolledByTheSummary(t *testing.T) {
	model := &fakeModel{}
	repo, a, session := brainAgent(t, model)
	if err := repo.CreateCategory(&db.Category{FilesystemID: session.FilesystemID, Name: "照片", Path: "/照片"}); err != nil {
		t.Fatal(err)
	}
	first, err := a.chatEpochSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateCategory(&db.Category{FilesystemID: session.FilesystemID, Name: "软件", Path: "/软件"}); err != nil {
		t.Fatal(err)
	}
	again, err := a.chatEpochSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if RenderSharedContext(first) != RenderSharedContext(again) || len(again.Categories) != 1 {
		t.Fatal("within an epoch the stored snapshot is returned, not a fresh render")
	}
	// A fold opens a new epoch from the state at that moment.
	summary := &db.ChatTurn{SessionID: session.ID, Role: db.ChatRoleSummary, Content: "摘要"}
	if err := repo.AddChatTurn(summary); err != nil {
		t.Fatal(err)
	}
	next, err := a.chatEpochSnapshot(summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Categories) != 2 {
		t.Fatalf("a new epoch re-snapshots: %+v", next.Categories)
	}
	// Bookkeeping rows never reach the user's chat or the history.
	if listed, _ := repo.ListChatTurns(session.ID); len(listed) != 0 {
		t.Fatalf("snapshot rows are hidden: %+v", listed)
	}
	if s, turns, _ := repo.ChatHistory(session.ID, 0); s == nil || s.ID != summary.ID || len(turns) != 0 {
		t.Fatalf("history ignores snapshot rows: %+v %+v", s, turns)
	}
}

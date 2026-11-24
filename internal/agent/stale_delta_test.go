package agent

import (
	"context"
	"strings"
	"testing"

	"FileEngine/biz/dal/db/model"
)

// A change listed in one turn and reverted before the next must not stay the
// last word: the next message says the earlier list no longer holds.
func TestWireRevertedChangeIsRetractedNextTurn(t *testing.T) {
	ws := newWireServer(t, byRound(text("好的")))
	_, a, session := wireChatAgent(t, ws)
	repo := a.repo
	ctx := context.Background()
	if _, err := a.RunChat(ctx, NewLogger(session.ID, 1, 0), "第一轮", nil); err != nil {
		t.Fatal(err)
	}
	cat := &model.Category{FilesystemID: session.FilesystemID, Name: "临时", Path: "/临时"}
	if err := repo.CreateCategory(cat); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunChat(ctx, NewLogger(session.ID, 2, 0), "第二轮", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteCategory(cat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunChat(ctx, NewLogger(session.ID, 3, 0), "第三轮", nil); err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	second := string(reqs[1].messages[len(reqs[1].messages)-1])
	third := string(reqs[2].messages[len(reqs[2].messages)-1])
	if !strings.Contains(second, "新增分类 临时") {
		t.Fatalf("turn 2 lists the new category: %s", second)
	}
	if !strings.Contains(third, "无变化") || strings.Contains(third, "新增分类") {
		t.Fatalf("turn 3 retracts it: %s", third)
	}
	if !strings.Contains(third, "取代之前消息里的") {
		t.Fatal("a delta says it replaces earlier ones")
	}
}

func TestChatSnapshotRowStaysSmall(t *testing.T) {
	repo, a, session := brainAgent(t, &fakeModel{})
	if _, err := repo.SavePlan(session.ID, strings.Repeat("方案正文。", 4000), 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.chatEpochSnapshot(nil); err != nil {
		t.Fatal(err)
	}
	var row model.ChatTurn
	if err := repo.DB().Where("session_id = ? AND role = ?", session.ID, chatRoleSnapshot).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if len(row.Content) > 40000 {
		t.Fatalf("stored snapshot is %d bytes", len(row.Content))
	}
	base, _ := a.chatEpochSnapshot(nil)
	cur, _ := LoadSharedContext(repo, a.session)
	if d := RenderDelta(base, cur, false); d != "" {
		t.Fatalf("a cut plan must not look changed: %s", d)
	}
}

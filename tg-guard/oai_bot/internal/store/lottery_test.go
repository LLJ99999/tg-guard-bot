package store

import (
	"path/filepath"
	"testing"
	"time"

	"oai_bot/internal/model"
)

func TestLotteryFlow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	l := &model.Lottery{GroupID: -1, Title: "t", PrizeType: model.PrizeCode, Winners: 2, Status: model.LotteryOpen}
	if err := s.CreateLottery(l, []string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{1, 2, 2, 3} {
		if _, _, err := s.JoinLottery(&model.LotteryEntry{LotteryID: l.ID, UserID: uid, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Lottery(l.ID)
	if got.Entries != 3 || s.CountLotteryCodes(l.ID) != 2 {
		t.Fatalf("entries=%d codes=%d", got.Entries, s.CountLotteryCodes(l.ID))
	}
	if !s.ClaimLottery(l.ID, model.LotteryDrawn) || s.ClaimLottery(l.ID, model.LotteryDrawn) {
		t.Fatal("claim should succeed exactly once")
	}
	id := s.AddDelayedDelete(1, 2, time.Now().Add(-time.Minute))
	ds, _ := s.DueDeletes(time.Now())
	if len(ds) != 1 || ds[0].ID != id {
		t.Fatal("due deletes")
	}
}

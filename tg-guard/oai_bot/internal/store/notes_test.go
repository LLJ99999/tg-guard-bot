package store

import (
	"path/filepath"
	"testing"

	"oai_bot/internal/model"
)

func TestNotesAndReplyFreq(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	n := &model.Note{GroupID: -1, Name: "faq", Content: model.Content{Media: "photo:abc"}, FileName: "a.jpg", CreatedBy: "Alice"}
	if err := st.SaveNote(n); err != nil {
		t.Fatal(err)
	}
	// 同名再存是更新
	if err := st.SaveNote(&model.Note{GroupID: -1, Name: "faq", Content: model.Content{Text: "新内容", Media: "document:x"}, FileName: "b.pdf", CreatedBy: "Bob"}); err != nil {
		t.Fatal(err)
	}
	ns, _ := st.Notes(-1)
	if len(ns) != 1 || ns[0].Text != "新内容" || ns[0].FileName != "b.pdf" || ns[0].CreatedBy != "Bob" {
		t.Fatalf("notes %+v", ns)
	}
	st.NoteViewed(ns[0].ID)
	st.NoteViewed(ns[0].ID)
	got, _ := st.NoteByID(-1, ns[0].ID)
	if got == nil || got.Views != 2 {
		t.Fatalf("views %+v", got)
	}
	if x, _ := st.NoteByID(-2, ns[0].ID); x != nil {
		t.Fatal("别的群不能取到")
	}

	// 自动回复：同关键词再存要能更新（以前 delete_after 列名写错会报错）
	if err := st.SaveReply(&model.AutoReply{GroupID: -1, Keyword: "价格", Delete: 30, Content: model.Content{Text: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReply(&model.AutoReply{GroupID: -1, Keyword: "价格", Delete: 60, Cooldown: 300, PerUser: true, Content: model.Content{Text: "b"}}); err != nil {
		t.Fatal(err)
	}
	rs, _ := st.Replies(-1)
	if len(rs) != 1 || rs[0].Delete != 60 || rs[0].Cooldown != 300 || !rs[0].PerUser || rs[0].Text != "b" {
		t.Fatalf("replies %+v", rs)
	}
	if ok, err := st.SetReplyFreq(-1, "价格", -1, false, 50); !ok || err != nil {
		t.Fatal(ok, err)
	}
	rs, _ = st.Replies(-1)
	if rs[0].Cooldown != -1 || rs[0].PerUser || rs[0].Chance != 50 || rs[0].Text != "b" {
		t.Fatalf("freq %+v", rs[0])
	}
	if ok, _ := st.SetReplyFreq(-1, "没有", 1, false, 0); ok {
		t.Fatal("不存在的关键词应返回 false")
	}
}

func TestNoteLogs(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	n := &model.Note{GroupID: -1, Name: "faq", Content: model.Content{Text: "x"}}
	st.SaveNote(n)
	ns, _ := st.Notes(-1)
	if got, _ := st.NoteByIDAny(ns[0].ID); got == nil || got.GroupID != -1 {
		t.Fatal("NoteByIDAny")
	}
	st.AddNoteLog(&model.NoteLog{GroupID: -1, NoteID: ns[0].ID, NoteName: "faq", UserID: 1, Name: "A"})
	st.AddNoteLog(&model.NoteLog{GroupID: -1, NoteID: 99, NoteName: "b", UserID: 2, Name: "B"})
	st.AddNoteLog(&model.NoteLog{GroupID: -2, NoteID: 5, NoteName: "c", UserID: 3})
	all, _ := st.NoteLogs(-1, 0, 0)
	if len(all) != 2 || all[0].Name != "B" {
		t.Fatalf("all %+v", all)
	}
	one, _ := st.NoteLogs(-1, ns[0].ID, 0)
	if len(one) != 1 || one[0].UserID != 1 {
		t.Fatalf("one %+v", one)
	}
}

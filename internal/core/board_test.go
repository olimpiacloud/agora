package core

import (
	"context"
	"fmt"
	"testing"
)

func TestBoardReadPages(t *testing.T) {
	m := newTestManager(t)
	w, err := m.CreateWorkspace(context.Background(), CreateWorkspaceReq{Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, err := m.BoardPost(w.ID, "", "note", "", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	since := int64(0)
	pages := 0
	for {
		v := m.BoardRead(w.ID, since, 3)
		pages++
		for _, p := range v.Posts {
			got = append(got, p.Text)
		}
		if !v.More {
			if len(v.Posts) > 0 && v.Seq < v.Posts[len(v.Posts)-1].Seq {
				t.Fatalf("seq %d behind last post", v.Seq)
			}
			since = v.Seq
			break
		}
		if v.Seq != v.Posts[len(v.Posts)-1].Seq || len(v.Posts) != 3 {
			t.Fatalf("truncated page: seq %d, %d posts", v.Seq, len(v.Posts))
		}
		since = v.Seq
		if pages > 5 {
			t.Fatal("no progress")
		}
	}
	if fmt.Sprint(got) != "[0 1 2 3 4 5 6]" || pages != 3 {
		t.Fatalf("read %v in %d pages", got, pages)
	}
	if v := m.BoardRead(w.ID, since, 3); len(v.Posts) != 0 || v.More {
		t.Fatalf("after the end: %+v", v)
	}
}

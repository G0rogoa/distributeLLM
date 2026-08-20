package lifecycle

import "testing"

func TestRingIsBoundedAndOrdered(t *testing.T) {
	s := New(2)
	s.Add(Request{RequestID: "a"})
	s.Add(Request{RequestID: "b"})
	s.Add(Request{RequestID: "c"})
	got := s.Snapshot()
	if len(got) != 2 || got[0].RequestID != "b" || got[1].RequestID != "c" {
		t.Fatalf("got=%+v", got)
	}
}

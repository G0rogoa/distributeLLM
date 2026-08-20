package admission

import "testing"

func TestBoundedAdmission(t *testing.T) {
	limiter := New(1)
	release, ok := limiter.Acquire()
	if !ok {
		t.Fatal("first acquire rejected")
	}
	if _, ok := limiter.Acquire(); ok {
		t.Fatal("second acquire accepted")
	}
	release()
	if _, ok := limiter.Acquire(); !ok {
		t.Fatal("slot not released")
	}
}

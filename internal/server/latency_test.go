package server

import (
	"testing"
	"time"
)

func TestLatencyRing(t *testing.T) {
	var l latencyRing
	if l.view() != nil {
		t.Fatal("empty ring should have no view")
	}
	for i := 1; i <= latencyWindow+2; i++ {
		l.add(time.Duration(i)*10*time.Millisecond, time.Now())
	}
	v := l.view()
	// last 8 samples: 30..100 ms -> avg 65
	if v.LastMS != 100 || v.AvgMS != 65 || v.Samples != latencyWindow {
		t.Fatalf("view: %+v", v)
	}
}

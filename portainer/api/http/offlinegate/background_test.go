package offlinegate

import (
	"testing"
	"time"
)

func TestBackgroundReadLeaseCoordinatesWithSnapshot(t *testing.T) {
	gate := NewOfflineGate()
	release, err := gate.ReadLease(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started, acquired, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { close(started); unlock := gate.Lock(); close(acquired); <-finish; unlock() }()
	<-started
	select {
	case <-acquired:
		t.Fatal("snapshot acquired while background write still active")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("snapshot did not acquire after writer finished")
	}
	if unlock, err := gate.ReadLease(time.Millisecond); err == nil {
		unlock()
		t.Fatal("background writer passed snapshot gate")
	}
	close(finish)
	unlock, err := gate.ReadLease(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

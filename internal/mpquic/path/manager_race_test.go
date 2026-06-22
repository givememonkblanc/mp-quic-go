package path

import (
	"sync"
	"testing"
)

func TestManagerConcurrentAccess(t *testing.T) {
	mgr := NewManager(8)

	mgr.EnsureInitialPath("peer0")

	for id := ID(1); id <= ID(8); id++ {
		if _, err := mgr.RegisterPath(id, "peer"); err != nil {
			t.Fatalf("register path %d failed: %v", id, err)
		}
	}

	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)

		go func(worker int) {
			defer wg.Done()

			for i := 0; i < 1000; i++ {
				id := ID(i % 9)

				_, _ = mgr.UpdateRSSI(id, -30-(worker+i)%60)
				_, _ = mgr.UpdateStatus(id, StatusAvailable, true)
				_, _ = mgr.Get(id)
				_ = mgr.Snapshot()
				_, _ = mgr.NextUnusedPathID()
				_ = mgr.MaxPathID()
			}
		}(worker)
	}

	wg.Wait()
}
package util

import (
	"sync"
	"sync/atomic"
)

type WaitGroupWithCount struct {
	wg    sync.WaitGroup
	count int32
}

func (wg *WaitGroupWithCount) Add(delta int) {
	wg.wg.Add(delta)
	atomic.AddInt32(&wg.count, int32(delta))
}

func (wg *WaitGroupWithCount) Done() {
	wg.wg.Done()
	atomic.AddInt32(&wg.count, -1)
}

func (wg *WaitGroupWithCount) Wait() {
	wg.wg.Wait()
}

func (wg *WaitGroupWithCount) Count() int {
	return int(atomic.LoadInt32(&wg.count))
}

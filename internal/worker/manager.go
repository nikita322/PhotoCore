package worker

import "runtime"

// PoolManager управляет двумя пулами воркеров: быстрым и медленным
type PoolManager struct {
	Fast *Pool
	Slow *Pool
}

// NewPoolManager создает новый менеджер пулов с оптимальным числом воркеров для текущей системы
func NewPoolManager() *PoolManager {
	numCPU := runtime.NumCPU()

	fastWorkers := 2
	if numCPU < 2 {
		fastWorkers = 1
	}

	slowWorkers := 2
	if numCPU < 4 {
		slowWorkers = 1
	}

	return &PoolManager{
		Fast: NewPool("fast", fastWorkers, 0),
		Slow: NewPool("slow", slowWorkers, 0),
	}
}

// Start запускает все пулы
func (pm *PoolManager) Start() {
	pm.Fast.Start()
	pm.Slow.Start()
}

// Stop останавливает все пулы (сначала slow, потом fast)
func (pm *PoolManager) Stop() {
	pm.Slow.Stop()
	pm.Fast.Stop()
}

// PoolManagerStats содержит статистику обоих пулов
type PoolManagerStats struct {
	Fast Stats `json:"fast"`
	Slow Stats `json:"slow"`
}

// Stats возвращает статистику обоих пулов
func (pm *PoolManager) Stats() PoolManagerStats {
	return PoolManagerStats{
		Fast: pm.Fast.Stats(),
		Slow: pm.Slow.Stats(),
	}
}

// TotalQueueLength возвращает общую длину очередей
func (pm *PoolManager) TotalQueueLength() int {
	return pm.Fast.QueueLength() + pm.Slow.QueueLength()
}

// TotalProcessingCount возвращает общее число активных воркеров
func (pm *PoolManager) TotalProcessingCount() int64 {
	return pm.Fast.Stats().ActiveWorkers + pm.Slow.Stats().ActiveWorkers
}

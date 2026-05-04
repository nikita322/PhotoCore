package worker

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
)

// TaskType определяет тип задачи
type TaskType string

const (
	TaskGenerateThumbnail TaskType = "generate_thumbnail"
	TaskExtractMetadata   TaskType = "extract_metadata"
	TaskProcessRAW        TaskType = "process_raw"
	TaskProcessVideo      TaskType = "process_video"
)

// TaskPriority определяет приоритет задачи
type TaskPriority int

const (
	PriorityLow    TaskPriority = 0
	PriorityNormal TaskPriority = 1
	PriorityHigh   TaskPriority = 2
)

// Task представляет задачу для обработки
type Task struct {
	ID        string
	Type      TaskType
	Priority  TaskPriority
	MediaID   string
	MediaPath string
	Size      media.ThumbnailSize // для thumbnail: small, medium, large
	CreatedAt time.Time
	Attempts  int
}

// TaskResult содержит результат выполнения задачи
type TaskResult struct {
	TaskID     string
	Success    bool
	Error      error
	Duration   time.Duration
	OutputPath string
}

// Handler обрабатывает задачи определенного типа
type Handler func(ctx context.Context, task *Task) (*TaskResult, error)

// Pool управляет пулом воркеров с поддержкой приоритетов
type Pool struct {
	name        string
	numWorkers  int
	highQueue   chan *Task
	normalQueue chan *Task
	lowQueue    chan *Task
	resultQueue chan *TaskResult
	handlers    map[TaskType]Handler
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex

	// Статистика
	stats Stats
}

// Stats содержит статистику пула
type Stats struct {
	TotalTasks     int64 `json:"total_tasks"`
	CompletedTasks int64 `json:"completed_tasks"`
	FailedTasks    int64 `json:"failed_tasks"`
	QueuedTasks    int64 `json:"queued_tasks"`
	ActiveWorkers  int64 `json:"active_workers"`
}

const (
	defaultQueueSize    = 500
	maxRetries          = 3
	maxHeapAlloc        = 2 * 1024 * 1024 * 1024 // 2GB
	workerTaskTimeout   = 5 * time.Minute
	memoryCheckInterval = 10 * time.Second
)

var retryBackoff = []time.Duration{
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
}

// NewPool создает новый пул воркеров
func NewPool(name string, numWorkers int, queueSize int) *Pool {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		name:        name,
		numWorkers:  numWorkers,
		highQueue:   make(chan *Task, queueSize),
		normalQueue: make(chan *Task, queueSize),
		lowQueue:    make(chan *Task, queueSize),
		resultQueue: make(chan *TaskResult, queueSize),
		handlers:    make(map[TaskType]Handler),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// RegisterHandler регистрирует обработчик для типа задачи
func (p *Pool) RegisterHandler(taskType TaskType, handler Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[taskType] = handler
}

// Start запускает воркеры
func (p *Pool) Start() {
	logger.InfoLog.Printf("[%s] Starting worker pool with %d workers", p.name, p.numWorkers)

	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Горутина для обработки результатов
	go p.processResults()
}

// Stop останавливает пул
func (p *Pool) Stop() {
	logger.InfoLog.Printf("[%s] Stopping worker pool...", p.name)
	p.cancel()
	close(p.highQueue)
	close(p.normalQueue)
	close(p.lowQueue)
	p.wg.Wait()
	close(p.resultQueue)
	logger.InfoLog.Printf("[%s] Worker pool stopped", p.name)
}

// Submit добавляет задачу в очередь с учетом приоритета
func (p *Pool) Submit(task *Task) bool {
	var queue chan *Task
	switch task.Priority {
	case PriorityHigh:
		queue = p.highQueue
	case PriorityNormal:
		queue = p.normalQueue
	case PriorityLow:
		queue = p.lowQueue
	default:
		queue = p.normalQueue
	}

	select {
	case <-p.ctx.Done():
		return false
	case queue <- task:
		atomic.AddInt64(&p.stats.TotalTasks, 1)
		atomic.AddInt64(&p.stats.QueuedTasks, 1)
		return true
	default:
		logger.InfoLog.Printf("[%s] Task queue full (priority=%d), dropping task %s", p.name, task.Priority, task.ID)
		return false
	}
}

// SubmitBlocking добавляет задачу с блокировкой
func (p *Pool) SubmitBlocking(task *Task) bool {
	var queue chan *Task
	switch task.Priority {
	case PriorityHigh:
		queue = p.highQueue
	case PriorityNormal:
		queue = p.normalQueue
	case PriorityLow:
		queue = p.lowQueue
	default:
		queue = p.normalQueue
	}

	select {
	case <-p.ctx.Done():
		return false
	case queue <- task:
		atomic.AddInt64(&p.stats.TotalTasks, 1)
		atomic.AddInt64(&p.stats.QueuedTasks, 1)
		return true
	}
}

// Stats возвращает статистику пула
func (p *Pool) Stats() Stats {
	return Stats{
		TotalTasks:     atomic.LoadInt64(&p.stats.TotalTasks),
		CompletedTasks: atomic.LoadInt64(&p.stats.CompletedTasks),
		FailedTasks:    atomic.LoadInt64(&p.stats.FailedTasks),
		QueuedTasks:    atomic.LoadInt64(&p.stats.QueuedTasks),
		ActiveWorkers:  atomic.LoadInt64(&p.stats.ActiveWorkers),
	}
}

// QueueLength возвращает текущую длину очереди
func (p *Pool) QueueLength() int {
	return len(p.highQueue) + len(p.normalQueue) + len(p.lowQueue)
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	logger.InfoLog.Printf("[%s] Worker %d started", p.name, id)

	for {
		// Проверка давления памяти
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if m.HeapAlloc > maxHeapAlloc {
			logger.InfoLog.Printf("[%s] Worker %d: memory pressure (heap=%dMB), throttling", p.name, id, m.HeapAlloc/1024/1024)
			select {
			case <-p.ctx.Done():
				logger.InfoLog.Printf("[%s] Worker %d stopping", p.name, id)
				return
			case <-time.After(memoryCheckInterval):
				continue
			}
		}

		// Приоритет: сначала high queue без блокировки
		select {
		case <-p.ctx.Done():
			logger.InfoLog.Printf("[%s] Worker %d stopping", p.name, id)
			return
		case task, ok := <-p.highQueue:
			if !ok {
				logger.InfoLog.Printf("[%s] Worker %d: high queue closed", p.name, id)
				return
			}
			p.processTask(id, task)
			continue
		default:
		}

		// Затем любая очередь с блокировкой
		select {
		case <-p.ctx.Done():
			logger.InfoLog.Printf("[%s] Worker %d stopping", p.name, id)
			return
		case task, ok := <-p.highQueue:
			if !ok {
				return
			}
			p.processTask(id, task)
		case task, ok := <-p.normalQueue:
			if !ok {
				return
			}
			p.processTask(id, task)
		case task, ok := <-p.lowQueue:
			if !ok {
				return
			}
			p.processTask(id, task)
		}
	}
}

func (p *Pool) processTask(workerID int, task *Task) {
	atomic.AddInt64(&p.stats.ActiveWorkers, 1)
	atomic.AddInt64(&p.stats.QueuedTasks, -1)
	defer atomic.AddInt64(&p.stats.ActiveWorkers, -1)

	start := time.Now()

	p.mu.RLock()
	handler, ok := p.handlers[task.Type]
	p.mu.RUnlock()

	var result *TaskResult

	if !ok {
		result = &TaskResult{
			TaskID:   task.ID,
			Success:  false,
			Error:    fmt.Errorf("no handler for task type %s", task.Type),
			Duration: time.Since(start),
		}
		logger.InfoLog.Printf("[%s] Worker %d: no handler for task type %s", p.name, workerID, task.Type)
	} else {
		ctx, cancel := context.WithTimeout(p.ctx, workerTaskTimeout)
		res, err := handler(ctx, task)
		cancel()

		if res != nil {
			result = res
		} else {
			result = &TaskResult{
				TaskID:   task.ID,
				Success:  err == nil,
				Error:    err,
				Duration: time.Since(start),
			}
		}
	}

	if result.Success {
		atomic.AddInt64(&p.stats.CompletedTasks, 1)
	} else {
		atomic.AddInt64(&p.stats.FailedTasks, 1)

		// Retry logic с exponential backoff
		if result.Error != nil && !IsPermanentError(result.Error) && task.Attempts < maxRetries {
			task.Attempts++
			delay := retryBackoff[task.Attempts-1]
			atomic.AddInt64(&p.stats.FailedTasks, -1) // Откатываем, т.к. будет retry

			time.AfterFunc(delay, func() {
				logger.InfoLog.Printf("[%s] Retrying task %s (attempt %d/%d) after %v", p.name, task.ID, task.Attempts, maxRetries, delay)
				if !p.Submit(task) {
					logger.InfoLog.Printf("[%s] Retry failed for task %s: queue full", p.name, task.ID)
					atomic.AddInt64(&p.stats.FailedTasks, 1)
				}
			})
		}
	}

	// Отправляем результат
	select {
	case p.resultQueue <- result:
	default:
		// Result queue full, log and continue
	}
}

func (p *Pool) processResults() {
	for result := range p.resultQueue {
		if !result.Success && result.Error != nil {
			logger.InfoLog.Printf("[%s] Task %s failed: %v (took %v)", p.name, result.TaskID, result.Error, result.Duration)
		}
	}
}

// ResultsChan возвращает канал результатов для внешней обработки
func (p *Pool) ResultsChan() <-chan *TaskResult {
	return p.resultQueue
}

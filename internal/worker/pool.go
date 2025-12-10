package worker

import (
	"context"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
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
	Size      string // для thumbnail: small, medium, large
	CreatedAt time.Time
	Attempts  int
}

// TaskResult содержит результат выполнения задачи
type TaskResult struct {
	TaskID    string
	Success   bool
	Error     error
	Duration  time.Duration
	OutputPath string
}

// Handler обрабатывает задачи определенного типа
type Handler func(ctx context.Context, task *Task) (*TaskResult, error)

// Pool управляет пулом воркеров
type Pool struct {
	numWorkers   int
	taskQueue    chan *Task
	resultQueue  chan *TaskResult
	handlers     map[TaskType]Handler
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex

	// Статистика
	stats Stats
}

// Stats содержит статистику пула
type Stats struct {
	TotalTasks     int64
	CompletedTasks int64
	FailedTasks    int64
	QueuedTasks    int64
	ActiveWorkers  int64
}

// NewPool создает новый пул воркеров
func NewPool(numWorkers int, queueSize int) *Pool {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	if queueSize <= 0 {
		queueSize = 1000
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		numWorkers:  numWorkers,
		taskQueue:   make(chan *Task, queueSize),
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
	log.Printf("Starting worker pool with %d workers", p.numWorkers)

	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Горутина для обработки результатов
	go p.processResults()
}

// Stop останавливает пул
func (p *Pool) Stop() {
	log.Println("Stopping worker pool...")
	p.cancel()
	close(p.taskQueue)
	p.wg.Wait()
	close(p.resultQueue)
	log.Println("Worker pool stopped")
}

// Submit добавляет задачу в очередь
func (p *Pool) Submit(task *Task) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.taskQueue <- task:
		atomic.AddInt64(&p.stats.TotalTasks, 1)
		atomic.AddInt64(&p.stats.QueuedTasks, 1)
		return true
	default:
		// Очередь переполнена
		log.Printf("Task queue full, dropping task %s", task.ID)
		return false
	}
}

// SubmitBlocking добавляет задачу с блокировкой
func (p *Pool) SubmitBlocking(task *Task) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.taskQueue <- task:
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
	return len(p.taskQueue)
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	log.Printf("Worker %d started", id)

	for {
		select {
		case <-p.ctx.Done():
			log.Printf("Worker %d stopping", id)
			return
		case task, ok := <-p.taskQueue:
			if !ok {
				log.Printf("Worker %d: task queue closed", id)
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
			Error:    nil,
			Duration: time.Since(start),
		}
		log.Printf("Worker %d: no handler for task type %s", workerID, task.Type)
	} else {
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Minute)
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
			log.Printf("Task %s failed: %v (took %v)", result.TaskID, result.Error, result.Duration)
		}
	}
}

// ResultsChan возвращает канал результатов для внешней обработки
func (p *Pool) ResultsChan() <-chan *TaskResult {
	return p.resultQueue
}

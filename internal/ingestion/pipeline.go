package ingestion

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

const (
	flushInterval          = 100 * time.Millisecond
	dedupTTL               = 60 * time.Second
	dedupCleanupInterval   = 30 * time.Second
	highWatermarkPercent   = 80
	rejectWatermarkPercent = 90
)

var errQueueFull = errors.New("queue full")

// ErrQueueFull is returned when the ingestion queue is at capacity.
var ErrQueueFull = errQueueFull

// Pipeline is the bounded ingestion queue and worker pool skeleton.
type Pipeline struct {
	spanCh    chan *models.Span
	repo      storage.Repository
	batchSize int
	workers   int
	queueSize int

	submittedSpans           atomic.Int64
	flushedBatches           atomic.Int64
	failedFlushes            atomic.Int64
	ingestQueueOverflowTotal atomic.Int64
	highQueueWarningActive   atomic.Bool
	startOnce                sync.Once

	dedupMu  sync.Mutex
	dedupSet map[string]time.Time
}

// NewPipeline constructs a new ingestion pipeline with a bounded queue.
func NewPipeline(repo storage.Repository, batchSize int, workers int, queueSize int) *Pipeline {
	if batchSize <= 0 {
		batchSize = 1
	}
	if queueSize <= 0 {
		queueSize = 1
	}

	getMetrics()
	return &Pipeline{
		spanCh:    make(chan *models.Span, queueSize),
		repo:      repo,
		batchSize: batchSize,
		workers:   workers,
		queueSize: queueSize,
		dedupSet:  make(map[string]time.Time),
	}
}

// Start launches the worker pool and stops workers when the context is canceled.
func (p *Pipeline) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		for workerID := 0; workerID < p.workers; workerID++ {
			go p.runWorker(ctx, workerID)
		}
		go p.runDedupCleanup(ctx)
	})
}

// Submit enqueues a span without blocking.
func (p *Pipeline) Submit(span *models.Span) error {
	if span == nil {
		return nil
	}

	if p.isDuplicate(span.SpanID.String()) {
		return nil
	}

	queueLen := len(p.spanCh)
	if p.isQueueAboveThreshold(queueLen, highWatermarkPercent) && !p.highQueueWarningActive.Swap(true) {
		log.Printf("ingestion queue high-water mark queue_len=%d queue_capacity=%d", queueLen, p.queueSize)
	} else if !p.isQueueAboveThreshold(queueLen, highWatermarkPercent) {
		p.highQueueWarningActive.Store(false)
	}

	if p.isQueueAboveThreshold(queueLen, rejectWatermarkPercent) {
		p.recordQueueOverflow()
		return ErrQueueFull
	}

	select {
	case p.spanCh <- span:
		p.submittedSpans.Add(1)
		return nil
	default:
		p.recordQueueOverflow()
		return errQueueFull
	}
}

func (p *Pipeline) isDuplicate(spanID string) bool {
	now := time.Now()
	expiration := now.Add(dedupTTL)

	p.dedupMu.Lock()
	defer p.dedupMu.Unlock()

	if expiresAt, exists := p.dedupSet[spanID]; exists && expiresAt.After(now) {
		return true
	}

	p.dedupSet[spanID] = expiration
	return false
}

func (p *Pipeline) runDedupCleanup(ctx context.Context) {
	ticker := time.NewTicker(dedupCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.cleanupExpiredDedupEntries(time.Now())
		}
	}
}

func (p *Pipeline) cleanupExpiredDedupEntries(now time.Time) {
	p.dedupMu.Lock()
	defer p.dedupMu.Unlock()

	for spanID, expiresAt := range p.dedupSet {
		if !expiresAt.After(now) {
			delete(p.dedupSet, spanID)
		}
	}
}

func (p *Pipeline) recordQueueOverflow() {
	p.ingestQueueOverflowTotal.Add(1)
	getMetrics().ingestQueueOverflowTotal.Inc()
}

func (p *Pipeline) isQueueAboveThreshold(queueLen int, thresholdPercent int) bool {
	if p.queueSize <= 0 {
		return false
	}

	return queueLen*100 >= p.queueSize*thresholdPercent
}

func (p *Pipeline) runWorker(ctx context.Context, workerID int) {
	log.Printf("ingestion worker started id=%d batch_size=%d", workerID, p.batchSize)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*models.Span, 0, p.batchSize)

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = p.flushBatch(shutdownCtx, workerID, batch)
			cancel()
			log.Printf("ingestion worker stopped id=%d err=%v", workerID, ctx.Err())
			return
		case <-ticker.C:
			batch = p.flushBatch(ctx, workerID, batch)
		case span := <-p.spanCh:
			if span == nil {
				continue
			}

			batch = append(batch, span)
			if len(batch) >= p.batchSize {
				batch = p.flushBatch(ctx, workerID, batch)
			}
		}
	}
}

func (p *Pipeline) flushBatch(ctx context.Context, workerID int, batch []*models.Span) []*models.Span {
	if len(batch) == 0 {
		return batch[:0]
	}

	start := time.Now()

	if p.repo == nil {
		p.failedFlushes.Add(1)
		getMetrics().ingestFailedTotal.Inc()
		log.Printf("ingestion batch flush failed worker_id=%d err=%v size=%d", workerID, errors.New("repository is nil"), len(batch))
		return batch
	}

	flushBatch := make([]*models.Span, len(batch))
	copy(flushBatch, batch)

	writeStart := time.Now()
	err := p.repo.CreateSpanBatch(ctx, flushBatch)
	writeLatency := time.Since(writeStart).Seconds()
	getMetrics().cassandraWriteLatencySeconds.Observe(writeLatency)
	if err != nil {
		p.failedFlushes.Add(1)
		getMetrics().ingestFailedTotal.Inc()
		log.Printf("ingestion batch flush failed worker_id=%d err=%v size=%d", workerID, err, len(flushBatch))
		return batch
	}

	for _, trace := range summarizeTraces(flushBatch) {
		if err := p.repo.CreateTrace(ctx, trace); err != nil {
			p.failedFlushes.Add(1)
			getMetrics().ingestFailedTotal.Inc()
			log.Printf("trace summary upsert failed worker_id=%d trace_id=%s err=%v", workerID, trace.TraceID, err)
		}
	}

	p.flushedBatches.Add(1)
	getMetrics().ingestBatchDurationSeconds.Observe(time.Since(start).Seconds())
	log.Printf("ingestion batch flushed worker_id=%d size=%d", workerID, len(flushBatch))

	return batch[:0]
}

func summarizeTraces(spans []*models.Span) []*models.Trace {
	type aggregate struct {
		serviceName string
		startTime   time.Time
		endTime     time.Time
		rootSpanID  uuid.UUID
		status      int
	}

	aggregates := make(map[uuid.UUID]*aggregate)
	for _, span := range spans {
		if span == nil {
			continue
		}

		agg, exists := aggregates[span.TraceID]
		if !exists {
			aggregates[span.TraceID] = &aggregate{
				serviceName: span.ServiceName,
				startTime:   span.StartTime.UTC(),
				endTime:     span.StartTime.UTC().Add(time.Duration(span.DurationMs) * time.Millisecond),
				rootSpanID:  span.SpanID,
			}
			agg = aggregates[span.TraceID]
		}

		spanStart := span.StartTime.UTC()
		spanEnd := spanStart.Add(time.Duration(span.DurationMs) * time.Millisecond)
		if spanStart.Before(agg.startTime) {
			agg.startTime = spanStart
			agg.serviceName = span.ServiceName
			agg.rootSpanID = span.SpanID
		}
		if span.ParentID == uuid.Nil {
			agg.serviceName = span.ServiceName
			agg.rootSpanID = span.SpanID
			if spanStart.Before(agg.startTime) {
				agg.startTime = spanStart
			}
		}
		if spanEnd.After(agg.endTime) {
			agg.endTime = spanEnd
		}
		if span.Tags != nil {
			if value, ok := span.Tags["error"]; ok && value != "" {
				agg.status = 1
			}
		}
	}

	traces := make([]*models.Trace, 0, len(aggregates))
	for traceID, agg := range aggregates {
		traces = append(traces, &models.Trace{
			TraceID:     traceID,
			ServiceName: agg.serviceName,
			StartTime:   agg.startTime,
			RootSpanID:  agg.rootSpanID,
			DurationMs:  agg.endTime.Sub(agg.startTime).Milliseconds(),
			Status:      agg.status,
		})
	}

	return traces
}

package ingestion

import (
	"bytes"
	"context"
	"log"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

func TestPipelineFlushesWhenBatchSizeIsReached(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := storage.NewMockRepository()
	pipeline := NewPipeline(repo, 3, 1, 10)
	pipeline.Start(ctx)

	traceID := uuid.New()
	for i := 0; i < 3; i++ {
		err := pipeline.Submit(newTestSpan(traceID))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		return repo.CreateSpanBatchCount == 1
	}, time.Second, 10*time.Millisecond)

	spans, err := repo.ListSpansByTrace(context.Background(), traceID)
	require.NoError(t, err)
	require.Len(t, spans, 3)
	require.EqualValues(t, 1, pipeline.flushedBatches.Load())
}

func TestPipelineUsesBatchWritesOnlyAndLogsFlushes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := storage.NewMockRepository()
	pipeline := NewPipeline(repo, 2, 1, 10)

	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	pipeline.Start(ctx)

	traceID := uuid.New()
	require.NoError(t, pipeline.Submit(newTestSpan(traceID)))
	require.NoError(t, pipeline.Submit(newTestSpan(traceID)))

	require.Eventually(t, func() bool {
		return repo.CreateSpanBatchCount == 1
	}, time.Second, 10*time.Millisecond)

	require.Zero(t, repo.CreateSpanCallCount)
	require.Eventually(t, func() bool {
		return bytes.Contains(output.Bytes(), []byte("ingestion batch flushed")) &&
			bytes.Contains(output.Bytes(), []byte("size=2"))
	}, time.Second, 10*time.Millisecond)
}

func TestPipelineFlushesOnTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := storage.NewMockRepository()
	pipeline := NewPipeline(repo, 10, 1, 10)
	pipeline.Start(ctx)

	traceID := uuid.New()
	err := pipeline.Submit(newTestSpan(traceID))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return repo.CreateSpanBatchCount == 1
	}, 2*time.Second, 10*time.Millisecond)

	spans, err := repo.ListSpansByTrace(context.Background(), traceID)
	require.NoError(t, err)
	require.Len(t, spans, 1)
}

func TestPipelineSubmitReturnsErrorWhenQueueIsFull(t *testing.T) {
	pipeline := NewPipeline(storage.NewMockRepository(), 10, 0, 1)

	err := pipeline.Submit(newTestSpan(uuid.New()))
	require.NoError(t, err)

	err = pipeline.Submit(newTestSpan(uuid.New()))
	require.ErrorIs(t, err, errQueueFull)
	require.EqualError(t, err, "queue full")
	require.EqualValues(t, 1, pipeline.ingestQueueOverflowTotal.Load())
}

func TestPipelineRejectsAtNinetyPercentQueueCapacity(t *testing.T) {
	pipeline := NewPipeline(storage.NewMockRepository(), 10, 0, 10)

	for i := 0; i < 9; i++ {
		require.NoError(t, pipeline.Submit(newTestSpan(uuid.New())))
	}

	err := pipeline.Submit(newTestSpan(uuid.New()))
	require.ErrorIs(t, err, ErrQueueFull)
	require.EqualValues(t, 1, pipeline.ingestQueueOverflowTotal.Load())
}

func TestPipelineLogsWarningWhenQueueExceedsEightyPercent(t *testing.T) {
	pipeline := NewPipeline(storage.NewMockRepository(), 10, 0, 5)

	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	for i := 0; i < 5; i++ {
		err := pipeline.Submit(newTestSpan(uuid.New()))
		require.NoError(t, err)
	}

	require.Contains(t, output.String(), "ingestion queue high-water mark")
}

func newTestSpan(traceID uuid.UUID) *models.Span {
	return &models.Span{
		TraceID:     traceID,
		SpanID:      uuid.New(),
		ParentID:    uuid.Nil,
		ServiceName: "pipeline-test",
		StartTime:   time.Now().UTC(),
		DurationMs:  25,
		Tags:        map[string]string{"source": "test"},
	}
}

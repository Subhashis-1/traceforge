package api

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

func TestCache(t *testing.T) {
	key := "service:auth"
	trace := &models.Trace{TraceID: uuid.New()}

	SetCachedSearch(key, []*models.Trace{trace})
	searchCache.Wait()

	got, ok := GetCachedSearch(key)
	if !ok || len(got) != 1 {
		t.Fatalf("cache miss after set")
	}

	time.Sleep(11 * time.Second)
	if _, ok := GetCachedSearch(key); ok {
		t.Fatalf("cache still alive after TTL")
	}
}

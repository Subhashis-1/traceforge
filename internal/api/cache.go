package api

import (
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/Subhashis-1/traceforge/internal/models"
)

const searchCacheTTL = 10 * time.Second

var searchCache *ristretto.Cache

func init() {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e4,
		MaxCost:     1000,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}

	searchCache = cache
}

// GetCachedSearch returns cached search results for the raw DSL query string.
func GetCachedSearch(key string) ([]*models.Trace, bool) {
	if searchCache == nil {
		return nil, false
	}

	value, ok := searchCache.Get(key)
	if !ok {
		return nil, false
	}

	results, ok := value.([]*models.Trace)
	if !ok {
		return nil, false
	}

	return results, true
}

// SetCachedSearch stores search results for the raw DSL query string.
func SetCachedSearch(key string, results []*models.Trace) {
	if searchCache == nil {
		return
	}

	searchCache.SetWithTTL(key, results, 1, searchCacheTTL)
}

# Query Patterns for Trace Forge

This document defines the core read access patterns that drive the Cassandra schema design for Trace Forge. Each pattern is optimized for low-latency queries (<30ms locally) and avoids ALLOW FILTERING scans.

---

## 1. Trace List by Service & Time

**Human-readable name:** Service Trace Timeline

**Query intention:**  
List the most recent N traces for a specific service within a given time window, ordered by start time (descending).

**Example use case:**  
"Show me the last 50 traces for `payment-service` between 2:00 PM and 3:00 PM today."

**Cassandra query pattern:**

```cql
SELECT trace_id, root_span_id, start_time, duration, status, tags
FROM traces_by_service
WHERE service_name = ?
  AND date_bucket = ?
  AND start_time >= ?
  AND start_time <= ?
ORDER BY start_time DESC
LIMIT 50;
```

**Primary key design:**

- **Partition key:** `(service_name, date_bucket)`
- **Clustering columns:** `start_time, trace_id`

**Why this works:**

- Queries filter by exact `service_name` and `date_bucket` (single partition)
- Time-range scans use clustering order on `start_time`
- `trace_id` as final clustering column ensures uniqueness
- Descending order matches UI "most recent first" requirement

---

## 2. Spans by Trace ID

**Human-readable name:** Trace Detail Tree

**Query intention:**  
Retrieve all spans belonging to a specific trace, ordered to reconstruct the parent-child hierarchy.

**Example use case:**  
"Show me the full span tree for trace `abc-123` to visualize the waterfall diagram."

**Cassandra query pattern:**

```cql
SELECT span_id, parent_id, service_name, operation_name, start_time, duration, tags
FROM spans_by_trace
WHERE trace_id = ?
ORDER BY start_time ASC;
```

**Primary key design:**

- **Partition key:** `trace_id`
- **Clustering columns:** `start_time, span_id`

**Why this works:**

- Single partition query by `trace_id` (fast point lookup)
- All spans for a trace are co-located in the same partition
- Time-ordered clustering enables waterfall reconstruction
- Parent-child relationships resolved via `parent_id` field (no join needed)

---

## 3. Events by Session

**Human-readable name:** Session Event Timeline

**Query intention:**  
List all events (user actions, errors, logs) for a specific session within a time range, ordered chronologically.

**Example use case:**  
"Show me all user events for session `xyz-789` during the problematic checkout flow."

**Cassandra query pattern:**

```cql
SELECT event_id, ts, event_type, payload, tags
FROM events_by_session
WHERE session_id = ?
  AND date_bucket = ?
  AND ts >= ?
  AND ts <= ?
ORDER BY ts ASC
LIMIT 1000;
```

**Primary key design:**

- **Partition key:** `(session_id, date_bucket)`
- **Clustering columns:** `ts, event_id`

**Why this works:**

- Session-based partitioning keeps related events together
- Time-bucketing prevents unbounded partition growth
- Chronological ordering supports session replay use cases
- Payload stored as `blob` for flexible schema-less event data

---

## 4. Trace Detail by Trace ID

**Human-readable name:** Direct Trace Lookup

**Query intention:**  
Fetch a complete trace (including pre-serialized span tree) by its unique ID for deep-link sharing or alert correlation.

**Example use case:**  
"Load trace `abc-123` directly from a shared URL or alert notification."

**Cassandra query pattern:**

```cql
SELECT trace_id, serialized, created_at
FROM trace_by_id
WHERE trace_id = ?;
```

**Primary key design:**

- **Partition key:** `trace_id`
- **Clustering columns:** None (single-row partition)

**Why this works:**

- Fastest possible lookup (single row, no clustering)
- `serialized` blob contains pre-computed trace + span hierarchy (denormalized)
- Optional optimization table—can be omitted if `spans_by_trace` is sufficient
- Ideal for direct deep-links, alert integrations, and API lookups

---

## Time-Bucket Strategy (YYYY-MM-DD)

### Why time-bucketing is required

Cassandra performs best with **bounded partitions**. Without time-bucketing:

| Problem              | Impact                                                                                                                                    |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| **Unbounded growth** | A popular service like `payment-service` could accumulate millions of traces in a single partition, causing write hotspots and slow reads |
| **Hot partitions**   | All queries target the same partition, overwhelming a single node                                                                         |
| **TTL inefficiency** | Cassandra TTL works at the row level; large partitions make TTL cleanup expensive                                                         |

### Bucket implementation

All time-bucketed tables use a `date_bucket` column:

```cql
date_bucket date  -- Formatted as YYYY-MM-DD (e.g., 2026-04-11)
```

**Benefits:**

1. **Partition distribution:** Queries spread across ~30 buckets per month per service
2. **TTL management:** Apply 30-day TTL at the bucket level for automatic cleanup
3. **Query efficiency:** Time-range queries touch only relevant buckets (1-3 typically)
4. **Predictable partition size:** Each bucket contains ~1 day of data per service/session

### Bucket calculation (Go example)

```go
func getDateBucket(t time.Time) string {
    return t.Format("2006-01-02") // YYYY-MM-DD
}

// For queries spanning multiple days:
func getDateBuckets(start, end time.Time) []string {
    var buckets []string
    for current := start; !current.After(end); current = current.AddDate(0, 0, 1) {
        buckets = append(buckets, getDateBucket(current))
    }
    return buckets
}
```

**Trade-off:**  
Queries spanning multiple days require querying multiple buckets (application-side fan-out). This is acceptable because:

- Most UI queries are "last N minutes/hours" (single bucket)
- Fan-out is limited (typically <7 buckets for week-long queries)
- Parallel queries to multiple buckets still outperform unbounded partition scans

---

## Summary Table

| Pattern               | Partition Key                 | Clustering Columns     | Time-Bucket? | TTL     |
| --------------------- | ----------------------------- | ---------------------- | ------------ | ------- |
| Trace List by Service | `(service_name, date_bucket)` | `start_time, trace_id` | ✅ Yes       | 30 days |
| Spans by Trace ID     | `trace_id`                    | `start_time, span_id`  | ❌ No        | 30 days |
| Events by Session     | `(session_id, date_bucket)`   | `ts, event_id`         | ✅ Yes       | 30 days |
| Trace Detail Lookup   | `trace_id`                    | None                   | ❌ No        | 30 days |

**Next steps:**  
Use these patterns to define the physical schema in `docs/schema.cql` with exact CQL types, indexes (if needed), and TTL settings.

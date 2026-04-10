-- Trace Forge Cassandra Schema
-- ================================
-- Query-First Design Principles:
-- 1. Partition keys are chosen based on read patterns, not entity relationships
-- 2. Denormalization is preferred over joins (Cassandra does not support joins)
-- 3. Time-bucketing prevents unbounded partition growth
-- 4. TTL is applied at table level for automatic data expiration
--
-- Access Patterns Supported:
-- - List traces by service & time range (traces_by_service)
-- - Get all spans for a trace (spans_by_trace)
-- - List events by session & time range (events_by_session)
-- - Direct trace lookup by ID (trace_by_id)

-- Create keyspace for Trace Forge
CREATE KEYSPACE IF NOT EXISTS traceforge
WITH replication = {
    'class': 'SimpleStrategy',
    'replication_factor': '1'
}
AND durable_writes = true;

USE traceforge;

-- Table 1: traces_by_service
-- Query Pattern: "List the most recent N traces for a specific service within a time window"
-- Partition Key: (service_name, date_bucket) - groups traces by service and day
-- Clustering: start_time, trace_id - enables time-range queries with descending order
-- TTL: 30 days (2592000 seconds) - automatic cleanup of old traces
CREATE TABLE IF NOT EXISTS traces_by_service (
    service_name text,
    date_bucket date,
    start_time timestamp,
    trace_id uuid,
    root_span_id uuid,
    duration bigint,
    status int,
    tags map<text, text>,
    PRIMARY KEY ((service_name, date_bucket), start_time, trace_id)
) WITH default_time_to_live = 2592000
    AND comment = 'Hot table – recent traces searchable by service & time bucket'
    AND CLUSTERING ORDER BY (start_time DESC, trace_id ASC);

-- Table 2: spans_by_trace
-- Query Pattern: "Retrieve all spans belonging to a specific trace for waterfall visualization"
-- Partition Key: trace_id - all spans for a trace are co-located in one partition
-- Clustering: span_id - unique identifier within the trace
-- Note: No TTL here - spans are deleted when parent trace expires (cascading cleanup handled by app)
CREATE TABLE IF NOT EXISTS spans_by_trace (
    trace_id uuid,
    span_id uuid,
    parent_id uuid,
    service_name text,
    start_time timestamp,
    duration bigint,
    tags map<text, text>,
    PRIMARY KEY (trace_id, span_id)
) WITH comment = 'Denormalized spans for a single trace'
    AND CLUSTERING ORDER BY (span_id ASC);

-- Table 3: events_by_session
-- Query Pattern: "List all events for a specific session within a time range for session replay"
-- Partition Key: (session_id, date_bucket) - groups events by session and day
-- Clustering: ts, event_id - chronological ordering for session timeline reconstruction
-- TTL: 30 days (2592000 seconds) - automatic cleanup of old session events
CREATE TABLE IF NOT EXISTS events_by_session (
    session_id uuid,
    date_bucket date,
    ts timestamp,
    event_id uuid,
    payload blob,
    tags map<text, text>,
    PRIMARY KEY ((session_id, date_bucket), ts, event_id)
) WITH default_time_to_live = 2592000
    AND comment = 'User‑session events for replay'
    AND CLUSTERING ORDER BY (ts ASC, event_id ASC);

-- Table 4: trace_by_id (Optional)
-- Query Pattern: "Fast direct lookup of a complete trace by ID for deep-link sharing"
-- Partition Key: trace_id - single-row partition for fastest possible point lookup
-- Use Case: Storing pre-serialized trace (protobuf/JSON) to avoid joining spans_by_trace
-- Trade-off: Data duplication vs. read performance for direct trace access
CREATE TABLE IF NOT EXISTS trace_by_id (
    trace_id uuid PRIMARY KEY,
    serialized blob
) WITH comment = 'Optional materialized view for direct trace fetch (stores full protobuf/json)';

-- Indexes
-- =======
-- Note: Cassandra secondary indexes are generally not recommended for high-cardinality columns.
-- The query patterns above are satisfied by the primary key design alone.
-- If filtering by tag values becomes necessary, consider:
-- 1. Materialized views (with caution - they have limitations)
-- 2. Application-side filtering after fetching by primary key
-- 3. Separate denormalized tables for specific tag-based queries

-- Verification Queries
-- ====================
-- After running migrations, verify with:
-- 
-- DESCRIBE KEYSPACE traceforge;
-- DESCRIBE TABLE traces_by_service;
-- DESCRIBE TABLE spans_by_trace;
-- DESCRIBE TABLE events_by_session;
-- DESCRIBE TABLE trace_by_id;
--
-- Check partition distribution:
-- SELECT token(service_name, date_bucket) FROM traces_by_service LIMIT 5;
-- SELECT token(session_id, date_bucket) FROM events_by_session LIMIT 5;

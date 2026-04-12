-- Trace Forge Cassandra Schema
-- ============================
-- Query-first design:
-- - traces_by_service => service_name + date_bucket
-- - spans_by_trace    => trace_id
-- - events_by_session => session_id + date_bucket
-- - trace_by_id       => trace_id

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
    AND comment = 'Hot table for recent traces by service and time bucket'
    AND CLUSTERING ORDER BY (start_time DESC, trace_id ASC);

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

CREATE TABLE IF NOT EXISTS events_by_session (
    session_id uuid,
    date_bucket date,
    ts timestamp,
    event_id uuid,
    payload blob,
    tags map<text, text>,
    PRIMARY KEY ((session_id, date_bucket), ts, event_id)
) WITH default_time_to_live = 2592000
    AND comment = 'User session events for replay'
    AND CLUSTERING ORDER BY (ts ASC, event_id ASC);

CREATE TABLE IF NOT EXISTS trace_by_id (
    trace_id uuid PRIMARY KEY,
    serialized blob
) WITH comment = 'Direct trace lookup metadata and optional serialized payload';

ALTER TABLE trace_by_id ADD service_name text;
ALTER TABLE trace_by_id ADD date_bucket date;
ALTER TABLE trace_by_id ADD start_time timestamp;
ALTER TABLE trace_by_id ADD root_span_id uuid;
ALTER TABLE trace_by_id ADD duration bigint;
ALTER TABLE trace_by_id ADD status int;
ALTER TABLE trace_by_id ADD tags map<text, text>;

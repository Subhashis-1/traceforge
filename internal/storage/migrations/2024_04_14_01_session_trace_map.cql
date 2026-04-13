-- Migration: 2024_04_14_01_session_trace_map
-- Description: Maps a UI session ID to the backend trace ID that originated the request
-- Created: 2024-04-14

-- Up migration
CREATE TABLE IF NOT EXISTS traceforge.session_trace_map (
    session_id uuid PRIMARY KEY,
    trace_id   uuid,
    created_at timestamp
) WITH comment = 'Maps a UI session ID to the backend trace ID that originated the request.'
  AND default_time_to_live = 2592000;   -- 30 days TTL

-- Down migration
-- DROP TABLE IF EXISTS traceforge.session_trace_map;

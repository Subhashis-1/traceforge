CREATE TABLE IF NOT EXISTS traceforge.session_trace_map (
    session_id uuid PRIMARY KEY,
    trace_id   uuid,
    created_at timestamp
) WITH comment = 'Maps a UI session ID to the backend trace ID that originated the request.'
  AND default_time_to_live = 2592000;   -- 30 days TTL

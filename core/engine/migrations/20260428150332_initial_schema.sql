-- R4.2 — initial schema for engine job state and audit rows.
-- See ADR-0023 §2 for the schema design and §13 for the migration
-- discipline (versioned, immutable, forward-only).

CREATE TABLE jobs (
    id UUID PRIMARY KEY,
    dsl TEXT NOT NULL,
    driver TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    rows_extracted BIGINT,
    error TEXT,
    resolved_engine_endpoint TEXT,
    output_sink_kind TEXT NOT NULL CHECK (output_sink_kind IN ('stdout', 'kafka', 's3', 'webhook'))
);

CREATE TABLE job_rows (
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    row_index BIGINT NOT NULL,
    json_value JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, row_index)
);

CREATE INDEX idx_jobs_status_created ON jobs(status, created_at);
CREATE INDEX idx_jobs_output_sink_kind ON jobs(output_sink_kind);
CREATE INDEX idx_job_rows_job_id ON job_rows(job_id);

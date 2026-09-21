-- BidOS schema. Portable SQL: TEXT ids, ISO-8601 UTC timestamps, INTEGER booleans, TEXT json.
CREATE TABLE IF NOT EXISTS organizations (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, mode TEXT NOT NULL DEFAULT 'demo',
  vertical_pack_id TEXT NOT NULL DEFAULT 'saas-it', data_sensitivity TEXT NOT NULL DEFAULT 'synthetic',
  expires_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL, email TEXT NOT NULL, role TEXT NOT NULL, password_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS users_org ON users(org_id);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  org_id TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  org_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (org_id, key)
);
CREATE TABLE IF NOT EXISTS bids (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL, buyer_name TEXT NOT NULL DEFAULT '', buyer_url TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL DEFAULT '', estimated_value REAL NOT NULL DEFAULT 0, deadline TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'intake', description TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS bids_org ON bids(org_id);
CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  bid_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, mime_type TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL DEFAULT 1, approval_state TEXT NOT NULL DEFAULT 'unapproved',
  source_type TEXT NOT NULL DEFAULT 'upload', file_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '', client_disclosure TEXT NOT NULL DEFAULT '', trust TEXT NOT NULL DEFAULT 'internal',
  tags TEXT NOT NULL DEFAULT '[]', metadata TEXT NOT NULL DEFAULT '{}', parse_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS documents_org ON documents(org_id);
CREATE INDEX IF NOT EXISTS documents_bid ON documents(bid_id);
CREATE TABLE IF NOT EXISTS document_chunks (
  id TEXT PRIMARY KEY, document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  section_path TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, page_number INTEGER NOT NULL DEFAULT 0,
  sheet_name TEXT NOT NULL DEFAULT '', cell_range TEXT NOT NULL DEFAULT '', chunk_index INTEGER NOT NULL,
  text_hash TEXT NOT NULL DEFAULT '', embedding BLOB, embedding_model_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS chunks_doc ON document_chunks(document_id);
CREATE TABLE IF NOT EXISTS requirements (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  code TEXT NOT NULL, section TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, category TEXT NOT NULL DEFAULT 'general',
  mandatory INTEGER NOT NULL DEFAULT 1, source_document_id TEXT NOT NULL DEFAULT '', source_locator TEXT NOT NULL DEFAULT '',
  source_span TEXT NOT NULL DEFAULT '', anchor_status TEXT NOT NULL DEFAULT 'anchored',
  change_state TEXT NOT NULL DEFAULT 'unchanged', status TEXT NOT NULL DEFAULT 'not_started',
  owner_id TEXT NOT NULL DEFAULT '', reviewer_id TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0,
  confirmed INTEGER NOT NULL DEFAULT 0, is_trap INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS requirements_bid ON requirements(bid_id);
CREATE TABLE IF NOT EXISTS evidence (
  id TEXT PRIMARY KEY, requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
  chunk_id TEXT NOT NULL, score REAL NOT NULL DEFAULT 0, excerpt TEXT NOT NULL DEFAULT '', approved INTEGER NOT NULL DEFAULT 1,
  rank INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS evidence_req ON evidence(requirement_id);
CREATE TABLE IF NOT EXISTS answers (
  id TEXT PRIMARY KEY, requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
  draft_text TEXT NOT NULL DEFAULT '', final_text TEXT NOT NULL DEFAULT '', generation_mode TEXT NOT NULL DEFAULT 'demo',
  confidence_label TEXT NOT NULL DEFAULT 'Insufficient evidence', status TEXT NOT NULL DEFAULT 'drafted',
  version INTEGER NOT NULL DEFAULT 1, approved_by TEXT NOT NULL DEFAULT '', approved_at TEXT NOT NULL DEFAULT '',
  library_entry_id TEXT NOT NULL DEFAULT '', unsupported_count INTEGER NOT NULL DEFAULT 0,
  verified_at TEXT NOT NULL DEFAULT '', verification TEXT NOT NULL DEFAULT '{}', sme_question TEXT NOT NULL DEFAULT '',
  evidence_gaps TEXT NOT NULL DEFAULT '', provider_id TEXT NOT NULL DEFAULT 'demo', model TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS answers_req ON answers(requirement_id);
CREATE TABLE IF NOT EXISTS citations (
  id TEXT PRIMARY KEY, answer_id TEXT NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
  marker INTEGER NOT NULL, chunk_id TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS citations_answer ON citations(answer_id);
CREATE INDEX IF NOT EXISTS citations_chunk ON citations(chunk_id);
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  requirement_id TEXT NOT NULL DEFAULT '', assignee_id TEXT NOT NULL DEFAULT '', requested_by TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'sme', status TEXT NOT NULL DEFAULT 'open', due_at TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', question TEXT NOT NULL DEFAULT '', comment TEXT NOT NULL DEFAULT '',
  response TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_bid ON tasks(bid_id);
CREATE INDEX IF NOT EXISTS tasks_assignee ON tasks(assignee_id);
CREATE TABLE IF NOT EXISTS reviews (
  id TEXT PRIMARY KEY, answer_id TEXT NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
  reviewer_id TEXT NOT NULL, status TEXT NOT NULL, comment TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS qa_results (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  check_type TEXT NOT NULL, severity TEXT NOT NULL, message TEXT NOT NULL, entity_type TEXT NOT NULL DEFAULT '',
  entity_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'open', resolution TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS qa_bid ON qa_results(bid_id);
CREATE TABLE IF NOT EXISTS export_jobs (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  format TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'succeeded', file_path TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS answer_library (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  canonical_question TEXT NOT NULL, answer_text TEXT NOT NULL, citation_chunk_ids TEXT NOT NULL DEFAULT '[]',
  tags TEXT NOT NULL DEFAULT '[]', category TEXT NOT NULL DEFAULT '', owner_id TEXT NOT NULL DEFAULT '',
  approved_by_id TEXT NOT NULL DEFAULT '', approved_at TEXT NOT NULL DEFAULT '', review_by TEXT NOT NULL DEFAULT '',
  source_answer_id TEXT NOT NULL DEFAULT '', source_bid_id TEXT NOT NULL DEFAULT '', reuse_count INTEGER NOT NULL DEFAULT 0,
  embedding BLOB, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS library_org ON answer_library(org_id);
CREATE TABLE IF NOT EXISTS addenda (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  document_id TEXT NOT NULL, received_at TEXT NOT NULL, diff_summary TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS clarifications (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  requirement_id TEXT NOT NULL DEFAULT '', question TEXT NOT NULL, sent_at TEXT NOT NULL DEFAULT '',
  due_at TEXT NOT NULL DEFAULT '', buyer_response TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'open',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pipeline_runs (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'queued', mode TEXT NOT NULL DEFAULT 'manual', current_stage TEXT NOT NULL DEFAULT 'INTAKE',
  gate TEXT NOT NULL DEFAULT '', gate_owner TEXT NOT NULL DEFAULT '', gate_due TEXT NOT NULL DEFAULT '',
  auto_gates INTEGER NOT NULL DEFAULT 0, only_requirements TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_bid ON pipeline_runs(bid_id);
CREATE TABLE IF NOT EXISTS pipeline_steps (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
  bid_id TEXT NOT NULL, stage TEXT NOT NULL, entity_type TEXT NOT NULL DEFAULT '', entity_id TEXT NOT NULL DEFAULT '',
  input_hash TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
  output_ref TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', provider_mode TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS steps_run ON pipeline_steps(run_id);
CREATE INDEX IF NOT EXISTS steps_key ON pipeline_steps(bid_id, stage, entity_id, input_hash);
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'queued', run_at TEXT NOT NULL, lease_until TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 3, idempotency_key TEXT NOT NULL DEFAULT '',
  progress INTEGER NOT NULL DEFAULT 0, log TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS jobs_idem ON jobs(idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status, run_at);
CREATE TABLE IF NOT EXISTS provider_calls (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL DEFAULT '', task_kind TEXT NOT NULL, prompt_id TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL, cached INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 1, latency_ms INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0, outcome TEXT NOT NULL DEFAULT 'ok', error_class TEXT NOT NULL DEFAULT '',
  fallback INTEGER NOT NULL DEFAULT 0, repaired INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ai_cache (
  key TEXT PRIMARY KEY, provider_id TEXT NOT NULL, model TEXT NOT NULL, output TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS eval_runs (
  id TEXT PRIMARY KEY, suite TEXT NOT NULL, provider_mode TEXT NOT NULL, metrics TEXT NOT NULL DEFAULT '{}',
  passed INTEGER NOT NULL DEFAULT 0, report_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS activity_log (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL,
  entity_type TEXT NOT NULL DEFAULT '', entity_id TEXT NOT NULL DEFAULT '', metadata TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS activity_org ON activity_log(org_id, created_at);
CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY, org_id TEXT NOT NULL, user_id TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL DEFAULT '', read INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS notifications_user ON notifications(user_id, read);
CREATE TABLE IF NOT EXISTS proposal_sections (
  id TEXT PRIMARY KEY, bid_id TEXT NOT NULL REFERENCES bids(id) ON DELETE CASCADE,
  key TEXT NOT NULL, title TEXT NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0, content TEXT NOT NULL DEFAULT '',
  answer_ids TEXT NOT NULL DEFAULT '[]', updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sections_bid ON proposal_sections(bid_id);
CREATE TABLE IF NOT EXISTS qualification (
  bid_id TEXT PRIMARY KEY REFERENCES bids(id) ON DELETE CASCADE, scores TEXT NOT NULL DEFAULT '{}',
  flags TEXT NOT NULL DEFAULT '[]', priority TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name TEXT NOT NULL DEFAULT '',
  password_hash TEXT,
  password_scheme TEXT NOT NULL DEFAULT 'bcrypt',
  avatar_data TEXT,
  avatar_mime TEXT,
  banned INTEGER NOT NULL DEFAULT 0,
  invite_pending INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audience TEXT NOT NULL CHECK (audience IN ('web','cli','extension')),
  access_hash TEXT NOT NULL UNIQUE,
  refresh_hash TEXT UNIQUE,
  csrf_hash TEXT,
  user_agent TEXT NOT NULL DEFAULT '',
  origin TEXT NOT NULL DEFAULT '',
  revoked_at TEXT,
  access_expires_at TEXT NOT NULL,
  refresh_expires_at TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_access_hash ON sessions(access_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_hash ON sessions(refresh_hash);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  used_at TEXT,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS bookmarks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  title TEXT,
  description TEXT,
  favicon TEXT,
  thumbnail TEXT,
  sanitized_html TEXT,
  text_content TEXT,
  domain TEXT,
  reading_time INTEGER NOT NULL DEFAULT 0,
  reading_progress REAL NOT NULL DEFAULT 0 CHECK(reading_progress >= 0 AND reading_progress <= 1),
  read_status INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT 'web',
  x_tweet_id TEXT,
  x_author_username TEXT,
  x_author_name TEXT,
  x_tweet_url TEXT,
  x_metrics_json TEXT,
  canonical_url TEXT NOT NULL DEFAULT '',
  content_kind TEXT NOT NULL DEFAULT '',
  source_published_at TEXT,
  source_author_id TEXT,
  source_publisher_key TEXT,
  processed_at TEXT,
  fetch_version TEXT NOT NULL DEFAULT '',
  summary_version TEXT NOT NULL DEFAULT '',
  enrichment_version TEXT NOT NULL DEFAULT '',
  embedding BLOB,
  embedding_model TEXT,
  embedding_dim INTEGER NOT NULL DEFAULT 0,
  version INTEGER NOT NULL DEFAULT 1,
  resurfacing_snoozed_until TEXT,
  resurfacing_archived INTEGER NOT NULL DEFAULT 0,
  last_accessed TEXT,
  view_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, url)
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user_created ON bookmarks(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user_domain ON bookmarks(user_id, domain);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user_source ON bookmarks(user_id, source);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookmarks_id_user ON bookmarks(id, user_id);

CREATE TABLE IF NOT EXISTS bookmark_evidence (
  id TEXT PRIMARY KEY,
  bookmark_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  evidence_kind TEXT NOT NULL,
  evidence_origin TEXT NOT NULL,
  authority INTEGER NOT NULL DEFAULT 0,
  content_text TEXT NOT NULL DEFAULT '',
  sanitized_html TEXT NOT NULL DEFAULT '',
  canonical_url TEXT NOT NULL DEFAULT '',
  author_id TEXT NOT NULL DEFAULT '',
  publisher_key TEXT NOT NULL DEFAULT '',
  published_at TEXT,
  extraction_method TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  quality_status TEXT NOT NULL DEFAULT 'failed',
  quality_reasons_json TEXT NOT NULL DEFAULT '[]',
  extractor_version TEXT NOT NULL DEFAULT '',
  is_selected INTEGER NOT NULL DEFAULT 0 CHECK(is_selected IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(bookmark_id, user_id) REFERENCES bookmarks(id, user_id) ON DELETE CASCADE,
  UNIQUE(bookmark_id, evidence_kind, content_hash, extractor_version)
);

CREATE INDEX IF NOT EXISTS idx_evidence_user_bookmark ON bookmark_evidence(user_id, bookmark_id, authority DESC);
CREATE INDEX IF NOT EXISTS idx_evidence_user_published ON bookmark_evidence(user_id, published_at DESC) WHERE published_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_evidence_user_quality_version ON bookmark_evidence(user_id, quality_status, extractor_version);
CREATE INDEX IF NOT EXISTS idx_evidence_content_hash ON bookmark_evidence(content_hash) WHERE content_hash != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_evidence_selected_bookmark ON bookmark_evidence(bookmark_id) WHERE is_selected = 1;

CREATE TABLE IF NOT EXISTS capture_attempts (
  id TEXT PRIMARY KEY,
  bookmark_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  retry_of_id TEXT REFERENCES capture_attempts(id) ON DELETE SET NULL,
  status TEXT NOT NULL CHECK(status IN ('queued','running','complete','partial','failed')),
  requested_url TEXT NOT NULL,
  final_url TEXT NOT NULL DEFAULT '',
  engine TEXT NOT NULL DEFAULT 'direct_http',
  engine_version TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  queued_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  FOREIGN KEY(bookmark_id,user_id) REFERENCES bookmarks(id,user_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_capture_attempts_owner ON capture_attempts(user_id,bookmark_id,queued_at DESC);
CREATE INDEX IF NOT EXISTS idx_capture_attempts_status ON capture_attempts(status,queued_at);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  bookmark_id TEXT NOT NULL,
  capture_attempt_id TEXT NOT NULL REFERENCES capture_attempts(id) ON DELETE CASCADE,
  evidence_id TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL,
  artifact_type TEXT NOT NULL CHECK(artifact_type IN ('source_response','screenshot','pdf','self_contained_html','uploaded_file')),
  mime_type TEXT NOT NULL,
  byte_size INTEGER NOT NULL CHECK(byte_size >= 0),
  sha256 TEXT NOT NULL,
  storage_key TEXT NOT NULL,
  original_filename TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  deleted_at TEXT,
  FOREIGN KEY(bookmark_id,user_id) REFERENCES bookmarks(id,user_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_artifacts_owner ON artifacts(user_id,bookmark_id,created_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifacts_attempt ON artifacts(capture_attempt_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_attempt_type ON artifacts(capture_attempt_id,artifact_type);
CREATE INDEX IF NOT EXISTS idx_artifacts_storage_key ON artifacts(storage_key);

CREATE VIRTUAL TABLE IF NOT EXISTS bookmarks_fts USING fts5(
  title,
  description,
  text_content,
  content='bookmarks',
  content_rowid='rowid'
);

CREATE TABLE IF NOT EXISTS search_index (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK(item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '',
  links TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_type, item_id)
);

CREATE INDEX IF NOT EXISTS idx_search_index_user_updated ON search_index(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_search_index_user_type ON search_index(user_id, item_type, updated_at DESC);

CREATE TABLE IF NOT EXISTS result_feedback (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK(item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  surface TEXT NOT NULL DEFAULT 'search',
  feedback TEXT NOT NULL CHECK(feedback IN ('useful','not_useful','snooze_longer','never_resurface')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_type, item_id, surface)
);

CREATE INDEX IF NOT EXISTS idx_result_feedback_item ON result_feedback(user_id, item_type, item_id);

CREATE TABLE IF NOT EXISTS knowledge_feedback (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL CHECK(target_type IN ('insight','relationship')),
  target_id TEXT NOT NULL,
  feedback TEXT NOT NULL CHECK(feedback IN ('useful','not_useful','snooze','dismiss','confirm')),
  detector_family TEXT NOT NULL DEFAULT '',
  detector_version TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  snoozed_until TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_feedback_target ON knowledge_feedback(user_id, target_type, target_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS insight_impressions (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  insight_id TEXT NOT NULL,
  detector_family TEXT NOT NULL,
  detector_version TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  impression_count INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(user_id, insight_id, detector_version)
);

CREATE INDEX IF NOT EXISTS idx_insight_impressions_detector ON insight_impressions(user_id, detector_family, detector_version, last_seen_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
  user_id UNINDEXED,
  item_type UNINDEXED,
  item_id UNINDEXED,
  title,
  body,
  tags,
  links,
  source,
  updated_at UNINDEXED
);

CREATE TABLE IF NOT EXISTS ai_summaries (
  id TEXT PRIMARY KEY,
  bookmark_id TEXT NOT NULL UNIQUE REFERENCES bookmarks(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  one_sentence TEXT,
  bullet_points_json TEXT NOT NULL DEFAULT '[]',
  long_form TEXT,
  highlights_json TEXT NOT NULL DEFAULT '[]',
  suggested_tags_json TEXT NOT NULL DEFAULT '[]',
  processing_status TEXT NOT NULL DEFAULT 'pending',
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  prompt_version TEXT NOT NULL DEFAULT '',
  validator_version TEXT NOT NULL DEFAULT '',
  evidence_hash TEXT NOT NULL DEFAULT '',
  validation_status TEXT NOT NULL DEFAULT '',
  validation_reasons_json TEXT NOT NULL DEFAULT '[]',
  highlight_spans_json TEXT NOT NULL DEFAULT '[]',
  generated_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notes_user_updated ON notes(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS daily_notes (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  note_date TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, note_date)
);

CREATE INDEX IF NOT EXISTS idx_daily_notes_user_updated ON daily_notes(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS knowledge_objects (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  object_type TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  fields_json TEXT NOT NULL DEFAULT '{}',
  source_item_type TEXT NOT NULL DEFAULT '',
  source_item_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_objects_user_type ON knowledge_objects(user_id, object_type, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_objects_user_updated ON knowledge_objects(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS bookmark_notes (
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY(bookmark_id, note_id)
);

CREATE TABLE IF NOT EXISTS annotations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  quote TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  selector_json TEXT NOT NULL DEFAULT '{}',
  tags_json TEXT NOT NULL DEFAULT '[]',
  evidence_id TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_annotations_bookmark ON annotations(user_id, bookmark_id, created_at DESC);

CREATE TABLE IF NOT EXISTS tags (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, slug)
);

CREATE TABLE IF NOT EXISTS tag_aliases (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  alias TEXT NOT NULL,
  alias_slug TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(user_id, alias_slug)
);

CREATE TABLE IF NOT EXISTS bookmark_tags (
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL,
  PRIMARY KEY(bookmark_id, tag_id)
);

CREATE TABLE IF NOT EXISTS saved_searches (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  query TEXT NOT NULL DEFAULT '',
  filters_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, name)
);

CREATE TABLE IF NOT EXISTS review_events (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK (item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('completed','snoozed')),
  snoozed_until TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_review_events_item ON review_events(user_id, item_type, item_id, created_at DESC);

CREATE TABLE IF NOT EXISTS item_states (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK (item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  stage TEXT NOT NULL CHECK (stage IN ('inbox','processing','processed','archived')),
  importance INTEGER NOT NULL DEFAULT 0 CHECK (importance BETWEEN 0 AND 5),
  next_action TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id, item_type, item_id)
);

CREATE INDEX IF NOT EXISTS idx_item_states_user_stage ON item_states(user_id, stage, updated_at DESC);

CREATE TABLE IF NOT EXISTS item_links (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  from_type TEXT NOT NULL CHECK (from_type IN ('bookmark','note')),
  from_id TEXT NOT NULL,
  to_type TEXT NOT NULL CHECK (to_type IN ('bookmark','note')),
  to_id TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL,
  UNIQUE(user_id, from_type, from_id, to_type, to_id, label)
);

CREATE INDEX IF NOT EXISTS idx_item_links_from ON item_links(user_id, from_type, from_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_item_links_to ON item_links(user_id, to_type, to_id, created_at DESC);

CREATE TABLE IF NOT EXISTS reminders (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK (item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  due_at TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  recurrence TEXT NOT NULL CHECK (recurrence IN ('none','daily','weekly','monthly','custom')) DEFAULT 'none',
  recurrence_interval_days INTEGER NOT NULL DEFAULT 0,
  notification_channel TEXT NOT NULL CHECK (notification_channel IN ('in_app','email')) DEFAULT 'in_app',
  note TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending','completed')) DEFAULT 'pending',
  created_at TEXT NOT NULL,
  completed_at TEXT,
  last_notified_at TEXT,
  last_completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_reminders_user_due ON reminders(user_id, status, due_at);
CREATE INDEX IF NOT EXISTS idx_reminders_notification_due ON reminders(status, notification_channel, due_at, last_notified_at);

CREATE TABLE IF NOT EXISTS action_items (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK (item_type IN ('bookmark','note')),
  item_id TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','completed')) DEFAULT 'pending',
  created_at TEXT NOT NULL,
  completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_action_items_user_status ON action_items(user_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_action_items_item ON action_items(user_id, item_type, item_id, status);

CREATE TABLE IF NOT EXISTS assistant_actions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action_type TEXT NOT NULL CHECK (action_type IN ('update_item_state','create_link','create_reminder','create_action_item')),
  payload_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL CHECK (status IN ('pending','executed','rejected','failed')) DEFAULT 'pending',
  result_json TEXT NOT NULL DEFAULT '{}',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  decided_at TEXT,
  executed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_assistant_actions_user_status ON assistant_actions(user_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS collections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_id TEXT REFERENCES collections(id) ON DELETE RESTRICT,
  sibling_order INTEGER NOT NULL DEFAULT 0,
  name TEXT NOT NULL,
  description TEXT,
  color TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_collections_tree ON collections(user_id,parent_id,sibling_order,name);

CREATE TABLE IF NOT EXISTS collection_bookmarks (
  collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at TEXT NOT NULL,
  PRIMARY KEY(collection_id, bookmark_id)
);

CREATE TABLE IF NOT EXISTS bookmark_accesses (
  id TEXT PRIMARY KEY,
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  accessed_at TEXT NOT NULL,
  context TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS bookmark_entities (
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entity TEXT NOT NULL,
  normalized_key TEXT NOT NULL DEFAULT '',
  entity_type TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  extraction_method TEXT NOT NULL DEFAULT '',
  evidence_id TEXT,
  evidence_text TEXT NOT NULL DEFAULT '',
  evidence_start INTEGER NOT NULL DEFAULT 0,
  evidence_end INTEGER NOT NULL DEFAULT 0,
  enrichment_version TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(bookmark_id, entity)
);

CREATE TABLE IF NOT EXISTS bookmark_concepts (
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  concept TEXT NOT NULL,
  normalized_key TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  extraction_method TEXT NOT NULL DEFAULT '',
  evidence_id TEXT,
  evidence_text TEXT NOT NULL DEFAULT '',
  evidence_start INTEGER NOT NULL DEFAULT 0,
  evidence_end INTEGER NOT NULL DEFAULT 0,
  enrichment_version TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(bookmark_id, concept)
);

CREATE INDEX IF NOT EXISTS idx_entities_quality ON bookmark_entities(user_id, enrichment_version, confidence DESC);
CREATE INDEX IF NOT EXISTS idx_concepts_quality ON bookmark_concepts(user_id, enrichment_version, confidence DESC);
CREATE INDEX IF NOT EXISTS idx_entities_bookmark_quality ON bookmark_entities(bookmark_id, user_id, enrichment_version, confidence DESC);
CREATE INDEX IF NOT EXISTS idx_concepts_bookmark_quality ON bookmark_concepts(bookmark_id, user_id, enrichment_version, confidence DESC);

CREATE TABLE IF NOT EXISTS import_jobs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  total_bookmarks INTEGER NOT NULL DEFAULT 0,
  content_fetched INTEGER NOT NULL DEFAULT 0,
  ai_processed INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'processing',
  estimated_completion_time TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS import_sources (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  import_job_id TEXT REFERENCES import_jobs(id) ON DELETE SET NULL,
  source_type TEXT NOT NULL,
  source_name TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS x_connections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  x_user_id TEXT,
  x_username TEXT,
  x_name TEXT,
  x_profile_image TEXT,
  access_token_cipher TEXT NOT NULL,
  refresh_token_cipher TEXT,
  token_expires_at TEXT,
  scopes_json TEXT NOT NULL DEFAULT '[]',
  connected_at TEXT NOT NULL,
  last_sync_at TEXT,
  sync_status TEXT NOT NULL DEFAULT 'idle',
  total_synced INTEGER NOT NULL DEFAULT 0,
  next_cursor TEXT
);

CREATE TABLE IF NOT EXISTS oauth_states (
  state TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  verifier_cipher TEXT NOT NULL,
  redirect_uri TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value_cipher TEXT,
  value_plain TEXT,
  key_id TEXT,
  updated_by TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_settings (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(user_id,key)
);

CREATE TABLE IF NOT EXISTS feed_subscriptions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  collection_id TEXT REFERENCES collections(id) ON DELETE SET NULL,
  tags_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'pending',
  last_error TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  last_poll_at TEXT,
  next_poll_at TEXT NOT NULL,
  failure_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id,url)
);

CREATE TABLE IF NOT EXISTS feed_entries (
  subscription_id TEXT NOT NULL REFERENCES feed_subscriptions(id) ON DELETE CASCADE,
  entry_key TEXT NOT NULL,
  bookmark_id TEXT REFERENCES bookmarks(id) ON DELETE SET NULL,
  fingerprint TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY(subscription_id,entry_key)
);
CREATE INDEX IF NOT EXISTS idx_feed_subscriptions_due ON feed_subscriptions(enabled,next_poll_at);
CREATE INDEX IF NOT EXISTS idx_feed_entries_fingerprint ON feed_entries(subscription_id,fingerprint);

CREATE TABLE IF NOT EXISTS public_shares (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_digest TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  expires_at TEXT,
  revoked_at TEXT,
  indexable INTEGER NOT NULL DEFAULT 0 CHECK(indexable IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_public_shares_owner ON public_shares(user_id,created_at DESC);

CREATE TABLE IF NOT EXISTS public_share_items (
  share_id TEXT NOT NULL REFERENCES public_shares(id) ON DELETE CASCADE,
  bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  evidence_id TEXT REFERENCES bookmark_evidence(id) ON DELETE SET NULL,
  public_title TEXT NOT NULL DEFAULT '',
  public_description TEXT NOT NULL DEFAULT '',
  public_url TEXT NOT NULL DEFAULT '',
  public_domain TEXT NOT NULL DEFAULT '',
  public_reader_html TEXT NOT NULL DEFAULT '',
  public_text TEXT NOT NULL DEFAULT '',
  public_published_at TEXT NOT NULL DEFAULT '',
  added_at TEXT NOT NULL,
  PRIMARY KEY(share_id,bookmark_id)
);

CREATE TABLE IF NOT EXISTS public_share_artifacts (
  share_id TEXT NOT NULL REFERENCES public_shares(id) ON DELETE CASCADE,
  artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
  artifact_type TEXT NOT NULL CHECK(artifact_type IN ('screenshot','pdf')),
  added_at TEXT NOT NULL,
  PRIMARY KEY(share_id,artifact_id)
);

CREATE TABLE IF NOT EXISTS rate_limits (
  key TEXT PRIMARY KEY,
  window_start TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  priority INTEGER NOT NULL DEFAULT 100,
  payload_json TEXT NOT NULL DEFAULT '{}',
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  leased_until TEXT,
  last_error TEXT,
  run_after TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_ready ON jobs(status, run_after, priority);

CREATE TABLE IF NOT EXISTS quality_reprocess_runs (
  id TEXT PRIMARY KEY,
  scope_type TEXT NOT NULL CHECK(scope_type IN ('user','all')),
  scope_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  target_fetch_version TEXT NOT NULL,
  target_summary_version TEXT NOT NULL,
  target_enrichment_version TEXT NOT NULL,
  backup_sha256 TEXT NOT NULL DEFAULT '',
  protected_digest TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK(status IN ('planned','queued','running','completed','partial','failed')) DEFAULT 'planned',
  total_candidates INTEGER NOT NULL DEFAULT 0,
  queued_count INTEGER NOT NULL DEFAULT 0,
  completed_count INTEGER NOT NULL DEFAULT 0,
  partial_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  preserved_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_quality_reprocess_runs_scope ON quality_reprocess_runs(scope_type, scope_user_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS quality_reprocess_items (
  run_id TEXT NOT NULL REFERENCES quality_reprocess_runs(id) ON DELETE CASCADE,
  bookmark_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
  status TEXT NOT NULL CHECK(status IN ('eligible','queued','processing','completed','partial','failed','skipped')) DEFAULT 'eligible',
  reason TEXT NOT NULL DEFAULT '',
  expected_evidence_hash TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(run_id, bookmark_id),
  FOREIGN KEY(bookmark_id, user_id) REFERENCES bookmarks(id, user_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_quality_reprocess_items_job ON quality_reprocess_items(job_id) WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_quality_reprocess_items_status ON quality_reprocess_items(run_id, status, updated_at);

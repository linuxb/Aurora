use regex::Regex;
use rocksdb::{DB, Direction, IteratorMode, Options};
use serde_json;
use std::collections::HashSet;
use std::env;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use crate::types::{ListQueryParams, MemoryEntry, SearchQuery, SearchQueryParams};

pub trait MemoryStore: Send + Sync {
    fn ingest(&self, entry: MemoryEntry);
    fn list_all(&self) -> Vec<MemoryEntry>;
    fn search(&self, query: &SearchQuery) -> Vec<MemoryEntry>;
}

#[derive(Default)]
pub struct InMemoryStore {
    entries: Mutex<Vec<MemoryEntry>>,
}

impl MemoryStore for InMemoryStore {
    fn ingest(&self, entry: MemoryEntry) {
        if let Ok(mut entries) = self.entries.lock() {
            entries.push(entry);
        }
    }

    fn list_all(&self) -> Vec<MemoryEntry> {
        self.entries
            .lock()
            .map(|guard| guard.clone())
            .unwrap_or_default()
    }

    fn search(&self, query: &SearchQuery) -> Vec<MemoryEntry> {
        filter_entries(self.list_all(), query)
    }
}

pub struct FileMarkdownStore {
    root_dir: PathBuf,
    max_files_per_session: usize,
    write_guard: Mutex<()>,
}

impl FileMarkdownStore {
    pub fn new(root_dir: PathBuf, max_files_per_session: usize) -> Self {
        let _ = fs::create_dir_all(&root_dir);
        Self {
            root_dir,
            max_files_per_session,
            write_guard: Mutex::new(()),
        }
    }

    fn user_session_dir(&self, user_id: &str, session_id: &str) -> PathBuf {
        self.root_dir
            .join(sanitize_path_component(user_id))
            .join(sanitize_path_component(session_id))
    }

    fn write_entry(&self, entry: &MemoryEntry) {
        let _guard = match self.write_guard.lock() {
            Ok(guard) => guard,
            Err(_) => return,
        };

        let dir = self.user_session_dir(&entry.user_id, &entry.session_id);
        if fs::create_dir_all(&dir).is_err() {
            return;
        }

        let ts = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_millis())
            .unwrap_or(0);
        let filename = format!("{}_{}.md", ts, sanitize_path_component(&entry.task_id));
        let path = dir.join(filename);
        let content = format!(
            "---\nuser_id: {}\nsession_id: {}\ndag_id: {}\ntask_id: {}\nobserved_at: {}\nhard_facts: {}\nrels: {}\n---\n{}\n\n```raw\n{}\n```\n",
            entry.user_id,
            entry.session_id,
            entry.dag_id,
            entry.task_id,
            entry.observed_at,
            entry.hard_facts.join(" | "),
            entry
                .rels
                .iter()
                .map(|r| format!("{}>{}>{}", r[0], r[1], r[2]))
                .collect::<Vec<_>>()
                .join(" | "),
            entry.summary,
            entry.raw_output
        );
        let _ = fs::write(path, content);
        self.rotate_session_files(&dir);
    }

    fn rotate_session_files(&self, session_dir: &Path) {
        if self.max_files_per_session == 0 {
            return;
        }
        let mut files: Vec<PathBuf> = match fs::read_dir(session_dir) {
            Ok(read_dir) => read_dir
                .flatten()
                .map(|e| e.path())
                .filter(|p| p.extension().and_then(|v| v.to_str()) == Some("md"))
                .collect(),
            Err(_) => return,
        };
        if files.len() <= self.max_files_per_session {
            return;
        }
        files.sort();
        let remove_count = files.len().saturating_sub(self.max_files_per_session);
        for old in files.into_iter().take(remove_count) {
            let _ = fs::remove_file(old);
        }
    }

    fn read_all_entries(&self) -> Vec<MemoryEntry> {
        let mut out = Vec::new();
        walk_markdown_files(&self.root_dir, &mut |path| {
            if let Ok(raw) = fs::read_to_string(path)
                && let Some(entry) = parse_markdown_entry(&raw)
            {
                out.push(entry);
            }
        });
        out
    }
}

impl MemoryStore for FileMarkdownStore {
    fn ingest(&self, entry: MemoryEntry) {
        self.write_entry(&entry);
    }

    fn list_all(&self) -> Vec<MemoryEntry> {
        self.read_all_entries()
    }

    fn search(&self, query: &SearchQuery) -> Vec<MemoryEntry> {
        filter_entries(self.read_all_entries(), query)
    }
}

pub struct RocksDbStore {
    db: DB,
}

impl RocksDbStore {
    pub fn open(path: &str) -> Result<Self, String> {
        let mut opts = Options::default();
        opts.create_if_missing(true);
        let db = DB::open(&opts, path).map_err(|e| format!("open rocksdb failed: {e}"))?;
        Ok(Self { db })
    }

    fn make_key(entry: &MemoryEntry) -> String {
        format!(
            "{}:{}:{}:{:020}:{}",
            sanitize_path_component(&entry.user_id),
            sanitize_path_component(&entry.session_id),
            sanitize_path_component(&entry.dag_id),
            entry.observed_at,
            sanitize_path_component(&entry.task_id)
        )
    }

    fn make_prefix(query: &SearchQuery) -> String {
        let mut prefix = sanitize_path_component(&query.user_id);
        prefix.push(':');
        if !query.session_id.is_empty() {
            prefix.push_str(&sanitize_path_component(&query.session_id));
            prefix.push(':');
            if !query.dag_id.is_empty() {
                prefix.push_str(&sanitize_path_component(&query.dag_id));
                prefix.push(':');
            }
        }
        prefix
    }
}

impl MemoryStore for RocksDbStore {
    fn ingest(&self, entry: MemoryEntry) {
        let key = Self::make_key(&entry);
        if let Ok(value) = serde_json::to_vec(&entry) {
            let _ = self.db.put(key.as_bytes(), value);
        }
    }

    fn list_all(&self) -> Vec<MemoryEntry> {
        let mut out = Vec::new();
        for kv in self.db.iterator(rocksdb::IteratorMode::Start) {
            let (_, value) = match kv {
                Ok(v) => v,
                Err(_) => continue,
            };
            if let Ok(entry) = serde_json::from_slice::<MemoryEntry>(&value) {
                out.push(entry);
            }
        }
        out
    }

    fn search(&self, query: &SearchQuery) -> Vec<MemoryEntry> {
        let prefix = Self::make_prefix(query);
        let mut entries = Vec::new();
        for kv in self
            .db
            .iterator(IteratorMode::From(prefix.as_bytes(), Direction::Forward))
        {
            let (key, value) = match kv {
                Ok(v) => v,
                Err(_) => continue,
            };
            let key_str = match std::str::from_utf8(key.as_ref()) {
                Ok(v) => v,
                Err(_) => continue,
            };
            if !key_str.starts_with(&prefix) {
                break;
            }
            if let Ok(entry) = serde_json::from_slice::<MemoryEntry>(&value) {
                entries.push(entry);
            }
        }
        filter_entries(entries, query)
    }
}

pub fn build_store_from_env() -> Arc<dyn MemoryStore> {
    let backend = env::var("POLARIS_MEMORY_BACKEND")
        .unwrap_or_else(|_| "memory".to_string())
        .to_lowercase();
    match backend.as_str() {
        "rocksdb" => {
            let path = env::var("POLARIS_MEMORY_ROCKSDB_PATH")
                .unwrap_or_else(|_| "/tmp/polaris-rocksdb".to_string());
            match RocksDbStore::open(&path) {
                Ok(store) => Arc::new(store),
                Err(_) => Arc::new(InMemoryStore::default()),
            }
        }
        "file_md" => {
            let root = env::var("POLARIS_MEMORY_FS_DIR")
                .unwrap_or_else(|_| "/tmp/polaris-memory".to_string());
            let max_files_per_session = env::var("POLARIS_MEMORY_FS_MAX_FILES_PER_SESSION")
                .ok()
                .and_then(|v| v.parse::<usize>().ok())
                .unwrap_or(200);
            Arc::new(FileMarkdownStore::new(
                PathBuf::from(root),
                max_files_per_session,
            ))
        }
        _ => Arc::new(InMemoryStore::default()),
    }
}

pub fn build_search_query(params: SearchQueryParams) -> Result<SearchQuery, String> {
    let user_id = params.user_id.unwrap_or_default();
    if user_id.trim().is_empty() {
        return Err("user_id is required".to_string());
    }
    Ok(SearchQuery {
        user_id,
        session_id: params.session_id.unwrap_or_default(),
        dag_id: params.dag_id.unwrap_or_else(|| "default".to_string()),
        q: params.q.unwrap_or_default(),
        limit: params.limit.unwrap_or(20),
    })
}

pub fn build_list_query(params: ListQueryParams) -> Result<SearchQuery, String> {
    let user_id = params.user_id.unwrap_or_default();
    let session_id = params.session_id.unwrap_or_default();
    if user_id.trim().is_empty() || session_id.trim().is_empty() {
        return Err("user_id and session_id are required".to_string());
    }
    Ok(SearchQuery {
        user_id,
        session_id,
        dag_id: params.dag_id.unwrap_or_else(|| "default".to_string()),
        q: String::new(),
        limit: params.limit.unwrap_or(3),
    })
}

pub fn filter_entries(entries: Vec<MemoryEntry>, query: &SearchQuery) -> Vec<MemoryEntry> {
    let normalized_q = query.q.to_lowercase();
    let mut out = Vec::new();
    for entry in entries.into_iter().rev() {
        if entry.user_id != query.user_id {
            continue;
        }
        if !query.session_id.is_empty() && entry.session_id != query.session_id {
            continue;
        }
        if !query.dag_id.is_empty() && entry.dag_id != query.dag_id {
            continue;
        }
        if !normalized_q.is_empty() {
            let hay = format!("{}\n{}", entry.summary.to_lowercase(), entry.raw_output.to_lowercase());
            if !hay.contains(&normalized_q) {
                continue;
            }
        }
        out.push(entry);
        if out.len() >= query.limit {
            break;
        }
    }
    out
}

pub fn fulltext_contains(entry: &MemoryEntry, q: &str) -> bool {
    let normalized = q.to_lowercase();
    if normalized.is_empty() {
        return true;
    }
    let hay = format!("{}\n{}", entry.summary.to_lowercase(), entry.raw_output.to_lowercase());
    hay.contains(&normalized)
}

pub fn is_recent_context(semantic_query: &str) -> bool {
    let q = semantic_query.to_lowercase();
    q.contains("recent") || q.contains("latest") || q.contains("last")
}

pub fn extract_hard_facts(summary: &str, raw_output: &str) -> Vec<String> {
    let text = format!("{}\n{}", summary, raw_output);
    let mut out = Vec::new();
    let ip_re = Regex::new(r"\b(?:\d{1,3}\.){3}\d{1,3}\b").expect("invalid regex");
    for cap in ip_re.find_iter(&text) {
        out.push(format!("IP={}", cap.as_str()));
    }
    let err_re = Regex::new(r"(?i)\b(error|timeout|failed|refused)\b").expect("invalid regex");
    if err_re.is_match(&text) {
        out.push("HAS_ERROR_SIGNAL=true".to_string());
    }
    dedup_keep_order(out)
}

pub fn dedup_keep_order(items: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut out = Vec::new();
    for item in items {
        if seen.insert(item.clone()) {
            out.push(item);
        }
    }
    out
}

pub fn apply_rolling_reduce(store: &Arc<dyn MemoryStore>, mut incoming: MemoryEntry) -> MemoryEntry {
    let threshold = env::var("POLARIS_ROLLING_TOKEN_THRESHOLD")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(2000);
    let mut query = SearchQuery {
        user_id: incoming.user_id.clone(),
        session_id: incoming.session_id.clone(),
        dag_id: incoming.dag_id.clone(),
        q: String::new(),
        limit: 20,
    };
    let recent = store.search(&query);
    let current_tokens = recent.iter().map(|e| e.summary.len() / 4).sum::<usize>() + incoming.summary.len() / 4;
    if current_tokens <= threshold {
        return incoming;
    }

    query.limit = env::var("POLARIS_ROLLING_WINDOW")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(5);
    let mut window = store.search(&query);
    window.insert(0, incoming.clone());

    let merged_summary = window
        .iter()
        .map(|e| e.summary.as_str())
        .collect::<Vec<_>>()
        .join(" | ");
    let merged_facts = dedup_keep_order(
        window
            .iter()
            .flat_map(|e| e.hard_facts.clone())
            .collect::<Vec<_>>(),
    );

    incoming.summary = format!("[rolling] {}", truncate(&merged_summary, 500));
    incoming.hard_facts = merged_facts;
    incoming
}

fn truncate(input: &str, max_len: usize) -> String {
    if input.len() <= max_len {
        return input.to_string();
    }
    input.chars().take(max_len).collect()
}

fn parse_markdown_entry(input: &str) -> Option<MemoryEntry> {
    let mut user_id = String::new();
    let mut session_id = String::new();
    let mut dag_id = "default".to_string();
    let mut task_id = String::new();
    let mut observed_at = 0_u64;
    let mut hard_facts = Vec::new();
    let mut rels = Vec::new();
    let mut in_header = false;
    let mut header_done = false;
    let mut summary_lines = Vec::new();
    let mut raw_lines = Vec::new();
    let mut in_raw_block = false;

    for line in input.lines() {
        if line.trim() == "---" && !in_header && !header_done {
            in_header = true;
            continue;
        }
        if line.trim() == "---" && in_header {
            in_header = false;
            header_done = true;
            continue;
        }
        if in_header {
            if let Some((k, v)) = line.split_once(':') {
                let key = k.trim();
                let value = v.trim().to_string();
                match key {
                    "user_id" => user_id = value,
                    "session_id" => session_id = value,
                    "dag_id" => dag_id = value,
                    "task_id" | "step_id" => task_id = value,
                    "observed_at" => observed_at = value.parse::<u64>().unwrap_or(0),
                    "hard_facts" => {
                        hard_facts = value
                            .split('|')
                            .map(|v| v.trim().to_string())
                            .filter(|v| !v.is_empty())
                            .collect()
                    }
                    "rels" => {
                        rels = value
                            .split('|')
                            .filter_map(|v| {
                                let items = v.trim().split('>').map(|s| s.trim().to_string()).collect::<Vec<_>>();
                                if items.len() == 3 {
                                    Some([items[0].clone(), items[1].clone(), items[2].clone()])
                                } else {
                                    None
                                }
                            })
                            .collect();
                    }
                    _ => {}
                }
            }
            continue;
        }
        if header_done {
            if line.trim() == "```raw" {
                in_raw_block = true;
                continue;
            }
            if line.trim() == "```" && in_raw_block {
                in_raw_block = false;
                continue;
            }
            if in_raw_block {
                raw_lines.push(line.to_string());
            } else {
                summary_lines.push(line.to_string());
            }
        }
    }

    if user_id.is_empty() || session_id.is_empty() || task_id.is_empty() {
        return None;
    }

    let summary = summary_lines.join("\n").trim().to_string();
    let raw_output = raw_lines.join("\n").trim().to_string();
    Some(MemoryEntry {
        user_id,
        session_id,
        dag_id,
        task_id,
        raw_output,
        summary,
        hard_facts,
        rels,
        observed_at,
    })
}

fn sanitize_path_component(value: &str) -> String {
    value
        .chars()
        .map(|ch| match ch {
            'a'..='z' | 'A'..='Z' | '0'..='9' | '-' | '_' => ch,
            _ => '_',
        })
        .collect()
}

fn walk_markdown_files(root: &Path, f: &mut dyn FnMut(&Path)) {
    let read_dir = match fs::read_dir(root) {
        Ok(v) => v,
        Err(_) => return,
    };
    for entry in read_dir.flatten() {
        let path = entry.path();
        if path.is_dir() {
            walk_markdown_files(&path, f);
            continue;
        }
        if path.extension().and_then(|v| v.to_str()) == Some("md") {
            f(&path);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{
        apply_rolling_reduce, dedup_keep_order, extract_hard_facts, filter_entries, InMemoryStore,
        MemoryStore, RocksDbStore, SearchQuery,
    };
    use crate::types::MemoryEntry;
    use std::fs;
    use std::sync::Arc;
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn filter_entries_should_scope_by_user_session_and_dag() {
        let entries = vec![
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d1".to_string(),
                task_id: "t1".to_string(),
                raw_output: String::new(),
                summary: "hello".to_string(),
                hard_facts: vec![],
                rels: vec![],
                observed_at: 1,
            },
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d2".to_string(),
                task_id: "t2".to_string(),
                raw_output: String::new(),
                summary: "hello".to_string(),
                hard_facts: vec![],
                rels: vec![],
                observed_at: 2,
            },
        ];
        let got = filter_entries(
            entries,
            &SearchQuery {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d1".to_string(),
                q: String::new(),
                limit: 10,
            },
        );
        assert_eq!(got.len(), 1);
        assert_eq!(got[0].task_id, "t1");
    }

    #[test]
    fn rolling_reduce_should_merge_hard_facts_when_threshold_hit() {
        let store = Arc::new(InMemoryStore::default()) as Arc<dyn MemoryStore>;
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t1".to_string(),
            raw_output: String::new(),
            summary: "a".repeat(9000),
            hard_facts: vec!["IP=1.1.1.1".to_string()],
            rels: vec![],
            observed_at: 1,
        });
        let got = apply_rolling_reduce(
            &store,
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d1".to_string(),
                task_id: "t2".to_string(),
                raw_output: String::new(),
                summary: "b".repeat(9000),
                hard_facts: vec!["HAS_ERROR_SIGNAL=true".to_string()],
                rels: vec![],
                observed_at: 2,
            },
        );
        assert!(got.summary.starts_with("[rolling]"));
        assert!(got.hard_facts.contains(&"IP=1.1.1.1".to_string()));
        assert!(got.hard_facts.contains(&"HAS_ERROR_SIGNAL=true".to_string()));
    }

    #[test]
    fn extract_and_dedup_hard_facts_should_work() {
        let facts = extract_hard_facts("timeout", "connect 10.0.0.1 failed");
        let merged = dedup_keep_order(vec![
            "A".to_string(),
            "A".to_string(),
            facts.first().cloned().unwrap_or_default(),
        ]);
        assert_eq!(merged.first().map(|v| v.as_str()), Some("A"));
    }

    #[test]
    fn rocksdb_store_should_ingest_and_search() {
        let root = format!(
            "/tmp/polaris-rocksdb-test-{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        );
        let store = RocksDbStore::open(&root).expect("open rocksdb");
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t1".to_string(),
            raw_output: "raw".to_string(),
            summary: "hello rocksdb".to_string(),
            hard_facts: vec![],
            rels: vec![],
            observed_at: 1,
        });
        let got = store.search(&SearchQuery {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            q: "rocksdb".to_string(),
            limit: 5,
        });
        assert_eq!(got.len(), 1);
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn rocksdb_prefix_search_should_scope_and_keep_recent_first() {
        let root = format!(
            "/tmp/polaris-rocksdb-prefix-test-{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        );
        let store = RocksDbStore::open(&root).expect("open rocksdb");
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t_old".to_string(),
            raw_output: String::new(),
            summary: "same query".to_string(),
            hard_facts: vec![],
            rels: vec![],
            observed_at: 10,
        });
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t_new".to_string(),
            raw_output: String::new(),
            summary: "same query".to_string(),
            hard_facts: vec![],
            rels: vec![],
            observed_at: 20,
        });
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s2".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t_other_session".to_string(),
            raw_output: String::new(),
            summary: "same query".to_string(),
            hard_facts: vec![],
            rels: vec![],
            observed_at: 30,
        });
        let got = store.search(&SearchQuery {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            q: "same".to_string(),
            limit: 10,
        });
        assert_eq!(got.len(), 2);
        assert_eq!(got[0].task_id, "t_new");
        assert_eq!(got[1].task_id, "t_old");
        let _ = fs::remove_dir_all(root);
    }
}

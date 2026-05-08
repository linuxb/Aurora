use axum::{
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
    Json, Router,
};
use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::collections::HashMap;
use std::env;
use std::fs;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone, Debug, Serialize, Deserialize)]
struct MemoryEntry {
    user_id: String,
    session_id: String,
    task_id: String,
    summary: String,
}

#[derive(Clone, Debug)]
struct SearchQuery {
    user_id: String,
    session_id: String,
    q: String,
    limit: usize,
}

trait MemoryStore: Send + Sync {
    fn ingest(&self, entry: MemoryEntry);
    fn list_all(&self) -> Vec<MemoryEntry>;
    fn search(&self, query: &SearchQuery) -> Vec<MemoryEntry>;
}

#[derive(Default)]
struct InMemoryStore {
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
        let entries = self.list_all();
        filter_entries(entries, query)
    }
}

struct FileMarkdownStore {
    root_dir: PathBuf,
    max_files_per_session: usize,
    write_guard: Mutex<()>,
}

impl FileMarkdownStore {
    fn new(root_dir: PathBuf, max_files_per_session: usize) -> Self {
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
            "---\nuser_id: {}\nsession_id: {}\ntask_id: {}\n---\n{}\n",
            entry.user_id, entry.session_id, entry.task_id, entry.summary
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

        files.sort_by(|a, b| {
            a.file_name()
                .and_then(|n| n.to_str())
                .cmp(&b.file_name().and_then(|n| n.to_str()))
        });

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
        let entries = self.read_all_entries();
        filter_entries(entries, query)
    }
}

#[derive(Clone)]
struct AppState {
    store: Arc<dyn MemoryStore>,
}

#[derive(Deserialize)]
struct IngestRequest {
    user_id: String,
    session_id: String,
    task_id: String,
    summary: String,
}

#[derive(Deserialize)]
struct SearchQueryParams {
    user_id: Option<String>,
    session_id: Option<String>,
    q: Option<String>,
    limit: Option<usize>,
}

#[derive(Serialize)]
struct EntriesResponse {
    count: usize,
    entries: Vec<MemoryEntry>,
}

#[derive(Clone, Debug, Serialize)]
struct GraphNode {
    id: String,
    label: String,
    node_type: String,
    weight: usize,
}

#[derive(Clone, Debug, Serialize)]
struct GraphEdge {
    source: String,
    target: String,
    relation: String,
    weight: usize,
}

#[derive(Serialize)]
struct GraphResponse {
    node_count: usize,
    edge_count: usize,
    nodes: Vec<GraphNode>,
    edges: Vec<GraphEdge>,
}

#[tokio::main]
async fn main() {
    let addr = env::var("POLARIS_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let socket_addr: SocketAddr = addr.parse().expect("invalid POLARIS_ADDR");

    let state = AppState {
        store: build_store_from_env(),
    };

    let app = Router::new()
        .route("/healthz", get(healthz))
        .route("/memory", get(list_memory))
        .route("/memory/search", get(search_memory))
        .route("/memory/graph/search", get(search_memory_graph))
        .route("/ingest", post(ingest_memory))
        .with_state(state);

    println!("polaris listening on {}", socket_addr);
    let listener = tokio::net::TcpListener::bind(socket_addr)
        .await
        .expect("failed to bind polaris address");
    axum::serve(listener, app).await.expect("polaris serve failed");
}

fn build_store_from_env() -> Arc<dyn MemoryStore> {
    let backend = env::var("POLARIS_MEMORY_BACKEND")
        .unwrap_or_else(|_| "memory".to_string())
        .to_lowercase();
    match backend.as_str() {
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

async fn healthz() -> impl IntoResponse {
    Json(json!({"service":"polaris","status":"ok"}))
}

async fn list_memory(State(state): State<AppState>) -> impl IntoResponse {
    let entries = state.store.list_all();
    Json(EntriesResponse {
        count: entries.len(),
        entries,
    })
}

async fn search_memory(
    State(state): State<AppState>,
    Query(params): Query<SearchQueryParams>,
) -> impl IntoResponse {
    let search = match build_search_query(params) {
        Ok(v) => v,
        Err(err) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"code":"invalid_argument","message":err})),
            )
                .into_response();
        }
    };

    let entries = state.store.search(&search);
    (
        StatusCode::OK,
        Json(json!({"count": entries.len(), "entries": entries})),
    )
        .into_response()
}

async fn search_memory_graph(
    State(state): State<AppState>,
    Query(params): Query<SearchQueryParams>,
) -> impl IntoResponse {
    let search = match build_search_query(params) {
        Ok(v) => v,
        Err(err) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"code":"invalid_argument","message":err})),
            )
                .into_response();
        }
    };

    let entries = state.store.search(&search);
    let (nodes, edges) = build_entity_relation_graph(entries);
    (
        StatusCode::OK,
        Json(GraphResponse {
            node_count: nodes.len(),
            edge_count: edges.len(),
            nodes,
            edges,
        }),
    )
        .into_response()
}

async fn ingest_memory(
    State(state): State<AppState>,
    Json(req): Json<IngestRequest>,
) -> impl IntoResponse {
    if req.user_id.trim().is_empty()
        || req.session_id.trim().is_empty()
        || req.task_id.trim().is_empty()
        || req.summary.trim().is_empty()
    {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({
                "code":"invalid_payload",
                "message":"expect JSON with user_id/session_id/task_id/summary"
            })),
        )
            .into_response();
    }

    let entry = MemoryEntry {
        user_id: req.user_id,
        session_id: req.session_id,
        task_id: req.task_id,
        summary: req.summary,
    };
    state.store.ingest(entry.clone());

    (
        StatusCode::OK,
        Json(json!({
            "status":"ok",
            "stored": {
                "user_id": entry.user_id,
                "session_id": entry.session_id,
                "task_id": entry.task_id
            }
        })),
    )
        .into_response()
}

fn build_search_query(params: SearchQueryParams) -> Result<SearchQuery, String> {
    let user_id = params.user_id.unwrap_or_default();
    if user_id.trim().is_empty() {
        return Err("user_id is required".to_string());
    }
    Ok(SearchQuery {
        user_id,
        session_id: params.session_id.unwrap_or_default(),
        q: params.q.unwrap_or_default(),
        limit: params.limit.unwrap_or(20),
    })
}

fn filter_entries(entries: Vec<MemoryEntry>, query: &SearchQuery) -> Vec<MemoryEntry> {
    let normalized_q = query.q.to_lowercase();
    let mut out = Vec::new();

    for entry in entries.into_iter().rev() {
        if entry.user_id != query.user_id {
            continue;
        }
        if !query.session_id.is_empty() && entry.session_id != query.session_id {
            continue;
        }
        if !normalized_q.is_empty() && !entry.summary.to_lowercase().contains(&normalized_q) {
            continue;
        }
        out.push(entry);
        if out.len() >= query.limit {
            break;
        }
    }
    out
}

fn extract_entities(summary: &str) -> Vec<String> {
    let token_re = Regex::new(r"[A-Za-z0-9_\-]{4,}").expect("invalid regex");
    let mut terms = Vec::new();
    for cap in token_re.find_iter(summary) {
        let token = cap.as_str().to_lowercase();
        if !terms.contains(&token) {
            terms.push(token);
        }
        if terms.len() >= 10 {
            break;
        }
    }
    terms
}

fn build_entity_relation_graph(entries: Vec<MemoryEntry>) -> (Vec<GraphNode>, Vec<GraphEdge>) {
    let mut node_weights: HashMap<String, usize> = HashMap::new();
    let mut edge_weights: HashMap<(String, String), usize> = HashMap::new();

    for entry in entries {
        let entities = extract_entities(&entry.summary);
        for entity in &entities {
            *node_weights.entry(entity.clone()).or_insert(0) += 1;
        }

        for i in 0..entities.len() {
            for j in (i + 1)..entities.len() {
                let a = entities[i].clone();
                let b = entities[j].clone();
                let (left, right) = if a <= b { (a, b) } else { (b, a) };
                *edge_weights.entry((left, right)).or_insert(0) += 1;
            }
        }
    }

    let nodes = node_weights
        .into_iter()
        .map(|(entity, weight)| GraphNode {
            id: entity.clone(),
            label: entity,
            node_type: "entity".to_string(),
            weight,
        })
        .collect::<Vec<_>>();

    let edges = edge_weights
        .into_iter()
        .map(|((source, target), weight)| GraphEdge {
            source,
            target,
            relation: "co_occurs".to_string(),
            weight,
        })
        .collect::<Vec<_>>();

    (nodes, edges)
}

fn parse_markdown_entry(input: &str) -> Option<MemoryEntry> {
    let mut user_id = String::new();
    let mut session_id = String::new();
    let mut task_id = String::new();
    let mut in_header = false;
    let mut header_done = false;
    let mut summary_lines: Vec<String> = Vec::new();

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
                    "task_id" => task_id = value,
                    _ => {}
                }
            }
            continue;
        }
        if header_done {
            summary_lines.push(line.to_string());
        }
    }

    if user_id.is_empty() || session_id.is_empty() || task_id.is_empty() {
        return None;
    }

    Some(MemoryEntry {
        user_id,
        session_id,
        task_id,
        summary: summary_lines.join("\n").trim().to_string(),
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

fn walk_markdown_files(root: &Path, cb: &mut dyn FnMut(&Path)) {
    let read = match fs::read_dir(root) {
        Ok(v) => v,
        Err(_) => return,
    };

    for entry in read.flatten() {
        let path = entry.path();
        if path.is_dir() {
            walk_markdown_files(&path, cb);
            continue;
        }
        if path.extension().and_then(|v| v.to_str()) == Some("md") {
            cb(&path);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_markdown_ok() {
        let raw = "---\nuser_id: u1\nsession_id: s1\ntask_id: t1\n---\npayment timeout recovered\n";
        let entry = parse_markdown_entry(raw).expect("expected markdown parse success");
        assert_eq!(entry.user_id, "u1");
        assert_eq!(entry.session_id, "s1");
        assert_eq!(entry.task_id, "t1");
    }

    #[test]
    fn in_memory_store_should_enforce_user_scope_and_limit() {
        let store = InMemoryStore::default();
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            task_id: "t1".to_string(),
            summary: "payment failed".to_string(),
        });
        store.ingest(MemoryEntry {
            user_id: "u2".to_string(),
            session_id: "s2".to_string(),
            task_id: "t2".to_string(),
            summary: "other user summary".to_string(),
        });
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            task_id: "t3".to_string(),
            summary: "payment recovered".to_string(),
        });

        let rows = store.search(&SearchQuery {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            q: "payment".to_string(),
            limit: 1,
        });
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].task_id, "t3");
    }

    #[test]
    fn file_markdown_store_should_rotate_old_entries_by_session() {
        let test_root = env::temp_dir().join(format!(
            "polaris_memory_rotate_test_{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        ));

        let store = FileMarkdownStore::new(test_root.clone(), 2);
        for idx in 0..4 {
            store.ingest(MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                task_id: format!("t{}", idx),
                summary: format!("summary {}", idx),
            });
        }

        let session_dir = test_root.join("u1").join("s1");
        let file_count = fs::read_dir(&session_dir)
            .ok()
            .map(|it| it.flatten().count())
            .unwrap_or(0);
        assert!(file_count <= 2);
        let _ = fs::remove_dir_all(test_root);
    }

    #[test]
    fn file_markdown_store_should_ignore_corrupted_markdown() {
        let test_root = env::temp_dir().join(format!(
            "polaris_memory_corrupt_test_{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        ));
        let store = FileMarkdownStore::new(test_root.clone(), 50);
        let user_session_dir = test_root.join("u1").join("s1");
        let _ = fs::create_dir_all(&user_session_dir);
        let _ = fs::write(user_session_dir.join("broken.md"), "broken content");

        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            task_id: "ok_task".to_string(),
            summary: "valid summary".to_string(),
        });

        let rows = store.search(&SearchQuery {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            q: "".to_string(),
            limit: 10,
        });
        assert!(rows.iter().any(|r| r.task_id == "ok_task"));
        let _ = fs::remove_dir_all(test_root);
    }

    #[test]
    fn build_entity_relation_graph_should_create_nodes_and_edges() {
        let entries = vec![
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                task_id: "t1".to_string(),
                summary: "payment timeout recovered".to_string(),
            },
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                task_id: "t2".to_string(),
                summary: "payment retried successfully".to_string(),
            },
        ];

        let (nodes, edges) = build_entity_relation_graph(entries);
        assert!(!nodes.is_empty());
        assert!(!edges.is_empty());
    }
}

use std::collections::HashMap;
use std::env;
use std::fs;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone, Debug)]
struct MemoryEntry {
    user_id: String,
    session_id: String,
    task_id: String,
    summary: String,
}

#[derive(Clone, Default)]
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
}

struct FileMarkdownStore {
    root_dir: PathBuf,
}

impl FileMarkdownStore {
    fn new(root_dir: PathBuf) -> Self {
        if let Err(err) = fs::create_dir_all(&root_dir) {
            eprintln!("create memory fs dir failed: {}", err);
        }
        Self { root_dir }
    }

    fn user_session_dir(&self, user_id: &str, session_id: &str) -> PathBuf {
        self.root_dir
            .join(sanitize_path_component(user_id))
            .join(sanitize_path_component(session_id))
    }

    fn write_entry(&self, entry: &MemoryEntry) {
        let dir = self.user_session_dir(&entry.user_id, &entry.session_id);
        if let Err(err) = fs::create_dir_all(&dir) {
            eprintln!("create memory dir failed: {}", err);
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
        if let Err(err) = fs::write(path, content) {
            eprintln!("write memory markdown failed: {}", err);
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
}

#[derive(Clone)]
struct AppState {
    store: Arc<dyn MemoryStore>,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            store: Arc::new(InMemoryStore::default()),
        }
    }
}

fn main() {
    let addr = env::var("POLARIS_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let listener = TcpListener::bind(&addr).expect("failed to bind polaris address");
    let state = AppState {
        store: build_store_from_env(),
    };

    println!("polaris listening on {}", addr);

    for stream in listener.incoming() {
        match stream {
            Ok(stream) => handle_connection(stream, state.clone()),
            Err(err) => eprintln!("accept error: {}", err),
        }
    }
}

fn build_store_from_env() -> Arc<dyn MemoryStore> {
    let backend = env::var("POLARIS_MEMORY_BACKEND")
        .unwrap_or_else(|_| "memory".to_string())
        .to_lowercase();
    match backend.as_str() {
        "file_md" => {
            let root = env::var("POLARIS_MEMORY_FS_DIR")
                .unwrap_or_else(|_| "/tmp/polaris-memory".to_string());
            Arc::new(FileMarkdownStore::new(PathBuf::from(root)))
        }
        _ => Arc::new(InMemoryStore::default()),
    }
}

fn handle_connection(mut stream: TcpStream, state: AppState) {
    let mut buffer = [0u8; 16 * 1024];
    let read_count = match stream.read(&mut buffer) {
        Ok(n) => n,
        Err(err) => {
            eprintln!("read error: {}", err);
            return;
        }
    };

    let request = String::from_utf8_lossy(&buffer[..read_count]);
    let (method, full_path) = parse_request_line(&request);
    let (path, query) = split_path_and_query(full_path);

    if method == "GET" && path == "/healthz" {
        respond_json(&mut stream, 200, r#"{"service":"polaris","status":"ok"}"#);
        return;
    }

    if method == "GET" && path == "/memory" {
        let body = rows_to_json(state.store.list_all());
        respond_json(&mut stream, 200, &body);
        return;
    }

    if method == "GET" && path == "/memory/search" {
        let params = parse_query_params(query);
        let search = SearchQuery {
            user_id: params.get("user_id").cloned().unwrap_or_default(),
            session_id: params.get("session_id").cloned().unwrap_or_default(),
            q: params.get("q").cloned().unwrap_or_default(),
            limit: params
                .get("limit")
                .and_then(|raw| raw.parse::<usize>().ok())
                .unwrap_or(20),
        };
        if search.user_id.is_empty() {
            respond_json(
                &mut stream,
                400,
                r#"{"code":"invalid_argument","message":"user_id is required"}"#,
            );
            return;
        }
        let rows = state.store.search(&search);
        respond_json(&mut stream, 200, &rows_to_json(rows));
        return;
    }

    if method == "POST" && path == "/ingest" {
        if let Some(body) = extract_body(&request)
            && let Some(entry) = parse_ingest_payload(body)
        {
            state.store.ingest(entry.clone());
            let msg = format!(
                "{{\"status\":\"ok\",\"stored\":{{\"user_id\":\"{}\",\"session_id\":\"{}\",\"task_id\":\"{}\"}}}}",
                escape_json(&entry.user_id),
                escape_json(&entry.session_id),
                escape_json(&entry.task_id)
            );
            respond_json(&mut stream, 200, &msg);
            return;
        }
        respond_json(
            &mut stream,
            400,
            r#"{"code":"invalid_payload","message":"expect JSON with user_id/session_id/task_id/summary"}"#,
        );
        return;
    }

    respond_json(
        &mut stream,
        404,
        r#"{"code":"not_found","message":"route not found"}"#,
    );
}

fn parse_request_line(req: &str) -> (&str, &str) {
    if let Some(line) = req.lines().next() {
        let mut parts = line.split_whitespace();
        if let (Some(method), Some(path)) = (parts.next(), parts.next()) {
            return (method, path);
        }
    }
    ("", "")
}

fn split_path_and_query(path: &str) -> (&str, &str) {
    if let Some(idx) = path.find('?') {
        (&path[..idx], &path[idx + 1..])
    } else {
        (path, "")
    }
}

fn parse_query_params(query: &str) -> HashMap<String, String> {
    let mut out = HashMap::new();
    for pair in query.split('&') {
        if pair.is_empty() {
            continue;
        }
        let mut parts = pair.splitn(2, '=');
        let key = parts.next().unwrap_or_default().trim();
        let value = parts.next().unwrap_or_default().trim().replace('+', " ");
        if !key.is_empty() {
            out.insert(key.to_string(), value);
        }
    }
    out
}

fn extract_body(req: &str) -> Option<&str> {
    req.find("\r\n\r\n").map(|idx| &req[idx + 4..])
}

fn parse_ingest_payload(body: &str) -> Option<MemoryEntry> {
    let user_id = extract_json_string(body, "user_id")?;
    let session_id = extract_json_string(body, "session_id")?;
    let task_id = extract_json_string(body, "task_id")?;
    let summary = extract_json_string(body, "summary")?;
    Some(MemoryEntry {
        user_id,
        session_id,
        task_id,
        summary,
    })
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
    let summary = summary_lines.join("\n").trim().to_string();
    Some(MemoryEntry {
        user_id,
        session_id,
        task_id,
        summary,
    })
}

fn extract_json_string(input: &str, key: &str) -> Option<String> {
    let pattern = format!("\"{}\"", key);
    let key_idx = input.find(&pattern)?;
    let rest = &input[key_idx + pattern.len()..];
    let colon_idx = rest.find(':')?;
    let mut value = rest[colon_idx + 1..].trim_start();
    if !value.starts_with('"') {
        return None;
    }
    value = &value[1..];

    let mut escaped = false;
    let mut out = String::new();
    for c in value.chars() {
        if escaped {
            out.push(c);
            escaped = false;
            continue;
        }
        if c == '\\' {
            escaped = true;
            continue;
        }
        if c == '"' {
            return Some(out);
        }
        out.push(c);
    }
    None
}

fn rows_to_json(entries: Vec<MemoryEntry>) -> String {
    let mut rows = Vec::with_capacity(entries.len());
    for entry in entries {
        rows.push(format!(
            "{{\"user_id\":\"{}\",\"session_id\":\"{}\",\"task_id\":\"{}\",\"summary\":\"{}\"}}",
            escape_json(&entry.user_id),
            escape_json(&entry.session_id),
            escape_json(&entry.task_id),
            escape_json(&entry.summary)
        ));
    }
    format!(
        "{{\"count\":{},\"entries\":[{}]}}",
        rows.len(),
        rows.join(",")
    )
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

fn escape_json(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
}

fn respond_json(stream: &mut TcpStream, status: u16, body: &str) {
    let status_text = match status {
        200 => "OK",
        400 => "Bad Request",
        404 => "Not Found",
        _ => "Internal Server Error",
    };
    let resp = format!(
        "HTTP/1.1 {} {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        status,
        status_text,
        body.len(),
        body
    );
    if let Err(err) = stream.write_all(resp.as_bytes()) {
        eprintln!("write error: {}", err);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ingest_ok() {
        let body = r#"{"user_id":"u1","session_id":"sess_1","task_id":"task_2","summary":"ok"}"#;
        let entry = parse_ingest_payload(body).expect("expected parse success");
        assert_eq!(entry.user_id, "u1");
        assert_eq!(entry.session_id, "sess_1");
        assert_eq!(entry.task_id, "task_2");
        assert_eq!(entry.summary, "ok");
    }

    #[test]
    fn parse_ingest_fail_without_user_id() {
        let body = r#"{"session_id":"sess_1","task_id":"task_2","summary":"ok"}"#;
        assert!(parse_ingest_payload(body).is_none());
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
        assert_eq!(rows[0].user_id, "u1");
    }

    #[test]
    fn file_markdown_store_should_persist_and_search() {
        let test_root = env::temp_dir().join(format!(
            "polaris_memory_test_{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        ));
        let store = FileMarkdownStore::new(test_root.clone());
        store.ingest(MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            task_id: "t1".to_string(),
            summary: "alpha memory".to_string(),
        });
        store.ingest(MemoryEntry {
            user_id: "u2".to_string(),
            session_id: "s2".to_string(),
            task_id: "t2".to_string(),
            summary: "beta memory".to_string(),
        });
        let rows = store.search(&SearchQuery {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            q: "alpha".to_string(),
            limit: 10,
        });
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].task_id, "t1");
        let _ = fs::remove_dir_all(test_root);
    }
}

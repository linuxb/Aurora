use std::collections::HashMap;
use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};

#[derive(Clone, Debug)]
struct MemoryEntry {
    user_id: String,
    session_id: String,
    task_id: String,
    summary: String,
}

#[derive(Clone, Default)]
struct AppState {
    entries: Arc<Mutex<Vec<MemoryEntry>>>,
}

#[derive(Default)]
struct SearchQuery {
    user_id: String,
    session_id: String,
    q: String,
    limit: usize,
}

fn main() {
    let addr = env::var("POLARIS_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let listener = TcpListener::bind(&addr).expect("failed to bind polaris address");
    let state = AppState::default();

    println!("polaris listening on {}", addr);

    for stream in listener.incoming() {
        match stream {
            Ok(stream) => handle_connection(stream, state.clone()),
            Err(err) => eprintln!("accept error: {}", err),
        }
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
        let body = dump_memory_json(&state);
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
        let rows = search_entries(&state, &search);
        respond_json(&mut stream, 200, &rows_to_json(rows));
        return;
    }

    if method == "POST" && path == "/ingest" {
        if let Some(body) = extract_body(&request) {
            if let Some(entry) = parse_ingest_payload(body) {
                if let Ok(mut entries) = state.entries.lock() {
                    entries.push(entry.clone());
                }
                let msg = format!(
                    "{{\"status\":\"ok\",\"stored\":{{\"user_id\":\"{}\",\"session_id\":\"{}\",\"task_id\":\"{}\"}}}}",
                    escape_json(&entry.user_id),
                    escape_json(&entry.session_id),
                    escape_json(&entry.task_id)
                );
                respond_json(&mut stream, 200, &msg);
                return;
            }
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

fn search_entries(state: &AppState, query: &SearchQuery) -> Vec<MemoryEntry> {
    let entries = state
        .entries
        .lock()
        .map(|guard| guard.clone())
        .unwrap_or_default();

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

fn dump_memory_json(state: &AppState) -> String {
    let entries = state
        .entries
        .lock()
        .map(|guard| guard.clone())
        .unwrap_or_default();
    rows_to_json(entries)
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
    fn search_entries_should_enforce_user_scope_and_limit() {
        let state = AppState::default();
        if let Ok(mut guard) = state.entries.lock() {
            guard.push(MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                task_id: "t1".to_string(),
                summary: "payment failed".to_string(),
            });
            guard.push(MemoryEntry {
                user_id: "u2".to_string(),
                session_id: "s2".to_string(),
                task_id: "t2".to_string(),
                summary: "other user summary".to_string(),
            });
            guard.push(MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                task_id: "t3".to_string(),
                summary: "payment recovered".to_string(),
            });
        }
        let rows = search_entries(
            &state,
            &SearchQuery {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                q: "payment".to_string(),
                limit: 1,
            },
        );
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].task_id, "t3");
        assert_eq!(rows[0].user_id, "u1");
    }
}

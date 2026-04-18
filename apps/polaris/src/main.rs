use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};

#[derive(Clone, Debug)]
struct MemoryEntry {
    session_id: String,
    task_id: String,
    summary: String,
}

#[derive(Clone, Default)]
struct AppState {
    entries: Arc<Mutex<Vec<MemoryEntry>>>,
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
    let (method, path) = parse_request_line(&request);

    if method == "GET" && path == "/healthz" {
        respond_json(&mut stream, 200, r#"{"service":"polaris","status":"ok"}"#);
        return;
    }

    if method == "GET" && path == "/memory" {
        let body = dump_memory_json(&state);
        respond_json(&mut stream, 200, &body);
        return;
    }

    if method == "POST" && path == "/ingest" {
        if let Some(body) = extract_body(&request) {
            if let Some(entry) = parse_ingest_payload(body) {
                if let Ok(mut entries) = state.entries.lock() {
                    entries.push(entry.clone());
                }
                let msg = format!(
                    "{{\"status\":\"ok\",\"stored\":{{\"session_id\":\"{}\",\"task_id\":\"{}\"}}}}",
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
            r#"{"code":"invalid_payload","message":"expect JSON with session_id/task_id/summary"}"#,
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

fn extract_body(req: &str) -> Option<&str> {
    req.find("\r\n\r\n").map(|idx| &req[idx + 4..])
}

fn parse_ingest_payload(body: &str) -> Option<MemoryEntry> {
    let session_id = extract_json_string(body, "session_id")?;
    let task_id = extract_json_string(body, "task_id")?;
    let summary = extract_json_string(body, "summary")?;

    Some(MemoryEntry {
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

fn dump_memory_json(state: &AppState) -> String {
    let entries = state
        .entries
        .lock()
        .map(|guard| guard.clone())
        .unwrap_or_default();

    let mut rows = Vec::with_capacity(entries.len());
    for entry in entries {
        rows.push(format!(
            "{{\"session_id\":\"{}\",\"task_id\":\"{}\",\"summary\":\"{}\"}}",
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
        let body = r#"{"session_id":"sess_1","task_id":"task_2","summary":"ok"}"#;
        let entry = parse_ingest_payload(body).expect("expected parse success");
        assert_eq!(entry.session_id, "sess_1");
        assert_eq!(entry.task_id, "task_2");
        assert_eq!(entry.summary, "ok");
    }

    #[test]
    fn parse_ingest_fail_without_summary() {
        let body = r#"{"session_id":"sess_1","task_id":"task_2"}"#;
        assert!(parse_ingest_payload(body).is_none());
    }
}

use std::collections::HashMap;
use std::env;
use std::fs::OpenOptions;
use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

#[cfg(feature = "memgraph_bolt")]
use neo4rs::{Graph, query};
#[cfg(feature = "memgraph_bolt")]
use tokio::sync::Mutex as AsyncMutex;
#[cfg(feature = "memgraph_bolt")]
use tokio::time::{Duration, sleep, timeout};

use crate::model::types::{GraphEdge, GraphNode};

pub trait GraphStore: Send + Sync {
    fn upsert_graph(
        &self,
        user_id: &str,
        session_id: &str,
        dag_id: &str,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
    );
}

pub struct NoopGraphStore;

impl GraphStore for NoopGraphStore {
    fn upsert_graph(
        &self,
        _user_id: &str,
        _session_id: &str,
        _dag_id: &str,
        _nodes: &[GraphNode],
        _edges: &[GraphEdge],
    ) {
    }
}

#[derive(Default)]
pub struct InMemoryGraphStore {
    inner: Mutex<HashMap<String, (Vec<GraphNode>, Vec<GraphEdge>)>>,
}

impl GraphStore for InMemoryGraphStore {
    fn upsert_graph(
        &self,
        user_id: &str,
        session_id: &str,
        dag_id: &str,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
    ) {
        let key = format!("{}:{}:{}", user_id, session_id, dag_id);
        if let Ok(mut guard) = self.inner.lock() {
            guard.insert(key, (nodes.to_vec(), edges.to_vec()));
        }
    }
}

pub struct MemgraphStubStore {
    log_path: String,
}

impl MemgraphStubStore {
    fn new(log_path: String) -> Self {
        Self { log_path }
    }
}

impl GraphStore for MemgraphStubStore {
    fn upsert_graph(
        &self,
        user_id: &str,
        session_id: &str,
        dag_id: &str,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
    ) {
        let observed_at = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);
        let mut statements = Vec::new();
        for node in nodes {
            statements.push(render_memgraph_node_cypher(
                user_id,
                session_id,
                dag_id,
                observed_at,
                node,
            ));
        }
        for edge in edges {
            statements.push(render_memgraph_edge_cypher(
                user_id,
                session_id,
                dag_id,
                observed_at,
                edge,
            ));
        }
        append_lines(&self.log_path, &statements);
    }
}

#[cfg(feature = "memgraph_bolt")]
pub struct MemgraphBoltStore {
    uri: String,
    user: String,
    pass: String,
    graph: Arc<AsyncMutex<Option<Arc<Graph>>>>,
    retries: usize,
    timeout_ms: u64,
    retry_backoff_ms: u64,
}

#[cfg(feature = "memgraph_bolt")]
impl MemgraphBoltStore {
    fn new(uri: String, user: String, pass: String) -> Self {
        Self {
            uri,
            user,
            pass,
            graph: Arc::new(AsyncMutex::new(None)),
            retries: parse_positive_usize_env("MEM3_MEMGRAPH_RETRIES", 2),
            timeout_ms: parse_positive_u64_env("MEM3_MEMGRAPH_TIMEOUT_MS", 1500),
            retry_backoff_ms: parse_positive_u64_env("MEM3_MEMGRAPH_RETRY_BACKOFF_MS", 100),
        }
    }

    async fn get_or_connect(&self) -> Option<Arc<Graph>> {
        {
            let guard = self.graph.lock().await;
            if let Some(graph) = guard.as_ref() {
                return Some(graph.clone());
            }
        }
        let connected =
            match Graph::new(self.uri.clone(), self.user.clone(), self.pass.clone()).await {
                Ok(g) => Arc::new(g),
                Err(_) => return None,
            };
        let mut guard = self.graph.lock().await;
        *guard = Some(connected.clone());
        Some(connected)
    }

    async fn reset_connection(&self) {
        let mut guard = self.graph.lock().await;
        *guard = None;
    }

    async fn write_once(
        &self,
        user_id: String,
        session_id: String,
        dag_id: String,
        nodes: Vec<GraphNode>,
        edges: Vec<GraphEdge>,
        observed_at: i64,
    ) -> bool {
        let graph = match self.get_or_connect().await {
            Some(g) => g,
            None => return false,
        };
        for node in nodes {
            let q = query(
                "MERGE (e:Entity {id: $id, user_id: $user_id}) \
                 SET e.label = $label, e.type = $type, e.session_id = $session_id, e.dag_id = $dag_id, e.observed_at = $observed_at",
            )
            .param("id", node.id)
            .param("user_id", user_id.clone())
            .param("label", node.label)
            .param("type", node.node_type)
            .param("session_id", session_id.clone())
            .param("dag_id", dag_id.clone())
            .param("observed_at", observed_at);
            if graph.run(q).await.is_err() {
                return false;
            }
        }
        for edge in edges {
            let q = query(
                "MATCH (a:Entity {id: $src, user_id: $user_id}), (b:Entity {id: $dst, user_id: $user_id}) \
                 MERGE (a)-[r:RELATED {relation: $relation, user_id: $user_id, session_id: $session_id, dag_id: $dag_id}]->(b) \
                 SET r.weight = $weight, r.observed_at = $observed_at",
            )
            .param("src", edge.source)
            .param("dst", edge.target)
            .param("relation", edge.relation)
            .param("user_id", user_id.clone())
            .param("session_id", session_id.clone())
            .param("dag_id", dag_id.clone())
            .param("weight", edge.weight as i64)
            .param("observed_at", observed_at);
            if graph.run(q).await.is_err() {
                return false;
            }
        }
        true
    }
}

#[cfg(feature = "memgraph_bolt")]
impl GraphStore for MemgraphBoltStore {
    fn upsert_graph(
        &self,
        user_id: &str,
        session_id: &str,
        dag_id: &str,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
    ) {
        let user_id = user_id.to_string();
        let session_id = session_id.to_string();
        let dag_id = dag_id.to_string();
        let nodes = nodes.to_vec();
        let edges = edges.to_vec();
        let this = MemgraphBoltStore {
            uri: self.uri.clone(),
            user: self.user.clone(),
            pass: self.pass.clone(),
            graph: self.graph.clone(),
            retries: self.retries,
            timeout_ms: self.timeout_ms,
            retry_backoff_ms: self.retry_backoff_ms,
        };
        let observed_at = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0);

        tokio::spawn(async move {
            let max_attempts = this.retries + 1;
            for attempt in 0..max_attempts {
                let write_future = this.write_once(
                    user_id.clone(),
                    session_id.clone(),
                    dag_id.clone(),
                    nodes.clone(),
                    edges.clone(),
                    observed_at,
                );
                let ok = match timeout(Duration::from_millis(this.timeout_ms), write_future).await {
                    Ok(v) => v,
                    Err(_) => false,
                };
                if ok {
                    return;
                }
                this.reset_connection().await;
                if attempt + 1 < max_attempts {
                    sleep(Duration::from_millis(this.retry_backoff_ms)).await;
                }
            }
        });
    }
}

pub fn build_graph_store_from_env() -> Arc<dyn GraphStore> {
    let backend = env::var("MEM3_GRAPH_BACKEND")
        .unwrap_or_else(|_| "noop".to_string())
        .to_lowercase();
    match backend.as_str() {
        "in_memory" => Arc::new(InMemoryGraphStore::default()),
        "memgraph_stub" => Arc::new(MemgraphStubStore::new(
            env::var("MEM3_MEMGRAPH_STUB_LOG")
                .unwrap_or_else(|_| "/tmp/mem3-memgraph.cypher.log".to_string()),
        )),
        #[cfg(feature = "memgraph_bolt")]
        "memgraph_bolt" => Arc::new(MemgraphBoltStore::new(
            env::var("MEM3_MEMGRAPH_URI").unwrap_or_else(|_| "127.0.0.1:7687".to_string()),
            env::var("MEM3_MEMGRAPH_USER").unwrap_or_else(|_| "neo4j".to_string()),
            env::var("MEM3_MEMGRAPH_PASS").unwrap_or_else(|_| "neo4j".to_string()),
        )),
        _ => Arc::new(NoopGraphStore),
    }
}

fn append_lines(path: &str, lines: &[String]) {
    if lines.is_empty() {
        return;
    }
    let mut file = match OpenOptions::new().create(true).append(true).open(path) {
        Ok(v) => v,
        Err(_) => return,
    };
    for line in lines {
        let _ = writeln!(file, "{line}");
    }
}

fn parse_positive_usize_env(key: &str, fallback: usize) -> usize {
    match env::var(key).ok().and_then(|v| v.parse::<usize>().ok()) {
        Some(v) if v > 0 => v,
        _ => fallback,
    }
}

fn parse_positive_u64_env(key: &str, fallback: u64) -> u64 {
    match env::var(key).ok().and_then(|v| v.parse::<u64>().ok()) {
        Some(v) if v > 0 => v,
        _ => fallback,
    }
}

fn escape_cypher(value: &str) -> String {
    value.replace('\\', "\\\\").replace('\'', "\\'")
}

fn render_memgraph_node_cypher(
    user_id: &str,
    session_id: &str,
    dag_id: &str,
    observed_at: u64,
    node: &GraphNode,
) -> String {
    format!(
        "MERGE (e:Entity {{id:'{}', user_id:'{}'}}) SET e.label='{}', e.type='{}', e.session_id='{}', e.dag_id='{}', e.observed_at={};",
        escape_cypher(&node.id),
        escape_cypher(user_id),
        escape_cypher(&node.label),
        escape_cypher(&node.node_type),
        escape_cypher(session_id),
        escape_cypher(dag_id),
        observed_at
    )
}

fn render_memgraph_edge_cypher(
    user_id: &str,
    session_id: &str,
    dag_id: &str,
    observed_at: u64,
    edge: &GraphEdge,
) -> String {
    format!(
        "MATCH (a:Entity {{id:'{}', user_id:'{}'}}), (b:Entity {{id:'{}', user_id:'{}'}}) MERGE (a)-[r:RELATED {{relation:'{}', user_id:'{}', session_id:'{}', dag_id:'{}'}}]->(b) SET r.weight={}, r.observed_at={};",
        escape_cypher(&edge.source),
        escape_cypher(user_id),
        escape_cypher(&edge.target),
        escape_cypher(user_id),
        escape_cypher(&edge.relation),
        escape_cypher(user_id),
        escape_cypher(session_id),
        escape_cypher(dag_id),
        edge.weight,
        observed_at
    )
}

#[cfg(test)]
mod tests {
    use super::{
        MemgraphStubStore, append_lines, parse_positive_u64_env, parse_positive_usize_env,
        render_memgraph_edge_cypher, render_memgraph_node_cypher,
    };
    use crate::graph::store::GraphStore;
    use crate::model::types::{GraphEdge, GraphNode};
    use std::fs;
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn render_memgraph_cypher_should_include_user_scope() {
        let node = GraphNode {
            id: "svc_a".to_string(),
            label: "ServiceA".to_string(),
            node_type: "system".to_string(),
            weight: 1,
        };
        let edge = GraphEdge {
            source: "svc_a".to_string(),
            target: "svc_b".to_string(),
            relation: "calls".to_string(),
            weight: 2,
        };
        let n = render_memgraph_node_cypher("u1", "s1", "d1", 123, &node);
        let e = render_memgraph_edge_cypher("u1", "s1", "d1", 123, &edge);
        assert!(n.contains("user_id:'u1'"));
        assert!(e.contains("relation:'calls'"));
        assert!(e.contains("session_id:'s1'"));
    }

    #[test]
    fn memgraph_stub_should_write_cypher_log() {
        let path = format!(
            "/tmp/mem3-memgraph-stub-{}.log",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        );
        let store = MemgraphStubStore::new(path.clone());
        store.upsert_graph(
            "u1",
            "s1",
            "d1",
            &[GraphNode {
                id: "svc_a".to_string(),
                label: "ServiceA".to_string(),
                node_type: "system".to_string(),
                weight: 1,
            }],
            &[GraphEdge {
                source: "svc_a".to_string(),
                target: "svc_b".to_string(),
                relation: "calls".to_string(),
                weight: 1,
            }],
        );
        let content = fs::read_to_string(&path).unwrap_or_default();
        assert!(content.contains("MERGE (e:Entity"));
        assert!(content.contains("MERGE (a)-[r:RELATED"));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn append_lines_should_append_to_file() {
        let path = format!(
            "/tmp/mem3-append-lines-{}.log",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        );
        append_lines(&path, &["a".to_string(), "b".to_string()]);
        let content = fs::read_to_string(&path).unwrap_or_default();
        assert!(content.contains("a"));
        assert!(content.contains("b"));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn parse_positive_env_should_fallback_for_invalid_values() {
        assert_eq!(parse_positive_usize_env("MEM3_TEST_NO_SUCH_USIZE", 3), 3);
        assert_eq!(parse_positive_u64_env("MEM3_TEST_NO_SUCH_U64", 5), 5);
    }
}

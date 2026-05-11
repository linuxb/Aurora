use std::collections::HashMap;
use std::env;
use std::sync::{Arc, Mutex};

use crate::types::{GraphEdge, GraphNode};

pub trait GraphStore: Send + Sync {
    fn upsert_graph(&self, user_id: &str, session_id: &str, dag_id: &str, nodes: &[GraphNode], edges: &[GraphEdge]);
}

pub struct NoopGraphStore;

impl GraphStore for NoopGraphStore {
    fn upsert_graph(&self, _user_id: &str, _session_id: &str, _dag_id: &str, _nodes: &[GraphNode], _edges: &[GraphEdge]) {}
}

#[derive(Default)]
pub struct InMemoryGraphStore {
    inner: Mutex<HashMap<String, (Vec<GraphNode>, Vec<GraphEdge>)>>,
}

impl GraphStore for InMemoryGraphStore {
    fn upsert_graph(&self, user_id: &str, session_id: &str, dag_id: &str, nodes: &[GraphNode], edges: &[GraphEdge]) {
        let key = format!("{}:{}:{}", user_id, session_id, dag_id);
        if let Ok(mut guard) = self.inner.lock() {
            guard.insert(key, (nodes.to_vec(), edges.to_vec()));
        }
    }
}

pub fn build_graph_store_from_env() -> Arc<dyn GraphStore> {
    let backend = env::var("POLARIS_GRAPH_BACKEND")
        .unwrap_or_else(|_| "noop".to_string())
        .to_lowercase();
    match backend.as_str() {
        "in_memory" => Arc::new(InMemoryGraphStore::default()),
        _ => Arc::new(NoopGraphStore),
    }
}

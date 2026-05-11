use serde::{Deserialize, Serialize};
use std::sync::Arc;

use crate::graph_store::GraphStore;
use crate::store::MemoryStore;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct MemoryEntry {
    pub user_id: String,
    pub session_id: String,
    pub dag_id: String,
    pub task_id: String,
    pub raw_output: String,
    pub summary: String,
    pub hard_facts: Vec<String>,
    pub rels: Vec<[String; 3]>,
    pub observed_at: u64,
}

#[derive(Clone, Debug)]
pub struct SearchQuery {
    pub user_id: String,
    pub session_id: String,
    pub dag_id: String,
    pub q: String,
    pub limit: usize,
}

#[derive(Clone)]
pub struct AppState {
    pub store: Arc<dyn MemoryStore>,
    pub graph_store: Arc<dyn GraphStore>,
}

#[derive(Deserialize)]
pub struct IngestRequest {
    pub user_id: String,
    pub session_id: String,
    pub dag_id: Option<String>,
    // step_id is an API alias of arqo task_id.
    pub step_id: Option<String>,
    pub task_id: Option<String>,
    pub raw_output: Option<String>,
    pub summary: Option<String>,
    pub hard_facts: Option<Vec<String>>,
    pub rels: Option<Vec<[String; 3]>>,
}

#[derive(Deserialize)]
pub struct SearchQueryParams {
    pub user_id: Option<String>,
    pub session_id: Option<String>,
    pub dag_id: Option<String>,
    pub q: Option<String>,
    pub limit: Option<usize>,
}

#[derive(Deserialize)]
pub struct ListQueryParams {
    pub user_id: Option<String>,
    pub session_id: Option<String>,
    pub dag_id: Option<String>,
    pub limit: Option<usize>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct MemHint {
    pub strategy: Option<String>,
    pub target_step_id: Option<String>,
    pub semantic_query: Option<String>,
}

#[derive(Deserialize)]
pub struct MemHintSearchRequest {
    pub user_id: String,
    pub session_id: String,
    pub dag_id: Option<String>,
    pub limit: Option<usize>,
    pub mem_hint: Option<MemHint>,
}

#[derive(Serialize)]
pub struct EntriesResponse {
    pub count: usize,
    pub entries: Vec<MemoryEntry>,
}

#[derive(Clone, Debug, Serialize)]
pub struct GraphNode {
    pub id: String,
    pub label: String,
    pub node_type: String,
    pub weight: usize,
}

#[derive(Clone, Debug, Serialize)]
pub struct GraphEdge {
    pub source: String,
    pub target: String,
    pub relation: String,
    pub weight: usize,
}

#[derive(Serialize)]
pub struct GraphResponse {
    pub node_count: usize,
    pub edge_count: usize,
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

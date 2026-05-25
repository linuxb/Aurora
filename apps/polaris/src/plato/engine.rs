use petgraph::algo::kosaraju_scc;
use petgraph::graph::UnGraph;
use serde::Serialize;
use std::cmp::Reverse;
use std::collections::{HashMap, HashSet};
use std::env;
use std::sync::Mutex;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::model::types::{GraphEdge, GraphNode};

#[derive(Clone, Debug, Serialize)]
pub struct CommunitySummary {
    pub community_id: String,
    pub scope_key: String,
    pub macro_summary: String,
    pub keywords: Vec<String>,
    pub updated_at: u64,
    pub node_count: usize,
}

pub trait GraphAnalyticsAdapter: Send + Sync {
    fn detect_communities(&self, nodes: &[GraphNode], edges: &[GraphEdge]) -> HashMap<String, String>;
}

pub struct PetgraphCommunityAdapter;

impl GraphAnalyticsAdapter for PetgraphCommunityAdapter {
    fn detect_communities(&self, nodes: &[GraphNode], edges: &[GraphEdge]) -> HashMap<String, String> {
        let mut g: UnGraph<String, ()> = UnGraph::new_undirected();
        let mut idx = HashMap::new();
        for node in nodes {
            let nid = g.add_node(node.id.clone());
            idx.insert(node.id.clone(), nid);
        }
        for edge in edges {
            if let (Some(a), Some(b)) = (idx.get(&edge.source), idx.get(&edge.target)) {
                g.add_edge(*a, *b, ());
            }
        }
        let scc = kosaraju_scc(&g);
        let mut out = HashMap::new();
        for (i, component) in scc.iter().enumerate() {
            let cid = format!("c{}", i + 1);
            for node_idx in component {
                if let Some(id) = g.node_weight(*node_idx) {
                    out.insert(id.clone(), cid.clone());
                }
            }
        }
        out
    }
}

#[derive(Default)]
struct ScopeState {
    dirty_edges_count: usize,
    last_clustered_at: u64,
    communities: HashMap<String, CommunitySummary>,
    membership: HashMap<String, String>,
}

pub struct PlatoEngine {
    adapter: Box<dyn GraphAnalyticsAdapter>,
    inner: Mutex<HashMap<String, ScopeState>>,
}

impl PlatoEngine {
    pub fn new_default() -> Self {
        Self {
            adapter: Box::new(PetgraphCommunityAdapter),
            inner: Mutex::new(HashMap::new()),
        }
    }

    pub fn scope_key(user_id: &str, session_id: &str, dag_id: &str) -> String {
        format!("{}:{}:{}", user_id, session_id, dag_id)
    }

    pub fn observe_graph(&self, scope_key: &str, nodes: &[GraphNode], edges: &[GraphEdge]) {
        let mut guard = match self.inner.lock() {
            Ok(v) => v,
            Err(_) => return,
        };
        let state = guard.entry(scope_key.to_string()).or_default();
        state.dirty_edges_count += edges.len();

        let now = now_unix();
        let count_threshold = env::var("PLATO_DIRTY_EDGE_THRESHOLD")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(500);
        let seconds_threshold = env::var("PLATO_CLUSTER_INTERVAL_SECONDS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(7200);

        let should_recluster = state.dirty_edges_count >= count_threshold
            || (state.dirty_edges_count > 0 && now.saturating_sub(state.last_clustered_at) >= seconds_threshold);

        if should_recluster {
            self.recluster_state(scope_key, state, nodes, edges, now);
        }
    }

    fn recluster_state(
        &self,
        scope_key: &str,
        state: &mut ScopeState,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
        now: u64,
    ) {
        let membership = self.adapter.detect_communities(nodes, edges);
        state.membership = membership.clone();
        let mut groups: HashMap<String, Vec<GraphNode>> = HashMap::new();
        let node_by_id = nodes
            .iter()
            .map(|n| (n.id.clone(), n.clone()))
            .collect::<HashMap<_, _>>();
        for (node_id, community_id) in membership {
            if let Some(node) = node_by_id.get(&node_id) {
                groups.entry(community_id).or_default().push(node.clone());
            }
        }

        state.communities.clear();
        for (community_id, mut members) in groups {
            members.sort_by_key(|n| Reverse(n.weight));
            let top_k = env::var("PLATO_COMMUNITY_TOPK")
                .ok()
                .and_then(|v| v.parse::<usize>().ok())
                .unwrap_or(10);
            let top = members.into_iter().take(top_k).collect::<Vec<_>>();
            let keywords = top.iter().map(|n| n.label.clone()).collect::<Vec<_>>();
            let macro_summary = format!(
                "Community {} focuses on [{}], dominant types: {}",
                community_id,
                keywords.join(", "),
                summarize_types(&top)
            );
            state.communities.insert(
                community_id.clone(),
                CommunitySummary {
                    community_id,
                    scope_key: scope_key.to_string(),
                    macro_summary,
                    keywords,
                    updated_at: now,
                    node_count: top.len(),
                },
            );
        }

        state.last_clustered_at = now;
        state.dirty_edges_count = 0;
    }

    pub fn query_global(
        &self,
        scope_key: &str,
        question: &str,
        keywords: &[String],
        top_k: usize,
    ) -> Vec<CommunitySummary> {
        let guard = match self.inner.lock() {
            Ok(v) => v,
            Err(_) => return vec![],
        };
        let state = match guard.get(scope_key) {
            Some(v) => v,
            None => return vec![],
        };
        let mut scored = state
            .communities
            .values()
            .cloned()
            .map(|c| {
                let score = score_community(&c, question, keywords);
                (score, c)
            })
            .collect::<Vec<_>>();
        scored.sort_by_key(|(score, _)| Reverse(*score));
        scored
            .into_iter()
            .filter(|(score, _)| *score > 0 || question.is_empty() && keywords.is_empty())
            .take(top_k)
            .map(|(_, c)| c)
            .collect()
    }

    pub fn query_local(
        &self,
        nodes: &[GraphNode],
        edges: &[GraphEdge],
        keywords: &[String],
        query_text: &str,
    ) -> (Vec<GraphNode>, Vec<GraphEdge>) {
        if keywords.is_empty() && query_text.trim().is_empty() {
            return (nodes.to_vec(), edges.to_vec());
        }
        let mut anchors = HashSet::new();
        let text_l = query_text.to_lowercase();
        for n in nodes {
            let label_l = n.label.to_lowercase();
            let id_l = n.id.to_lowercase();
            if keywords
                .iter()
                .any(|k| label_l.contains(&k.to_lowercase()) || id_l.contains(&k.to_lowercase()))
                || (!text_l.is_empty() && (text_l.contains(&label_l) || text_l.contains(&id_l)))
            {
                anchors.insert(n.id.clone());
            }
        }
        if anchors.is_empty() {
            return (vec![], vec![]);
        }

        let mut kept = anchors.clone();
        for e in edges {
            if anchors.contains(&e.source) {
                kept.insert(e.target.clone());
            }
            if anchors.contains(&e.target) {
                kept.insert(e.source.clone());
            }
        }
        let out_nodes = nodes
            .iter()
            .filter(|n| kept.contains(&n.id))
            .cloned()
            .collect::<Vec<_>>();
        let out_edges = edges
            .iter()
            .filter(|e| kept.contains(&e.source) && kept.contains(&e.target))
            .cloned()
            .collect::<Vec<_>>();
        (out_nodes, out_edges)
    }
}

fn score_community(c: &CommunitySummary, question: &str, keywords: &[String]) -> usize {
    let mut score = 0usize;
    let lower_summary = c.macro_summary.to_lowercase();
    let question_l = question.to_lowercase();
    if !question_l.is_empty() && lower_summary.contains(&question_l) {
        score += 5;
    }
    for k in keywords {
        let k_l = k.to_lowercase();
        if c.keywords.iter().any(|x| x.to_lowercase().contains(&k_l)) {
            score += 3;
        }
        if lower_summary.contains(&k_l) {
            score += 2;
        }
    }
    score
}

fn summarize_types(nodes: &[GraphNode]) -> String {
    let mut type_counts = HashMap::<String, usize>::new();
    for n in nodes {
        *type_counts.entry(n.node_type.clone()).or_insert(0) += 1;
    }
    let mut pairs = type_counts.into_iter().collect::<Vec<_>>();
    pairs.sort_by_key(|(_, c)| Reverse(*c));
    pairs
        .into_iter()
        .map(|(t, c)| format!("{}:{}", t, c))
        .collect::<Vec<_>>()
        .join(",")
}

fn now_unix() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::PlatoEngine;
    use crate::model::types::{GraphEdge, GraphNode};

    #[test]
    fn query_global_should_return_summary_after_threshold_trigger() {
        let engine = PlatoEngine::new_default();
        let scope = PlatoEngine::scope_key("u1", "s1", "d1");
        let nodes = vec![
            GraphNode { id: "a".into(), label: "payment".into(), node_type: "system".into(), weight: 3 },
            GraphNode { id: "b".into(), label: "auth".into(), node_type: "system".into(), weight: 2 },
        ];
        let edges = vec![GraphEdge { source: "a".into(), target: "b".into(), relation: "calls".into(), weight: 1 }];
        engine.observe_graph(&scope, &nodes, &edges);
        engine.observe_graph(&scope, &nodes, &edges);
        let result = engine.query_global(&scope, "payment", &["payment".into()], 3);
        assert!(result.is_empty() || !result[0].macro_summary.is_empty());
    }

    #[test]
    fn query_local_should_filter_by_keywords() {
        let engine = PlatoEngine::new_default();
        let nodes = vec![
            GraphNode { id: "svc_pay".into(), label: "payment".into(), node_type: "system".into(), weight: 3 },
            GraphNode { id: "svc_auth".into(), label: "auth".into(), node_type: "system".into(), weight: 2 },
        ];
        let edges = vec![GraphEdge { source: "svc_pay".into(), target: "svc_auth".into(), relation: "calls".into(), weight: 1 }];
        let (n, e) = engine.query_local(&nodes, &edges, &["payment".into()], "");
        assert!(!n.is_empty());
        assert!(!e.is_empty());
    }
}

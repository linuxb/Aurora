use regex::Regex;
use std::collections::{HashMap, HashSet};

use crate::types::{GraphEdge, GraphNode, MemoryEntry};

#[derive(Clone, Debug)]
struct TypedEntity {
    id: String,
    label: String,
    node_type: String,
}

pub fn build_entity_relation_graph(entries: Vec<MemoryEntry>) -> (Vec<GraphNode>, Vec<GraphEdge>) {
    let mut node_weights: HashMap<String, usize> = HashMap::new();
    let mut node_meta: HashMap<String, (String, String)> = HashMap::new();
    let mut edge_weights: HashMap<(String, String, String), usize> = HashMap::new();

    for entry in entries {
        if !entry.rels.is_empty() {
            for rel in &entry.rels {
                let src_label = rel[0].trim();
                let dst_label = rel[1].trim();
                let relation = rel[2].trim();
                if src_label.is_empty() || dst_label.is_empty() || relation.is_empty() {
                    continue;
                }
                let src_id = src_label.to_lowercase();
                let dst_id = dst_label.to_lowercase();
                let src_type = infer_entity_type(src_label);
                let dst_type = infer_entity_type(dst_label);
                *node_weights.entry(src_id.clone()).or_insert(0) += 1;
                *node_weights.entry(dst_id.clone()).or_insert(0) += 1;
                node_meta
                    .entry(src_id.clone())
                    .or_insert((src_label.to_string(), src_type));
                node_meta
                    .entry(dst_id.clone())
                    .or_insert((dst_label.to_string(), dst_type));
                *edge_weights
                    .entry((src_id, dst_id, relation.to_string()))
                    .or_insert(0) += 1;
            }
            continue;
        }

        let entities = extract_typed_entities(&entry.summary);
        for entity in &entities {
            *node_weights.entry(entity.id.clone()).or_insert(0) += 1;
            node_meta
                .entry(entity.id.clone())
                .or_insert((entity.label.clone(), entity.node_type.clone()));
        }

        for i in 0..entities.len() {
            for j in (i + 1)..entities.len() {
                let a = entities[i].clone();
                let b = entities[j].clone();
                let (left, right) = if a.id <= b.id { (a, b) } else { (b, a) };
                let relation = infer_relation_type(&left.node_type, &right.node_type);
                *edge_weights
                    .entry((left.id.clone(), right.id.clone(), relation))
                    .or_insert(0) += 1;
            }
        }
    }

    let nodes = node_weights
        .into_iter()
        .map(|(entity_id, weight)| {
            let (label, node_type) = node_meta
                .get(&entity_id)
                .cloned()
                .unwrap_or((entity_id.clone(), "keyword".to_string()));
            GraphNode {
                id: entity_id,
                label,
                node_type,
                weight,
            }
        })
        .collect::<Vec<_>>();

    let edges = edge_weights
        .into_iter()
        .map(|((source, target, relation), weight)| GraphEdge {
            source,
            target,
            relation,
            weight,
        })
        .collect::<Vec<_>>();

    (nodes, edges)
}

fn extract_typed_entities(summary: &str) -> Vec<TypedEntity> {
    let token_re = Regex::new(r"[A-Za-z0-9_\-]{4,}").expect("invalid regex");
    let mut entities = Vec::new();
    let mut seen = HashSet::new();

    for cap in token_re.find_iter(summary) {
        let raw = cap.as_str();
        let normalized = raw.to_lowercase();
        if seen.contains(&normalized) {
            continue;
        }
        seen.insert(normalized.clone());
        entities.push(TypedEntity {
            id: normalized,
            label: raw.to_string(),
            node_type: infer_entity_type(raw),
        });
        if entities.len() >= 12 {
            break;
        }
    }
    entities
}

fn infer_entity_type(raw: &str) -> String {
    let lower = raw.to_lowercase();
    if Regex::new(r"^t\d+$").expect("invalid regex").is_match(&lower) {
        return "task".to_string();
    }
    if Regex::new(r"^\d{4}(-\d{2}){0,2}$")
        .expect("invalid regex")
        .is_match(raw)
    {
        return "time".to_string();
    }
    if matches!(
        lower.as_str(),
        "success" | "failed" | "timeout" | "retried" | "recovered" | "error" | "pending"
    ) {
        return "status".to_string();
    }
    if matches!(
        lower.as_str(),
        "mysql" | "tidb" | "redis" | "kvrocks" | "docker" | "http" | "grpc"
    ) {
        return "system".to_string();
    }
    "keyword".to_string()
}

fn infer_relation_type(left: &str, right: &str) -> String {
    let is_status = |v: &str| v == "status";
    let is_task = |v: &str| v == "task";
    let is_time = |v: &str| v == "time";
    let is_system = |v: &str| v == "system";
    if (is_status(left) && is_task(right)) || (is_status(right) && is_task(left)) {
        return "status_of".to_string();
    }
    if (is_time(left) && is_task(right)) || (is_time(right) && is_task(left)) {
        return "happened_at".to_string();
    }
    if (is_system(left) && is_task(right)) || (is_system(right) && is_task(left)) {
        return "executed_by".to_string();
    }
    "co_occurs".to_string()
}

#[cfg(test)]
mod tests {
    use super::build_entity_relation_graph;
    use crate::types::MemoryEntry;

    #[test]
    fn build_graph_should_create_typed_nodes_and_edges() {
        let entries = vec![
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d1".to_string(),
                task_id: "t1".to_string(),
                raw_output: String::new(),
                summary: "t101 timeout mysql 2026-05-09".to_string(),
                hard_facts: vec![],
                rels: vec![],
                observed_at: 1,
            },
            MemoryEntry {
                user_id: "u1".to_string(),
                session_id: "s1".to_string(),
                dag_id: "d1".to_string(),
                task_id: "t2".to_string(),
                raw_output: String::new(),
                summary: "payment retried success".to_string(),
                hard_facts: vec![],
                rels: vec![],
                observed_at: 2,
            },
        ];

        let (nodes, edges) = build_entity_relation_graph(entries);
        assert!(!nodes.is_empty());
        assert!(!edges.is_empty());
        assert!(nodes.iter().any(|n| n.node_type == "task"));
        assert!(nodes.iter().any(|n| n.node_type == "status"));
    }

    #[test]
    fn build_graph_should_prefer_explicit_rels() {
        let entries = vec![MemoryEntry {
            user_id: "u1".to_string(),
            session_id: "s1".to_string(),
            dag_id: "d1".to_string(),
            task_id: "t1".to_string(),
            raw_output: String::new(),
            summary: "fallback summary words".to_string(),
            hard_facts: vec![],
            rels: vec![[
                "ServiceA".to_string(),
                "ServiceB".to_string(),
                "calls".to_string(),
            ]],
            observed_at: 1,
        }];

        let (nodes, edges) = build_entity_relation_graph(entries);
        assert!(nodes.iter().any(|n| n.id == "servicea"));
        assert!(nodes.iter().any(|n| n.id == "serviceb"));
        assert!(edges.iter().any(|e| {
            e.source == "servicea" && e.target == "serviceb" && e.relation == "calls"
        }));
    }
}

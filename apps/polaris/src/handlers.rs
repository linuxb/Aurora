use axum::{
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
    Json,
};
use serde_json::json;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::enrich::EnrichInput;
use crate::graph::build_entity_relation_graph;
use crate::store::{
    apply_rolling_reduce, build_list_query, build_search_query, dedup_keep_order, extract_hard_facts,
    fulltext_contains, is_recent_context,
};
use crate::types::{
    AppState, EntriesResponse, GraphResponse, IngestRequest, ListQueryParams, MemHint,
    MemHintSearchRequest, MemoryEntry, SearchQuery, SearchQueryParams,
};

pub async fn healthz() -> impl IntoResponse {
    Json(json!({"service":"polaris","status":"ok"}))
}

pub async fn list_memory(State(state): State<AppState>) -> impl IntoResponse {
    let entries = state.store.list_all();
    Json(EntriesResponse {
        count: entries.len(),
        entries,
    })
}

pub async fn list_memory_recent(
    State(state): State<AppState>,
    Query(params): Query<ListQueryParams>,
) -> impl IntoResponse {
    let search = match build_list_query(params) {
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

pub async fn search_memory(
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

pub async fn search_memory_graph(
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

pub async fn ingest_memory(
    State(state): State<AppState>,
    Json(req): Json<IngestRequest>,
) -> impl IntoResponse {
    let task_id = req
        .task_id
        .clone()
        .or(req.step_id.clone())
        .unwrap_or_default();
    if req.user_id.trim().is_empty() || req.session_id.trim().is_empty() || task_id.trim().is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({
                "code":"invalid_payload",
                "message":"expect JSON with user_id/session_id and task_id(or step_id)"
            })),
        )
            .into_response();
    }

    let observed_at = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let dag_id = req.dag_id.clone().unwrap_or_else(|| "default".to_string());
    let summary = req.summary.clone().unwrap_or_default();
    let raw_output = req.raw_output.clone().unwrap_or_default();
    let mut hard_facts = req.hard_facts.clone().unwrap_or_default();
    hard_facts.extend(extract_hard_facts(&summary, &raw_output));
    let enriched = state.enricher.enrich(EnrichInput {
        summary: summary.clone(),
        raw_output: raw_output.clone(),
        hard_facts: dedup_keep_order(hard_facts),
        rels: req.rels.clone().unwrap_or_default(),
    });

    let entry = MemoryEntry {
        user_id: req.user_id,
        session_id: req.session_id,
        dag_id: dag_id.clone(),
        task_id: task_id.clone(),
        raw_output,
        summary,
        hard_facts: dedup_keep_order(enriched.hard_facts),
        rels: enriched.rels,
        observed_at,
    };

    let final_entry = apply_rolling_reduce(&state.store, entry);
    state.store.ingest(final_entry.clone());
    let (nodes, edges) = build_entity_relation_graph(vec![final_entry.clone()]);
    let scope_key = crate::plato::PlatoEngine::scope_key(
        &final_entry.user_id,
        &final_entry.session_id,
        &final_entry.dag_id,
    );
    state.plato.observe_graph(&scope_key, &nodes, &edges);
    state.graph_store.upsert_graph(
        &final_entry.user_id,
        &final_entry.session_id,
        &final_entry.dag_id,
        &nodes,
        &edges,
    );

    (
        StatusCode::OK,
        Json(json!({
            "status":"ok",
            "stored": {
                "user_id": final_entry.user_id,
                "session_id": final_entry.session_id,
                "dag_id": final_entry.dag_id,
                "task_id": final_entry.task_id
            }
        })),
    )
        .into_response()
}

pub async fn search_by_hint(
    State(state): State<AppState>,
    Json(req): Json<MemHintSearchRequest>,
) -> impl IntoResponse {
    if req.user_id.trim().is_empty() || req.session_id.trim().is_empty() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"code":"invalid_argument","message":"user_id and session_id are required"})),
        )
            .into_response();
    }
    let hint = req.mem_hint.unwrap_or(MemHint {
        version: None,
        target_system: None,
        query_type: None,
        strategy: Some("NONE".to_string()),
        target_step_id: None,
        semantic_query: None,
        keywords: None,
        intent_question: None,
        query: None,
    });
    let semantic_query = hint
        .query
        .as_ref()
        .and_then(|q| q.text.clone())
        .or(hint.semantic_query.clone())
        .or(hint.intent_question.clone())
        .unwrap_or_default();
    let dag_id = req.dag_id.clone().unwrap_or_else(|| "default".to_string());
    let keywords = hint
        .query
        .as_ref()
        .and_then(|q| q.keywords.clone())
        .or(hint.keywords.clone())
        .unwrap_or_default();
    let base_query = SearchQuery {
        user_id: req.user_id.clone(),
        session_id: req.session_id.clone(),
        dag_id: dag_id.clone(),
        q: semantic_query.clone(),
        limit: req.limit.unwrap_or(3),
    };

    if is_recent_context(&semantic_query) {
        let mut recent = base_query.clone();
        recent.q = String::new();
        recent.limit = 3;
        let entries = state.store.search(&recent);
        return (
            StatusCode::OK,
            Json(json!({"route":"RBO_RECENT_LIST","count":entries.len(),"entries":entries})),
        )
            .into_response();
    }

    let strategy = normalize_strategy(&hint);
    match strategy.as_str() {
        "KV_POINT_GET" => {
            let target_task_id = hint
                .query
                .as_ref()
                .and_then(|q| q.target_task_id.clone())
                .or(hint.target_step_id.clone())
                .unwrap_or_default();
            if target_task_id.trim().is_empty() {
                return (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"code":"invalid_argument","message":"target_step_id is required for KV_POINT_GET"})),
                )
                    .into_response();
            }
            let mut q = base_query.clone();
            q.q = String::new();
            q.limit = 200;
            let entries = state
                .store
                .search(&q)
                .into_iter()
                .filter(|e| e.task_id == target_task_id)
                .collect::<Vec<_>>();
            (
                StatusCode::OK,
                Json(json!({"route":"KV_POINT_GET","count":entries.len(),"entries":entries})),
            )
                .into_response()
        }
        "GRAPH_LOCAL_TRAVERSAL" | "GRAPH_TRAVERSAL" => {
            let entries = state.store.search(&base_query);
            let (nodes, edges) = build_entity_relation_graph(entries.clone());
            let (local_nodes, local_edges) =
                state
                    .plato
                    .query_local(&nodes, &edges, &keywords, &semantic_query);
            if !local_nodes.is_empty() {
                return (
                    StatusCode::OK,
                    Json(json!({"route":"GRAPH_LOCAL_TRAVERSAL","node_count":local_nodes.len(),"edge_count":local_edges.len(),"nodes":local_nodes,"edges":local_edges})),
                )
                    .into_response();
            }
            if !nodes.is_empty() {
                return (
                    StatusCode::OK,
                    Json(json!({"route":"GRAPH_TRAVERSAL","node_count":nodes.len(),"edge_count":edges.len(),"nodes":nodes,"edges":edges})),
                )
                    .into_response();
            }
            let mut fallback = base_query.clone();
            fallback.limit = 50;
            let fallback_entries = state
                .store
                .search(&fallback)
                .into_iter()
                .filter(|e| fulltext_contains(e, &semantic_query))
                .collect::<Vec<_>>();
            (
                StatusCode::OK,
                Json(json!({"route":"GRAPH_LOCAL_FALLBACK_SCAN","count":fallback_entries.len(),"entries":fallback_entries})),
            )
                .into_response()
        }
        "GRAPH_GLOBAL_SUMMARY" => {
            let entries = state.store.search(&base_query);
            let (nodes, edges) = build_entity_relation_graph(entries.clone());
            let scope_key = crate::plato::PlatoEngine::scope_key(
                &req.user_id,
                &req.session_id,
                &dag_id,
            );
            state.plato.observe_graph(&scope_key, &nodes, &edges);
            let summaries = state.plato.query_global(&scope_key, &semantic_query, &keywords, 3);
            if !summaries.is_empty() {
                return (
                    StatusCode::OK,
                    Json(json!({"route":"GRAPH_GLOBAL_SUMMARY","count":summaries.len(),"communities":summaries})),
                )
                    .into_response();
            }
            (
                StatusCode::OK,
                Json(json!({"route":"GRAPH_GLOBAL_EMPTY","count":0,"communities":[] })),
            )
                .into_response()
        }
        _ => {
            let mut q = base_query;
            q.q = String::new();
            let entries = state.store.search(&q);
            (
                StatusCode::OK,
                Json(json!({"route":"NONE_LIST","count":entries.len(),"entries":entries})),
            )
                .into_response()
        }
    }
}

fn normalize_strategy(hint: &MemHint) -> String {
    let _schema_version = hint.version.clone().unwrap_or_else(|| "1.0".to_string());
    let target_system = hint
        .target_system
        .clone()
        .unwrap_or_else(|| "AUTO".to_string())
        .to_uppercase();
    if let Some(strategy) = hint.strategy.clone() {
        return strategy.to_uppercase();
    }
    if let Some(query_type) = hint.query_type.clone() {
        return match query_type.to_uppercase().as_str() {
            "LOCAL" => "GRAPH_LOCAL_TRAVERSAL".to_string(),
            "GLOBAL" => "GRAPH_GLOBAL_SUMMARY".to_string(),
            _ => "NONE".to_string(),
        };
    }
    if target_system == "PLATO_GRAPH" {
        return "GRAPH_LOCAL_TRAVERSAL".to_string();
    }
    "NONE".to_string()
}

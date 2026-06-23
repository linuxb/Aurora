use axum::{
    Json,
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
};
use serde_json::{Value, json};
use std::time::{SystemTime, UNIX_EPOCH};

use crate::graph::extractor::build_entity_relation_graph;
use crate::memory::enrich::EnrichInput;
use crate::memory::store::{
    apply_rolling_reduce, build_list_query, build_search_query, dedup_keep_order,
    extract_hard_facts, fulltext_contains, is_recent_context,
};
use crate::model::types::{
    AppState, EntriesResponse, GraphResponse, IngestRequest, ListQueryParams, Mem3IngestRequest,
    Mem3SearchRequest, MemHint, MemHintSearchRequest, MemoryEntry, SearchQuery, SearchQueryParams,
};

pub async fn healthz() -> impl IntoResponse {
    Json(json!({"service":"mem3","status":"ok","api_version":"1.0"}))
}

pub async fn ingest_v1(
    State(state): State<AppState>,
    Json(req): Json<Mem3IngestRequest>,
) -> impl IntoResponse {
    if req.version != "1.0"
        || req.idempotency_key.trim().is_empty()
        || req.scope.tenant_id.trim().is_empty()
        || req.scope.agent_id.trim().is_empty()
        || req.scope.session_id.trim().is_empty()
        || req.scope.dag_id.trim().is_empty()
    {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"code":"invalid_payload","message":"invalid Mem3 ingest envelope"})),
        )
            .into_response();
    }

    let observed_at = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let user_id = req
        .scope
        .user_id
        .clone()
        .unwrap_or_else(|| req.scope.tenant_id.clone());
    let storage_user_id = trusted_scope_key(&req.scope.tenant_id, &req.scope.agent_id, &user_id);

    let (task_id, raw_output, summary) = match req.kind.as_str() {
        "DAG_CONTEXT" => {
            let raw_query = req
                .payload
                .get("raw_query")
                .and_then(Value::as_str)
                .unwrap_or_default();
            if raw_query.trim().is_empty() || req.payload.get("intent_slot").is_none() {
                return (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"code":"invalid_payload","message":"DAG_CONTEXT requires raw_query and intent_slot"})),
                )
                    .into_response();
            }
            (
                format!("dag_context:{}", req.scope.dag_id),
                req.payload.to_string(),
                raw_query.to_string(),
            )
        }
        "TASK_OUTPUT" => {
            let task_id = req
                .payload
                .get("task_id")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string();
            if task_id.trim().is_empty()
                || req
                    .payload
                    .get("sequence")
                    .and_then(Value::as_u64)
                    .is_none()
                || req.payload.get("output").is_none()
            {
                return (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"code":"invalid_payload","message":"TASK_OUTPUT requires task_id, sequence and output"})),
                )
                    .into_response();
            }
            let summary = req
                .payload
                .get("skill_summary")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string();
            (task_id, req.payload.to_string(), summary)
        }
        _ => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"code":"invalid_payload","message":"kind must be DAG_CONTEXT or TASK_OUTPUT"})),
            )
                .into_response();
        }
    };

    let entry = MemoryEntry {
        user_id: storage_user_id,
        session_id: req.scope.session_id.clone(),
        dag_id: req.scope.dag_id.clone(),
        task_id,
        raw_output,
        summary,
        hard_facts: Vec::new(),
        rels: Vec::new(),
        observed_at,
    };
    let final_entry = apply_rolling_reduce(&state.store, entry);
    state.store.ingest(final_entry.clone());

    if req.kind == "TASK_OUTPUT" {
        let (nodes, edges) = build_entity_relation_graph(vec![final_entry.clone()]);
        let scope_key = crate::plato::engine::PlatoEngine::scope_key(
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
    }

    (
        StatusCode::ACCEPTED,
        Json(json!({
            "version":"1.0",
            "ingest_id": req.idempotency_key,
            "accepted": true,
            "async_status":"QUEUED",
            "summary_version": Value::Null
        })),
    )
        .into_response()
}

pub async fn search_v1(
    State(state): State<AppState>,
    Json(req): Json<Mem3SearchRequest>,
) -> impl IntoResponse {
    if req.version != "1.0"
        || req.scope.tenant_id.trim().is_empty()
        || req.scope.agent_id.trim().is_empty()
        || req.scope.session_id.trim().is_empty()
        || req.scope.dag_id.trim().is_empty()
        || req.current_task.task_id.trim().is_empty()
        || !matches!(req.current_task.node_type.as_str(), "skill" | "planner")
    {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"code":"invalid_argument","message":"invalid Mem3 search envelope"})),
        )
            .into_response();
    }

    let user_id = req
        .scope
        .user_id
        .clone()
        .unwrap_or_else(|| req.scope.tenant_id.clone());
    let storage_user_id = trusted_scope_key(&req.scope.tenant_id, &req.scope.agent_id, &user_id);
    let _task_context = (
        req.current_task.sequence,
        req.current_task.parent_task_ids.len(),
        req.current_task.mem_hint_source_task_ids.len(),
    );
    let limit = req.recent_limit.min(50);
    let base_query = SearchQuery {
        user_id: storage_user_id,
        session_id: req.scope.session_id.clone(),
        dag_id: req.scope.dag_id.clone(),
        q: String::new(),
        limit: limit.saturating_add(10),
    };
    let mut recent = state
        .store
        .search(&base_query)
        .into_iter()
        .filter(|entry| !entry.task_id.starts_with("dag_context:"))
        .take(limit)
        .collect::<Vec<_>>();
    recent.reverse();

    let recent_outputs = recent
        .iter()
        .map(|entry| {
            let payload = serde_json::from_str::<Value>(&entry.raw_output).unwrap_or(Value::Null);
            json!({
                "task_id": entry.task_id,
                "sequence": payload.get("sequence").and_then(Value::as_u64).unwrap_or(0),
                "node_type": payload.get("node_type").and_then(Value::as_str).unwrap_or("skill"),
                "output": payload.get("output").cloned().unwrap_or(payload)
            })
        })
        .collect::<Vec<_>>();
    let latest = recent.last();
    let latest_sequence = latest
        .and_then(|entry| serde_json::from_str::<Value>(&entry.raw_output).ok())
        .and_then(|payload| payload.get("sequence").and_then(Value::as_u64))
        .unwrap_or(0);
    let latest_summary = latest
        .map(|entry| entry.summary.clone())
        .unwrap_or_default();

    let strategy = normalize_strategy(&req.mem_hint);
    let query_text = req
        .mem_hint
        .query
        .as_ref()
        .and_then(|query| query.text.clone())
        .or(req.mem_hint.semantic_query.clone())
        .unwrap_or_default();
    let mut retrieval_items = Vec::new();
    if strategy == "KV_POINT_GET" {
        let target = req
            .mem_hint
            .query
            .as_ref()
            .and_then(|query| query.target_task_id.clone())
            .or(req.mem_hint.target_step_id.clone())
            .unwrap_or_default();
        retrieval_items = state
            .store
            .search(&SearchQuery {
                limit: 200,
                ..base_query.clone()
            })
            .into_iter()
            .filter(|entry| entry.task_id == target)
            .filter_map(|entry| serde_json::to_value(entry).ok())
            .collect();
    } else if strategy != "NONE" {
        retrieval_items = state
            .store
            .search(&SearchQuery {
                q: query_text,
                limit: 10,
                ..base_query.clone()
            })
            .into_iter()
            .filter_map(|entry| serde_json::to_value(entry).ok())
            .collect();
    }

    (
        StatusCode::OK,
        Json(json!({
            "version":"1.0",
            "working_memory":{
                "recent_outputs":recent_outputs,
                "latest_summary":{
                    "summary":latest_summary,
                    "summary_version":latest_sequence,
                    "through_sequence": if latest.is_some() { latest_sequence as i64 } else { -1 }
                }
            },
            "retrieval":{"strategy":strategy,"items":retrieval_items},
            "consistency":{
                "latest_ingested_sequence":latest_sequence,
                "summary_through_sequence":latest_sequence,
                "summary_pending":false
            }
        })),
    )
        .into_response()
}

fn trusted_scope_key(tenant_id: &str, agent_id: &str, user_id: &str) -> String {
    format!("{tenant_id}::{agent_id}::{user_id}")
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
    if req.user_id.trim().is_empty()
        || req.session_id.trim().is_empty()
        || task_id.trim().is_empty()
    {
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
    let scope_key = crate::plato::engine::PlatoEngine::scope_key(
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
            Json(
                json!({"code":"invalid_argument","message":"user_id and session_id are required"}),
            ),
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
            let scope_key = crate::plato::engine::PlatoEngine::scope_key(
                &req.user_id,
                &req.session_id,
                &dag_id,
            );
            state.plato.observe_graph(&scope_key, &nodes, &edges);
            let summaries = state
                .plato
                .query_global(&scope_key, &semantic_query, &keywords, 3);
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

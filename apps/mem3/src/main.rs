mod api;
mod graph;
mod memory;
mod model;
mod plato;

use axum::{
    Router,
    routing::{get, post},
};
use std::env;
use std::net::SocketAddr;

use api::handlers::{
    healthz, ingest_memory, ingest_v1, list_memory, list_memory_recent, search_by_hint,
    search_memory, search_memory_graph, search_v1,
};
use graph::store::build_graph_store_from_env;
use memory::enrich::build_enricher_from_env;
use memory::store::build_store_from_env;
use model::types::AppState;
use plato::engine::PlatoEngine;

#[tokio::main]
async fn main() {
    let addr = env::var("MEM3_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let socket_addr: SocketAddr = addr.parse().expect("invalid MEM3_ADDR");

    let state = AppState {
        store: build_store_from_env(),
        graph_store: build_graph_store_from_env(),
        enricher: build_enricher_from_env(),
        plato: std::sync::Arc::new(PlatoEngine::new_default()),
    };

    let app = Router::new()
        .route("/healthz", get(healthz))
        .route("/v1/memory/ingest", post(ingest_v1))
        .route("/v1/memory/search", post(search_v1))
        .route("/memory", get(list_memory))
        .route("/memory/list", get(list_memory_recent))
        .route("/memory/search", get(search_memory))
        .route("/memory/search_by_hint", post(search_by_hint))
        .route("/memory/graph/search", get(search_memory_graph))
        .route("/ingest", post(ingest_memory))
        .with_state(state);

    println!("mem3 listening on {}", socket_addr);
    let listener = tokio::net::TcpListener::bind(socket_addr)
        .await
        .expect("failed to bind mem3 address");
    axum::serve(listener, app).await.expect("mem3 serve failed");
}

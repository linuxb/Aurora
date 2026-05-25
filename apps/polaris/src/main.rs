mod api;
mod graph;
mod memory;
mod model;
mod plato;

use axum::{
    routing::{get, post},
    Router,
};
use std::env;
use std::net::SocketAddr;

use api::handlers::{
    healthz, ingest_memory, list_memory, list_memory_recent, search_by_hint, search_memory,
    search_memory_graph,
};
use graph::store::build_graph_store_from_env;
use memory::enrich::build_enricher_from_env;
use memory::store::build_store_from_env;
use model::types::AppState;
use plato::engine::PlatoEngine;

#[tokio::main]
async fn main() {
    let addr = env::var("POLARIS_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let socket_addr: SocketAddr = addr.parse().expect("invalid POLARIS_ADDR");

    let state = AppState {
        store: build_store_from_env(),
        graph_store: build_graph_store_from_env(),
        enricher: build_enricher_from_env(),
        plato: std::sync::Arc::new(PlatoEngine::new_default()),
    };

    let app = Router::new()
        .route("/healthz", get(healthz))
        .route("/memory", get(list_memory))
        .route("/memory/list", get(list_memory_recent))
        .route("/memory/search", get(search_memory))
        .route("/memory/search_by_hint", post(search_by_hint))
        .route("/memory/graph/search", get(search_memory_graph))
        .route("/ingest", post(ingest_memory))
        .with_state(state);

    println!("polaris listening on {}", socket_addr);
    let listener = tokio::net::TcpListener::bind(socket_addr)
        .await
        .expect("failed to bind polaris address");
    axum::serve(listener, app).await.expect("polaris serve failed");
}

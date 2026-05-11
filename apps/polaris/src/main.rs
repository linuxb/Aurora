mod graph;
mod handlers;
mod store;
mod types;

use axum::{routing::{get, post}, Router};
use std::env;
use std::net::SocketAddr;

use handlers::{
    healthz, ingest_memory, list_memory, list_memory_recent, search_by_hint, search_memory,
    search_memory_graph,
};
use store::build_store_from_env;
use types::AppState;

#[tokio::main]
async fn main() {
    let addr = env::var("POLARIS_ADDR").unwrap_or_else(|_| "127.0.0.1:8082".to_string());
    let socket_addr: SocketAddr = addr.parse().expect("invalid POLARIS_ADDR");

    let state = AppState {
        store: build_store_from_env(),
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

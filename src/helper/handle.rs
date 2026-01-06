use anyhow::Result;
use bytes::Bytes;
use http_body_util::combinators::BoxBody;
use hyper::body::Incoming;
use hyper::server::conn::http1;
use hyper::service::service_fn;
use hyper::{Request, Response};
use hyper_util::rt::TokioIo;
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::net::TcpListener;

use crate::helper::stats::{StatsManager, BoxError};
use crate::helper::proxy::ProxyHandler;

#[derive(Clone)]
pub struct Handle {
    pub stats: Arc<StatsManager>,
    pub proxy: Arc<ProxyHandler>,
}

impl Handle {
    pub fn new(
        reverse_proxy: String,
        headers: String,
        blocked_files: String,
        stats_file: String,
        socks_proxy: String,
    ) -> Self {
        let stats_manager = Arc::new(StatsManager::new(stats_file));
        
        let proxy_handler = Arc::new(ProxyHandler::new(
            reverse_proxy,
            headers,
            blocked_files,
            socks_proxy,
            stats_manager.clone(),
        ));

        Handle {
            stats: stats_manager,
            proxy: proxy_handler,
        }
    }

    pub async fn serve_http(
        self: Arc<Self>,
        req: Request<Incoming>,
    ) -> Result<Response<BoxBody<Bytes, BoxError>>> {
        let path = req.uri().path();

        if path.starts_with("/stats") {
            // Stats & History
            return Ok(self.stats.handle_request(req)?);
        }

        // Default to Proxy
        Ok(self.proxy.handle_request(req).await?)
    }
}

pub async fn start_server(
    bind: String,
    remote: String,
    _ip: String,
    headers: String,
    blocked: String,
    socks_proxy: String,
) -> Result<()> {
    println!("Listening on {}, forwarding to {}", bind, remote);

    let handle = Arc::new(Handle::new(
        remote,
        headers,
        blocked,
        "proxy_stats.json".to_string(),
        socks_proxy,
    ));

    handle.stats.clone().start_periodic_save().await;

    let addr: SocketAddr = bind.parse()?;
    let listener = TcpListener::bind(addr).await?;

    loop {
        let (stream, _) = listener.accept().await?;
        let io = TokioIo::new(stream);
        let handle = handle.clone();

        tokio::task::spawn(async move {
            let service = service_fn(move |req| {
                let handle = handle.clone();
                async move { handle.serve_http(req).await }
            });

            if let Err(_err) = http1::Builder::new().serve_connection(io, service).await {
                // Ignore disconnects
            }
        });
    }
}

use anyhow::Result;
use bytes::Bytes;
use futures_util::StreamExt;
use http_body_util::{BodyExt, StreamBody};
use http_body_util::combinators::BoxBody;
use hyper::body::{Frame, Incoming};
use hyper::{HeaderMap, Method, Request, Response, StatusCode, Uri};
use parking_lot::RwLock;
use rand::Rng;
use reqwest::Client;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc::{self, UnboundedSender};

use crate::helper::stats::{StatsManager, BoxError};

// Server manager for failover and load balancing
#[derive(Debug, Clone)]
pub struct ServerManager {
    servers: Arc<RwLock<Vec<String>>>,
    original_servers: Arc<RwLock<Vec<String>>>,
}

impl ServerManager {
    pub fn new(servers: Vec<String>) -> Self {
        ServerManager {
            servers: Arc::new(RwLock::new(servers.clone())),
            original_servers: Arc::new(RwLock::new(servers)),
        }
    }

    pub fn get_server(&self) -> String {
        let servers = self.servers.read();
        if servers.is_empty() {
            drop(servers);
            let original = self.original_servers.read();
            let mut servers_write = self.servers.write();
            *servers_write = original.clone();
            drop(servers_write);
            drop(original);

            let servers = self.servers.read();
            if !servers.is_empty() {
                return servers[0].clone();
            }
            return String::new();
        }

        let idx = rand::thread_rng().gen_range(0..servers.len());
        servers[idx].clone()
    }

    pub fn mark_server_as_failed(&self, server: &str) {
        let mut servers = self.servers.write();
        servers.retain(|s| !s.contains(server));

        if servers.is_empty() {
            let original = self.original_servers.read();
            *servers = original.clone();
            println!("Reset to full server list with {} servers", servers.len());
        } else {
            println!(
                "Removed failed server: {} - Remaining servers: {}",
                server,
                servers.len()
            );
        }
    }
}

#[derive(Clone)]
pub struct ProxyHandler {
    pub client: Client,
    pub server_manager: Arc<ServerManager>,
    pub parsed_headers: Arc<HeaderMap>,
    pub parsed_blocked_files: Arc<Vec<String>>,
    pub stats: Arc<StatsManager>,
    pub log_tx: UnboundedSender<String>,
}

impl ProxyHandler {
    pub fn new(
        reverse_proxy: String,
        headers: String,
        blocked_files: String,
        socks_proxy: String,
        stats: Arc<StatsManager>,
    ) -> Self {
        // Parse headers once
        let mut parsed_headers = HeaderMap::new();
        if !headers.is_empty() {
            for header in headers.split(';') {
                if let Some((key, value)) = header.split_once(':') {
                    if let (Ok(k), Ok(v)) = (
                        hyper::header::HeaderName::from_bytes(key.trim().as_bytes()),
                        hyper::header::HeaderValue::from_str(value.trim())
                    ) {
                        parsed_headers.insert(k, v);
                    }
                }
            }
        }

        // Parse blocked files once
        let parsed_blocked_files = if !blocked_files.is_empty() {
            blocked_files.split('|').map(|s| s.to_string()).collect()
        } else {
            Vec::new()
        };

        // Initialize Client once
        let client = Self::build_http_client(&socks_proxy).unwrap_or_else(|e| {
            eprintln!("Failed to build HTTP client: {}", e);
            Client::builder().build().unwrap()
        });

        // Async Logger Channel
        let (tx, mut rx) = mpsc::unbounded_channel::<String>();
        
        // Spawn background logger task
        tokio::spawn(async move {
            while let Some(msg) = rx.recv().await {
                println!("{}", msg);
            }
        });

        ProxyHandler {
            client,
            parsed_headers: Arc::new(parsed_headers),
            parsed_blocked_files: Arc::new(parsed_blocked_files),
            server_manager: Arc::new(ServerManager::new(
                reverse_proxy.split('|').map(|s| s.to_string()).collect(),
            )),
            stats,
            log_tx: tx,
        }
    }

    fn log(&self, msg: String) {
        let _ = self.log_tx.send(msg);
    }

    fn build_http_client(socks_proxy: &str) -> Result<Client, anyhow::Error> {
        let mut builder = Client::builder()
            .danger_accept_invalid_certs(true)
            .pool_idle_timeout(Some(Duration::from_secs(15)))
            .connect_timeout(Duration::from_secs(30))
            .pool_max_idle_per_host(128);

        if !socks_proxy.is_empty() {
            if let Ok(proxy) = reqwest::Proxy::all(socks_proxy) {
                builder = builder.proxy(proxy);
                if let Ok(url) = socks_proxy.parse::<url::Url>() {
                    println!("Configured SOCKS proxy: {}://{}", url.scheme(), url.host_str().unwrap_or("unknown"));
                }
            } else {
                if let Some(proxy_url) = Self::parse_custom_socks_format(socks_proxy) {
                    if let Ok(proxy) = reqwest::Proxy::all(&proxy_url) {
                        builder = builder.proxy(proxy);
                        let parts: Vec<&str> = socks_proxy.split(':').collect();
                        if parts.len() >= 3 {
                            println!("Configured SOCKS proxy (custom format): {}://{}:{}", parts[0], parts[1], parts[2]);
                        }
                    }
                }
            }
        }

        Ok(builder.build()?)
    }

    fn parse_custom_socks_format(socks_config: &str) -> Option<String> {
        let parts: Vec<&str> = socks_config.split(':').collect();
        if parts.len() != 5 {
            return None;
        }

        let socks_type = parts[0];
        let host = parts[1];
        let port = parts[2];
        let username = parts[3];
        let password = parts[4];

        if socks_type != "socks5" && socks_type != "socks4" && socks_type != "socks4a" {
            return None;
        }

        if !username.is_empty() && !password.is_empty() {
            Some(format!(
                "{}://{}:{}@{}:{}",
                socks_type, username, password, host, port
            ))
        } else {
            Some(format!("{}://{}:{}", socks_type, host, port))
        }
    }

    pub async fn handle_request(
        &self,
        req: Request<Incoming>,
    ) -> Result<Response<BoxBody<Bytes, BoxError>>, anyhow::Error> {
        let path = req.uri().path().to_string();

        // Increment request count
        self.stats.inc_request();

        self.log(format!(
            "{} {} {:?} {}",
            req.uri(),
            req.method(),
            req.version(),
            req.headers()
                .get("user-agent")
                .and_then(|v| v.to_str().ok())
                .unwrap_or("")
        ));

        if self.is_blocked_file(&path) {
            self.log(format!("Blocked file extension for URL: {}", path));
            self.stats.inc_blocked();
            let body = http_body_util::Full::new(Bytes::from("Forbidden"))
                .map_err(|never| match never {})
                .boxed();
            return Ok(Response::builder()
                .status(StatusCode::FORBIDDEN)
                .body(body)
                .unwrap());
        }

        let used_remote = self.server_manager.get_server();
        self.log(format!("Using remote server: {}", used_remote));

        let uri_str = req.uri().path_and_query()
            .map(|pq| pq.as_str())
            .unwrap_or("/");
        
        let backend_url = if used_remote.ends_with('/') && uri_str.starts_with('/') {
            format!("{}{}", used_remote.trim_end_matches('/'), uri_str)
        } else if !used_remote.ends_with('/') && !uri_str.starts_with('/') {
            format!("{}/{}", used_remote, uri_str)
        } else {
            format!("{}{}", used_remote, uri_str)
        };

        match self.forward_request(req, &backend_url).await {
            Ok(response) => {
                let status = response.status();
                if status == StatusCode::TOO_MANY_REQUESTS {
                    self.log("Server returned 429 - Too Many Requests".to_string());
                    self.server_manager.mark_server_as_failed(&used_remote);
                }
                Ok(response)
            }
            Err(e) => {
                self.log(format!("Error forwarding request to {}: {:?}", backend_url, e));
                let body = http_body_util::Full::new(Bytes::from(format!("Bad Gateway: {}", e)))
                    .map_err(|never| match never {})
                    .boxed();
                Ok(Response::builder()
                    .status(StatusCode::BAD_GATEWAY)
                    .body(body)
                    .unwrap())
            }
        }
    }

    fn is_blocked_file(&self, path: &str) -> bool {
        if self.parsed_blocked_files.is_empty() {
            return false;
        }

        if let Some(dot_pos) = path.rfind('.') {
            let file_ext = &path[dot_pos + 1..].to_lowercase();
            return self.parsed_blocked_files
                .iter()
                .any(|ext| ext.eq_ignore_ascii_case(file_ext));
        }

        false
    }

    async fn forward_request(
        &self,
        req: Request<Incoming>,
        backend_url: &str,
    ) -> Result<Response<BoxBody<Bytes, BoxError>>, anyhow::Error> {
        let uri: Uri = backend_url.parse()?;
        
        let (parts, body) = req.into_parts();
        
        let method = match parts.method {
            Method::GET => reqwest::Method::GET,
            Method::POST => reqwest::Method::POST,
            Method::PUT => reqwest::Method::PUT,
            Method::DELETE => reqwest::Method::DELETE,
            Method::HEAD => reqwest::Method::HEAD,
            Method::OPTIONS => reqwest::Method::OPTIONS,
            Method::PATCH => reqwest::Method::PATCH,
            _ => reqwest::Method::GET,
        };

        // Buffer the body to allow retries
        // Note: For very large files this might be an issue, but for normal API/Web use it handles the retry requirement
        let body_bytes = body.collect().await?.to_bytes();
        self.stats.get_incoming_counter().fetch_add(body_bytes.len() as i64, Ordering::Relaxed);

        let max_retries = 3;
        let mut last_error = None;

        for attempt in 0..max_retries {
            let mut request_builder = self.client.request(method.clone(), uri.to_string());

            for (k, v) in self.parsed_headers.iter() {
                request_builder = request_builder.header(k, v);
            }

            for (key, value) in parts.headers.iter() {
                if key != hyper::header::HOST {
                     request_builder = request_builder.header(key, value);
                }
            }

            // Use the buffered body
            let req_body = reqwest::Body::from(body_bytes.clone());
            request_builder = request_builder.body(req_body);

            match request_builder.send().await {
                Ok(response) => {
                    let status = response.status();
                    let headers = response.headers().clone();

                    // Stream Response Body
                    let outgoing_counter = self.stats.get_outgoing_counter();
                    let backend_counter = self.stats.get_backend_counter();
                    
                    let stream = response.bytes_stream().map(move |result| {
                        match result {
                            Ok(bytes) => {
                                let len = bytes.len() as i64;
                                outgoing_counter.fetch_add(len, Ordering::Relaxed);
                                backend_counter.fetch_add(len, Ordering::Relaxed);
                                Ok(Frame::data(bytes))
                            },
                            Err(e) => Err(Box::new(e) as BoxError),
                        }
                    });

                    let body = BodyExt::boxed(StreamBody::new(stream));

                    let mut resp_builder = Response::builder().status(status.as_u16());
                    for (key, value) in headers.iter() {
                         resp_builder = resp_builder.header(key, value);
                    }

                    return Ok(resp_builder.body(body)?);
                }
                Err(e) => {
                    self.log(format!("Request to {} failed (attempt {}/{}): {}", backend_url, attempt + 1, max_retries, e));
                    last_error = Some(e);
                    if attempt < max_retries - 1 {
                        tokio::time::sleep(Duration::from_millis(500)).await;
                    }
                }
            }
        }

        if let Some(e) = last_error {
            return Err(e.into());
        }
        Err(anyhow::anyhow!("Request failed after retries"))
    }
}

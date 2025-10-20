use anyhow::{Context, Result};
use bytes::Bytes;
use chrono::Local;
use http_body_util::Full;
use hyper::body::Incoming;
use hyper::server::conn::http1;
use hyper::service::service_fn;
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use parking_lot::RwLock;
use rand::Rng;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::Write;
use std::net::SocketAddr;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::TcpListener;
use tokio::time::sleep;

// Stats data structures
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DailyStatsData {
    pub requests: i64,
    pub incoming_bytes: i64,
    pub outgoing_bytes: i64,
    pub backend_bytes: i64,
    pub blocked_count: i64,
    pub total_bytes: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AllStatsData {
    pub daily_stats: HashMap<String, DailyStatsData>,
}

#[derive(Debug, Clone, Serialize)]
pub struct StatsResponseData {
    pub requests: i64,
    pub incoming_gb: f64,
    pub outgoing_gb: f64,
    pub backend_gb: f64,
    pub total_gb: f64,
    pub blocked_count: i64,
}

#[derive(Debug, Clone, Serialize)]
pub struct AllStatsResponseData {
    pub daily_stats: HashMap<String, StatsResponseData>,
}

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
            // Reset to original servers if empty
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

        // Simple random selection
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

    pub fn set_original_servers(&self, servers: Vec<String>) {
        let mut original = self.original_servers.write();
        *original = servers.clone();

        let current_servers = self.servers.read();
        if current_servers.is_empty() {
            drop(current_servers);
            let mut servers_write = self.servers.write();
            *servers_write = servers;
        }
    }
}

// Main handle struct
#[derive(Clone)]
pub struct Handle {
    pub reverse_proxy: String,
    pub headers: String,
    pub blocked_files: String,
    pub socks_proxy: String,
    pub stats_file: String,
    // Traffic counters
    pub request_count: Arc<RwLock<i64>>,
    pub incoming_bytes: Arc<RwLock<i64>>,
    pub outgoing_bytes: Arc<RwLock<i64>>,
    pub backend_bytes: Arc<RwLock<i64>>,
    pub blocked_count: Arc<RwLock<i64>>,
    // Server manager
    pub server_manager: Arc<ServerManager>,
}

impl Handle {
    pub fn new(
        reverse_proxy: String,
        headers: String,
        blocked_files: String,
        stats_file: String,
        socks_proxy: String,
    ) -> Self {
        let handle = Handle {
            reverse_proxy: reverse_proxy.clone(),
            headers,
            blocked_files,
            socks_proxy,
            stats_file: stats_file.clone(),
            request_count: Arc::new(RwLock::new(0)),
            incoming_bytes: Arc::new(RwLock::new(0)),
            outgoing_bytes: Arc::new(RwLock::new(0)),
            backend_bytes: Arc::new(RwLock::new(0)),
            blocked_count: Arc::new(RwLock::new(0)),
            server_manager: Arc::new(ServerManager::new(
                reverse_proxy.split('|').map(|s| s.to_string()).collect(),
            )),
        };

        // Load existing stats
        if let Err(e) = handle.load_stats() {
            eprintln!("Warning: Failed to load stats: {}", e);
        }

        handle
    }

    pub fn get_stats(&self) -> (i64, i64, i64, i64, i64) {
        (
            *self.request_count.read(),
            *self.incoming_bytes.read(),
            *self.outgoing_bytes.read(),
            *self.backend_bytes.read(),
            *self.blocked_count.read(),
        )
    }

    pub fn save_stats(&self) -> Result<()> {
        let today = Local::now().format("%Y-%m-%d").to_string();

        // Read existing data first
        let mut all_stats = if Path::new(&self.stats_file).exists() {
            let file = File::open(&self.stats_file)
                .context("Failed to open stats file")?;
            serde_json::from_reader(file).unwrap_or_else(|_| AllStatsData {
                daily_stats: HashMap::new(),
            })
        } else {
            AllStatsData {
                daily_stats: HashMap::new(),
            }
        };

        // Update today's stats
        let (req_count, in_bytes, out_bytes, backend_bytes, blocked_count) = self.get_stats();
        all_stats.daily_stats.insert(
            today,
            DailyStatsData {
                requests: req_count,
                incoming_bytes: in_bytes,
                outgoing_bytes: out_bytes,
                backend_bytes,
                blocked_count,
                total_bytes: in_bytes + out_bytes + backend_bytes,
            },
        );

        // Ensure directory exists
        if let Some(parent) = Path::new(&self.stats_file).parent() {
            std::fs::create_dir_all(parent)?;
        }

        let mut file = File::create(&self.stats_file)
            .context("Failed to create stats file")?;
        let json = serde_json::to_string_pretty(&all_stats)?;
        file.write_all(json.as_bytes())?;

        Ok(())
    }

    pub fn load_stats(&self) -> Result<()> {
        if !Path::new(&self.stats_file).exists() {
            return Ok(());
        }

        let file = File::open(&self.stats_file)
            .context("Failed to open stats file")?;
        let all_stats: AllStatsData = serde_json::from_reader(file)
            .context("Failed to decode stats")?;

        // Load today's stats if available
        let today = Local::now().format("%Y-%m-%d").to_string();
        if let Some(data) = all_stats.daily_stats.get(&today) {
            *self.request_count.write() = data.requests;
            *self.incoming_bytes.write() = data.incoming_bytes;
            *self.outgoing_bytes.write() = data.outgoing_bytes;
            *self.backend_bytes.write() = data.backend_bytes;
            *self.blocked_count.write() = data.blocked_count;

            println!(
                "Loaded today's stats from file: {} requests, {} blocked, {} total bytes",
                data.requests,
                data.blocked_count,
                data.incoming_bytes + data.outgoing_bytes + data.backend_bytes
            );
        } else {
            println!("No stats found for today, starting fresh");
        }

        Ok(())
    }

    fn serve_stats(&self) -> Response<Full<Bytes>> {
        let (req_count, in_bytes, out_bytes, backend_bytes, blocked_count) = self.get_stats();
        let total_bytes = in_bytes + out_bytes + backend_bytes;

        let response = StatsResponseData {
            requests: req_count,
            incoming_gb: in_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
            outgoing_gb: out_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
            backend_gb: backend_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
            total_gb: total_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
            blocked_count,
        };

        let json = serde_json::to_string_pretty(&response).unwrap();
        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(Full::new(Bytes::from(json)))
            .unwrap()
    }

    fn serve_history_stats(&self) -> Response<Full<Bytes>> {
        let all_stats = if Path::new(&self.stats_file).exists() {
            let file = File::open(&self.stats_file).ok();
            file.and_then(|f| serde_json::from_reader(f).ok())
                .unwrap_or_else(|| AllStatsData {
                    daily_stats: HashMap::new(),
                })
        } else {
            AllStatsData {
                daily_stats: HashMap::new(),
            }
        };

        // Convert to response format
        let mut response = AllStatsResponseData {
            daily_stats: HashMap::new(),
        };

        for (date, daily_data) in all_stats.daily_stats {
            let total_bytes =
                daily_data.incoming_bytes + daily_data.outgoing_bytes + daily_data.backend_bytes;
            response.daily_stats.insert(
                date,
                StatsResponseData {
                    requests: daily_data.requests,
                    incoming_gb: daily_data.incoming_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
                    outgoing_gb: daily_data.outgoing_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
                    backend_gb: daily_data.backend_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
                    total_gb: total_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
                    blocked_count: daily_data.blocked_count,
                },
            );
        }

        let json = serde_json::to_string_pretty(&response).unwrap();
        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(Full::new(Bytes::from(json)))
            .unwrap()
    }

    fn reset_stats(&self) -> Response<Full<Bytes>> {
        *self.request_count.write() = 0;
        *self.incoming_bytes.write() = 0;
        *self.outgoing_bytes.write() = 0;
        *self.backend_bytes.write() = 0;
        *self.blocked_count.write() = 0;

        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(Full::new(Bytes::from(r#"{"status": "stats reset"}"#)))
            .unwrap()
    }

    pub async fn serve_http(
        self: Arc<Self>,
        req: Request<Incoming>,
    ) -> Result<Response<Full<Bytes>>> {
        let path = req.uri().path();

        // Handle stats endpoints
        if path == "/stats" {
            return Ok(self.serve_stats());
        }
        if path == "/stats/history" {
            return Ok(self.serve_history_stats());
        }
        if path == "/stats/reset" && req.method() == Method::POST {
            return Ok(self.reset_stats());
        }

        // Increment request count
        *self.request_count.write() += 1;

        println!(
            "{} {} {:?} {}",
            req.uri(),
            req.method(),
            req.version(),
            req.headers()
                .get("user-agent")
                .and_then(|v| v.to_str().ok())
                .unwrap_or("")
        );

        // Check for blocked file extensions
        if self.is_blocked_file(path) {
            println!("Blocked file extension for URL: {}", path);
            *self.blocked_count.write() += 1;
            return Ok(Response::builder()
                .status(StatusCode::FORBIDDEN)
                .body(Full::new(Bytes::from("Forbidden")))
                .unwrap());
        }

        // Get a server to use
        let used_remote = self.server_manager.get_server();
        println!("Using remote server: {}", used_remote);

        // TODO: Implement actual proxy logic here
        // For now, return a placeholder response
        Ok(Response::builder()
            .status(StatusCode::OK)
            .body(Full::new(Bytes::from("Proxy response placeholder")))
            .unwrap())
    }

    fn is_blocked_file(&self, path: &str) -> bool {
        if self.blocked_files.is_empty() {
            return false;
        }

        let extensions: Vec<&str> = self.blocked_files.split('|').collect();
        if let Some(dot_pos) = path.rfind('.') {
            let file_ext = &path[dot_pos + 1..].to_lowercase();
            return extensions
                .iter()
                .any(|ext| ext.eq_ignore_ascii_case(file_ext));
        }

        false
    }

    pub async fn start_periodic_save(self: Arc<Self>) {
        tokio::spawn(async move {
            loop {
                sleep(Duration::from_secs(60)).await;
                if let Err(e) = self.save_stats() {
                    eprintln!("Failed to save stats: {}", e);
                }
            }
        });
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

    // Start periodic stats saving
    handle.clone().start_periodic_save().await;

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

            if let Err(err) = http1::Builder::new().serve_connection(io, service).await {
                eprintln!("Error serving connection: {:?}", err);
            }
        });
    }
}

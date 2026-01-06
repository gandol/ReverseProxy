use anyhow::{Context, Result};
use bytes::Bytes;
use chrono::Local;
use http_body_util::{BodyExt, Full};
use http_body_util::combinators::BoxBody;
use hyper::{Method, Request, Response, StatusCode};
use hyper::body::Incoming;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::Write;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;

pub type BoxError = Box<dyn std::error::Error + Send + Sync + 'static>;

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

#[derive(Clone)]
pub struct StatsManager {
    pub request_count: Arc<AtomicI64>,
    pub incoming_bytes: Arc<AtomicI64>,
    pub outgoing_bytes: Arc<AtomicI64>,
    pub backend_bytes: Arc<AtomicI64>,
    pub blocked_count: Arc<AtomicI64>,
    pub stats_file: String,
}

impl StatsManager {
    pub fn new(stats_file: String) -> Self {
        let mgr = StatsManager {
            request_count: Arc::new(AtomicI64::new(0)),
            incoming_bytes: Arc::new(AtomicI64::new(0)),
            outgoing_bytes: Arc::new(AtomicI64::new(0)),
            backend_bytes: Arc::new(AtomicI64::new(0)),
            blocked_count: Arc::new(AtomicI64::new(0)),
            stats_file,
        };
        
        if let Err(e) = mgr.load_stats() {
            eprintln!("Warning: Failed to load stats: {}", e);
        }
        
        mgr
    }

    pub fn handle_request(&self, req: Request<Incoming>) -> Result<Response<BoxBody<Bytes, BoxError>>> {
        let path = req.uri().path();

        if path == "/stats" {
            return Ok(self.serve_stats());
        }
        if path == "/stats/history" {
            return Ok(self.serve_history_stats());
        }
        if path == "/stats/reset" && req.method() == Method::POST {
            return Ok(self.reset_stats());
        }

        let body = Full::new(Bytes::from("Not Found"))
            .map_err(|never| match never {})
            .boxed();
        Ok(Response::builder()
            .status(StatusCode::NOT_FOUND)
            .body(body)
            .unwrap())
    }

    pub fn inc_request(&self) {
        self.request_count.fetch_add(1, Ordering::Relaxed);
    }

    pub fn inc_blocked(&self) {
        self.blocked_count.fetch_add(1, Ordering::Relaxed);
    }

    pub fn get_incoming_counter(&self) -> Arc<AtomicI64> {
        self.incoming_bytes.clone()
    }

    pub fn get_outgoing_counter(&self) -> Arc<AtomicI64> {
        self.outgoing_bytes.clone()
    }

    pub fn get_backend_counter(&self) -> Arc<AtomicI64> {
        self.backend_bytes.clone()
    }

    pub fn get_stats(&self) -> (i64, i64, i64, i64, i64) {
        (
            self.request_count.load(Ordering::Relaxed),
            self.incoming_bytes.load(Ordering::Relaxed),
            self.outgoing_bytes.load(Ordering::Relaxed),
            self.backend_bytes.load(Ordering::Relaxed),
            self.blocked_count.load(Ordering::Relaxed),
        )
    }

    pub fn save_stats(&self) -> Result<()> {
        let today = Local::now().format("%Y-%m-%d").to_string();

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

        let today = Local::now().format("%Y-%m-%d").to_string();
        if let Some(data) = all_stats.daily_stats.get(&today) {
            self.request_count.store(data.requests, Ordering::Relaxed);
            self.incoming_bytes.store(data.incoming_bytes, Ordering::Relaxed);
            self.outgoing_bytes.store(data.outgoing_bytes, Ordering::Relaxed);
            self.backend_bytes.store(data.backend_bytes, Ordering::Relaxed);
            self.blocked_count.store(data.blocked_count, Ordering::Relaxed);

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

    fn serve_stats(&self) -> Response<BoxBody<Bytes, BoxError>> {
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
        let body = Full::new(Bytes::from(json)).map_err(|never| match never {}).boxed();
        
        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(body)
            .unwrap()
    }

    fn serve_history_stats(&self) -> Response<BoxBody<Bytes, BoxError>> {
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
        let body = Full::new(Bytes::from(json)).map_err(|never| match never {}).boxed();

        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(body)
            .unwrap()
    }

    fn reset_stats(&self) -> Response<BoxBody<Bytes, BoxError>> {
        self.request_count.store(0, Ordering::Relaxed);
        self.incoming_bytes.store(0, Ordering::Relaxed);
        self.outgoing_bytes.store(0, Ordering::Relaxed);
        self.backend_bytes.store(0, Ordering::Relaxed);
        self.blocked_count.store(0, Ordering::Relaxed);

        let body = Full::new(Bytes::from(r#"{"status": "stats reset"}"#))
            .map_err(|never| match never {})
            .boxed();

        Response::builder()
            .status(StatusCode::OK)
            .header("Content-Type", "application/json")
            .body(body)
            .unwrap()
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

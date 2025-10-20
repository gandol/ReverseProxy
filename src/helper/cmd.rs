use clap::Parser;

#[derive(Parser, Debug, Clone)]
#[command(name = "proxyrust")]
#[command(about = "ReverseProxy in Rust with traffic calculation and daily statistics")]
pub struct Cmd {
    /// Listen on ip:port
    #[arg(short = 'l', long, default_value = "0.0.0.0:8888")]
    pub bind: String,

    /// Reverse proxy addr
    #[arg(short = 'r', long, default_value = "http://idea.lanyus.com:80")]
    pub remote: String,

    /// Reverse proxy addr server ip
    #[arg(long = "ip", default_value = "")]
    pub ip: String,

    /// Custom headers to add to requests
    #[arg(short = 'h', long, default_value = "")]
    pub headers: String,

    /// Block file extensions (e.g., exe|zip)
    #[arg(short = 'b', long, default_value = "")]
    pub blocked_files: String,

    /// Run as daemon in background
    #[arg(short = 'd', long, default_value = "false")]
    pub daemon: bool,

    /// SOCKS proxy address (URL format: socks5://user:pass@host:port or custom format: socks5:host:port:username:password)
    #[arg(long = "socks", default_value = "")]
    pub socks_proxy: String,
}

pub fn parse_cmd() -> Cmd {
    Cmd::parse()
}

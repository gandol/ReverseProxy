mod helper;

use anyhow::Result;
use daemonize::Daemonize;
use helper::{parse_cmd, start_server};
use std::fs::File;
use std::process;

#[tokio::main]
async fn main() -> Result<()> {
    let cmd = parse_cmd();

    // Handle daemon mode
    if cmd.daemon {
        println!("Starting daemon mode...");

        // Create log file for daemon output
        let log_file = File::create("proxy.log")
            .expect("Failed to create log file");

        let daemonize = Daemonize::new()
            .pid_file("proxy.pid")
            .stdout(log_file.try_clone().unwrap())
            .stderr(log_file);

        match daemonize.start() {
            Ok(_) => {
                // In daemon process now
                println!("Daemon started successfully");
            }
            Err(e) => {
                eprintln!("Failed to daemonize: {}", e);
                process::exit(1);
            }
        }

        // Continue with server startup in daemon mode
        start_server(
            cmd.bind.clone(),
            cmd.remote.clone(),
            cmd.ip.clone(),
            cmd.headers.clone(),
            cmd.blocked_files.clone(),
            cmd.socks_proxy.clone(),
        )
        .await?;
    } else {
        // Normal mode
        start_server(
            cmd.bind,
            cmd.remote,
            cmd.ip,
            cmd.headers,
            cmd.blocked_files,
            cmd.socks_proxy,
        )
        .await?;
    }

    Ok(())
}

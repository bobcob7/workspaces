use std::io::{self, BufRead};
use ws_desktop_lib::online::{auth, config::Config, server};

const USAGE: &str = "usage: ws-online [serve]        serve until stopped (the default)
       ws-online hash-password  read a password from stdin, print its argon2 hash";

#[tokio::main]
async fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let code = match args
        .iter()
        .map(String::as_str)
        .collect::<Vec<_>>()
        .as_slice()
    {
        [] | ["serve"] => serve().await,
        ["hash-password"] => hash_password(),
        _ => {
            eprintln!("{USAGE}");
            2
        }
    };
    std::process::exit(code);
}

async fn serve() -> i32 {
    let config = match Config::from_env().and_then(|config| config.prepare().map(|()| config)) {
        Ok(config) => config,
        Err(e) => {
            eprintln!("ws-online: {e}");
            return 1;
        }
    };
    match server::run(config).await {
        Ok(()) => 0,
        Err(e) => {
            eprintln!("ws-online: {e}");
            1
        }
    }
}

fn hash_password() -> i32 {
    let mut line = String::new();
    if let Err(e) = io::stdin().lock().read_line(&mut line) {
        eprintln!("ws-online: {e}");
        return 1;
    }
    let password = line.trim_end_matches('\n');
    if password.is_empty() {
        eprintln!("ws-online: empty password");
        return 2;
    }
    match auth::hash_password(password) {
        Ok(hash) => {
            println!("{hash}");
            0
        }
        Err(e) => {
            eprintln!("ws-online: {e}");
            1
        }
    }
}

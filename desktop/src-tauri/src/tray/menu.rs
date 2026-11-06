//! The tray icon and its menu (ADR 0050). Native and thin: only
//! `MenuAction::parse` is unit tested; the rest is checked by cargo build,
//! clippy, and by hand.

use super::view::{TrayGroup, TrayView};
use serde::Serialize;
use tauri::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tauri::tray::{TrayIcon, TrayIconBuilder};
use tauri::{AppHandle, Emitter, Manager, Wry};

/// The tray's id.
pub const TRAY_ID: &str = "main";
/// The event the frontend listens for to select an agent.
pub const SELECT_EVENT: &str = "tray-select";
/// The window the tray shows.
pub const MAIN_WINDOW: &str = "main";

/// What a menu item does, from its id.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MenuAction {
    /// Shows the main window.
    Open,
    /// Exits the app.
    Quit,
    /// Shows the main window and selects the agent with this key.
    Select(String),
}

impl MenuAction {
    /// `"open"`, `"quit"`, or `"agent:<key>"` with a non-empty key; any
    /// other id (headers, "and N more") is None.
    pub fn parse(id: &str) -> Option<Self> {
        match id {
            "open" => Some(Self::Open),
            "quit" => Some(Self::Quit),
            _ => id
                .strip_prefix("agent:")
                .filter(|key| !key.is_empty())
                .map(|key| Self::Select(key.to_string())),
        }
    }
}

/// The `tray-select` payload: `{"key": "<entry key>"}`.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TraySelect {
    /// The selected agent's entry key.
    pub key: String,
}

/// Keeps the tray alive: it's removed when its last handle drops.
pub struct TrayHandle(pub TrayIcon);

/// Builds the tray with an empty view and manages its handle.
pub fn create(app: &AppHandle) -> tauri::Result<()> {
    let view = super::view::tray_view(&[]);
    let mut builder = TrayIconBuilder::with_id(TRAY_ID)
        .tooltip(&view.tooltip)
        .menu(&build_menu(app, &view)?)
        .show_menu_on_left_click(true)
        .on_menu_event(on_menu_event);
    if let Some(icon) = app.default_window_icon() {
        builder = builder.icon(icon.clone());
    }
    let tray = builder.build(app)?;
    app.manage(TrayHandle(tray));
    Ok(())
}

/// Sets the title, tooltip, and menu from `view`. Errors are logged.
pub fn refresh(app: &AppHandle, view: &TrayView) {
    let Some(tray) = app.try_state::<TrayHandle>() else {
        return;
    };
    if let Err(err) = tray.0.set_title(view.title.as_deref()) {
        eprintln!("tray: setting the title failed: {err}");
    }
    if let Err(err) = tray.0.set_tooltip(Some(&view.tooltip)) {
        eprintln!("tray: setting the tooltip failed: {err}");
    }
    match build_menu(app, view) {
        Ok(menu) => {
            if let Err(err) = tray.0.set_menu(Some(menu)) {
                eprintln!("tray: setting the menu failed: {err}");
            }
        }
        Err(err) => eprintln!("tray: building the menu failed: {err}"),
    }
}

/// Shows, unminimizes, and focuses the main window. Errors are logged.
pub fn show_main(app: &AppHandle) {
    let Some(window) = app.get_webview_window(MAIN_WINDOW) else {
        return;
    };
    if let Err(err) = window.show() {
        eprintln!("tray: showing the window failed: {err}");
    }
    if let Err(err) = window.unminimize() {
        eprintln!("tray: unminimizing the window failed: {err}");
    }
    if let Err(err) = window.set_focus() {
        eprintln!("tray: focusing the window failed: {err}");
    }
}

fn build_menu(app: &AppHandle, view: &TrayView) -> tauri::Result<Menu<Wry>> {
    let menu = Menu::new(app)?;
    if !view.needs_input.items.is_empty() || view.needs_input.more > 0 {
        append_group(
            &menu,
            app,
            &view.needs_input,
            ("header:needs", "Needs input"),
            "more:needs",
        )?;
    }
    if !view.done.items.is_empty() || view.done.more > 0 {
        append_group(&menu, app, &view.done, ("header:done", "Done"), "more:done")?;
    }
    menu.append(&MenuItem::with_id(
        app,
        "open",
        "Open Workspaces",
        true,
        None::<&str>,
    )?)?;
    menu.append(&MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?)?;
    Ok(menu)
}

fn append_group(
    menu: &Menu<Wry>,
    app: &AppHandle,
    group: &TrayGroup,
    header: (&str, &str),
    more_id: &str,
) -> tauri::Result<()> {
    menu.append(&MenuItem::with_id(
        app,
        header.0,
        header.1,
        false,
        None::<&str>,
    )?)?;
    for item in &group.items {
        let id = format!("agent:{}", item.key);
        menu.append(&MenuItem::with_id(
            app,
            id,
            &item.label,
            true,
            None::<&str>,
        )?)?;
    }
    if group.more > 0 {
        let text = format!("and {} more", group.more);
        menu.append(&MenuItem::with_id(app, more_id, text, false, None::<&str>)?)?;
    }
    menu.append(&PredefinedMenuItem::separator(app)?)
}

fn on_menu_event(app: &AppHandle, event: MenuEvent) {
    match MenuAction::parse(event.id.as_ref()) {
        Some(MenuAction::Open) => show_main(app),
        Some(MenuAction::Quit) => app.exit(0),
        Some(MenuAction::Select(key)) => {
            show_main(app);
            if let Err(err) = app.emit_to(MAIN_WINDOW, SELECT_EVENT, TraySelect { key }) {
                eprintln!("tray: emitting the selection failed: {err}");
            }
        }
        None => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_reads_fixed_ids() {
        assert_eq!(MenuAction::parse("open"), Some(MenuAction::Open));
        assert_eq!(MenuAction::parse("quit"), Some(MenuAction::Quit));
    }

    #[test]
    fn parse_reads_agent_key_after_prefix() {
        assert_eq!(
            MenuAction::parse("agent:fix-login-1a2b3c4d"),
            Some(MenuAction::Select("fix-login-1a2b3c4d".to_string()))
        );
    }

    #[test]
    fn parse_rejects_empty_key_and_disabled_ids() {
        assert_eq!(MenuAction::parse("agent:"), None);
        assert_eq!(MenuAction::parse("header:needs"), None);
        assert_eq!(MenuAction::parse("more:done"), None);
        assert_eq!(MenuAction::parse(""), None);
    }

    #[test]
    fn select_payload_is_camel_case_json() {
        let json = serde_json::to_value(TraySelect { key: "k".into() }).unwrap();
        assert_eq!(json, serde_json::json!({"key": "k"}));
    }
}

//! GoHard for Zed (#65): starts `gohard lsp` for VHDL files.
//!
//! The VHDL language comes from Zed's "vhdl" extension. This extension
//! finds `gohard` on the worktree's PATH (or at `lsp.gohard.binary.path`)
//! and passes the user's `lsp.gohard.settings` to the server as its
//! workspace configuration. It sends no initialization options of its
//! own; Zed adds `lsp.gohard.initialization_options` when set.
//!
//! cmd/gohard's TestZedStdio starts the server with SERVER and ARGS read
//! from this file, so keep them as plain constants.

use zed_extension_api::{self as zed, settings::LspSettings, LanguageServerId, Result, Worktree};

/// The language server ID of extension.toml and the binary name.
const SERVER: &str = "gohard";

/// The arguments that start the language server over stdio.
const ARGS: &[&str] = &["lsp"];

struct GoHard;

impl zed::Extension for GoHard {
    fn new() -> Self {
        GoHard
    }

    fn language_server_command(
        &mut self,
        _id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<zed::Command> {
        let binary = LspSettings::for_worktree(SERVER, worktree)
            .ok()
            .and_then(|s| s.binary);
        let path = binary
            .as_ref()
            .and_then(|b| b.path.clone())
            .or_else(|| worktree.which(SERVER))
            .ok_or_else(|| {
                "gohard is not on PATH: install it (see \
                 https://github.com/connerohnesorge/gohard/blob/main/docs/editors.md) \
                 or set lsp.gohard.binary.path"
                    .to_string()
            })?;
        let args = binary
            .as_ref()
            .and_then(|b| b.arguments.clone())
            .unwrap_or_else(|| ARGS.iter().map(|a| a.to_string()).collect());
        let mut env = worktree.shell_env();
        if let Some(extra) = binary.and_then(|b| b.env) {
            env.extend(extra);
        }
        Ok(zed::Command {
            command: path,
            args,
            env,
        })
    }

    fn language_server_workspace_configuration(
        &mut self,
        _id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        Ok(LspSettings::for_worktree(SERVER, worktree)
            .ok()
            .and_then(|s| s.settings))
    }
}

zed::register_extension!(GoHard);

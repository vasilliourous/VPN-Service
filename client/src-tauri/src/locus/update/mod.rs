//! The Locus update path.
//!
//! Replaces the retired client's self-replacing binary and the upstream Tauri
//! updater's static manifest with a **hub-mediated, entitlement-aware,
//! per-platform** system that already exists on the server side.
//!
//! # The four pieces
//!
//! | Module | Responsibility |
//! |---|---|
//! | [`version`] | Is the advertised version actionable? Never string comparison. |
//! | [`signal`] | Decode what the hub advertised, for *this* platform. |
//! | [`apply`] | Download, verify SHA-256 (fail closed), stage privately. |
//! | [`install`] | Hand the verified bytes to the platform installer. |
//!
//! # What is deliberately not here
//!
//! - **A second config/apply path.** Config handling stays in
//!   `core::manager::config`.
//! - **Binary swapping.** The installer owns installation. The old client's
//!   hand-rolled swap destroyed an installation (FIXES #22), and an installed
//!   application has no need of it.
//! - **Rollout arithmetic.** The hub gates which clients *see* an update. The
//!   client does not compute the rollout; it either receives an offer or it does
//!   not. Duplicating that logic would mean two sources of truth for who gets
//!   what.

pub mod apply;
pub mod install;
pub mod signal;
pub mod version;

pub use apply::{DownloadedUpdate, UpdatePhase};
pub use signal::{Platform, SignalRejection, UpdateOffer, decode, decode_public_manifest};
pub use version::{compare, is_newer, rejection_reason};

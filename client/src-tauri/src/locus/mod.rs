//! Locus product logic.
//!
//! Everything in this module tree is ours. The rest of `src-tauri/src/` is
//! inherited from Clash Verge Rev, and the seam matters: Verge owns core
//! management, config generation and validation; this tree owns *entitlement*
//! (who is allowed to connect), *identity* (which device they are) and the
//! *update path*, which is hub-mediated rather than Tauri-plugin-mediated.
//!
//! The behavioural contract for every module here was paid for by the retired
//! Wails client (`legacy/wails-client/`) and is pinned by tests ported from it.
//! Read `client/docs/LOGIC-INVENTORY.md` before changing semantics, and keep
//! the wire names in [`contract`] as the single source of truth — several of
//! them are frozen by clients already in the field.

pub mod activation;
pub mod apply;
pub mod contract;
pub mod device;
pub mod expiry;
pub mod heartbeat;
pub mod runtime;
pub mod store;
pub mod tier;
pub mod update;

pub use contract::{HUB_URL, TierConfig};

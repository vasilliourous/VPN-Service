# Agent Guidelines — Locus client

Instructions for AI coding agents working in `client/`, the Locus desktop client.

> **`client/` is the Locus client, and it is functional.** It began as a copy of
> Clash Verge Rev v2.5.5 and now carries Locus logic in
> `src-tauri/src/locus/`: contract, activation, device fingerprint, heartbeat,
> tier→config, store, apply, runtime supervisor and the hub-mediated updater.
>
> The contract docs in `client/docs/` were written before the port and several
> now describe intent rather than the code. **Where a doc and the code disagree,
> the code wins and the doc gets fixed in the same change.**
>
> Two behaviours are load-bearing and easy to break:
>  * the generated proxy GROUP must not share a name with a proxy inside it —
>    mihomo rejects the whole config as a reference loop, and it is valid YAML,
>    so only running the engine catches it;
>  * the frozen wire names (`uot_port`, the `download_*`→`update_*` rename, the
>    `macos_*`/`darwin-*` artifact asymmetry) are contracts with the deployed hub
>    and published releases. Do not "tidy" them.
>
> Validation beyond unit tests: `cargo test`, then run the real sidecar against
> generated output (`verge-mihomo -t -f <config>`), because a config can pass
> every test and still be refused by the engine.

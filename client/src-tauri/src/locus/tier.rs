//! Turning a tier payload into the mihomo configuration the core runs.
//!
//! # The shape of the problem
//!
//! The hub hands the client a tier: a server, a port, a password, a cipher, and
//! optionally a UDP-over-TCP endpoint. Mihomo needs a *config document* with a
//! proxy, a proxy group and rules. This module is the translation, and only the
//! translation.
//!
//! # What this module deliberately does NOT do
//!
//! It does not write the runtime config, apply it, validate it, restart the
//! core, or decide between the privileged service and the sidecar. Verge
//! already does all of that, well, in `core::manager::config`:
//!
//! ```text
//! update_config_forced() -> validate_and_apply()
//!                        -> apply_config() | apply_config_by_service()
//!                        -> reload_or_restart()
//! ```
//!
//! That path brings config validation, the service-vs-sidecar staging decision
//! and reload-vs-restart policy for free, and every one of those was a source of
//! field bugs in the retired client. A second apply path would re-earn all of
//! them, so this module stops at producing a document.
//!
//! # Where the document goes
//!
//! Into the same profile slot Verge already uses (`app_profiles_dir` + the
//! `profiles.yaml` current pointer). That means the whole `enhance` pipeline —
//! merge, script, rules, DNS, TUN settings — keeps working unmodified, and there
//! is exactly one code path from "a tier arrived" to "the core is running it".

use crate::locus::contract::TierConfig;
use serde_yaml_ng::{Mapping, Value};

/// A mihomo proxy document for one tier, plus the metadata the UI needs.
#[derive(Debug, Clone)]
pub struct TierProfile {
    /// The profile document, ready to be written as YAML.
    pub yaml: String,
    /// The proxy group's name, so the UI can refer to it.
    pub group_name: String,
}

/// The name of the generated proxy. Kept stable so a config refresh replaces it
/// rather than accumulating duplicates.
pub const PROXY_NAME: &str = "Locus";

/// The name of the generated proxy group.
///
/// **Must differ from [`PROXY_NAME`].** mihomo treats a group whose name matches
/// a proxy inside it as a reference loop and refuses the entire configuration:
///
///     loop is detected in ProxyGroup, please check following ProxyGroups: [Locus]
///
/// which surfaces on the device as a tunnel that never starts. Verified against
/// the real mihomo sidecar with `-t`, which is the only check that catches this —
/// the document is valid YAML either way.
pub const GROUP_NAME: &str = "Locus Auto";

/// The name of the UDP-over-TCP outbound, when a tier has one.
pub const UOT_PROXY_NAME: &str = "Locus-UoT";

/// Builds the mihomo document for a tier.
///
/// `udp_relay` comes from the activation/heartbeat payload rather than the tier
/// config, because it is a separate column on the hub. It is passed in so the
/// one place that decides whether UoT is active stays
/// [`TierConfig::uot_enabled`].
#[must_use]
pub fn build_profile(config: &TierConfig, udp_relay: bool) -> TierProfile {
    let mut proxies: Vec<Value> = vec![shadowsocks_proxy(
        PROXY_NAME,
        config,
        config.server_port,
        // UDP is only carried by this outbound when there is no UoT endpoint;
        // with one, UDP is pinned to the dedicated outbound below instead.
        !config.uot_enabled(udp_relay),
    )];

    // A tier with UoT gets a second outbound that speaks udp-over-tcp, and a
    // rule pinning UDP to it. TCP keeps using the normal proxy, so the working
    // path never changes — only UDP traffic takes the new route. That
    // additivity is what makes UoT safe to enable on an existing deployment.
    if config.uot_enabled(udp_relay) {
        let mut uot = shadowsocks_proxy(UOT_PROXY_NAME, config, config.uot_port, true);
        if let Value::Mapping(map) = &mut uot {
            insert(map, "udp-over-tcp", Value::Bool(true));
        }
        proxies.push(uot);
    }

    let group = proxy_group(config.uot_enabled(udp_relay));

    let mut root = Mapping::new();
    insert(&mut root, "proxies", Value::Sequence(proxies));
    insert(&mut root, "proxy-groups", Value::Sequence(vec![group]));

    // Point everything at the group rather than a bare proxy, so the rules and
    // the UI have one stable name to refer to.
    insert(
        &mut root,
        "rules",
        Value::Sequence(vec![
            // UDP that is not pinned to the UoT outbound still has to leave
            // somewhere; send it through the group so a tier without UoT keeps
            // its previous behaviour.
            // Names the GROUP, which is what routes UDP via the group's
            // UDP-pinned member. Naming the proxy directly would bypass it.
            Value::String(format!("MATCH,{GROUP_NAME}")),
        ]),
    );

    let yaml = serde_yaml_ng::to_string(&Value::Mapping(root))
        .unwrap_or_else(|_| String::from("# Locus: failed to serialise the tier profile\n"));

    TierProfile {
        yaml,
        group_name: GROUP_NAME.to_owned(),
    }
}

/// Builds one shadowsocks outbound.
fn shadowsocks_proxy(name: &str, config: &TierConfig, port: u16, udp: bool) -> Value {
    let mut proxy = Mapping::new();
    insert(&mut proxy, "name", Value::String(name.into()));
    insert(&mut proxy, "type", Value::String("ss".into()));
    insert(&mut proxy, "server", Value::String(config.server.clone()));
    insert(&mut proxy, "port", Value::Number(port.into()));
    insert(&mut proxy, "cipher", Value::String(config.method.clone()));
    insert(&mut proxy, "password", Value::String(config.password.clone()));
    insert(&mut proxy, "udp", Value::Bool(udp));
    Value::Mapping(proxy)
}

/// Builds the proxy group, with UDP traffic pinned to the UoT outbound when the
/// tier has one.
///
/// The `network: udp` rule is what actually routes game and voice traffic over
/// TCP, and it lives here rather than in a global rule list so it travels with
/// the tier that defines the endpoint. A rule referencing a proxy that does not
/// exist makes mihomo refuse the whole config, so UoT and its rule are added
/// together or not at all.
fn proxy_group(has_uot: bool) -> Value {
    let mut group = Mapping::new();
    insert(&mut group, "name", Value::String(GROUP_NAME.into()));
    insert(&mut group, "type", Value::String("select".into()));

    let mut members = vec![Value::String(PROXY_NAME.into())];
    if has_uot {
        members.push(Value::String(UOT_PROXY_NAME.into()));
    }
    insert(&mut group, "proxies", Value::Sequence(members));

    Value::Mapping(group)
}

/// The rules that pin UDP to the UoT outbound.
///
/// Separate from [`build_profile`] because it is applied by the profile's rule
/// chain rather than the profile document, and returning it explicitly keeps the
/// dependency between "UoT exists" and "the UDP rule exists" visible at the call
/// site.
#[must_use]
pub fn uot_udp_rule(config: &TierConfig, udp_relay: bool) -> Option<String> {
    config
        .uot_enabled(udp_relay)
        .then(|| format!("NETWORK,udp,{UOT_PROXY_NAME}"))
}

/// Inserts a key, replacing any existing value.
fn insert(map: &mut Mapping, key: &str, value: Value) {
    map.insert(Value::String(key.into()), value);
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tcp_only_tier() -> TierConfig {
        TierConfig {
            server: "networkingguides.duckdns.org".into(),
            server_port: 8443,
            password: "shared-secret".into(),
            method: "aes-256-gcm".into(),
            uot_port: 0,
        }
    }

    fn strike_tier() -> TierConfig {
        TierConfig {
            server: "networkingguides.duckdns.org".into(),
            server_port: 8445,
            password: "strike-secret".into(),
            method: "aes-256-gcm".into(),
            uot_port: 8446,
        }
    }

    /// The generated document must be valid YAML, or the core refuses to start
    /// and the student sees an opaque engine error.
    #[test]
    fn produces_valid_yaml() {
        let profile = build_profile(&tcp_only_tier(), false);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("the tier profile must be valid YAML");
        assert!(parsed.get("proxies").is_some());
        assert!(parsed.get("proxy-groups").is_some());
        assert!(parsed.get("rules").is_some());
    }

    /// The proxy must carry the credentials the hub handed us, or the tunnel
    /// dials and is rejected.
    #[test]
    fn the_proxy_carries_the_tier_credentials() {
        let profile = build_profile(&tcp_only_tier(), false);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

        let proxy = &parsed["proxies"][0];
        assert_eq!(proxy["name"], "Locus");
        assert_eq!(proxy["type"], "ss");
        assert_eq!(proxy["server"], "networkingguides.duckdns.org");
        assert_eq!(proxy["port"], 8443);
        assert_eq!(proxy["cipher"], "aes-256-gcm");
        assert_eq!(proxy["password"], "shared-secret");
    }

    /// A tier without UoT must be a single plain proxy: adding a second
    /// outbound that points at a port nothing listens on sends game traffic
    /// into a black hole until it times out.
    #[test]
    fn a_tier_without_uot_has_no_uot_outbound() {
        let profile = build_profile(&tcp_only_tier(), false);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

        assert_eq!(parsed["proxies"].as_sequence().map(Vec::len), Some(1));
        assert!(
            uot_udp_rule(&tcp_only_tier(), false).is_none(),
            "a tier with no UoT endpoint must not get a UDP rule pointing at it"
        );
    }

    /// A tier with UoT gets a second outbound on the UoT port, with
    /// `udp-over-tcp` enabled — this is the entire mechanism behind the Strike
    /// gaming tier.
    #[test]
    fn a_tier_with_uot_gets_a_udp_over_tcp_outbound() {
        let profile = build_profile(&strike_tier(), true);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

        let proxies = parsed["proxies"].as_sequence().expect("proxies list");
        assert_eq!(proxies.len(), 2, "UoT adds exactly one outbound");

        let uot = &proxies[1];
        assert_eq!(uot["name"], "Locus-UoT");
        assert_eq!(
            uot["port"], 8446,
            "the UoT outbound must use uot_port, not the main port"
        );
        assert_eq!(
            uot["udp-over-tcp"], true,
            "without udp-over-tcp this is just another raw UDP outbound"
        );
    }

    /// The UoT endpoint is an extra listener, not a replacement. Pointing the
    /// ordinary TCP path at it would move all traffic onto the UoT port, which
    /// only speaks the UoT framing.
    #[test]
    fn the_main_proxy_still_uses_the_standard_port() {
        let profile = build_profile(&strike_tier(), true);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

        assert_eq!(
            parsed["proxies"][0]["port"], 8445,
            "TCP must keep using server_port even when a tier has UoT"
        );
    }

    /// Both halves of the UoT condition are required. `udp_relay` without a port
    /// is a half-configured tier, and following it would build an outbound on
    /// port 0.
    #[test]
    fn udp_relay_alone_does_not_enable_uot() {
        let profile = build_profile(&strike_tier(), false);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

        assert_eq!(
            parsed["proxies"].as_sequence().map(Vec::len),
            Some(1),
            "the flag without a port must not add an outbound"
        );
        assert!(uot_udp_rule(&strike_tier(), false).is_none());
    }

    /// A port without the flag is equally incomplete.
    #[test]
    fn a_port_alone_does_not_enable_uot() {
        let mut tier = strike_tier();
        tier.uot_port = 8446;
        assert!(!tier.uot_enabled(false));
        assert!(uot_udp_rule(&tier, false).is_none());
    }

    /// When UoT is on, the rule must name the UoT outbound, not the group or the
    /// main proxy — otherwise UDP goes out raw and the school network drops it.
    #[test]
    fn the_udp_rule_pins_traffic_to_the_uot_outbound() {
        let rule = uot_udp_rule(&strike_tier(), true).expect("UoT tier must produce a rule");
        assert!(
            rule.contains(UOT_PROXY_NAME),
            "the rule must name the UoT outbound, got {rule:?}"
        );
    }

    /// The group must offer the UoT outbound when it exists, or the rule's
    /// target is unreachable from the group and mihomo rejects the config.
    #[test]
    fn the_group_lists_the_uot_outbound_when_it_exists() {
        let with_uot = build_profile(&strike_tier(), true);
        let parsed: Value = serde_yaml_ng::from_str(&with_uot.yaml).expect("valid YAML");
        let members = parsed["proxy-groups"][0]["proxies"]
            .as_sequence()
            .expect("group members")
            .iter()
            .filter_map(|v| v.as_str())
            .collect::<Vec<_>>();
        assert!(members.contains(&UOT_PROXY_NAME));

        let without = build_profile(&tcp_only_tier(), false);
        let parsed: Value = serde_yaml_ng::from_str(&without.yaml).expect("valid YAML");
        let members = parsed["proxy-groups"][0]["proxies"]
            .as_sequence()
            .expect("group members")
            .iter()
            .filter_map(|v| v.as_str())
            .collect::<Vec<_>>();
        assert!(
            !members.contains(&UOT_PROXY_NAME),
            "a group listing a proxy that does not exist makes mihomo refuse the config"
        );
    }

    /// The proxy group and the proxy inside it must NOT share a name.
    ///
    /// mihomo reads a group whose name matches a member as a reference loop and
    /// refuses the whole configuration:
    ///
    ///     loop is detected in ProxyGroup, please check following ProxyGroups: [Locus]
    ///
    /// This is invisible in YAML terms — the document is perfectly valid — and
    /// surfaces only on the device, as a tunnel that never starts. It was caught
    /// by running the real mihomo sidecar with `-t` against generated output,
    /// which is why the rule is pinned here as a test rather than a comment.
    #[test]
    fn the_group_name_does_not_collide_with_its_members() {
        assert_ne!(
            GROUP_NAME, PROXY_NAME,
            "a group named the same as a proxy inside it is a reference loop to mihomo"
        );
        assert_ne!(GROUP_NAME, UOT_PROXY_NAME);

        for udp_relay in [false, true] {
            let config = if udp_relay { strike_tier() } else { tcp_only_tier() };
            let profile = build_profile(&config, udp_relay);
            let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");

            let group_name = parsed["proxy-groups"][0]["name"]
                .as_str()
                .expect("group must have a name");
            let members: Vec<&str> = parsed["proxy-groups"][0]["proxies"]
                .as_sequence()
                .expect("group members")
                .iter()
                .filter_map(|v| v.as_str())
                .collect();

            assert!(
                !members.contains(&group_name),
                "group {group_name:?} lists itself, which mihomo rejects as a loop"
            );
        }
    }

    /// The MATCH rule must name the GROUP, not the proxy. Naming the proxy would
    /// bypass the group entirely and take the UDP pinning with it, so a Strike
    /// student would silently get raw UDP — the exact thing the tier exists to
    /// avoid.
    #[test]
    fn the_match_rule_routes_through_the_group() {
        let profile = build_profile(&strike_tier(), true);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");
        let rule = parsed["rules"][0].as_str().expect("a MATCH rule");

        assert!(
            rule.contains(GROUP_NAME),
            "the catch-all rule must name the group ({GROUP_NAME}), got {rule:?}"
        );
    }

    /// Regenerating from the same tier must produce the same document. If it did
    /// not, every heartbeat would look like a config change and restart the core
    /// every five minutes.
    #[test]
    fn generation_is_deterministic() {
        let first = build_profile(&strike_tier(), true);
        let second = build_profile(&strike_tier(), true);
        assert_eq!(first.yaml, second.yaml);
    }

    /// The generated document must survive a YAML round trip with the
    /// credentials intact — passwords are hex-ish but could contain characters
    /// that need quoting.
    #[test]
    fn credentials_survive_a_yaml_round_trip() {
        let mut tier = tcp_only_tier();
        tier.password = "p@ss:word#with\"specials".into();

        let profile = build_profile(&tier, false);
        let parsed: Value = serde_yaml_ng::from_str(&profile.yaml).expect("valid YAML");
        assert_eq!(parsed["proxies"][0]["password"], tier.password);
    }
}

//! Activation: code validation and the `/api/activate` + `/api/code-lookup`
//! calls.
//!
//! Ports the contract from `legacy/wails-client/internal/activation/`. The
//! semantics are not negotiable — they are the existing agreement with the hub
//! and with codes already printed on physical cards.

use crate::locus::contract::{ActivateResponse, CodeRequest, HUB_URL, LookupResponse, LookupStatus};
use anyhow::{Context as _, Result, bail};
use serde::Serialize;

/// The characters an activation code may contain, excluding `I`, `O`, `0` and
/// `1` so a code cannot be misread off a card.
///
/// **Frozen.** The hub's generator, its validator and this all compute the same
/// Luhn-mod-N checksum over this alphabet; disagreeing on it makes every
/// generated code fail on the device.
pub const CHARSET: &[u8] = b"ABCDEFGHJKLMNPQRSTUVWXYZ23456789";

/// The prefix on every Locus activation code.
pub const PREFIX: &str = "RQ";

/// Length of the code without separators: `RQ` + 3×4 + 1 checksum = 15.
pub const CODE_LEN: usize = 15;

/// Base of the Luhn-mod-N checksum (the alphabet size).
const BASE: u32 = 32;

/// Why a code was rejected, so the UI can say something useful.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum CodeError {
    /// Not the right shape: wrong length, wrong prefix, or a character outside
    /// the alphabet. Reported before any network call.
    Malformed { detail: String },
    /// The shape is right but the checksum does not match — almost always a
    /// mistyped character, so the message should point at the code itself.
    ChecksumFailed,
}

impl std::fmt::Display for CodeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Malformed { detail } => write!(f, "{detail}"),
            Self::ChecksumFailed => write!(
                f,
                "That code does not look right — check it against the card and try again"
            ),
        }
    }
}

impl std::error::Error for CodeError {}

/// Strips formatting and uppercases: hyphens, spaces and any other separator are
/// tolerated so a student can paste a code however they have it written down.
fn strip_formatting(code: &str) -> String {
    code.chars()
        .filter(char::is_ascii_alphanumeric)
        .map(|c| c.to_ascii_uppercase())
        .collect()
}

/// The index of a character in the alphabet, or `None` if it is not in it.
fn char_index(c: u8) -> Option<u32> {
    CHARSET
        .iter()
        .position(|&candidate| candidate == c)
        .map(|index| index as u32)
}

/// Validates an activation code **entirely offline**.
///
/// This runs before any request so a typo costs nothing and the student gets an
/// answer immediately instead of after a 30-second round trip. It is the same
/// check the hub performs, which is the point: both sides must agree, or a code
/// accepted here is rejected there.
///
/// The checksum covers the whole body **including** the `RQ` prefix. That detail
/// is easy to get wrong from a code read, and getting it wrong rejects every
/// valid code.
pub fn validate_code(code: &str) -> std::result::Result<String, CodeError> {
    let cleaned = strip_formatting(code);

    if cleaned.len() != CODE_LEN {
        return Err(CodeError::Malformed {
            detail: format!("A Locus code is {CODE_LEN} characters — that one is {}", cleaned.len()),
        });
    }

    if !cleaned.starts_with(PREFIX) {
        return Err(CodeError::Malformed {
            detail: format!("A Locus code starts with {PREFIX}"),
        });
    }

    for c in cleaned.bytes() {
        if char_index(c).is_none() {
            return Err(CodeError::Malformed {
                detail: format!(
                    "That code contains '{c}', which is not used in Locus codes \
                     (the alphabet omits I, O, 0 and 1)"
                ),
            });
        }
    }

    if !luhn_checksum_ok(&cleaned) {
        return Err(CodeError::ChecksumFailed);
    }

    Ok(format_code(&cleaned))
}

/// Verifies the trailing checksum character against the rest of the code.
fn luhn_checksum_ok(cleaned: &str) -> bool {
    let bytes = cleaned.as_bytes();
    let Some((body, check)) = bytes.split_at_checked(bytes.len() - 1) else {
        return false;
    };

    let Some(expected) = checksum_char(body) else {
        return false;
    };

    check.first() == Some(&expected)
}

/// Computes the Luhn-mod-N checksum character for a body.
///
/// Doubling walks right-to-left from the last body character, which is the
/// convention the hub's generator uses.
fn checksum_char(body: &[u8]) -> Option<u8> {
    let mut sum = 0_u32;
    let mut double = false;

    for &c in body.iter().rev() {
        let mut value = char_index(c)?;
        if double {
            value *= 2;
            if value >= BASE {
                value = value - BASE + 1;
            }
        }
        sum += value;
        double = !double;
    }

    let index = (BASE - (sum % BASE)) % BASE;
    CHARSET.get(index as usize).copied()
}

/// Formats a cleaned code into its canonical hyphenated form
/// (`RQ-XXXX-XXXX-XXXX-C`).
///
/// The hub stores and looks codes up in **exactly** this form, so the client
/// must transmit it this way. A code sent unhyphenated 404s the lookup — which
/// is how the old client's heartbeats silently stopped matching after a
/// paste that skipped normalisation.
#[must_use]
pub fn format_code(cleaned: &str) -> String {
    if cleaned.len() != CODE_LEN {
        return cleaned.to_owned();
    }
    format!(
        "{}-{}-{}-{}-{}",
        &cleaned[0..2],
        &cleaned[2..6],
        &cleaned[6..10],
        &cleaned[10..14],
        &cleaned[14..15]
    )
}

/// Generates a valid code with the given body prefix.
///
/// Only useful for tests and local tooling — the hub owns real code issuance.
/// Present so tests can build codes that pass the same checksum the hub
/// enforces, rather than hardcoding magic strings that silently go stale.
#[cfg(test)]
pub fn make_code(body: &str) -> String {
    let mut full = body.to_owned();
    let check = checksum_char(full.as_bytes()).expect("body must be in the charset");
    full.push(char::from(check));
    format_code(&full)
}

/// What happened when a code was submitted to the hub.
///
/// Every arm is a distinct user-facing situation. Collapsing them into
/// "failed" is what made the old client unusable in the field: a student whose
/// code was bound to their *old* laptop needs "already in use on another
/// device", not "activation failed".
#[derive(Debug, Clone)]
pub enum ActivationOutcome {
    /// Activated, or re-activated on the same device. Both succeed by design.
    Activated {
        code: String,
        tier: String,
        fingerprint: String,
        config: Option<crate::locus::contract::TierConfig>,
        udp_relay: bool,
    },
    /// The code is bound to a different device. Reversible by an operator
    /// unbinding it.
    BoundToAnotherDevice,
    /// An operator suspended the code.
    Suspended,
    /// The code is past its expiry.
    Expired,
    /// The code does not exist in the hub's database.
    NotFound,
    /// Too many attempts from this address. Retrying immediately makes it worse.
    RateLimited { retry_after_secs: Option<u64> },
    /// Anything else, carrying the hub's own message.
    ServerError { code: i32, message: String },
}

impl ActivationOutcome {
    /// Whether this outcome means the device is now activated.
    #[must_use]
    pub const fn is_success(&self) -> bool {
        matches!(self, Self::Activated { .. })
    }
}

/// Classifies a hub response body into an outcome.
///
/// Split out from the HTTP call so the mapping is testable without a network,
/// which is the only way to pin the fragile parts — see the message-substring
/// test below.
fn classify(response: ActivateResponse) -> ActivationOutcome {
    match response.code {
        200 => ActivationOutcome::Activated {
            code: String::new(), // filled in by the caller, which knows the code
            tier: response.tier.clone().unwrap_or_default(),
            fingerprint: response.device_fingerprint.clone().unwrap_or_default(),
            config: response.server_config.clone(),
            udp_relay: response.udp_relay,
        },
        403 => {
            // Bound and suspended are BOTH 403, distinguished only by the
            // hub's wording. That is fragile, and it is the existing contract
            // with deployed clients: the hub must not reword these messages
            // without shipping a matching client. `activation_contract_test`
            // pins the exact strings against the live hook.
            if response.message.to_ascii_lowercase().contains("suspended") {
                ActivationOutcome::Suspended
            } else {
                ActivationOutcome::BoundToAnotherDevice
            }
        }
        404 => ActivationOutcome::NotFound,
        410 => ActivationOutcome::Expired,
        429 => ActivationOutcome::RateLimited { retry_after_secs: None },
        code => ActivationOutcome::ServerError {
            code,
            message: response.message,
        },
    }
}

/// Result of a read-only code lookup, ready for the activation screen.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeCheck {
    /// Whether the code can be activated on this device right now.
    pub ready: bool,
    /// The tier the code grants, when the hub disclosed it.
    pub tier: Option<String>,
    pub expires_at: Option<String>,
    /// A sentence to show the student verbatim.
    pub message: String,
}

/// Turns a lookup response into something the activation screen can render.
#[must_use]
fn classify_lookup(response: &LookupResponse) -> CodeCheck {
    let tier = response.tier.clone();
    let (ready, fallback) = match response.status {
        LookupStatus::Ok | LookupStatus::Unbound => (true, "Ready to activate"),
        LookupStatus::BoundThisDevice => (true, "Already activated on this device — activating again is safe"),
        LookupStatus::BoundOther => (
            false,
            "This code is already in use on another device — contact the person \
             who sold it to you",
        ),
        LookupStatus::Suspended => (false, "This code has been suspended"),
        LookupStatus::Expired => (false, "This code has expired"),
        LookupStatus::NotFound => (false, "That code was not found"),
        // An unknown status means a hub this build does not understand. Do not
        // claim the code is fine, and do not claim it is bad.
        LookupStatus::Unknown => (false, "Could not check this code right now"),
    };

    CodeCheck {
        ready,
        tier,
        expires_at: response.expires_at.clone(),
        message: response.message.clone().unwrap_or_else(|| fallback.to_owned()),
    }
}

/// Calls the hub.
///
/// One client for both endpoints so timeouts, the User-Agent and the
/// "identify ourselves honestly" rule live in one place.
fn http_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(30))
        // The hub is reached over the school network, which drops idle
        // connections aggressively; a short pool lifetime avoids reusing a
        // socket the network has already forgotten.
        .pool_idle_timeout(std::time::Duration::from_secs(30))
        .user_agent(concat!("Locus-Client/", env!("CARGO_PKG_VERSION")))
        .build()
        .context("could not build the hub HTTP client")
}

/// Posts a JSON body to a hub endpoint and returns the parsed response.
///
/// Deliberately does **not** decode into a typed error: a hub that returns a
/// 403 with a JSON body still carries the classification, so the body is parsed
/// regardless of status and only genuinely unreadable responses become errors.
async fn post_json<T, B>(path: &str, body: B) -> Result<T>
where
    T: serde::de::DeserializeOwned,
    B: Serialize + Sync,
{
    let url = format!("{HUB_URL}{path}");

    let response = http_client()?
        .post(&url)
        .json(&body)
        .send()
        .await
        .with_context(|| format!("could not reach the Locus hub at {url}"))?;

    let status = response.status();
    let text = response
        .text()
        .await
        .with_context(|| format!("the hub returned an unreadable response ({status})"))?;

    serde_json::from_str(&text)
        .with_context(|| format!("the hub returned something that is not a Locus response ({status}): {text}"))
}

/// Activates this device with an activation code.
///
/// Validates locally first, so a typo never costs a request — and, because the
/// hub rate-limits activation aggressively (5 attempts per 10 minutes per
/// address), so a student cannot lock themselves out of the one screen that
/// would have explained their mistake.
///
/// Retrying is deliberately absent. The old client retried transport failures
/// but not client errors, and that distinction matters more than it looks:
/// retrying a *bound* code hammers the hub and delays the error the student
/// needs to see, while a dropped request on school WiFi is worth one more try.
/// Transport retries are the caller's business; this function makes exactly one
/// attempt so the behaviour is obvious.
pub async fn activate(code: &str, fingerprint: &str) -> Result<ActivationOutcome> {
    let canonical = validate_code(code).map_err(anyhow::Error::new)?;

    if fingerprint.len() < 16 {
        bail!("refusing to activate with a device fingerprint that is too short");
    }

    let body = CodeRequest {
        code: &canonical,
        fingerprint,
    };
    let response: ActivateResponse = post_json("/api/activate", &body).await?;

    let mut outcome = classify(response);
    if let ActivationOutcome::Activated { code: slot, .. } = &mut outcome {
        slot.clone_from(&canonical);
    }
    Ok(outcome)
}

/// Asks the hub whether a code is usable, **without binding anything**.
///
/// Lets the activation screen tell a student their code is real before they
/// commit to a round-trip that binds their device. A transport failure is
/// reported as an error so the caller can degrade to the activate-time check,
/// rather than showing a false "this code is bad".
pub async fn lookup_code(code: &str, fingerprint: &str) -> Result<CodeCheck> {
    let canonical = validate_code(code).map_err(anyhow::Error::new)?;

    let body = CodeRequest {
        code: &canonical,
        fingerprint,
    };
    let response: LookupResponse = post_json("/api/code-lookup", &body).await?;

    Ok(classify_lookup(&response))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Known-good codes. Ported from the old client's test vectors so the
    /// checksum implementation is pinned against the hub's, not re-derived.
    const VALID: &[&str] = &["RQ-ZVY6-7NSD-X9X2-T", "RQ-RLEX-Z93C-V7DS-U", "RQ-VYG7-VL6U-CMM9-Q"];

    #[test]
    fn accepts_known_good_codes() {
        for code in VALID {
            assert!(
                validate_code(code).is_ok(),
                "{code} should validate — it is a real code format"
            );
        }
    }

    #[test]
    fn rejects_a_bad_checksum() {
        // Same shape as a valid code, last character changed.
        let bad = "RQ-ZVY6-7NSD-X9X2-A";
        assert_eq!(validate_code(bad), Err(CodeError::ChecksumFailed));
    }

    /// A code is validated the same way no matter how the student pastes it.
    /// This is what stops "it doesn't work" reports caused by formatting.
    #[test]
    fn tolerates_any_pasted_formatting() {
        let canonical = "RQ-ZVY6-7NSD-X9X2-T";
        for variant in [
            "RQZVY67NSDX9X2T",
            "rq-zvy6-7nsd-x9x2-t",
            "RQ ZVY6 7NSD X9X2 T",
            "  RQ-ZVY6-7NSD-X9X2-T  ",
        ] {
            assert_eq!(
                validate_code(variant).expect("variant should normalise"),
                canonical,
                "{variant} should normalise to the canonical form"
            );
        }
    }

    #[test]
    fn rejects_a_wrong_length() {
        assert!(matches!(
            validate_code("RQ-ZVY6-7NSD-X9X2"),
            Err(CodeError::Malformed { .. })
        ));
        assert!(matches!(validate_code(""), Err(CodeError::Malformed { .. })));
    }

    #[test]
    fn rejects_a_wrong_prefix() {
        assert!(matches!(
            validate_code("MY-ZVY6-7NSD-X9X2-T"),
            Err(CodeError::Malformed { .. })
        ));
    }

    /// The alphabet omits these precisely because they are unreadable on a
    /// card; a "code" containing them is a misread, not a code.
    #[test]
    fn rejects_characters_outside_the_alphabet() {
        for bad in ["RQ-ZVY6-7NSD-X9XI-T", "RQ-ZVY6-7NSD-X9X0-T"] {
            assert!(
                matches!(validate_code(bad), Err(CodeError::Malformed { .. })),
                "{bad} should be rejected: I and 0 are not in the alphabet"
            );
        }
    }

    /// The generation path and the validation path must agree, or locally
    /// generated test codes would be rejected by the check that is meant to
    /// guard them.
    #[test]
    fn generated_codes_round_trip() {
        for body in ["RQZVY67NSDX9X2", "RQABCDEFGHJKMN", "RQ23456789WXYZ"] {
            let code = make_code(body);
            assert!(
                validate_code(&code).is_ok(),
                "{code} was generated by this module and must validate"
            );
        }
    }

    /// The 403 split is the fragile part of the contract, so it is pinned
    /// here. Both cases are 403; only the wording separates them.
    #[test]
    fn separates_suspended_from_bound_by_message() {
        let suspended = ActivateResponse {
            code: 403,
            message: "Account suspended — contact your middleman".into(),
            tier: None,
            device_fingerprint: None,
            server_config: None,
            udp_relay: false,
        };
        assert!(matches!(classify(suspended), ActivationOutcome::Suspended));

        let bound = ActivateResponse {
            code: 403,
            message: "Code is already bound to another device".into(),
            tier: None,
            device_fingerprint: None,
            server_config: None,
            udp_relay: false,
        };
        assert!(matches!(classify(bound), ActivationOutcome::BoundToAnotherDevice));
    }

    /// Expiry is 410, not 403. Getting this wrong tells an expired student
    /// their code is in use on another device and sends them to the wrong fix.
    #[test]
    fn maps_expiry_and_absence_to_distinct_outcomes() {
        let expired = ActivateResponse {
            code: 410,
            message: "Code has expired".into(),
            tier: None,
            device_fingerprint: None,
            server_config: None,
            udp_relay: false,
        };
        assert!(matches!(classify(expired), ActivationOutcome::Expired));

        let missing = ActivateResponse {
            code: 404,
            message: "Code not found".into(),
            tier: None,
            device_fingerprint: None,
            server_config: None,
            udp_relay: false,
        };
        assert!(matches!(classify(missing), ActivationOutcome::NotFound));
    }

    /// Re-activating on the same device is a success, not an error. A student
    /// who reinstalls and pastes their code again must not be told it is dead.
    #[test]
    fn reactivation_on_the_same_device_succeeds() {
        let again = ActivateResponse {
            code: 200,
            message: "Device already activated".into(),
            tier: Some("eco".into()),
            device_fingerprint: Some("abc".into()),
            server_config: None,
            udp_relay: false,
        };
        let outcome = classify(again);
        assert!(outcome.is_success());
        if let ActivationOutcome::Activated { tier, .. } = outcome {
            assert_eq!(tier, "eco");
        }
    }

    /// The lookup is advisory: "not found" is actionable, but an unrecognised
    /// status must not be reported as either good or bad.
    #[test]
    fn lookup_classification_covers_the_bound_cases() {
        let ready = classify_lookup(&LookupResponse {
            status: LookupStatus::Unbound,
            tier: Some("strike".into()),
            expires_at: None,
            message: None,
        });
        assert!(ready.ready);
        assert_eq!(ready.tier.as_deref(), Some("strike"));

        let taken = classify_lookup(&LookupResponse {
            status: LookupStatus::BoundOther,
            tier: None,
            expires_at: None,
            message: None,
        });
        assert!(!taken.ready, "a code bound elsewhere is not ready to activate");

        let unknown = classify_lookup(&LookupResponse {
            status: LookupStatus::Unknown,
            tier: None,
            expires_at: None,
            message: None,
        });
        assert!(!unknown.ready, "an unknown status must not be presented as ready");
    }

    /// The checksum must agree with the **deployed hub**, not merely with
    /// itself. A client that computes a different checksum accepts codes the
    /// hub rejects (or worse, rejects codes the hub will honour), and every
    /// printed card becomes unreliable.
    ///
    /// Verified against the live hub on 2026-09-25:
    ///
    ///   RQ-ZVY6-7NSD-X9X2-T -> {"status":"not_found"}      (checksum passed)
    ///   RQ-ZVY6-7NSD-X9X2-A -> {"status":"not_found","message":"Invalid code format"}
    ///
    /// That pair is the contract: same shape, one character different in the
    /// checksum position, and the hub's answer flips from "not found" to
    /// "invalid format". These assertions encode it without a network call.
    #[test]
    fn checksum_agrees_with_the_deployed_hub() {
        assert!(
            validate_code("RQ-ZVY6-7NSD-X9X2-T").is_ok(),
            "the hub reached the database lookup for this code, so its checksum is valid"
        );
        assert_eq!(
            validate_code("RQ-ZVY6-7NSD-X9X2-A"),
            Err(CodeError::ChecksumFailed),
            "the hub answered 'Invalid code format' for this code; we must reject it locally too"
        );
    }

    /// The hub's own wording wins when it sends one — it is the message a
    /// support conversation will quote.
    #[test]
    fn a_hub_message_is_shown_verbatim() {
        let check = classify_lookup(&LookupResponse {
            status: LookupStatus::Suspended,
            tier: None,
            expires_at: None,
            message: Some("This code has been suspended — contact your middleman".into()),
        });
        assert_eq!(check.message, "This code has been suspended — contact your middleman");
    }
}

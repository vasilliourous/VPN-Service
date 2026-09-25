use crate::core::{
    handle,
    validate::{ValidationErrorKind, ValidationOutcome},
};
use clash_verge_logging::{Type, logging};

/// The notice key for a validation failure.
///
/// A `target` parameter and its enum (`Runtime`/`Merge`/`Script`) were removed:
/// only the runtime config has been validated since the per-profile chain editors
/// went with the profile UI, so every caller passed the same value. The merge and
/// script notice keys remain in the i18n bundles and are still reachable through
/// the `ScriptSyntax`/`ScriptMissingMain` error kinds.
const fn notice_key(kind: ValidationErrorKind) -> &'static str {
    match kind {
        ValidationErrorKind::FileMissing => "config_validate::file_not_found",
        ValidationErrorKind::FileRead => "config_validate::yaml_read_error",
        ValidationErrorKind::YamlSyntax => "config_validate::yaml_syntax_error",
        ValidationErrorKind::YamlMapping => "config_validate::yaml_mapping_error",
        ValidationErrorKind::ScriptSyntax => "config_validate::script_syntax_error",
        ValidationErrorKind::ScriptMissingMain => "config_validate::script_missing_main",
        ValidationErrorKind::ProcessTerminated => "config_validate::process_terminated",
        ValidationErrorKind::CoreRejected | ValidationErrorKind::Timeout => "config_validate::error",
    }
}

pub fn handle_validation_notice(outcome: &ValidationOutcome, file_type: &str) {
    match outcome {
        ValidationOutcome::Invalid { kind, message } => {
            let status = notice_key(*kind);
            logging!(warn, Type::Config, "{} 验证失败: {}", file_type, message);
            handle::Handle::notice_message(status, message.to_owned());
        }
        ValidationOutcome::Busy | ValidationOutcome::Skipped { .. } => {
            let message = outcome.to_string();
            logging!(warn, Type::Config, "{} 验证跳过: {}", file_type, message);
            handle::Handle::notice_message("config_validate::error", message);
        }
        ValidationOutcome::Valid => {}
    }
}

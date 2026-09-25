use super::{CmdResult, StringifyErr as _, WithErrorCode as _, coded_error};
use crate::cmd::validate::{ValidationNoticeTarget, handle_validation_notice};
use crate::config::profiles;
use crate::utils::window_manager::WindowManager;
use crate::{
    config::{
        Config, IProfiles, PrfItem, PrfOption,
        profiles::{PROFILE_WRITE_LOCK, profiles_patch_item_safe, profiles_save_file_safe},
    },
    core::{CoreManager, handle, timer::Timer, validate::ValidationOutcome},
    feat,
};
use clash_verge_draft::{Draft, SharedDraft};
use clash_verge_logging::{Type, logging, logging_error};
use scopeguard::defer;
use smartstring::alias::String;
use std::sync::atomic::{AtomicBool, Ordering};

static CURRENT_SWITCHING_PROFILE: AtomicBool = AtomicBool::new(false);

#[tauri::command]
pub async fn get_profiles() -> CmdResult<SharedDraft<IProfiles>> {
    let draft = Config::profiles().await;
    let data = draft.data_arc();
    Ok(data)
}

#[tauri::command]
pub async fn enhance_profiles() -> CmdResult<ValidationOutcome> {
    match feat::enhance_profiles().await {
        Ok(outcome) if outcome.is_valid() => {
            handle::Handle::refresh_clash();
            Ok(outcome)
        }
        Ok(outcome) => {
            logging!(
                warn,
                Type::Cmd,
                "Reactivate profiles command failed validation: {}",
                outcome
            );
            handle_validation_notice(&outcome, ValidationNoticeTarget::Runtime, "运行时配置");
            Ok(outcome)
        }
        Err(e) => {
            logging!(error, Type::Cmd, "enhance profiles failed: {e:#}");
            Err(coded_error("PROFILE_ENHANCE_FAILED", e))
        }
    }
}

#[tauri::command]
pub async fn update_profile(index: String, option: Option<PrfOption>) -> CmdResult {
    match feat::update_profile(&index, option.as_ref(), true).await {
        Ok(_) => Ok(()),
        Err(e) => {
            logging!(error, Type::Cmd, "update profile failed: {e:#}");
            Err(coded_error("PROFILE_UPDATE_FAILED", e))
        }
    }
}

async fn restore_previous_profile(prev_profile: &String) -> CmdResult<()> {
    logging!(info, Type::Cmd, "尝试恢复到之前的配置: {}", prev_profile);
    let restore_profiles = IProfiles {
        current: Some(prev_profile.to_owned()),
        items: None,
    };
    Config::profiles()
        .await
        .edit_draft(|d| d.patch_config(&restore_profiles));
    Config::profiles().await.apply();
    crate::process::AsyncHandler::spawn(|| async move {
        if let Err(e) = profiles_save_file_safe().await {
            logging!(warn, Type::Cmd, "异步保存恢复配置文件失败: {e:#}");
        }
    });
    Ok(())
}

async fn commit_current_profile(profiles: &Draft<IProfiles>, current: Option<String>) -> anyhow::Result<()> {
    profiles.discard();
    let Some(current) = current else {
        return Ok(());
    };

    profiles
        .with_data_modify(|mut committed| async move {
            committed.patch_config(&IProfiles {
                current: Some(current),
                items: None,
            });
            Ok((committed, ()))
        })
        .await
}

async fn handle_success(current_value: Option<&String>) -> CmdResult<ValidationOutcome> {
    commit_current_profile(&Config::profiles().await, current_value.cloned())
        .await
        .stringify_err()?;
    // Runtime refresh and tray rebuilding happen after saved node selections are restored.
    profiles::activate_selected_nodes();

    if let Err(e) = profiles_save_file_safe().await {
        logging!(warn, Type::Cmd, "异步保存配置文件失败: {e:#}");
    }

    if let Some(current) = current_value
        && WindowManager::get_main_window().is_some()
    {
        handle::Handle::notify_profile_changed(current);
    }

    logging_error!(Type::Config, Config::sync_dns_override().await);
    Ok(ValidationOutcome::Valid)
}

async fn discard_and_restore(current_profile: Option<&String>) -> CmdResult<()> {
    Config::profiles().await.discard();
    if let Some(prev_profile) = current_profile {
        restore_previous_profile(prev_profile).await?;
    }
    Ok(())
}

async fn handle_validation_failure(
    outcome: ValidationOutcome,
    current_profile: Option<&String>,
) -> CmdResult<ValidationOutcome> {
    logging!(warn, Type::Cmd, "配置验证失败: {}", outcome);
    discard_and_restore(current_profile).await?;
    handle_validation_notice(&outcome, ValidationNoticeTarget::Runtime, "运行时配置");
    Ok(outcome)
}

async fn handle_update_error<E: std::fmt::Display>(
    e: E,
    current_profile: Option<&String>,
) -> CmdResult<ValidationOutcome> {
    logging!(warn, Type::Cmd, "更新过程发生错误: {}", e,);
    discard_and_restore(current_profile).await?;
    let message: String = e.to_string().into();
    handle::Handle::notice_message("config_validate::boot_error", message.clone());
    Ok(ValidationOutcome::invalid_from_message(message))
}

async fn run_profile_config_update_transition<Update, UpdateFuture>(
    update_config: Update,
) -> anyhow::Result<ValidationOutcome>
where
    Update: FnOnce() -> UpdateFuture,
    UpdateFuture: std::future::Future<Output = anyhow::Result<ValidationOutcome>>,
{
    update_config().await
}

async fn perform_config_update(
    current_value: Option<&String>,
    current_profile: Option<&String>,
) -> CmdResult<ValidationOutcome> {
    defer! {
        CURRENT_SWITCHING_PROFILE.store(false, Ordering::Release);
    }
    let update_result = run_profile_config_update_transition(|| CoreManager::global().update_config_forced()).await;

    match update_result {
        Ok(outcome) if outcome.is_valid() => handle_success(current_value).await,
        Ok(outcome) => handle_validation_failure(outcome, current_profile).await,
        Err(e) => handle_update_error(e, current_profile).await,
    }
}

#[tauri::command]
#[tracing::instrument(skip_all, level = "info", fields(target = ?profiles.current))]
pub async fn patch_profiles_config(profiles: IProfiles) -> CmdResult<ValidationOutcome> {
    if CURRENT_SWITCHING_PROFILE
        .compare_exchange(false, true, Ordering::Acquire, Ordering::Relaxed)
        .is_err()
    {
        logging!(info, Type::Cmd, "当前正在切换配置，放弃请求");
        return Ok(ValidationOutcome::Busy);
    }
    let _profile_write_guard = PROFILE_WRITE_LOCK.lock().await;

    let target_profile = profiles.current.as_ref();

    let previous_profile = Config::profiles().await.data_arc().current.clone();

    Config::profiles().await.edit_draft(|d| d.patch_config(&profiles));

    perform_config_update(target_profile, previous_profile.as_ref())
        .await
        .map_err(|error| coded_error("PROFILE_SWITCH_FAILED", error))
}

pub async fn patch_profiles_config_by_profile_index(profile_index: String) -> CmdResult<ValidationOutcome> {
    let profiles = IProfiles {
        current: Some(profile_index),
        items: None,
    };
    patch_profiles_config(profiles).await
}

#[tauri::command]
pub async fn patch_profile(index: String, profile: PrfItem) -> CmdResult {
    let profiles = Config::profiles().await;
    let should_refresh_timer = if let Ok(old_profile) = profiles.latest_arc().get_item(&index)
        && let Some(new_option) = profile.option.as_ref()
    {
        let old_interval = old_profile.option.as_ref().and_then(|o| o.update_interval);
        let new_interval = new_option.update_interval;
        let old_allow_auto_update = old_profile.option.as_ref().and_then(|o| o.allow_auto_update);
        let new_allow_auto_update = new_option.allow_auto_update;
        (old_interval != new_interval) || (old_allow_auto_update != new_allow_auto_update)
    } else {
        false
    };

    // Prevent an in-flight restore from overwriting a newer UI or chain selection.
    let records_a_selection = profile.selected.is_some();

    profiles_patch_item_safe(&index, &profile)
        .await
        .with_error_code("PROFILE_UPDATE_FAILED")?;

    if records_a_selection {
        profiles::supersede_selected_activation();
    }

    if should_refresh_timer {
        crate::process::AsyncHandler::spawn(move || async move {
            logging!(info, Type::Timer, "Timer update settings changed, refreshing timer...");
            if let Err(e) = crate::core::Timer::global().refresh().await {
                logging!(error, Type::Timer, "Failed to refresh timer: {e:#}");
            } else {
                crate::core::handle::Handle::notify_timer_updated(&index);
            }
        });
    }

    Ok(())
}

#[tauri::command]
pub async fn get_next_update_time(uid: String) -> CmdResult<Option<i64>> {
    let timer = Timer::global();
    let next_time = timer.get_next_update_time(&uid).await;
    Ok(next_time)
}

#[cfg(test)]
mod tests {
    use super::{commit_current_profile, run_profile_config_update_transition};
    use crate::config::{IProfiles, PrfItem};
    use crate::core::validate::ValidationOutcome;
    use clash_verge_draft::Draft;
    use std::{
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        },
        task::Poll,
        time::Duration,
    };
    use tokio::sync::Barrier;

    struct CancellationProbe {
        cancelled: Arc<AtomicBool>,
        completed: Arc<AtomicBool>,
    }

    impl Drop for CancellationProbe {
        fn drop(&mut self) {
            if !self.completed.load(Ordering::Acquire) {
                self.cancelled.store(true, Ordering::Release);
            }
        }
    }

    fn profile(uid: &str) -> PrfItem {
        PrfItem {
            uid: Some(uid.into()),
            ..PrfItem::default()
        }
    }

    #[tokio::test]
    async fn committing_profile_switch_preserves_profiles_added_after_draft_creation() -> anyhow::Result<()> {
        let profiles = Draft::new(IProfiles {
            current: Some("a".into()),
            items: Some(vec![profile("a"), profile("b")]),
        });
        profiles.edit_draft(|draft| {
            draft.patch_config(&IProfiles {
                current: Some("b".into()),
                items: None,
            });
        });
        profiles
            .with_data_modify(|mut committed| async move {
                committed.items.get_or_insert_with(Vec::new).push(profile("new"));
                Ok((committed, ()))
            })
            .await?;

        commit_current_profile(&profiles, Some("b".into())).await?;

        let committed = profiles.data_arc();
        assert_eq!(committed.current.as_deref(), Some("b"));
        assert!(committed.get_item("new").is_ok());
        Ok(())
    }

    #[tokio::test(start_paused = true)]
    async fn profile_config_update_runs_past_former_deadline_without_cancellation() -> anyhow::Result<()> {
        let update_started = Arc::new(Barrier::new(2));
        let release_update = Arc::new(Barrier::new(2));
        let update_cancelled = Arc::new(AtomicBool::new(false));
        let update_completed = Arc::new(AtomicBool::new(false));

        let mut update = Box::pin(run_profile_config_update_transition({
            let update_started = Arc::clone(&update_started);
            let release_update = Arc::clone(&release_update);
            let update_cancelled = Arc::clone(&update_cancelled);
            let update_completed = Arc::clone(&update_completed);
            move || async move {
                let _probe = CancellationProbe {
                    cancelled: update_cancelled,
                    completed: Arc::clone(&update_completed),
                };
                update_started.wait().await;
                release_update.wait().await;
                update_completed.store(true, Ordering::Release);
                Ok(ValidationOutcome::Valid)
            }
        }));

        assert!(matches!(futures::poll!(update.as_mut()), Poll::Pending));
        update_started.wait().await;
        tokio::time::advance(Duration::from_secs(31)).await;

        assert!(matches!(futures::poll!(update.as_mut()), Poll::Pending));
        assert!(!update_cancelled.load(Ordering::Acquire));

        release_update.wait().await;
        assert!(update.await?.is_valid());
        assert!(update_completed.load(Ordering::Acquire));
        assert!(!update_cancelled.load(Ordering::Acquire));
        Ok(())
    }
}

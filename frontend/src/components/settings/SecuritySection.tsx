import { useState } from 'react';
import { EyeOff, Fingerprint, KeyRound, Lock, ScanFace, ShieldCheck, TriangleAlert } from 'lucide-react';
import { SetMasterPassword, TestBiometrics } from '../../../wailsjs/go/main/App';
import { Section, Row, Segmented, Toggle } from './SettingsDialog';
import { Select } from '../ui/Select';
import { errorText, useDialogs } from '../ui/Dialogs';
import { MasterPasswordDialog } from '../security/MasterPasswordDialog';
import { TrustedHostsDialog } from '../security/TrustedHostsDialog';
import { confirmAgentForwarding } from '../security/securityPrompts';
import { AUTO_LOCK_OPTIONS, CLEAR_AFTER_OPTIONS, updateSecurity } from '../../lib/settings';
import type { AppSettings, AutoLockMinutes, ClearAfterSeconds, Osc52Policy } from '../../lib/settings';
import { useCaptureProtection } from '../../lib/captureProtection';
import { lockNow, refreshLockStatus, useAppLock } from '../../lib/appLock';

export function SecuritySection({ settings, modKey }: { settings: AppSettings; modKey: string }) {
  const sec = settings.security;
  const dialogs = useDialogs();
  const lock = useAppLock();
  const protection = useCaptureProtection();
  const [passwordDialog, setPasswordDialog] = useState(false);
  const [trustedHosts, setTrustedHosts] = useState(false);
  const bioKind = lock.biometrics.kind || 'biometrics';
  const BioIcon = lock.biometrics.kind === 'Touch ID' ? Fingerprint : ScanFace;

  const removePassword = async () => {
    const current = await dialogs.prompt({
      title: 'Turn off app lock',
      label: 'Enter your master password to remove it:',
      password: true,
      confirmLabel: 'Remove password',
    });
    if (current === null) return;
    try {
      await SetMasterPassword(current, '');
      updateSecurity('lock', { biometrics: false });
      await refreshLockStatus();
      dialogs.toast('App lock turned off', 'success');
    } catch (e) {
      dialogs.toast(errorText(e), 'error');
    }
  };

  const setBiometrics = async (on: boolean) => {
    if (!on) return updateSecurity('lock', { biometrics: false });
    // Run the prompt once so a missing or misconfigured sensor shows up now,
    // not on the lock screen.
    try {
      await TestBiometrics();
      updateSecurity('lock', { biometrics: true });
      dialogs.toast(`${bioKind} unlock is on`, 'success');
    } catch (e) {
      const msg = errorText(e);
      if (!/cancel/i.test(msg)) dialogs.toast(msg, 'error');
    }
  };

  const setAgentForwarding = async (on: boolean) => {
    if (on && !(await confirmAgentForwarding(dialogs, ''))) return;
    updateSecurity('ssh', { allowAgentForwarding: on });
  };

  return (
    <div className="space-y-6">
      {/* ── App lock ── */}
      <Section
        title="App lock"
        action={
          lock.configured && (
            <button className="btn-ghost h-7 text-[12px]" onClick={() => void lockNow()}>
              <Lock size={12} /> Lock now
            </button>
          )
        }
      >
        <Row
          label="Master password"
          hint={
            lock.configured
              ? 'Set. Terminyxx starts locked and can lock itself; open sessions stay connected while locked. The lock hides the app but does not encrypt files on disk; saved passwords live in the OS keychain.'
              : 'Set one to lock Terminyxx: terminals, hosts and the sidebar are hidden until you unlock. The lock does not encrypt files on disk; saved passwords live in the OS keychain.'
          }
        >
          <div className="flex gap-2">
            {lock.configured && (
              <button className="btn-ghost h-8 px-3 text-[12.5px] hover:text-red-300" onClick={removePassword}>
                Remove
              </button>
            )}
            <button className="btn-secondary h-8" onClick={() => setPasswordDialog(true)}>
              <KeyRound size={13} /> {lock.configured ? 'Change…' : 'Set password…'}
            </button>
          </div>
        </Row>
        <Disabled when={!lock.configured}>
          <Row
            label={`Unlock with ${bioKind}`}
            hint={
              lock.biometrics.available
                ? `Use ${bioKind} instead of typing the master password. The password still works as a fallback.`
                : lock.biometrics.reason || 'Not available on this system.'
            }
          >
            <span className="flex items-center gap-2">
              <BioIcon size={14} className="text-gray-500" />
              <Toggle checked={sec.lock.biometrics && lock.biometrics.available} onChange={(v) => void setBiometrics(v)} />
            </span>
          </Row>
          <Row label="Auto-lock" hint="Lock after this long without keyboard or mouse input. Terminal output doesn't count as activity.">
            <Select
              className="w-36"
              value={String(sec.lock.autoLockMinutes)}
              options={AUTO_LOCK_OPTIONS.map((m) => ({ value: String(m), label: m === 0 ? 'Never' : m === 1 ? '1 minute' : `${m} minutes` }))}
              onChange={(v) => updateSecurity('lock', { autoLockMinutes: Number(v) as AutoLockMinutes })}
            />
          </Row>
          <Row label="Lock when minimized or asleep" hint="Locks as soon as the window is minimized or the computer wakes from sleep.">
            <Toggle checked={sec.lock.lockOnMinimize} onChange={(v) => updateSecurity('lock', { lockOnMinimize: v })} />
          </Row>
          <Row label="Lock shortcut" hint="Plain Ctrl+L stays with the shell (clear screen).">
            <Keys keys={[modKey, '⇧', 'L']} />
          </Row>
        </Disabled>
      </Section>

      {/* ── Clipboard ── */}
      <Section title="Clipboard">
        <Row label="Clear clipboard automatically" hint="After you copy from Terminyxx. It is cleared only if it still holds what you copied, so later copies are never wiped.">
          <Toggle checked={sec.clipboard.autoClear} onChange={(v) => updateSecurity('clipboard', { autoClear: v })} />
        </Row>
        <Disabled when={!sec.clipboard.autoClear}>
          <Row label="Clear after">
            <Segmented
              value={String(sec.clipboard.clearAfterSeconds)}
              options={CLEAR_AFTER_OPTIONS.map((s) => ({ value: String(s), label: `${s} s` }))}
              onChange={(v) => updateSecurity('clipboard', { clearAfterSeconds: Number(v) as ClearAfterSeconds })}
            />
          </Row>
          <Row label="What to clear" hint="Sensitive: passwords, tokens, keys and other text that looks like a secret.">
            <Segmented
              value={sec.clipboard.scope}
              options={[
                { value: 'sensitive', label: 'Sensitive text' },
                { value: 'all', label: 'Everything' },
              ]}
              onChange={(v) => updateSecurity('clipboard', { scope: v })}
            />
          </Row>
        </Disabled>
        <Row label="Remote clipboard access (OSC 52)" hint="Lets programs on the server (tmux, vim, neovim) copy to your clipboard. Reading your clipboard is never allowed.">
          <Segmented<Osc52Policy>
            value={sec.osc52}
            options={[
              { value: 'disabled', label: 'Off' },
              { value: 'prompt', label: 'Ask' },
              { value: 'write', label: 'Allow copy' },
            ]}
            onChange={(v) => updateSecurity('osc52', v)}
          />
        </Row>
      </Section>

      {/* ── Paste protection ── */}
      <Section title="Paste protection">
        <Row label="Confirm multi-line pastes" hint="Each line break runs a command. Shows the exact text before it is sent.">
          <Toggle checked={sec.paste.warnMultiline} onChange={(v) => updateSecurity('paste', { warnMultiline: v })} />
        </Row>
        <Row label="Warn about dangerous commands" hint="rm -rf, mkfs, dd, fork bombs, shutdown/reboot, chmod -R 777, curl | sh… and hidden control characters (pastejacking).">
          <Toggle checked={sec.paste.warnDangerous} onChange={(v) => updateSecurity('paste', { warnDangerous: v })} />
        </Row>
      </Section>

      {/* ── Presentation mode ── */}
      <Section title="Presentation mode">
        <Row
          label="Mask secrets in terminal output"
          hint="Values of PASSWORD=, TOKEN=, API_KEY=, Authorization headers, private keys and well-known token formats are replaced with ••• before they are displayed."
        >
          <span className="flex items-center gap-2">
            <EyeOff size={14} className="text-gray-500" />
            <Toggle checked={sec.masking.enabled} onChange={(v) => updateSecurity('masking', { enabled: v })} />
          </span>
        </Row>
        <Disabled when={!sec.masking.enabled}>
          <Row label="Also mask IP addresses" hint="IPv4 and IPv6 addresses, including in window titles.">
            <Toggle checked={sec.masking.maskIPs} onChange={(v) => updateSecurity('masking', { maskIPs: v })} />
          </Row>
        </Disabled>
        <Row label="Toggle shortcut" hint="Also in the status bar. Applies to new output; text already on screen is not changed.">
          <Keys keys={[modKey, '⇧', 'M']} />
        </Row>
      </Section>

      {/* ── SSH ── */}
      <Section title="SSH">
        <Row label="Modern algorithms only" hint="Disables SHA-1 (ssh-rsa, diffie-hellman-*-sha1), CBC and other legacy ciphers. Old devices can be allowed per host.">
          <Toggle checked={sec.ssh.modernAlgorithmsOnly} onChange={(v) => updateSecurity('ssh', { modernAlgorithmsOnly: v })} />
        </Row>
        <Row label="Allow agent forwarding" hint="Off by default. When on, hosts with “Forward SSH agent” enabled in their settings get access to your agent.">
          <Toggle checked={sec.ssh.allowAgentForwarding} onChange={(v) => void setAgentForwarding(v)} />
        </Row>
        <Row label="Trusted hosts" hint="Host keys you have verified (known_hosts). Changed keys are blocked with a warning.">
          <button className="btn-secondary h-8" onClick={() => setTrustedHosts(true)}>
            <ShieldCheck size={13} /> Manage…
          </button>
        </Row>
      </Section>

      {/* ── Screen sharing ── */}
      <Section title="Screen sharing">
        <Row
          label="Hide from screen sharing"
          hint="Keeps the Terminyxx window out of screen sharing, recordings and screenshots (Zoom, Teams, Meet, OBS…), so terminal output, hosts and passwords you type don't leak during a call. You still see the window normally; others see it as blank or not at all."
        >
          <Toggle checked={sec.hideFromCapture} onChange={(v) => updateSecurity('hideFromCapture', v)} />
        </Row>
        {sec.hideFromCapture && protection.error && (
          <div className="flex items-start gap-2 mt-2 rounded-lg bg-amber-400/10 px-3 py-2 text-2xs text-amber-300">
            <TriangleAlert size={13} className="flex-shrink-0 mt-px" />
            <span>This system didn't allow hiding the window, so it may still be visible when you share your screen. ({protection.error})</span>
          </div>
        )}
        <p className="text-2xs text-gray-500 mt-2 max-w-lg">
          While this is on, the status bar shows a notice when screen sharing is detected (on Windows: Zoom, Teams, Meet and other browser calls, Discord,
          OBS…). macOS can't detect sharing, so there the notice is always shown, and on macOS 15 and later some capture apps may ignore this flag.
        </p>
      </Section>

      {passwordDialog && (
        <MasterPasswordDialog
          configured={lock.configured}
          onClose={() => setPasswordDialog(false)}
          onDone={() => {
            setPasswordDialog(false);
            void refreshLockStatus();
            dialogs.toast(lock.configured ? 'Master password changed' : 'App lock is on', 'success');
          }}
        />
      )}
      {trustedHosts && <TrustedHostsDialog onClose={() => setTrustedHosts(false)} />}
    </div>
  );
}

function Disabled({ when, children }: { when: boolean; children: React.ReactNode }) {
  return <div className={when ? 'opacity-40 pointer-events-none select-none' : ''} aria-disabled={when}>{children}</div>;
}

function Keys({ keys }: { keys: string[] }) {
  return (
    <span className="flex gap-1">
      {keys.map((k, i) => (
        <span key={i} className="kbd px-1.5">
          {k}
        </span>
      ))}
    </span>
  );
}

import { useState } from 'react';
import type { RDPOptions, RDPProfile } from '../../types';
import { Modal } from '../ui/Dialogs';
import { Field, ProtocolSwitch, Toggle } from '../terminal/ConnectDialog';
import { DEFAULT_RDP_OPTIONS } from '../../lib/profiles';
import { SelectFolder } from '../../../wailsjs/go/main/App';
import { useSettings } from '../../lib/settings';

interface RdpDialogProps {
  initial?: Partial<RDPProfile>;
  onClose: () => void;
  onSave: (profile: RDPProfile) => void;
  /** Shown for a brand-new host: switches to the SSH dialog, carrying over what was typed. */
  onSwitchToSsh?: (carry: { host: string; folderId: string }) => void;
}

const RESOLUTIONS: [number, number][] = [
  [1280, 720],
  [1600, 900],
  [1920, 1080],
  [2560, 1440],
];

const AUDIO: { id: RDPOptions['audio']; label: string }[] = [
  { id: 'local', label: 'Play here' },
  { id: 'remote', label: 'Play on remote' },
  { id: 'off', label: 'Off' },
];

const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);

export function RdpDialog({ initial, onClose, onSave, onSwitchToSsh }: RdpDialogProps) {
  const [name, setName] = useState(initial?.name ?? '');
  const [host, setHost] = useState(initial?.host ?? '');
  const [port, setPort] = useState(initial?.port ?? 3389);
  const [username, setUsername] = useState(initial?.username ?? '');
  const [domain, setDomain] = useState(initial?.domain ?? '');
  // With the built-in client, options it doesn't support are hidden (their
  // saved values stay and still apply when the host is opened in the
  // system client).
  const builtin = useSettings().rdp.builtin;
  const [password, setPassword] = useState('');
  const [clearPassword, setClearPassword] = useState(false);
  const [width, setWidth] = useState(initial?.width ?? 1920);
  const [height, setHeight] = useState(initial?.height ?? 1080);
  const [fullscreen, setFullscreen] = useState(initial?.fullscreen ?? false);
  const [opts, setOpts] = useState<RDPOptions>({ ...DEFAULT_RDP_OPTIONS, ...initial?.options });
  const hasPassword = !!initial?.hasPassword && !clearPassword;

  const set = <K extends keyof RDPOptions>(key: K, value: RDPOptions[K]) => setOpts((o) => ({ ...o, [key]: value }));

  const addFolder = async () => {
    try {
      const dir = await SelectFolder();
      if (dir && !opts.drivePaths.includes(dir)) set('drivePaths', [...opts.drivePaths, dir]);
    } catch {
      /* dialog cancelled */
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onSave({
      id: initial?.id ?? crypto.randomUUID(),
      name: name.trim() || host.trim(),
      host: host.trim(),
      port: Number(port) || 3389,
      username: username.trim(),
      domain: domain.trim(),
      password,
      clearPassword,
      hasPassword: hasPassword || !!password,
      width: Number(width),
      height: Number(height),
      fullscreen,
      folderId: initial?.folderId,
      sortOrder: initial?.sortOrder,
      options: { ...opts, gateway: opts.gateway.trim(), drives: opts.drives && opts.drivePaths.length > 0 },
    });
  };

  return (
    <Modal title="RDP profile" subtitle="Opens in a tab with the built-in client, or in the system's client (Settings → Terminal)." onClose={onClose}>
      <form onSubmit={handleSubmit} className="px-5 pb-5 pt-1 space-y-4 max-h-[70vh] overflow-y-auto">
        {onSwitchToSsh && !initial?.id && <ProtocolSwitch value="rdp" onChange={() => onSwitchToSsh({ host: host.trim(), folderId: initial?.folderId ?? '' })} />}
        <div className="grid grid-cols-[1fr_88px] gap-2">
          <Field label="Host">
            <input className="input-field font-mono" value={host} onChange={(e) => setHost(e.target.value)} required autoFocus spellCheck={false} />
          </Field>
          <Field label="Port">
            <input className="input-field font-mono" type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} />
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <Field label="Username">
            <input className="input-field" value={username} onChange={(e) => setUsername(e.target.value)} />
          </Field>
          <Field label="Domain">
            <input className="input-field" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="optional" />
          </Field>
        </div>
        <Field
          label="Password"
          hint={
            isMac
              ? 'Stored in the Keychain and used by the built-in client. Windows App (system client) asks for it itself.'
              : 'Stored in the OS keychain. Used by the built-in client and handed to Remote Desktop Connection when you open the system client.'
          }
        >
          <input
            className="input-field"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={hasPassword ? '•••••••• (saved, leave empty to keep)' : 'optional'}
            autoComplete="new-password"
          />
        </Field>
        {hasPassword && !password && (
          <button type="button" className="text-2xs text-gray-500 hover:text-gray-300 -mt-2" onClick={() => setClearPassword(true)}>
            Remove saved password
          </button>
        )}
        <Field label="Name">
          <input className="input-field" value={name} onChange={(e) => setName(e.target.value)} placeholder={host || 'Office PC'} />
        </Field>
        <div>
          <span className="label">Resolution</span>
          <div className="flex flex-wrap gap-1.5">
            {RESOLUTIONS.map(([w, h]) => {
              const active = !fullscreen && w === width && h === height;
              return (
                <button
                  key={w}
                  type="button"
                  disabled={fullscreen}
                  onClick={() => {
                    setWidth(w);
                    setHeight(h);
                  }}
                  className={`h-7 px-2.5 rounded-lg text-2xs font-mono transition-colors disabled:opacity-40 ${
                    active ? 'bg-accent/15 text-accent ring-1 ring-inset ring-accent/40' : 'bg-white/[0.04] text-gray-400 hover:text-gray-200'
                  }`}
                >
                  {w}×{h}
                </button>
              );
            })}
          </div>
        </div>
        <div className="space-y-2.5">
          <Toggle checked={fullscreen} onChange={setFullscreen} label="Full screen" />
          <Toggle checked={opts.dynamicRes} onChange={(v) => set('dynamicRes', v)} label="Resize the remote desktop with the window" />
          {!builtin && <Toggle checked={opts.smartSizing} onChange={(v) => set('smartSizing', v)} label="Scale the remote desktop to the window" />}
          {!builtin && !isMac && <Toggle checked={opts.multiMon} onChange={(v) => set('multiMon', v)} label="Use all monitors" />}
          {!builtin && !isMac && <Toggle checked={opts.admin} onChange={(v) => set('admin', v)} label="Connect to the admin (console) session" />}
        </div>

        <div className="border-t border-white/[0.06] pt-3 space-y-2.5">
          <span className="label">Local resources</span>
          <Toggle checked={opts.clipboard} onChange={(v) => set('clipboard', v)} label="Clipboard" />
          {!builtin && (
            <>
              <Toggle checked={opts.printers} onChange={(v) => set('printers', v)} label="Printers" />
              <Toggle checked={opts.smartCards} onChange={(v) => set('smartCards', v)} label="Smart cards" />
              <Toggle checked={opts.comPorts} onChange={(v) => set('comPorts', v)} label="Serial (COM) ports" />
              <Toggle checked={opts.microphone} onChange={(v) => set('microphone', v)} label="Microphone" />
              {!isMac && <Toggle checked={opts.camera} onChange={(v) => set('camera', v)} label="Cameras" />}
              {!isMac && <Toggle checked={opts.usb} onChange={(v) => set('usb', v)} label="USB devices" />}
            </>
          )}
          <div>
            <span className="label">Audio</span>
            <div className="flex gap-1.5">
              {AUDIO.filter((a) => !builtin || a.id !== 'remote').map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => set('audio', a.id)}
                  className={`h-7 px-2.5 rounded-lg text-2xs transition-colors ${
                    opts.audio === a.id ? 'bg-accent/15 text-accent ring-1 ring-inset ring-accent/40' : 'bg-white/[0.04] text-gray-400 hover:text-gray-200'
                  }`}
                >
                  {a.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        {!builtin && (
        <div className="border-t border-white/[0.06] pt-3 space-y-2.5">
          <span className="label">Shared folders and drives</span>
          <Toggle checked={opts.allDrives} onChange={(v) => set('allDrives', v)} label="Share all local drives" />
          {!opts.allDrives && (
            <div className="space-y-1.5">
              {opts.drivePaths.map((p) => (
                <div key={p} className="flex items-center gap-2 rounded-lg bg-white/[0.04] px-2.5 h-7">
                  <span className="flex-1 truncate font-mono text-2xs text-gray-300" title={p}>
                    {p}
                  </span>
                  <button type="button" className="text-gray-500 hover:text-gray-200" aria-label={`Stop sharing ${p}`} onClick={() => set('drivePaths', opts.drivePaths.filter((x) => x !== p))}>
                    ×
                  </button>
                </div>
              ))}
              <button type="button" className="btn-ghost h-7 px-2.5 text-2xs" onClick={addFolder}>
                Add folder…
              </button>
              {isMac && <p className="text-2xs text-gray-600">Folder sharing on macOS is configured in Windows App itself.</p>}
            </div>
          )}
        </div>
        )}

        {!builtin && !isMac && (
          <Field label="RD Gateway" hint="Optional gateway host, e.g. gw.example.com">
            <input className="input-field font-mono" value={opts.gateway} onChange={(e) => set('gateway', e.target.value)} spellCheck={false} />
          </Field>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button type="button" className="btn-ghost h-8 px-3" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn-primary">
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

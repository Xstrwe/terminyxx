import { Box, Copy, EyeOff, ScrollText, FolderTree, Lock, Plug, RotateCw, ShieldAlert, ShieldCheck, Unplug } from 'lucide-react';
import { showCaptureNotice, useCaptureProtection } from '../../lib/captureProtection';
import type { TerminalTab } from '../../types';
import { TunnelsPopover } from '../terminal/TunnelsPopover';
import { RecordButton } from '../recordings/RecordButton';
import { HostMonitor } from './HostMonitor';

interface StatusBarProps {
  tab: TerminalTab | null;
  size: { cols: number; rows: number } | null;
  sftpOpen: boolean;
  openTabs: number;
  onToggleSftp: () => void;
  onDisconnect: () => void;
  onReconnect: () => void;
  onOpenSecurity: () => void;
  /** Presentation mode: secrets in output are masked. */
  masking: boolean;
  onToggleMasking: () => void;
  lockConfigured: boolean;
  onLock: () => void;
  onOpenSnippets: () => void;
  onOpenRecordings: () => void;
  onOpenContainers: () => void;
  /** "Type in all panes" state for the visible tab. */
  broadcast: { enabled: boolean; count: number };
  onToggleBroadcast: () => void;
}

const LABEL: Record<TerminalTab['status'], string> = {
  idle: 'Not connected',
  connecting: 'Connecting…',
  connected: 'Connected',
  disconnected: 'Disconnected',
};

export function StatusBar({ tab, size, sftpOpen, openTabs, onToggleSftp, onDisconnect, onReconnect, onOpenSecurity, masking, onToggleMasking, lockConfigured, onLock, onOpenSnippets, onOpenRecordings, onOpenContainers, broadcast, onToggleBroadcast }: StatusBarProps) {
  const protection = useCaptureProtection();
  const status = tab?.status;
  // SFTP-only and RDP panes have no terminal: hide the terminal-only items.
  const sftpOnly = !!tab?.profile?.sftpOnly || !!tab?.rdp;
  const dot =
    status === 'connected'
      ? 'bg-emerald-400 shadow-[0_0_6px_rgb(52_211_153/0.8)]'
      : status === 'connecting'
        ? 'bg-amber-400 animate-pulse'
        : status === 'disconnected'
          ? 'bg-red-400'
          : 'bg-gray-600';

  return (
    <footer className="flex items-center gap-3 h-7 px-3 flex-shrink-0 bg-app text-2xs text-gray-500 select-none">
      {tab ? (
        <>
          <span className="flex items-center gap-1.5 text-gray-400">
            <span className={`w-1.5 h-1.5 rounded-full ${dot}`} />
            {LABEL[tab.status]}
          </span>
          {tab.stream && <span className="font-mono text-gray-500 truncate">{tab.stream.kind === 'telnet' ? `${tab.stream.host}:${tab.stream.port}` : `${tab.stream.device} · ${tab.stream.baud} ${tab.stream.dataBits}${tab.stream.parity[0].toUpperCase()}${tab.stream.stopBits}`}</span>}
          {tab.rdp && (
            <span className="font-mono text-gray-500 truncate">
              RDP · {tab.rdp.username ? `${tab.rdp.username}@` : ''}{tab.rdp.host}
              {tab.rdp.port && tab.rdp.port !== 3389 ? `:${tab.rdp.port}` : ''}
            </span>
          )}
          {tab.profile && (
            <span className="font-mono text-gray-500 truncate">
              {tab.profile.username}@{tab.profile.host}
              {tab.profile.port !== 22 && `:${tab.profile.port}`}
            </span>
          )}
          {status === 'connected' && (
            <button className="btn-ghost h-5 px-1.5 text-2xs" onClick={onDisconnect} title="Disconnect">
              <Unplug size={11} /> Disconnect
            </button>
          )}
          {(status === 'disconnected' || status === 'idle') && (
            <button className="btn-ghost h-5 px-1.5 text-2xs text-accent hover:text-accent-hover" onClick={onReconnect}>
              {tab.profile || tab.stream || tab.rdp ? <RotateCw size={11} /> : <Plug size={11} />} {tab.profile || tab.stream || tab.rdp ? 'Reconnect' : 'Connect'}
            </button>
          )}
        </>
      ) : (
        <span>{openTabs === 0 ? 'Ready' : ''}</span>
      )}

      <div className="flex-1" />

      {showCaptureNotice(protection) && (
        <button
          className={`flex items-center gap-1.5 h-5 px-2 rounded-md transition-colors min-w-0 ${
            protection.error ? 'text-amber-300 hover:bg-amber-400/10' : 'text-emerald-300/90 hover:bg-emerald-400/10'
          }`}
          onClick={onOpenSecurity}
          title={
            protection.error
              ? `Could not hide the window: ${protection.error}`
              : protection.sharing
                ? `Screen sharing detected (${protection.sharingApps.join(', ')}). Terminyxx is hidden from it. Click to change this in Settings.`
                : 'The window is hidden from screen sharing. Click to change this in Settings.'
          }
        >
          {protection.error ? <ShieldAlert size={11} className="flex-shrink-0" /> : <ShieldCheck size={11} className="flex-shrink-0" />}
          <span className="truncate">
            {protection.error ? 'Screen sharing protection failed' : protection.sharing ? 'Screen sharing detected · Terminyxx is hidden' : 'Hidden from screen sharing'}
            <span className="text-gray-500"> · you can turn this off in Settings</span>
          </span>
        </button>
      )}

      <button
        className={`flex items-center gap-1 h-5 px-2 rounded-md transition-colors flex-shrink-0 ${
          masking ? 'bg-amber-400/15 text-amber-300 hover:bg-amber-400/25' : 'hover:bg-white/[0.06] hover:text-gray-300'
        }`}
        onClick={onToggleMasking}
        title={masking ? 'Secrets in terminal output are masked. Click to show them (Ctrl+Shift+M)' : 'Mask secrets in terminal output (Ctrl+Shift+M)'}
        aria-pressed={masking}
      >
        <EyeOff size={11} />
        {masking && 'Masking secrets'}
      </button>
      {broadcast.count > 1 && (
        <button
          className={`flex items-center gap-1 h-5 px-2 rounded-md transition-colors flex-shrink-0 ${
            broadcast.enabled ? 'bg-amber-400/15 text-amber-300 hover:bg-amber-400/25' : 'hover:bg-white/[0.06] hover:text-gray-300'
          }`}
          onClick={onToggleBroadcast}
          title={broadcast.enabled ? `Input goes to all ${broadcast.count} panes. Click to stop (Ctrl+Shift+G)` : 'Type in all panes at once (Ctrl+Shift+G)'}
          aria-pressed={broadcast.enabled}
        >
          <Copy size={11} />
          {broadcast.enabled && `Typing in ${broadcast.count} panes`}
        </button>
      )}
      <button className="flex items-center h-5 px-1.5 rounded-md transition-colors hover:bg-white/[0.06] hover:text-gray-300 flex-shrink-0" onClick={onOpenSnippets} title="Snippets (Ctrl+Shift+S)">
        <ScrollText size={11} />
      </button>
      {lockConfigured && (
        <button className="flex items-center h-5 px-1.5 rounded-md transition-colors hover:bg-white/[0.06] hover:text-gray-300 flex-shrink-0" onClick={onLock} title="Lock Terminyxx (Ctrl+Shift+L)">
          <Lock size={11} />
        </button>
      )}

      {status === 'connected' && size && !sftpOnly && (
        <span className="font-mono tabular-nums">
          {size.cols}×{size.rows}
        </span>
      )}
      {tab && !sftpOnly && <span>UTF-8</span>}
      {tab && !sftpOnly && <span>xterm-256color</span>}
      {tab && status === 'connected' && tab.profile && !sftpOnly && <HostMonitor tab={tab} />}
      {tab && status === 'connected' && tab.profile && !sftpOnly && (
        <button className="flex items-center h-5 px-1.5 rounded-md transition-colors hover:bg-white/[0.06] hover:text-gray-300 flex-shrink-0" onClick={onOpenContainers} title="Containers: Docker, Podman, Kubernetes">
          <Box size={11} />
        </button>
      )}
      {tab && status === 'connected' && !sftpOnly && <RecordButton tab={tab} onOpenLibrary={onOpenRecordings} />}
      {tab && status === 'connected' && tab.profile && <TunnelsPopover tab={tab} />}
      {status === 'connected' && !tab?.stream && !sftpOnly && (
        <button
          className={`flex items-center gap-1 h-5 px-2 rounded-md transition-colors ${
            sftpOpen ? 'bg-accent/15 text-accent' : 'hover:bg-white/[0.06] hover:text-gray-300'
          }`}
          onClick={onToggleSftp}
          title="Toggle SFTP (Ctrl+Shift+F)"
        >
          <FolderTree size={11} /> SFTP
        </button>
      )}
    </footer>
  );
}

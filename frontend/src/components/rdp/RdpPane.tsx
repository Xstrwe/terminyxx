import { useEffect, useRef, useState } from 'react';
import { Minimize2, Unplug } from 'lucide-react';
import type { IronError, UserInteraction } from '@devolutions/iron-remote-desktop';
import { RDPClose, RDPLastError } from '../../../wailsjs/go/main/App';
import type { main } from '../../../wailsjs/go/models';
import { RdpAudioPlayer } from '../../lib/rdpAudio';

type RdpModule = typeof import('@devolutions/iron-remote-desktop-rdp');

// The engine (IronRDP compiled to WebAssembly, ~5 MB) loads on first use.
let engine: Promise<RdpModule> | null = null;
function loadEngine(): Promise<RdpModule> {
  engine ??= (async () => {
    const [rdp] = await Promise.all([
      import('@devolutions/iron-remote-desktop-rdp'),
      import('@devolutions/iron-remote-desktop'), // registers <iron-remote-desktop>
    ]);
    await rdp.init('WARN');
    return rdp;
  })();
  engine.catch(() => (engine = null));
  return engine;
}

function isIronError(e: unknown): e is IronError {
  return !!e && typeof (e as IronError).kind === 'function' && typeof (e as IronError).backtrace === 'function';
}

// IronErrorKind values (the enum is declared, not exported at runtime).
const KIND_WRONG_PASSWORD = 1;
const KIND_LOGON_FAILURE = 2;
const KIND_ACCESS_DENIED = 3;
const KIND_RDCLEANPATH = 4;

async function describe(err: unknown, tabId: string, signingIn: boolean): Promise<{ text: string; auth: boolean }> {
  const gateway = await RDPLastError(tabId).catch(() => '');
  if (!isIronError(err)) return { text: gateway || String(err), auth: false };
  switch (err.kind()) {
    case KIND_WRONG_PASSWORD:
    case KIND_LOGON_FAILURE:
      return { text: 'Wrong user name or password', auth: true };
    case KIND_ACCESS_DENIED:
      return { text: 'Access denied', auth: true };
    case KIND_RDCLEANPATH:
      if (gateway) return { text: gateway, auth: false };
  }
  const first = err.backtrace().split('\n')[0];
  // With NLA, Windows answers wrong credentials by dropping the connection
  // (its log says ERROR_LOGON_FAILURE) instead of sending an error code.
  // Depending on timing the client sees the close or a truncated frame.
  if (signingIn && !gateway && /WebSocket\s+Closed|not enough bytes/i.test(first)) {
    return { text: 'The server closed the connection during sign-in, usually because of a wrong user name or password', auth: true };
  }
  return { text: gateway || first || 'Connection failed', auth: false };
}

const even = (n: number) => Math.max(200, Math.floor(n / 2) * 2);

/**
 * Retina and other high-density screens: the desktop is requested in device
 * pixels and Windows is asked to scale its UI by the same factor (as Windows
 * App does), so text is sharp instead of an upscaled low-resolution picture.
 */
function displayScale(): { ratio: number; percent: number } {
  const dpr = Math.min(Math.max(window.devicePixelRatio || 1, 1), 4);
  const ratio = Math.round(dpr * 4) / 4; // Windows' scale steps are 25%
  return { ratio, percent: Math.round(ratio * 100) };
}

/**
 * One built-in RDP session. `session` is single-use (its token expires once
 * the gateway accepts it), so a reconnect passes a new one and this pane
 * starts over.
 */
export function RdpPane({
  tabId,
  session,
  visible,
  focused,
  dynamicResolution,
  fullscreen = false,
  title = '',
  onExitFullscreen,
  onDisconnect,
  onConnected,
  onEnded,
}: {
  tabId: string;
  session: main.RDPSession;
  visible: boolean;
  focused: boolean;
  dynamicResolution: boolean;
  /** Covers the whole window (the app also puts the window in full screen). */
  fullscreen?: boolean;
  title?: string;
  onExitFullscreen?: () => void;
  onDisconnect?: () => void;
  onConnected: () => void;
  /** error is null when the session ended normally. */
  onEnded: (error: string | null, authFailed: boolean) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const element = useRef<HTMLElement | null>(null);
  const ui = useRef<UserInteraction | null>(null);
  const live = useRef(false);
  const cb = useRef({ onConnected, onEnded });
  cb.current = { onConnected, onEnded };

  useEffect(() => {
    let cancelled = false;
    let started = false;
    let connected = false;
    const container = host.current!;
    const audio = (session.audio || 'local') === 'local' ? new RdpAudioPlayer() : null;
    const resumeAudio = () => audio?.resume();
    container.addEventListener('pointerdown', resumeAudio);
    // The ticket is single-use: start on the next tick so a StrictMode
    // mount/unmount/mount (dev) doesn't spend it on a throwaway mount.
    const timer = window.setTimeout(() => {
      started = true;
      void start();
    }, 0);
    const start = async () => {
      try {
        const rdp = await loadEngine();
        if (cancelled) return;
        const el = document.createElement('iron-remote-desktop');
        Object.assign(el, { module: rdp.Backend });
        el.setAttribute('scale', 'fit');
        el.setAttribute('verbose', 'false');
        el.setAttribute('flexcenter', 'true');
        el.style.cssText = 'display:flex;position:absolute;inset:0;';
        const ready = new Promise<UserInteraction>((resolve) =>
          el.addEventListener('ready', (e) => resolve((e as CustomEvent).detail.irgUserInteraction), { once: true })
        );
        container.appendChild(el);
        element.current = el;
        const u = await ready;
        if (cancelled) return;
        ui.current = u;
        u.setEnableClipboard(session.clipboard);

        const rect = container.getBoundingClientRect();
        const fitPane = dynamicResolution || !session.width || !session.height;
        const { ratio, percent } = displayScale();
        const width = fitPane ? even(rect.width * ratio) : session.width;
        const height = fitPane ? even(rect.height * ratio) : session.height;
        const builder = u
          .configBuilder()
          .withUsername(session.username)
          .withPassword(session.password)
          .withServerDomain(session.domain)
          .withDestination(session.destination)
          .withProxyAddress(session.proxyUrl)
          .withAuthToken(session.token)
          .withDesktopSize({ width, height })
          .withExtension(rdp.displayControl(true));
        if (fitPane && percent > 100) builder.withExtension(rdp.desktopScaleFactor(percent));
        if (audio) builder.withExtension(rdp.audioPlayback(audio.onMessage));
        const config = builder.build();

        const info = await u.connect(config);
        connected = true;
        if (cancelled) {
          u.shutdown();
          return;
        }
        live.current = true;
        u.setVisibility(true);
        cb.current.onConnected();
        if (focused) el.focus();
        await info.run();
        live.current = false;
        if (!cancelled) cb.current.onEnded(null, false);
      } catch (err) {
        live.current = false;
        if (cancelled) return;
        const { text, auth } = await describe(err, tabId, !connected);
        cb.current.onEnded(text, auth);
      }
    };
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      container.removeEventListener('pointerdown', resumeAudio);
      audio?.close();
      if (!started) return;
      live.current = false;
      try {
        ui.current?.shutdown();
      } catch {
        // already ended
      }
      RDPClose(tabId).catch(() => {});
      element.current?.remove();
      element.current = null;
      ui.current = null;
    };
    // A new session object means a new connection; the rest is read once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session]);

  // Follow the pane size: the server changes its resolution (Display
  // Control), otherwise the picture is scaled to fit.
  useEffect(() => {
    if (!dynamicResolution) return;
    const container = host.current!;
    let timer = 0;
    let last = '';
    const ro = new ResizeObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        const r = container.getBoundingClientRect();
        if (!live.current || r.width < 50 || r.height < 50) return;
        const { ratio, percent } = displayScale();
        const w = even(r.width * ratio);
        const h = even(r.height * ratio);
        const key = `${w}x${h}@${percent}`;
        if (key === last) return;
        last = key;
        ui.current?.resize(w, h, percent);
      }, 400);
    });
    ro.observe(container);
    return () => {
      ro.disconnect();
      window.clearTimeout(timer);
    };
  }, [dynamicResolution]);

  useEffect(() => {
    if ((visible && focused) || fullscreen) element.current?.focus();
  }, [visible, focused, fullscreen]);

  return (
    <div ref={host} className={`${fullscreen ? 'fixed z-[55]' : 'absolute'} inset-0 bg-black overflow-hidden`}>
      {fullscreen && (
        <FullscreenBar
          title={title}
          onExit={() => {
            onExitFullscreen?.();
          }}
          onDisconnect={() => onDisconnect?.()}
          refocus={() => element.current?.focus()}
        />
      )}
    </div>
  );
}

/**
 * The connection bar of a full-screen session (like mstsc's): hidden at the
 * top edge, slides down when the pointer reaches it. Keyboard focus goes back
 * to the remote desktop after each click, so shortcuts keep reaching it.
 */
function FullscreenBar({
  title,
  onExit,
  onDisconnect,
  refocus,
}: {
  title: string;
  onExit: () => void;
  onDisconnect: () => void;
  refocus: () => void;
}) {
  const [shown, setShown] = useState(true);
  const hideTimer = useRef(0);
  // Show briefly on entry so the user learns where it is.
  useEffect(() => {
    hideTimer.current = window.setTimeout(() => setShown(false), 2500);
    return () => window.clearTimeout(hideTimer.current);
  }, []);
  const show = () => {
    window.clearTimeout(hideTimer.current);
    setShown(true);
  };
  const hide = () => {
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => setShown(false), 600);
  };
  const mod = navigator.platform.toLowerCase().includes('mac') ? '⌘⌥' : 'Ctrl+Alt+';
  return (
    <>
      <div className="absolute top-0 inset-x-0 h-1.5 z-10" onPointerEnter={show} />
      <div
        className={`absolute top-0 left-1/2 -translate-x-1/2 z-20 flex items-center gap-1 pl-4 pr-1.5 py-1.5 rounded-b-xl bg-surface-raised/95 backdrop-blur-xl shadow-pop ring-1 ring-white/[0.06] transition-transform duration-200 ${
          shown ? 'translate-y-0' : '-translate-y-full'
        }`}
        onPointerEnter={show}
        onPointerLeave={hide}
      >
        <span className="text-[12px] text-gray-300 max-w-[40vw] truncate pr-2">{title}</span>
        <button
          className="btn-secondary h-7 px-2.5 text-xs"
          title={`Exit full screen (${mod}Enter)`}
          onClick={() => {
            onExit();
            refocus();
          }}
        >
          <Minimize2 size={13} /> Exit full screen
        </button>
        <button className="icon-btn w-7 h-7 text-red-300 hover:text-red-200" title="Disconnect" onClick={onDisconnect}>
          <Unplug size={14} />
        </button>
      </div>
    </>
  );
}

import { useEffect, useRef } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { ClipboardPaste, Copy, Eraser, TextSelect } from 'lucide-react';
import { useContextMenu } from '../ui/ContextMenu';
import type { MenuEntry } from '../ui/ContextMenu';
import { EventsOn } from '../../../wailsjs/runtime/runtime';
import { inputTargets } from '../../lib/broadcast';
import { takeInitialInput } from '../../lib/initialInput';
import { OpenURL, SSHResizePTY, SSHStartShell, SSHWriteInput } from '../../../wailsjs/go/main/App';
import { fontStack, getSettings, subscribeSettings } from '../../lib/settings';
import type { AppSettings } from '../../lib/settings';
import { themeById } from '../../lib/themes';
import { OutputHighlighter, compileRules } from '../../lib/highlight';
import { copyText, readClipboard } from '../../lib/clipboard';
import { useDialogs } from '../ui/Dialogs';
import { confirmOsc52, confirmPaste } from '../security/securityPrompts';

interface XTermWrapperProps {
  connectionId: string;
  /** Shown on screen (its tab is active). */
  visible: boolean;
  /** The pane that receives keyboard input. */
  focused: boolean;
  connected: boolean;
  /** Extra context-menu entries (split actions). */
  menuExtras?: MenuEntry[];
  /** user@host, shown in clipboard prompts. */
  hostLabel?: string;
  onSize?: (cols: number, rows: number) => void;
}

export function XTermWrapper({ connectionId, visible, focused, connected, menuExtras, hostLabel, onSize }: XTermWrapperProps) {
  const onSizeRef = useRef(onSize);
  onSizeRef.current = onSize;
  const dialogs = useDialogs();
  const hostRef = useRef(hostLabel);
  hostRef.current = hostLabel;
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const startedOnce = useRef(false);
  const droppedOnce = useRef(false);
  const copyPasteRef = useRef<{ copy: () => boolean; paste: () => void; term: Terminal } | null>(null);
  const highlighterRef = useRef<OutputHighlighter | null>(null);
  const openMenu = useContextMenu();

  // Create the terminal and subscribe to output before the shell starts,
  // so the login banner and first prompt are not lost.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // Links come from the remote side: open only web links, and only after
    // showing the full target, both for OSC 8 hyperlinks (whose visible
    // text can differ) and for plain URLs detected in the output.
    const confirmOpen = (uri: string) => {
      if (!/^https?:\/\//i.test(uri)) return;
      void dialogs
        .confirm({ title: 'Open link?', confirmLabel: 'Open in browser', message: <code className="block text-xs break-all text-accent">{uri}</code> })
        .then((ok) => {
          if (ok) void OpenURL(uri).catch(() => {});
        });
    };

    const term = new Terminal({
      ...terminalOptions(getSettings()),
      cursorWidth: 2,
      letterSpacing: 0,
      allowProposedApi: true,
      allowTransparency: true,
      linkHandler: {
        allowNonHttpProtocols: false,
        activate: (_e, uri) => confirmOpen(uri),
      },
    });
    container.style.background = themeById(getSettings().appearance.themeId).colors.background;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon((_e, uri) => confirmOpen(uri)));
    term.open(container);
    termRef.current = term;
    fitRef.current = fit;

    // The bundled font may load after xterm measured cells; re-measure then.
    document.fonts
      .load(`13px "JetBrains Mono Variable"`)
      .then(() => {
        if (termRef.current !== term) return;
        term.options.fontFamily = fontStack(getSettings().appearance.fontFamily);
        try {
          fit.fit();
        } catch {
          // hidden
        }
      })
      .catch(() => {});

    // Highlight rules are compiled once per settings change, not per chunk.
    let rulesFor: AppSettings['highlight'] | null = null;
    let compiled: ReturnType<typeof compileRules> = [];
    const highlighter = new OutputHighlighter(() => {
      const h = getSettings().highlight;
      if (h !== rulesFor) {
        rulesFor = h;
        compiled = h.enabled ? compileRules(h.rules) : [];
      }
      return compiled;
    });
    highlighterRef.current = highlighter;
    const offOutput = EventsOn(`terminal:output:${connectionId}`, (data: string) => term.write(highlighter.process(data)));

    // Settings changes apply to open terminals right away.
    let applied = getSettings();
    const offSettings = subscribeSettings(() => {
      const next = getSettings();
      if (next === applied) return;
      const prev = applied;
      applied = next;
      if (next.appearance === prev.appearance && next.terminal === prev.terminal) return;
      Object.assign(term.options, terminalOptions(next));
      container.style.background = themeById(next.appearance.themeId).colors.background;
      resize();
    });

    term.onSelectionChange(() => {
      if (getSettings().terminal.copyOnSelect && term.hasSelection()) void copyText(term.getSelection());
    });

    // OSC 52: remote programs (tmux, vim) setting the local clipboard.
    // Reading it ("?") is never answered; writing follows the setting.
    let osc52Pending = false;
    term.parser.registerOscHandler(52, (data) => {
      const policy = getSettings().security.osc52;
      const semi = data.indexOf(';');
      const payload = semi >= 0 ? data.slice(semi + 1) : '';
      if (policy === 'disabled' || payload === '?' || payload === '') return true;
      let text: string;
      try {
        const bytes = Uint8Array.from(atob(payload), (c) => c.charCodeAt(0));
        text = new TextDecoder().decode(bytes);
      } catch {
        return true;
      }
      if (policy === 'write') {
        void copyText(text);
        return true;
      }
      if (osc52Pending) return true; // one prompt at a time
      osc52Pending = true;
      void confirmOsc52(text, hostRef.current ?? 'the server', dialogs)
        .then((ok) => {
          if (ok) void copyText(text);
        })
        .finally(() => (osc52Pending = false));
      return true;
    });

    term.onBell(() => {
      if (!getSettings().terminal.bell) return;
      container.classList.remove('terminal-bell');
      void container.offsetWidth; // restart the animation
      container.classList.add('terminal-bell');
    });

    term.onData((data) => {
      for (const id of inputTargets(connectionId)) SSHWriteInput(id, data).catch(() => {});
    });

    const copy = () => {
      const sel = term.getSelection();
      if (!sel) return false;
      void copyText(sel);
      term.clearSelection();
      return true;
    };
    // Paste protection: multi-line and dangerous text is shown first.
    const guardedPaste = async (text: string) => {
      if (!text) return;
      if (await confirmPaste(text, dialogs)) term.paste(text);
      term.focus();
    };
    const paste = () => {
      void readClipboard().then(guardedPaste);
    };
    copyPasteRef.current = { copy, paste, term };

    // Copy:  Ctrl+Shift+C, Ctrl+Insert, or Ctrl+C while text is selected
    //        (with no selection Ctrl+C stays SIGINT), Cmd+C on macOS.
    // Paste: Ctrl+Shift+V, Ctrl+V, Shift+Insert, Cmd+V on macOS.
    // preventDefault stops WebView2's own handling (Ctrl+Shift+C opens the
    // inspector, Ctrl+V/Ctrl+Shift+V would paste a second time).
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true;
      const mod = e.ctrlKey || e.metaKey;
      const isCopy =
        (mod && e.shiftKey && e.code === 'KeyC') ||
        (e.ctrlKey && !e.shiftKey && !e.altKey && e.code === 'Insert') ||
        (mod && !e.shiftKey && !e.altKey && e.code === 'KeyC' && term.hasSelection());
      if (isCopy) {
        e.preventDefault();
        copy();
        return false;
      }
      const isPaste =
        (mod && !e.altKey && e.code === 'KeyV') || (e.shiftKey && !mod && !e.altKey && e.code === 'Insert');
      if (isPaste) {
        e.preventDefault();
        paste();
        return false;
      }
      if (mod && e.shiftKey && APP_KEYS.includes(e.code)) return false; // app hotkeys
      return true;
    });

    // The browser's own paste event (e.g. from the Edit menu) would bypass
    // the handler above and double up with it; route everything through it.
    const onNativePaste = (e: ClipboardEvent) => {
      e.preventDefault();
      e.stopImmediatePropagation();
      const text = e.clipboardData?.getData('text/plain');
      if (text) void guardedPaste(text);
    };
    container.addEventListener('paste', onNativePaste, true);

    let lastSize = '';
    let frame = 0;
    const resize = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (!container.offsetParent) return; // hidden tab
        fit.fit();
        const size = `${term.cols}x${term.rows}`;
        if (size !== lastSize) {
          lastSize = size;
          onSizeRef.current?.(term.cols, term.rows);
          SSHResizePTY(connectionId, term.cols, term.rows).catch(() => {});
        }
      });
    };
    const observer = new ResizeObserver(resize);
    observer.observe(container);

    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      container.removeEventListener('paste', onNativePaste, true);
      offOutput();
      offSettings();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [connectionId]);

  // Start (or, after a reconnect, restart) the shell once connected.
  useEffect(() => {
    const term = termRef.current;
    if (!connected) {
      if (startedOnce.current) droppedOnce.current = true;
      return;
    }
    if (!term) return;
    try {
      fitRef.current?.fit();
    } catch {
      // container may be hidden; default size is fine
    }
    if (droppedOnce.current) {
      droppedOnce.current = false;
      highlighterRef.current?.reset();
      term.write('\r\n\x1b[2m── reconnected ──\x1b[0m\r\n');
    }
    startedOnce.current = true;
    SSHStartShell(connectionId, term.cols, term.rows)
      .then(() => {
        // e.g. `docker exec` for a pane opened from the container picker
        const first = takeInitialInput(connectionId);
        if (first) SSHWriteInput(connectionId, first).catch(() => {});
      })
      .catch((err) => {
        term.write(`\r\n\x1b[31mFailed to start shell: ${err}\x1b[0m\r\n`);
      });
  }, [connected, connectionId]);

  useEffect(() => {
    if (!visible) return;
    // Focus right away (not in rAF, which can be throttled): with split
    // panes, keyboard input must follow the focused pane immediately.
    if (focused) termRef.current?.focus();
    const t = setTimeout(() => {
      try {
        fitRef.current?.fit();
      } catch {
        // ignore
      }
      if (focused && document.activeElement !== termRef.current?.textarea) termRef.current?.focus();
    }, 0);
    return () => clearTimeout(t);
  }, [visible, focused]);

  const onContextMenu = (e: React.MouseEvent) => {
    const api = copyPasteRef.current;
    if (!api) return;
    const { term } = api;
    if (getSettings().terminal.rightClick === 'paste' && !e.shiftKey) {
      // PuTTY-style: right-click pastes (copies first if there is a selection).
      e.preventDefault();
      if (!api.copy()) api.paste();
      else term.focus();
      return;
    }
    openMenu(e, [
      { label: 'Copy', icon: <Copy size={13} />, hint: `${modKeyLabel}+Shift+C`, disabled: !term.hasSelection(), onSelect: () => { api.copy(); term.focus(); } },
      { label: 'Paste', icon: <ClipboardPaste size={13} />, hint: `${modKeyLabel}+V`, onSelect: api.paste },
      { type: 'separator' },
      { label: 'Select all', icon: <TextSelect size={13} />, onSelect: () => { term.selectAll(); term.focus(); } },
      { label: 'Clear screen', icon: <Eraser size={13} />, onSelect: () => { term.clear(); term.focus(); } },
      ...(menuExtras?.length ? [{ type: 'separator' } as MenuEntry, ...menuExtras] : []),
    ]);
  };

  return <div ref={containerRef} onContextMenu={onContextMenu} className={`relative h-full w-full ${visible ? 'block' : 'hidden'}`} />;
}

const modKeyLabel = /Mac/i.test(navigator.platform) ? '⌘' : 'Ctrl';

// Ctrl/Cmd+Shift+<key> combos the app handles; xterm must not consume them.
const APP_KEYS = ['KeyF', 'KeyP', 'KeyT', 'KeyW', 'KeyB', 'KeyD', 'KeyE', 'KeyL', 'KeyM'];

export function terminalOptions(s: AppSettings) {
  const theme = themeById(s.appearance.themeId);
  return {
    theme: theme.colors,
    // Safety net for colors outside the palette (256-color / truecolor
    // output), which can be unreadable on a light background.
    minimumContrastRatio: theme.light ? 4.5 : 1,
    // Thin glyphs look faint on white; a slightly heavier weight helps.
    fontWeight: theme.light ? 500 : 400,
    fontWeightBold: theme.light ? 700 : 600,
    // Bold normally switches to the bright palette; on light themes those
    // slots are paler (Solarized keeps greys there), so keep the base color.
    drawBoldTextInBrightColors: !theme.light,
    fontFamily: fontStack(s.appearance.fontFamily),
    fontSize: s.appearance.fontSize,
    lineHeight: s.appearance.lineHeight,
    cursorStyle: s.appearance.cursorStyle,
    cursorBlink: s.appearance.cursorBlink,
    scrollback: s.terminal.scrollback,
  };
}

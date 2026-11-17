import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Circle, Columns2, Copy, EyeOff, ScrollText, FolderPlus, FolderTree, Loader2, Lock, Monitor, PanelLeft, Plus, RotateCw, Rows2, Server, Settings, ShieldCheck, SquareArrowOutUpRight, SquareTerminal, Unplug, Upload, X } from 'lucide-react';
import { Sidebar, MAX_WIDTH, MIN_WIDTH } from './components/sidebar/Sidebar';
import { ImportDialog } from './components/sidebar/ImportDialog';
import { useHostLibrary } from './components/sidebar/useHostLibrary';
import { folderPath } from './components/sidebar/tree';
import { rdpForSave, sshForSave } from './lib/profiles';
import { TitleBar } from './components/layout/TitleBar';
import { StatusBar } from './components/layout/StatusBar';
import { Welcome } from './components/layout/Welcome';
import { CommandPalette } from './components/CommandPalette';
import type { PaletteItem } from './components/CommandPalette';
import { XTermWrapper } from './components/terminal/XTermWrapper';
import { SplitView } from './components/terminal/SplitView';
import { useContextMenu } from './components/ui/ContextMenu';
import type { MenuEntry } from './components/ui/ContextMenu';
import { contains, insertPane, neighbor, paneIds, removePane, setSizes as setSplitSizes } from './lib/layout';
import type { Side, TabGroup } from './lib/layout';
import { setBroadcastPanes, toggleBroadcast, useBroadcast } from './lib/broadcast';
import { SnippetsDialog } from './components/snippets/SnippetsDialog';
import { StreamDialog } from './components/terminal/StreamDialog';
import { ContainersDialog } from './components/terminal/ContainersDialog';
import { setInitialInput } from './lib/initialInput';
import { StreamConnect } from '../wailsjs/go/main/App';
import { connection } from '../wailsjs/go/models';
import { RecordingsDialog } from './components/recordings/RecordingsDialog';
import type { PaneDrag } from './lib/paneDrag';
import { ConnectDialog } from './components/terminal/ConnectDialog';
import { SftpPanel } from './components/sftp/SftpPanel';
import { RdpDialog } from './components/rdp/RdpDialog';
import { RdpPane } from './components/rdp/RdpPane';
import { openRdpSession } from './lib/rdpConnect';
import { SettingsDialog } from './components/settings/SettingsDialog';
import type { SettingsSection } from './components/settings/SettingsDialog';
import { useDialogs, errorText, modalSwap } from './components/ui/Dialogs';
import { loadVersion } from './lib/updates';
import { useHotkeys } from './hooks/useHotkeys';
import { ConnectCancelled, connectWithPrompts } from './lib/connect';
import { LockScreen } from './components/security/LockScreen';
import { lockNow, useAppLock } from './lib/appLock';
import { setClipboardNotifier } from './lib/clipboard';
import { getSettings, updateSecurity, useSettings } from './lib/settings';
import { modKey, usePlatform } from './lib/hosts';
import type { QuickTarget } from './lib/hosts';
import type { RDPProfile, SSHProfile, StreamSpec, TerminalTab } from './types';
import { EventsOn, WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from '../wailsjs/runtime/runtime';
import { LaunchRDP, OpenURL, RDPClose, SaveRDPProfile, SaveSSHProfile, SSHDisconnect } from '../wailsjs/go/main/App';

type ConnectDialogState = { key: number; tabId: string | null; initial?: Partial<SSHProfile> } | null;

const SIDEBAR_KEY = 'terminyxx.sidebar.v2';

interface SidebarPref {
  width: number;
  rail: boolean;
}

function readSidebarPref(): SidebarPref {
  try {
    const v = JSON.parse(localStorage.getItem(SIDEBAR_KEY) ?? '');
    if (typeof v.width === 'number') return { width: Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, v.width)), rail: !!v.rail };
  } catch {
    // first run or storage unavailable
  }
  return { width: 248, rail: false };
}

function App() {
  const dialogs = useDialogs();
  const platform = usePlatform();
  const mod = modKey(platform);
  const openMenu = useContextMenu();
  // Sessions (one per terminal) and the title-bar tabs that lay them out.
  const [tabs, setTabs] = useState<TerminalTab[]>([]);
  const [groups, setGroups] = useState<TabGroup[]>([]);
  const [activeTabId, setActiveTabId] = useState<string | null>(null);
  const [connectDialog, setConnectDialog] = useState<ConnectDialogState>(null);
  const [rdpDialog, setRdpDialog] = useState<{ key: number; initial?: Partial<RDPProfile> } | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [snippetsOpen, setSnippetsOpen] = useState(false);
  const [recordingsOpen, setRecordingsOpen] = useState(false);
  const [streamDialog, setStreamDialog] = useState<'telnet' | 'serial' | null>(null);
  const [containersFor, setContainersFor] = useState<string | null>(null);
  const broadcast = useBroadcast();
  const [settingsOpen, setSettingsOpen] = useState<SettingsSection | null>(null);
  const [sidebar, setSidebar] = useState<SidebarPref>(readSidebarPref);
  const library = useHostLibrary({
    connectSSH: (p) => handleProfileConnect(p),
    editSSH: (p) => openConnectDialog(null, p),
    newSSH: (folderId) => openConnectDialog(null, { folderId }),
    editRDP: (p) => openRdpDialog(p),
    connectRDP: (p, external) => handleConnectRDP(p, external),
    newRDP: (folderId) => openRdpDialog({ folderId }),
  });
  const sshProfiles = library.ssh;
  const rdpProfiles = library.rdp;
  const loadProfiles = library.reload;
  const [sftpOpenFor, setSftpOpenFor] = useState<Record<string, boolean>>({});
  const [sizes, setSizes] = useState<Record<string, { cols: number; rows: number }>>({});
  const dialogKey = useRef(0);
  const lock = useAppLock();
  const masking = useSettings().security.masking.enabled;

  useEffect(() => {
    const onChanged = () => void loadProfiles();
    window.addEventListener('terminyxx:library-changed', onChanged);
    return () => window.removeEventListener('terminyxx:library-changed', onChanged);
  }, [loadProfiles]);

  useEffect(() => {
    setClipboardNotifier((s) => dialogs.toast(`Copied. The clipboard will be cleared in ${s} s.`));
    return () => setClipboardNotifier(null);
  }, [dialogs]);

  useEffect(() => {
    loadVersion();
    const offReady = EventsOn('updater:ready', (payload?: { version?: string }) => {
      const v = (payload?.version ?? '').replace(/^v/, '');
      if (v) dialogs.toast(`Updated to v${v} — applies on restart`, 'success');
    });
    const offManual = EventsOn('updater:manual', (payload?: { version?: string; url?: string }) => {
      const v = (payload?.version ?? '').replace(/^v/, '');
      const url = payload?.url ?? '';
      dialogs.toast(
        `Terminyxx v${v || 'a new version'} is available, but this copy can't be updated automatically. Click to open the download.`,
        'info',
        () => {
          if (url) void OpenURL(url);
        },
      );
    });
    return () => {
      offReady();
      offManual();
    };
  }, [dialogs]);

  const lockApp = async () => {
    if (await lockNow()) return;
    dialogs.toast('Set a master password in Settings → Security to lock Terminyxx.');
    setSettingsOpen('security');
  };
  const toggleMasking = () => {
    const on = !getSettings().security.masking.enabled;
    updateSecurity('masking', { enabled: on });
    dialogs.toast(on ? 'Secrets in terminal output are now masked' : 'Secret masking is off', on ? 'success' : 'info');
  };

  // macOS: Cmd+L locks too (Cmd shortcuts never reach the shell there).
  useEffect(() => {
    if (platform !== 'darwin' || lock.locked) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && e.code === 'KeyL') {
        e.preventDefault();
        e.stopPropagation();
        void lockApp();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [platform, lock.locked]);

  const activeTab = tabs.find((t) => t.id === activeTabId) ?? null;
  const activeGroup = groups.find((g) => activeTabId && contains(g.root, activeTabId)) ?? null;
  const isSplit = !!activeGroup && activeGroup.root.kind === 'split';
  const groupPanes = useMemo(() => (activeGroup ? paneIds(activeGroup.root) : []), [activeGroup]);
  useEffect(() => {
    setBroadcastPanes(groupPanes);
  }, [groupPanes]);

  // Remember the focused pane per tab, so switching tabs restores it.
  useEffect(() => {
    if (!activeTabId) return;
    setGroups((prev) =>
      prev.some((g) => g.focus !== activeTabId && contains(g.root, activeTabId))
        ? prev.map((g) => (contains(g.root, activeTabId) ? { ...g, focus: activeTabId } : g))
        : prev
    );
  }, [activeTabId]);
  const sftpOpen = !!(activeTab && activeTab.status === 'connected' && !activeTab.profile?.sftpOnly && sftpOpenFor[activeTab.id]);
  const connectedProfileIds = useMemo(
    () => new Set(tabs.filter((t) => t.status === 'connected' && t.profile).map((t) => t.profile!.id)),
    [tabs]
  );

  useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_KEY, JSON.stringify(sidebar));
    } catch {
      // storage unavailable
    }
  }, [sidebar]);
  const toggleRail = () => setSidebar((s) => ({ ...s, rail: !s.rail }));

  const patchTab = useCallback((id: string, patch: Partial<TerminalTab>) => {
    setTabs((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }, []);

  // Track backend connection state (drops, `exit`, keepalive timeouts).
  const tabIds = tabs.map((t) => t.id).join(',');
  useEffect(() => {
    const offs = tabIds
      .split(',')
      .filter(Boolean)
      .map((id) =>
        EventsOn(`connection:state:${id}`, (state: string) => {
          if (state === 'disconnected') {
            setTabs((prev) =>
              prev.map((t) => (t.id === id && t.status === 'connected' ? { ...t, status: 'disconnected' } : t))
            );
          }
        })
      );
    return () => offs.forEach((off) => off());
  }, [tabIds]);

  /** Creates a session; with `place` it opens as a split next to that pane. */
  const newTab = (title = 'New tab', host = '', place?: { target: string; side: Side }): string => {
    const id = crypto.randomUUID();
    setTabs((prev) => [...prev, { id, title, host, status: 'idle', started: false }]);
    setGroups((prev) => {
      if (place && prev.some((g) => contains(g.root, place.target))) {
        return prev.map((g) => (contains(g.root, place.target) ? { ...g, root: insertPane(g.root, place.target, id, place.side), focus: id } : g));
      }
      return [...prev, { id: crypto.randomUUID(), root: { kind: 'pane', id }, focus: id }];
    });
    setActiveTabId(id);
    return id;
  };

  const connectTab = async (tabId: string, profile: SSHProfile): Promise<boolean> => {
    patchTab(tabId, { status: 'connecting', title: profile.name, host: profile.host, profile });
    try {
      const res = await connectWithPrompts(tabId, profile, dialogs);
      let tabProfile = profile;
      if (res.allowedLegacy) {
        tabProfile = { ...tabProfile, allowLegacyAlgorithms: true };
        const saved = sshProfiles.find((p) => p.id === profile.id);
        if (saved) {
          SaveSSHProfile(sshForSave({ ...saved, password: '', allowLegacyAlgorithms: true }))
            .then(loadProfiles)
            .catch(() => {});
        }
      }
      if (res.typedPassword !== undefined) {
        const saved = sshProfiles.find((p) => p.id === profile.id);
        if (res.rememberPassword && saved) {
          try {
            await SaveSSHProfile(sshForSave({ ...saved, password: res.typedPassword }));
            tabProfile = { ...tabProfile, password: '', hasPassword: true };
            loadProfiles();
          } catch (err) {
            dialogs.toast(`Password not saved: ${errorText(err)}`, 'error');
          }
        } else {
          // Keep it in memory so Reconnect works for this session.
          tabProfile = { ...tabProfile, password: res.typedPassword };
        }
      }
      patchTab(tabId, { status: 'connected', started: true, profile: tabProfile });
      return true;
    } catch (err) {
      setTabs((prev) =>
        prev.map((t) => (t.id === tabId ? { ...t, status: t.started ? 'disconnected' : 'idle' } : t))
      );
      if (!(err instanceof ConnectCancelled)) {
        dialogs.toast(`Could not connect to ${profile.host}: ${errorText(err)}`, 'error');
      }
      return false;
    }
  };

  function openConnectDialog(tabId: string | null, initial?: Partial<SSHProfile>) {
    dialogKey.current += 1;
    setConnectDialog({ key: dialogKey.current, tabId, initial });
  }

  function openRdpDialog(initial?: Partial<RDPProfile>) {
    dialogKey.current += 1;
    setRdpDialog({ key: dialogKey.current, initial });
  }

  const quickConnect = (target: QuickTarget) => {
    const existing = sshProfiles.find(
      (p) => p.host === target.host && p.port === target.port && (!target.username || p.username === target.username)
    );
    if (existing) return handleProfileConnect(existing);
    openConnectDialog(null, { host: target.host, port: target.port, username: target.username });
  };

  const handleDialogConnect = async (profile: SSHProfile, save: boolean, connect: boolean) => {
    if (!connect) {
      setConnectDialog(null);
      try {
        await SaveSSHProfile(sshForSave(profile));
        dialogs.toast(`Saved ${profile.name}`, 'success');
      } catch (err) {
        dialogs.toast(`Profile not saved: ${errorText(err)}`, 'error');
      }
      await loadProfiles();
      return;
    }
    const target = connectDialog?.tabId;
    const tab = tabs.find((t) => t.id === target);
    // Reuse an idle tab; otherwise open a new one.
    const tabId =
      tab && tab.status !== 'connected' && tab.status !== 'connecting' ? tab.id : newTab(profile.name, profile.host);
    setConnectDialog(null);
    if (save) {
      try {
        await SaveSSHProfile(sshForSave(profile));
        await loadProfiles();
      } catch (err) {
        dialogs.toast(`Profile not saved: ${errorText(err)}`, 'error');
      }
    }
    await connectTab(tabId, profile);
  };

  const handleDisconnect = async (id: string) => {
    if (tabs.find((t) => t.id === id)?.rdp) {
      RDPClose(id).catch(() => {});
      patchTab(id, { status: 'disconnected' });
      return;
    }
    try {
      await SSHDisconnect(id);
    } catch {
      // already gone
    }
    patchTab(id, { status: 'disconnected' });
  };

  const streamTitle = (s: StreamSpec) => (s.kind === 'telnet' ? `telnet ${s.host}` : s.device);

  const connectStream = async (tabId: string, spec: StreamSpec): Promise<boolean> => {
    patchTab(tabId, { status: 'connecting', title: streamTitle(spec), host: spec.kind === 'telnet' ? spec.host : spec.device, stream: spec, profile: undefined });
    try {
      await StreamConnect(connection.StreamRequest.createFrom({ id: tabId, ...spec }));
      patchTab(tabId, { status: 'connected', started: true });
      return true;
    } catch (err) {
      setTabs((prev) => prev.map((t) => (t.id === tabId ? { ...t, status: t.started ? 'disconnected' : 'idle' } : t)));
      dialogs.toast(`Could not connect: ${errorText(err)}`, 'error');
      return false;
    }
  };

  const openExec = async (tab: TerminalTab, c: connection.Container) => {
    if (!tab.profile) return;
    setContainersFor(null);
    const id = newTab(c.name, tab.host, { target: tab.id, side: 'right' });
    setInitialInput(id, c.execCommand + '\r');
    const ok = await connectTab(id, tab.profile);
    if (ok) patchTab(id, { title: `${c.name} · ${tab.profile.name}` });
  };

  const handleReconnect = (tab: TerminalTab) => {
    if (tab.rdp) void connectRdp(tab.id, tab.rdp);
    else if (tab.stream) void connectStream(tab.id, tab.stream);
    else if (tab.profile) connectTab(tab.id, tab.profile);
    else openConnectDialog(tab.id);
  };

  /** Closes sessions (panes); tabs left without panes disappear. */
  const closeSessions = (ids: string[]) => {
    if (ids.length === 0) return;
    for (const id of ids) {
      const tab = tabs.find((t) => t.id === id);
      if (tab && (tab.status === 'connected' || tab.status === 'connecting')) SSHDisconnect(id).catch(() => {});
    }
    setTabs((prev) => prev.filter((t) => !ids.includes(t.id)));
    setSftpOpenFor((s) => {
      const n = { ...s };
      ids.forEach((id) => delete n[id]);
      return n;
    });

    let nextActive = activeTabId;
    const nextGroups: TabGroup[] = [];
    groups.forEach((g) => {
      let root: TabGroup['root'] | null = g.root;
      let focus = g.focus;
      for (const id of ids) {
        if (!root || !contains(root, id)) continue;
        // Focus moves to a neighbour of the closed pane.
        const r: TabGroup['root'] = root;
        const near = (['left', 'top', 'right', 'bottom'] as Side[]).map((s) => neighbor(r, id, s)).find((n) => n && !ids.includes(n));
        root = removePane(root, id);
        if (root && focus === id) focus = near && contains(root, near) ? near : paneIds(root)[0];
      }
      if (root) nextGroups.push({ ...g, root, focus });
    });
    if (activeTabId && ids.includes(activeTabId)) {
      const gi = groups.findIndex((g) => contains(g.root, activeTabId));
      const same = nextGroups.find((g) => g.id === groups[gi]?.id);
      if (same) nextActive = same.focus;
      else {
        // The whole tab closed: go to the tab that takes its place.
        const before = groups.slice(0, gi).filter((g) => nextGroups.some((n) => n.id === g.id)).length;
        const next = nextGroups[Math.min(before, nextGroups.length - 1)];
        nextActive = next ? next.focus : null;
      }
    }
    setGroups(nextGroups);
    if (nextActive !== activeTabId) setActiveTabId(nextActive);
  };
  const handleCloseTab = (id: string) => closeSessions([id]);
  const closeGroup = (groupId: string) => {
    const g = groups.find((x) => x.id === groupId);
    if (g) closeSessions(paneIds(g.root));
  };

  /** Moves an open session next to `target`, or into its own tab when target is null. */
  const movePane = (id: string, target: string | null, side: Side) => {
    if (id === target) return;
    setGroups((prev) => {
      const sourceIdx = prev.findIndex((g) => contains(g.root, id));
      const without = prev.flatMap((g) => {
        if (!contains(g.root, id)) return [g];
        const root = removePane(g.root, id);
        return root ? [{ ...g, root, focus: g.focus === id ? paneIds(root)[0] : g.focus }] : [];
      });
      if (target && without.some((g) => contains(g.root, target))) {
        return without.map((g) => (contains(g.root, target) ? { ...g, root: insertPane(g.root, target, id, side), focus: id } : g));
      }
      const own: TabGroup = { id: crypto.randomUUID(), root: { kind: 'pane', id }, focus: id };
      const at = Math.min(Math.max(sourceIdx + 1, 0), without.length);
      return [...without.slice(0, at), own, ...without.slice(at)];
    });
    setActiveTabId(id);
  };
  const detachPane = (id: string) => {
    const g = groups.find((x) => contains(x.root, id));
    if (g && g.root.kind === 'split') movePane(id, null, 'right');
  };

  /** Opens a new pane next to `target`: the same host again, or a connect dialog. */
  const splitPane = (target: string | null, side: Side) => {
    const tab = tabs.find((t) => t.id === target);
    if (!tab) return;
    if (tab.profile) {
      const id = newTab(tab.profile.name, tab.profile.host, { target: tab.id, side });
      connectTab(id, tab.profile);
    } else if (tab.rdp) {
      const id = newTab(tab.rdp.name, tab.rdp.host, { target: tab.id, side });
      void connectRdp(id, tab.rdp);
    } else {
      const id = newTab('New tab', '', { target: tab.id, side });
      openConnectDialog(id);
    }
  };

  const handlePaneDrop = (drag: PaneDrag, target: string | null, side: Side) => {
    if (drag.kind === 'session') {
      movePane(drag.id, target, side);
      return;
    }
    const id = newTab(drag.profile.name, drag.profile.host, target ? { target, side } : undefined);
    connectTab(id, drag.profile);
  };

  const focusNeighbor = (side: Side) => {
    if (!activeGroup || !activeTabId) return;
    const n = neighbor(activeGroup.root, activeTabId, side);
    if (n) setActiveTabId(n);
  };

  const paneEntries = (id: string): MenuEntry[] => {
    const inSplit = groups.find((g) => contains(g.root, id))?.root.kind === 'split';
    return [
      { label: 'Split right', icon: <Columns2 size={13} />, hint: `${mod}+⇧+D`, onSelect: () => splitPane(id, 'right') },
      { label: 'Split down', icon: <Rows2 size={13} />, hint: `${mod}+⇧+E`, onSelect: () => splitPane(id, 'bottom') },
      ...(inSplit ? [{ label: 'Move to new tab', icon: <SquareArrowOutUpRight size={13} />, onSelect: () => detachPane(id) } as MenuEntry] : []),
    ];
  };
  const tabMenu = (e: React.MouseEvent, id: string) => {
    const g = groups.find((x) => contains(x.root, id));
    const many = !!g && g.root.kind === 'split';
    openMenu(e, [
      ...paneEntries(id),
      { type: 'separator' },
      { label: many ? 'Close pane' : 'Close tab', icon: <X size={13} />, hint: `${mod}+⇧+W`, onSelect: () => handleCloseTab(id) },
      ...(many ? [{ label: 'Close all panes in tab', icon: <X size={13} />, danger: true, onSelect: () => closeGroup(g!.id) } as MenuEntry] : []),
    ]);
  };

  function handleProfileConnect(profile: SSHProfile) {
    const id = newTab(profile.name, profile.host);
    connectTab(id, profile);
  }

  /** Opens an RDP host in a tab, or in the system's client (setting or `external`). */
  const handleConnectRDP = async (profile: RDPProfile, external = false) => {
    if (external || !getSettings().rdp.builtin) {
      try {
        await LaunchRDP(profile.id);
      } catch (err) {
        dialogs.toast(`Failed to launch RDP: ${errorText(err)}`, 'error');
      }
      return;
    }
    const id = newTab(profile.name, profile.host);
    await connectRdp(id, profile);
  };

  /** Gets a session ticket for an RDP tab; the RdpPane then connects with it. */
  const connectRdp = async (tabId: string, profile: RDPProfile, password = '') => {
    patchTab(tabId, { status: 'connecting', title: profile.name, host: profile.host, rdp: profile, profile: undefined, stream: undefined });
    try {
      const session = await openRdpSession(tabId, profile, dialogs, password);
      patchTab(tabId, { started: true, rdpSession: session });
    } catch (err) {
      setTabs((prev) => prev.map((t) => (t.id === tabId ? { ...t, status: t.started ? 'disconnected' : 'idle' } : t)));
      if (!(err instanceof ConnectCancelled)) dialogs.toast(`Could not connect to ${profile.host}: ${errorText(err)}`, 'error');
    }
  };

  /** The RDP pane shown in real full screen (window full screen + pane over everything). */
  const [rdpFull, setRdpFull] = useState<string | null>(null);
  const enterRdpFullscreen = (id: string) => {
    setRdpFull(id);
    WindowFullscreen();
  };
  const exitRdpFullscreen = () => {
    setRdpFull(null);
    WindowUnfullscreen();
  };
  const toggleRdpFullscreen = () => {
    if (rdpFull) exitRdpFullscreen();
    else if (activeTab?.rdp && activeTab.status === 'connected') enterRdpFullscreen(activeTab.id);
  };
  // Leave full screen when its session ends or closes.
  useEffect(() => {
    if (!rdpFull) return;
    const tab = tabs.find((t) => t.id === rdpFull);
    if (!tab || tab.status !== 'connected') exitRdpFullscreen();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rdpFull, tabs]);
  // The window can also leave full screen on its own (macOS green button,
  // Ctrl+Cmd+F, a Space switch): follow it.
  useEffect(() => {
    if (!rdpFull) return;
    let timer = 0;
    const onResize = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void WindowIsFullscreen().then((full) => {
          if (!full) setRdpFull(null);
        });
      }, 300);
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      window.clearTimeout(timer);
    };
  }, [rdpFull]);

  const handleRdpEnded = async (tab: TerminalTab, error: string | null, authFailed: boolean) => {
    // Drop the spent ticket (and the password in it); a reconnect gets a new one.
    patchTab(tab.id, { status: 'disconnected', rdpSession: undefined });
    if (!error || !tab.rdp) return;
    if (authFailed) {
      const pw = await dialogs.prompt({
        title: 'Wrong password',
        label: `${error}. Password for ${tab.rdp.username ? `${tab.rdp.username}@` : ''}${tab.rdp.host}`,
        password: true,
        confirmLabel: 'Connect',
      });
      if (pw !== null) void connectRdp(tab.id, tab.rdp, pw);
      return;
    }
    dialogs.toast(`RDP session with ${tab.rdp.host} ended: ${error}`, 'error');
  };

  const handleSaveRDP = async (profile: RDPProfile) => {
    try {
      await SaveRDPProfile(rdpForSave(profile));
    } catch (err) {
      dialogs.toast(`Profile not saved: ${errorText(err)}`, 'error');
    }
    await loadProfiles();
    setRdpDialog(null);
  };

  const toggleSftp = () => {
    if (activeTab?.status !== 'connected' || activeTab.stream || activeTab.rdp || activeTab.profile?.sftpOnly) return;
    setSftpOpenFor((s) => ({ ...s, [activeTab.id]: !s[activeTab.id] }));
  };

  const cycleTab = (dir: 1 | -1) => {
    if (groups.length < 2 || !activeGroup) return;
    const i = groups.indexOf(activeGroup);
    setActiveTabId(groups[(i + dir + groups.length) % groups.length].focus);
  };

  useHotkeys({
    'Ctrl+Shift+P': () => setPaletteOpen((v) => !v),
    'Ctrl+,': () => setSettingsOpen((v) => (v ? null : 'appearance')),
    'Ctrl+Shift+F': toggleSftp,
    'Ctrl+Shift+T': () => openConnectDialog(null),
    'Ctrl+Shift+W': () => activeTabId && handleCloseTab(activeTabId),
    'Ctrl+Shift+B': toggleRail,
    'Ctrl+Shift+]': () => cycleTab(1),
    'Ctrl+Shift+[': () => cycleTab(-1),
    'Ctrl+Shift+D': () => splitPane(activeTabId, 'right'),
    'Ctrl+Shift+E': () => splitPane(activeTabId, 'bottom'),
    'Ctrl+Shift+L': () => void lockApp(),
    'Ctrl+Shift+M': toggleMasking,
    'Ctrl+Shift+S': () => setSnippetsOpen((v) => !v),
    'Ctrl+Shift+G': toggleBroadcast,
    // Full screen for RDP panes; Ctrl+Alt+Enter is what other RDP clients use.
    'Ctrl+Alt+Enter': toggleRdpFullscreen,
    // Move focus between panes (only while split, so the shell keeps Alt+arrows otherwise).
    ...(isSplit
      ? {
          'Alt+Left': () => focusNeighbor('left'),
          'Alt+Right': () => focusNeighbor('right'),
          'Alt+Up': () => focusNeighbor('top'),
          'Alt+Down': () => focusNeighbor('bottom'),
        }
      : {}),
  }, !lock.locked);

  const paletteItems: PaletteItem[] = [
    ...sshProfiles.map<PaletteItem>((p) => ({
      id: `host:${p.id}`,
      group: 'Hosts',
      label: p.name,
      hint: [...folderPath(library.tree, p.folderId ?? ''), `${p.username}@${p.host}${p.port !== 22 ? ':' + p.port : ''}`].join(' / '),
      icon: <Server size={14} />,
      run: () => handleProfileConnect(p),
    })),
    ...rdpProfiles.map<PaletteItem>((p) => ({
      id: `rdp:${p.id}`,
      group: 'Hosts',
      label: p.name,
      hint: `RDP · ${p.host}`,
      icon: <Monitor size={14} />,
      run: () => handleConnectRDP(p),
    })),
    ...tabs.map<PaletteItem>((t) => ({
      id: `tab:${t.id}`,
      group: 'Tabs',
      label: `Go to ${t.title}`,
      hint: t.host,
      icon: <SquareTerminal size={14} />,
      run: () => setActiveTabId(t.id),
    })),
    { id: 'new', group: 'Actions', label: 'New SSH connection', icon: <Plus size={14} />, shortcut: [mod, '⇧', 'T'], run: () => openConnectDialog(null) },
    { id: 'rdp', group: 'Actions', label: 'New RDP profile', icon: <Monitor size={14} />, run: () => openRdpDialog() },
    { id: 'folder', group: 'Actions', label: 'New folder', icon: <FolderPlus size={14} />, run: () => library.newFolder('') },
    { id: 'import', group: 'Actions', label: 'Import hosts from another app', icon: <Upload size={14} />, keywords: 'openssh ssh config mobaxterm putty kitty winscp securecrt tabby termius filezilla rdp migrate', run: () => library.setImportOpen(true) },
    { id: 'settings', group: 'Actions', label: 'Settings', icon: <Settings size={14} />, keywords: 'preferences options config', shortcut: [mod, ','], run: () => setSettingsOpen('appearance') },
    { id: 'theme', group: 'Actions', label: 'Change terminal theme', icon: <Settings size={14} />, keywords: 'color scheme appearance font', run: () => setSettingsOpen('appearance') },
    { id: 'highlight', group: 'Actions', label: 'Highlighting rules', icon: <Settings size={14} />, keywords: 'keyword syntax highlight color error warning', run: () => setSettingsOpen('highlight') },
    { id: 'security', group: 'Actions', label: 'Security settings', icon: <ShieldCheck size={14} />, keywords: 'security hide screen share capture zoom teams privacy lock password clipboard paste algorithms agent', run: () => setSettingsOpen('security') },
    { id: 'lock', group: 'Actions', label: 'Lock Terminyxx', icon: <Lock size={14} />, keywords: 'lock away privacy master password', shortcut: [mod, '⇧', 'L'], run: () => void lockApp() },
    { id: 'mask', group: 'Actions', label: masking ? 'Stop masking secrets' : 'Mask secrets in terminal output', icon: <EyeOff size={14} />, keywords: 'presentation streamer mode hide password token redact', shortcut: [mod, '⇧', 'M'], run: toggleMasking },
    { id: 'trusted', group: 'Actions', label: 'Trusted hosts (known_hosts)', icon: <ShieldCheck size={14} />, keywords: 'host key fingerprint known_hosts revoke', run: () => setSettingsOpen('security') },
    { id: 'sync', group: 'Actions', label: 'Encrypted sync file (export / import hosts)', icon: <Settings size={14} />, keywords: 'backup restore transfer migrate cloud dropbox', run: () => setSettingsOpen('sync') },
    { id: 'telnet', group: 'Actions', label: 'New Telnet connection', icon: <Plus size={14} />, keywords: 'router switch network device', run: () => setStreamDialog('telnet') },
    { id: 'serial', group: 'Actions', label: 'New serial port connection', icon: <Plus size={14} />, keywords: 'com uart console usb tty baud', run: () => setStreamDialog('serial') },
    ...(activeTab?.status === 'connected' && activeTab.profile && !activeTab.profile.sftpOnly
      ? [{ id: 'containers', group: 'Actions', label: 'Containers (Docker, Podman, Kubernetes)', icon: <SquareTerminal size={14} />, keywords: 'docker exec shell pod kubectl podman', run: () => setContainersFor(activeTab.id) } as PaletteItem]
      : []),
    { id: 'recordings', group: 'Actions', label: 'Session recordings', icon: <Circle size={14} />, keywords: 'record replay playback history log search asciinema', run: () => setRecordingsOpen(true) },
    { id: 'snippets', group: 'Actions', label: 'Snippets', icon: <ScrollText size={14} />, keywords: 'commands macros saved scripts run', shortcut: [mod, '⇧', 'S'], run: () => setSnippetsOpen(true) },
    ...(isSplit
      ? [{ id: 'broadcast', group: 'Actions', label: broadcast.enabled ? 'Stop typing in all panes' : 'Type in all panes', icon: <Copy size={14} />, keywords: 'broadcast multi input sync panes', shortcut: [mod, '⇧', 'G'], run: toggleBroadcast } as PaletteItem]
      : []),
    { id: 'sidebar', group: 'Actions', label: sidebar.rail ? 'Expand sidebar' : 'Collapse sidebar', icon: <PanelLeft size={14} />, shortcut: [mod, '⇧', 'B'], run: toggleRail },
    ...(activeTab?.status === 'connected'
      ? [
          ...(activeTab.profile?.sftpOnly
            ? []
            : [{ id: 'sftp', group: 'Actions', label: sftpOpen ? 'Hide SFTP panel' : 'Show SFTP panel', icon: <FolderTree size={14} />, shortcut: [mod, '⇧', 'F'], run: toggleSftp } as PaletteItem]),
          { id: 'disc', group: 'Actions', label: `Disconnect ${activeTab.title}`, icon: <Unplug size={14} />, run: () => handleDisconnect(activeTab.id) } as PaletteItem,
        ]
      : []),
    ...(activeTab && activeTab.status === 'disconnected'
      ? [{ id: 'recon', group: 'Actions', label: `Reconnect ${activeTab.title}`, icon: <RotateCw size={14} />, run: () => handleReconnect(activeTab) } as PaletteItem]
      : []),
    ...(activeTab
      ? [
          { id: 'split-r', group: 'Actions', label: 'Split right', icon: <Columns2 size={14} />, keywords: 'pane vertical side by side', shortcut: [mod, '⇧', 'D'], run: () => splitPane(activeTab.id, 'right') } as PaletteItem,
          { id: 'split-d', group: 'Actions', label: 'Split down', icon: <Rows2 size={14} />, keywords: 'pane horizontal', shortcut: [mod, '⇧', 'E'], run: () => splitPane(activeTab.id, 'bottom') } as PaletteItem,
          ...(isSplit ? [{ id: 'detach', group: 'Actions', label: 'Move pane to new tab', icon: <SquareArrowOutUpRight size={14} />, keywords: 'unsplit', run: () => detachPane(activeTab.id) } as PaletteItem] : []),
        ]
      : []),
    ...(activeTab
      ? [{ id: 'close', group: 'Actions', label: isSplit ? 'Close pane' : 'Close tab', icon: <X size={14} />, shortcut: [mod, '⇧', 'W'], run: () => handleCloseTab(activeTab.id) } as PaletteItem]
      : []),
  ];

  return (
    <>
    <div
      className={`flex flex-col h-full w-full bg-app transition-[filter] duration-200 ${lock.locked ? 'blur-2xl saturate-50 select-none' : ''}`}
      // While locked nothing underneath can be focused, clicked or read by assistive tech.
      inert={lock.locked || undefined}
      aria-hidden={lock.locked || undefined}
    >
      <TitleBar
        platform={platform}
        tabs={tabs}
        groups={groups}
        activeTabId={activeTabId}
        sidebarOpen={!sidebar.rail}
        onToggleSidebar={toggleRail}
        onSelect={setActiveTabId}
        onClose={handleCloseTab}
        onCloseGroup={closeGroup}
        onTabMenu={tabMenu}
        onDetach={detachPane}
        onHome={() => setActiveTabId(null)}
        onPalette={() => setPaletteOpen(true)}
        onSettings={() => setSettingsOpen('appearance')}
      />

      <div className="flex flex-1 min-h-0">
        <Sidebar
          width={sidebar.width}
          rail={sidebar.rail}
          onResize={(width, rail) => setSidebar({ width, rail })}
          folders={library.folders}
          sshProfiles={sshProfiles}
          rdpProfiles={rdpProfiles}
          connectedIds={connectedProfileIds}
          renamingId={library.renamingId}
          onRenameDone={() => library.setRenamingId(null)}
          actions={library.actions}
          onNewHost={() => openConnectDialog(null)}
          onNewFolder={() => library.newFolder('')}
          onBackgroundMenu={library.backgroundMenu}
        />

        <main className="flex flex-col flex-1 min-w-0 gap-2 pr-2">
          <SplitView
            tabs={tabs}
            groups={groups}
            activeTabId={activeTabId}
            empty={
              <div className="card absolute inset-0 overflow-hidden">
                <Welcome
                  platform={platform}
                  profiles={sshProfiles}
                  onConnect={handleProfileConnect}
                  onQuickConnect={quickConnect}
                  onNew={() => openConnectDialog(null)}
                  onImport={() => library.setImportOpen(true)}
                />
              </div>
            }
            onFocus={setActiveTabId}
            onResize={(groupId, splitId, sizes) =>
              setGroups((prev) => prev.map((g) => (g.id === groupId ? { ...g, root: setSplitSizes(g.root, splitId, sizes) } : g)))
            }
            onDrop={handlePaneDrop}
            onSplit={splitPane}
            onClose={handleCloseTab}
            paneMenu={(e, id) =>
              openMenu(e, [...paneEntries(id), { type: 'separator' }, { label: 'Close pane', icon: <X size={13} />, onSelect: () => handleCloseTab(id) }])
            }
            renderPane={(tab, { visible, focused }) =>
              tab.started && tab.rdp ? (
                <div className="absolute inset-0 bg-black">
                  {tab.rdpSession && (
                  <RdpPane
                    tabId={tab.id}
                    session={tab.rdpSession}
                    visible={visible}
                    focused={focused}
                    dynamicResolution={tab.rdp.options?.dynamicRes ?? true}
                    fullscreen={rdpFull === tab.id}
                    title={`${tab.rdp.name} · ${tab.rdp.host}`}
                    onExitFullscreen={exitRdpFullscreen}
                    onDisconnect={() => handleDisconnect(tab.id)}
                    onConnected={() => {
                      patchTab(tab.id, { status: 'connected' });
                      // "Full screen" in the host's settings opens it that way.
                      if (tab.rdp?.fullscreen) enterRdpFullscreen(tab.id);
                    }}
                    onEnded={(error, auth) => void handleRdpEnded(tab, error, auth)}
                  />
                  )}
                  {tab.status === 'connecting' && <RdpConnecting tab={tab} />}
                  {tab.status !== 'connecting' && tab.status !== 'connected' && <StatusOverlay tab={tab} onReconnect={() => handleReconnect(tab)} />}
                </div>
              ) : tab.started && tab.profile?.sftpOnly ? (
                // SFTP-only host: no shell, the file manager is the whole pane.
                <>
                  {tab.status === 'connected' && (
                    <SftpPanel fill connectionId={tab.id} visible={visible} onClose={() => {}} />
                  )}
                  {tab.status !== 'connected' && <StatusOverlay tab={tab} onReconnect={() => handleReconnect(tab)} />}
                </>
              ) : tab.started ? (
                <>
                  <XTermWrapper
                    connectionId={tab.id}
                    visible={visible}
                    focused={focused}
                    connected={tab.status === 'connected'}
                    menuExtras={paneEntries(tab.id)}
                    hostLabel={tab.profile ? `${tab.profile.username}@${tab.profile.host}` : tab.host}
                    onSize={(cols, rows) => setSizes((s) => ({ ...s, [tab.id]: { cols, rows } }))}
                  />
                  {tab.status !== 'connected' && <StatusOverlay tab={tab} onReconnect={() => handleReconnect(tab)} />}
                </>
              ) : (
                <PendingTab tab={tab} onConnect={() => (tab.rdp ? void connectRdp(tab.id, tab.rdp) : openConnectDialog(tab.id))} />
              )
            }
          />

          {tabs.map(
            (tab) =>
              tab.status === 'connected' &&
              !tab.profile?.sftpOnly &&
              sftpOpenFor[tab.id] && (
                <SftpPanel
                  key={tab.id}
                  connectionId={tab.id}
                  visible={tab.id === activeTabId}
                  onClose={() => setSftpOpenFor((s) => ({ ...s, [tab.id]: false }))}
                />
              )
          )}
        </main>
      </div>

      <StatusBar
        tab={activeTab}
        size={activeTab ? sizes[activeTab.id] ?? null : null}
        sftpOpen={sftpOpen}
        openTabs={tabs.length}
        onToggleSftp={toggleSftp}
        onDisconnect={() => activeTab && handleDisconnect(activeTab.id)}
        onReconnect={() => activeTab && handleReconnect(activeTab)}
        onOpenSecurity={() => setSettingsOpen('security')}
        masking={masking}
        onToggleMasking={toggleMasking}
        lockConfigured={lock.configured}
        onLock={() => void lockApp()}
        onOpenSnippets={() => setSnippetsOpen(true)}
        onOpenRecordings={() => setRecordingsOpen(true)}
        onOpenContainers={() => activeTab && setContainersFor(activeTab.id)}
        onRdpFullscreen={() => activeTab && enterRdpFullscreen(activeTab.id)}
        broadcast={broadcast}
        onToggleBroadcast={toggleBroadcast}
      />

      {paletteOpen && (
        <CommandPalette items={paletteItems} onQuickConnect={quickConnect} onClose={() => setPaletteOpen(false)} />
      )}

      {connectDialog && (
        <ConnectDialog
          key={connectDialog.key}
          initial={connectDialog.initial}
          folders={library.folderChoices}
          onClose={() => setConnectDialog(null)}
          onConnect={handleDialogConnect}
          onSwitchToRdp={
            connectDialog.tabId
              ? undefined
              : ({ host, folderId }) => {
                  modalSwap.pending = true;
                  setConnectDialog(null);
                  openRdpDialog({ host, folderId: folderId || undefined });
                }
          }
        />
      )}

      {streamDialog && (
        <StreamDialog
          initial={streamDialog}
          onClose={() => setStreamDialog(null)}
          onConnect={(spec) => {
            setStreamDialog(null);
            const id = newTab(streamTitle(spec), spec.kind === 'telnet' ? spec.host : spec.device);
            void connectStream(id, spec);
          }}
        />
      )}

      {(() => {
        const t = tabs.find((x) => x.id === containersFor);
        return t ? <ContainersDialog tab={t} onClose={() => setContainersFor(null)} onOpen={(c) => void openExec(t, c)} /> : null;
      })()}

      {recordingsOpen && <RecordingsDialog onClose={() => setRecordingsOpen(false)} />}

      {snippetsOpen && (
        <SnippetsDialog
          paneId={activeTab?.status === 'connected' ? activeTab.id : null}
          builtins={{ host: activeTab?.profile?.host ?? '', user: activeTab?.profile?.username ?? '' }}
          broadcasting={broadcast.enabled}
          onClose={() => setSnippetsOpen(false)}
        />
      )}

      {settingsOpen && <SettingsDialog modKey={mod} initial={settingsOpen} onClose={() => setSettingsOpen(null)} />}

      {rdpDialog && (
        <RdpDialog
          key={rdpDialog.key}
          initial={rdpDialog.initial}
          onClose={() => setRdpDialog(null)}
          onSave={handleSaveRDP}
          onSwitchToSsh={({ host, folderId }) => {
            modalSwap.pending = true;
            setRdpDialog(null);
            openConnectDialog(null, { host, folderId: folderId || undefined });
          }}
        />
      )}

      {library.importOpen && (
        <ImportDialog
          existingSSH={sshProfiles}
          existingRDP={rdpProfiles}
          onClose={() => library.setImportOpen(false)}
          onImport={library.importHosts}
        />
      )}
    </div>
    {lock.locked && <LockScreen sessions={tabs.filter((t) => t.status === 'connected').length} />}
    </>
  );
}

function PendingTab({ tab, onConnect }: { tab: TerminalTab; onConnect: () => void }) {
  if (tab.status === 'connecting') {
    return (
      <div className="h-full flex flex-col items-center justify-center gap-4 animate-fade-in">
        <div className="relative w-12 h-12">
          <div className="absolute inset-0 rounded-full border-2 border-accent/15" />
          <div className="absolute inset-0 rounded-full border-2 border-transparent border-t-accent animate-spin" />
        </div>
        <div className="text-center">
          <p className="text-gray-200 font-medium">Connecting to {tab.title}</p>
          <p className="text-2xs text-gray-500 font-mono mt-1">
            {tab.profile ? `${tab.profile.username}@${tab.profile.host}:${tab.profile.port}` : tab.host}
          </p>
        </div>
      </div>
    );
  }
  return (
    <div className="h-full flex flex-col items-center justify-center gap-3 text-gray-500">
      <p>This tab is not connected.</p>
      <button className="btn-primary" onClick={onConnect}>
        Connect
      </button>
    </div>
  );
}

function RdpConnecting({ tab }: { tab: TerminalTab }) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/60 text-gray-300 animate-fade-in">
      <Loader2 size={22} className="animate-spin text-accent" />
      <span>Connecting to {tab.title}…</span>
    </div>
  );
}

function StatusOverlay({ tab, onReconnect }: { tab: TerminalTab; onReconnect: () => void }) {
  return (
    <div className="absolute inset-x-0 bottom-5 flex justify-center pointer-events-none">
      <div className="pointer-events-auto flex items-center gap-3 pl-4 pr-1.5 py-1.5 rounded-xl bg-surface-raised/90 backdrop-blur-xl shadow-pop animate-slide-up">
        {tab.status === 'connecting' ? (
          <span className="flex items-center gap-2 pr-3 text-gray-300">
            <Loader2 size={14} className="animate-spin text-accent" /> Reconnecting…
          </span>
        ) : (
          <>
            <span className="flex items-center gap-2 text-gray-300">
              <span className="w-1.5 h-1.5 rounded-full bg-red-400" /> Connection closed
            </span>
            <button className="btn-primary h-7" onClick={onReconnect}>
              <RotateCw size={13} /> Reconnect
            </button>
          </>
        )}
      </div>
    </div>
  );
}

export default App;

import { useRef } from 'react';
import { House, Minus, PanelLeft, Search, Settings, Square, X } from 'lucide-react';
import { Quit, WindowMinimise, WindowToggleMaximise } from '../../../wailsjs/runtime/runtime';
import type { TerminalTab } from '../../types';
import { hostColor, modKey } from '../../lib/hosts';
import type { Platform } from '../../lib/hosts';
import { paneIds } from '../../lib/layout';
import type { TabGroup } from '../../lib/layout';
import { getPaneDrag, setPaneDrag } from '../../lib/paneDrag';

interface TitleBarProps {
  platform: Platform;
  tabs: TerminalTab[];
  groups: TabGroup[];
  activeTabId: string | null;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  /** Focus a pane (and show its tab). */
  onSelect: (id: string) => void;
  /** Close one pane / session. */
  onClose: (id: string) => void;
  onCloseGroup: (groupId: string) => void;
  onTabMenu: (e: React.MouseEvent, id: string) => void;
  /** A pane dropped on the empty part of the tab bar becomes its own tab. */
  onDetach: (id: string) => void;
  /** Show the Welcome page; open sessions stay connected in the background. */
  onHome: () => void;
  onPalette: () => void;
  onSettings: () => void;
}

export function TitleBar({
  platform,
  tabs,
  groups,
  activeTabId,
  sidebarOpen,
  onToggleSidebar,
  onSelect,
  onClose,
  onCloseGroup,
  onTabMenu,
  onDetach,
  onHome,
  onPalette,
  onSettings,
}: TitleBarProps) {
  const mac = platform === 'darwin';

  // While dragging a tab or pane, hovering another tab switches to it so the
  // dragged item can be dropped into that tab's split.
  const hoverTimer = useRef<number | undefined>(undefined);
  const hoverSelect = (g: TabGroup) => {
    if (!getPaneDrag()) return;
    window.clearTimeout(hoverTimer.current);
    hoverTimer.current = window.setTimeout(() => onSelect(g.focus), 350);
  };
  const cancelHover = () => window.clearTimeout(hoverTimer.current);
  const dragProps = (id: string) => ({
    draggable: true,
    onDragStart: (e: React.DragEvent) => {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', id);
      setPaneDrag({ kind: 'session', id });
    },
    onDragEnd: () => {
      cancelHover();
      setPaneDrag(null);
    },
  });
  // Dropping a pane on the bar's empty space moves it out of its split.
  const detachZone = {
    onDragOver: (e: React.DragEvent) => {
      if (e.target !== e.currentTarget) return;
      const d = getPaneDrag();
      if (d?.kind !== 'session') return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
    },
    onDrop: (e: React.DragEvent) => {
      const d = getPaneDrag();
      if (d?.kind !== 'session') return;
      e.preventDefault();
      setPaneDrag(null);
      onDetach(d.id);
    },
  };
  const middleClose = (id: string) => (e: React.MouseEvent) => {
    if (e.button === 1) {
      e.preventDefault();
      onClose(id);
    }
  };

  return (
    <header
      className={`drag flex items-center flex-shrink-0 bg-app select-none ${mac ? 'h-[52px]' : 'h-10'}`}
      // macOS: the inset traffic lights sit ~26px from the top and end at ~88px,
      // so the bar is taller (their centre line = ours) and the content starts clear of them.
      style={{ paddingLeft: mac ? 96 : 8 }}
      onDoubleClick={(e) => {
        if (e.target === e.currentTarget) WindowToggleMaximise();
      }}
    >
      <div className="no-drag flex items-center gap-1 pr-2">
        {!mac && <Logo />}
        <button className={`icon-btn ${sidebarOpen ? 'text-gray-200' : ''}`} onClick={onToggleSidebar} title={`Toggle sidebar (${modKey(platform)}+Shift+B)`}>
          <PanelLeft size={15} />
        </button>
        <button
          className={`icon-btn flex-shrink-0 ${activeTabId === null && groups.length > 0 ? 'bg-white/[0.07] text-gray-100' : ''}`}
          onClick={onHome}
          title="Home"
          aria-label="Home"
        >
          <House size={15} />
        </button>
      </div>

      <nav className="no-drag flex items-center gap-1 min-w-0 overflow-x-auto [&::-webkit-scrollbar]:hidden" {...detachZone}>
        {groups.map((g) => {
          const panes = paneIds(g.root)
            .map((id) => tabs.find((t) => t.id === id))
            .filter((t): t is TerminalTab => !!t);
          if (panes.length === 0) return null;
          const groupActive = panes.some((t) => t.id === activeTabId);

          if (panes.length === 1) {
            const tab = panes[0];
            return (
              <div
                key={g.id}
                role="tab"
                aria-selected={groupActive}
                {...dragProps(tab.id)}
                onDragEnter={() => hoverSelect(g)}
                onDragLeave={cancelHover}
                className={`group relative flex items-center gap-2 h-7 pl-2.5 pr-1 rounded-lg cursor-default min-w-[120px] max-w-[220px] transition-colors ${
                  groupActive
                    ? 'bg-white/[0.07] text-gray-100 shadow-[inset_0_0_0_1px_rgb(255_255_255/0.06)]'
                    : 'text-gray-500 hover:text-gray-300 hover:bg-white/[0.03]'
                }`}
                onMouseDown={middleClose(tab.id)}
                onClick={() => onSelect(tab.id)}
                onContextMenu={(e) => onTabMenu(e, tab.id)}
                title={tabTitle(tab)}
              >
                <StatusDot status={tab.status} color={hostColor(tab.profile?.id ?? tab.host ?? tab.id)} />
                <span className="truncate flex-1 text-[12.5px] font-medium">{tab.title}</span>
                <button
                  className={`icon-btn w-5 h-5 ${groupActive ? 'opacity-60' : 'opacity-0'} group-hover:opacity-100`}
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    onClose(tab.id);
                  }}
                  aria-label="Close tab"
                >
                  <X size={12} />
                </button>
              </div>
            );
          }

          // A split: one tab with a segment per pane.
          return (
            <div
              key={g.id}
              role="tab"
              aria-selected={groupActive}
              onDragEnter={() => hoverSelect(g)}
              onDragLeave={cancelHover}
              className={`group relative flex items-center h-7 pl-2 pr-1 rounded-lg cursor-default flex-shrink-0 transition-colors ${
                groupActive
                  ? 'bg-white/[0.07] text-gray-100 shadow-[inset_0_0_0_1px_rgb(var(--accent)/0.35)]'
                  : 'text-gray-500 hover:text-gray-300 hover:bg-white/[0.03] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.05)]'
              }`}
              title={`Split view · ${panes.length} panes`}
            >
              <SplitIcon dir={g.root.kind === 'split' ? g.root.dir : 'row'} active={groupActive} />
              {panes.map((tab, i) => {
                const focused = groupActive && tab.id === activeTabId;
                return (
                  <div key={tab.id} className="flex items-center min-w-0">
                    {i > 0 && <span className="w-px h-3.5 bg-white/10 mx-0.5 flex-shrink-0" />}
                    <div
                      {...dragProps(tab.id)}
                      className={`flex items-center gap-1.5 h-[22px] px-1.5 rounded-md max-w-[150px] min-w-0 transition-colors ${
                        focused ? 'bg-white/[0.09] text-gray-100' : groupActive ? 'text-gray-400 hover:text-gray-200' : ''
                      }`}
                      onMouseDown={middleClose(tab.id)}
                      onClick={() => onSelect(tab.id)}
                      onContextMenu={(e) => onTabMenu(e, tab.id)}
                      title={`${tabTitle(tab)} · drag out to unsplit`}
                    >
                      <StatusDot status={tab.status} color={hostColor(tab.profile?.id ?? tab.host ?? tab.id)} />
                      <span className="truncate text-[12px] font-medium">{tab.title}</span>
                    </div>
                  </div>
                );
              })}
              <button
                className={`icon-btn w-5 h-5 ml-0.5 ${groupActive ? 'opacity-60' : 'opacity-0'} group-hover:opacity-100`}
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.stopPropagation();
                  onCloseGroup(g.id);
                }}
                aria-label="Close tab"
                title="Close all panes in this tab"
              >
                <X size={12} />
              </button>
            </div>
          );
        })}
      </nav>

      <div className="flex-1 h-full" onDoubleClick={() => WindowToggleMaximise()} {...detachZone} />

      <button
        className="no-drag flex items-center gap-2 h-7 pl-2.5 pr-1.5 mr-2 rounded-lg text-gray-500 bg-white/[0.03] border border-white/[0.06] hover:text-gray-300 hover:border-white/10 transition-colors w-64 whitespace-nowrap"
        onClick={onPalette}
      >
        <Search size={13} />
        <span className="text-[12px] flex-1 text-left">Search or connect…</span>
        <span className="flex gap-0.5"><span className="kbd">{modKey(platform)}</span><span className="kbd">⇧</span><span className="kbd">P</span></span>
      </button>

      <button
        className={`no-drag icon-btn flex-shrink-0 ${platform === 'windows' ? 'mr-1' : 'mr-2'}`}
        onClick={onSettings}
        title={`Settings (${modKey(platform)}+,)`}
        aria-label="Settings"
      >
        <Settings size={15} />
      </button>

      {platform === 'windows' && <WindowControls />}
    </header>
  );
}

export function StatusDot({ status, color }: { status: TerminalTab['status']; color: string }) {
  if (status === 'connecting') {
    return <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse flex-shrink-0" />;
  }
  if (status === 'disconnected') {
    return <span className="w-2 h-2 rounded-full ring-1 ring-inset ring-red-400/80 flex-shrink-0" />;
  }
  if (status === 'idle') {
    return <span className="w-2 h-2 rounded-full bg-gray-600 flex-shrink-0" />;
  }
  return (
    <span className="relative flex w-2 h-2 flex-shrink-0">
      <span className="absolute inset-0 rounded-full" style={{ background: color, boxShadow: `0 0 8px ${color}` }} />
    </span>
  );
}

const tabTitle = (tab: TerminalTab) =>
  tab.host ? `${tab.profile?.username ? tab.profile.username + '@' : ''}${tab.host}` : tab.title;

/** A tiny two-pane glyph that marks a split tab. */
function SplitIcon({ dir, active }: { dir: 'row' | 'column'; active: boolean }) {
  return (
    <span className={`grid gap-[2px] w-3.5 h-3 mr-1 flex-shrink-0 ${dir === 'row' ? 'grid-cols-2' : 'grid-rows-2'}`} aria-hidden>
      <span className={`rounded-[2px] ${active ? 'bg-accent/80' : 'bg-gray-500/70'}`} />
      <span className={`rounded-[2px] ${active ? 'bg-accent/40' : 'bg-gray-600/70'}`} />
    </span>
  );
}

function Logo() {
  return (
    <div className="flex items-center justify-center w-7 h-7 mr-0.5">
      <div className="w-[18px] h-[18px] rounded-[5px] bg-gradient-to-br from-accent to-fuchsia-500 flex items-center justify-center shadow-[0_0_12px_-2px_rgb(var(--accent)/0.7)]">
        <span className="text-[10px] font-bold text-white leading-none">›_</span>
      </div>
    </div>
  );
}

function WindowControls() {
  const base = 'no-drag inline-flex items-center justify-center w-11 h-10 text-gray-400 transition-colors';
  return (
    <div className="flex self-stretch">
      <button className={`${base} hover:bg-white/[0.06] hover:text-gray-100`} onClick={() => WindowMinimise()} aria-label="Minimize">
        <Minus size={15} />
      </button>
      <button className={`${base} hover:bg-white/[0.06] hover:text-gray-100`} onClick={() => WindowToggleMaximise()} aria-label="Maximize">
        <Square size={11} />
      </button>
      <button className={`${base} hover:bg-red-600 hover:text-white`} onClick={() => Quit()} aria-label="Close">
        <X size={16} />
      </button>
    </div>
  );
}

import { useState } from 'react';
import type { ReactNode } from 'react';
import { ArrowDown, ArrowUp, Check, Highlighter, Info, Keyboard, Palette, Pencil, Plus, RotateCcw, ShieldCheck, CloudUpload, SquareTerminal, Trash2, X } from 'lucide-react';
import { Modal } from '../ui/Dialogs';
import { Select } from '../ui/Select';
import { TerminalPreview } from './TerminalPreview';
import { DEFAULT_RULES, DEFAULT_SETTINGS, FONT_OPTIONS, HIGHLIGHT_COLORS, updateSettings, useSettings } from '../../lib/settings';
import type { AppSettings, HighlightColor, HighlightRule } from '../../lib/settings';
import { THEMES, themeById } from '../../lib/themes';
import type { TerminalTheme } from '../../lib/themes';
import { compileRule } from '../../lib/highlight';
import { SecuritySection } from './SecuritySection';
import { SyncSection } from './SyncSection';
import { useUpdates } from '../../lib/updates';

export type SettingsSection = 'appearance' | 'terminal' | 'highlight' | 'security' | 'sync' | 'shortcuts' | 'about';

const SECTIONS: { id: SettingsSection; label: string; icon: ReactNode }[] = [
  { id: 'appearance', label: 'Appearance', icon: <Palette size={15} /> },
  { id: 'terminal', label: 'Terminal', icon: <SquareTerminal size={15} /> },
  { id: 'highlight', label: 'Highlighting', icon: <Highlighter size={15} /> },
  { id: 'security', label: 'Security', icon: <ShieldCheck size={15} /> },
  { id: 'sync', label: 'Sync', icon: <CloudUpload size={15} /> },
  { id: 'shortcuts', label: 'Shortcuts', icon: <Keyboard size={15} /> },
  { id: 'about', label: 'About', icon: <Info size={15} /> },
];

export function SettingsDialog({ modKey, initial = 'appearance', onClose }: { modKey: string; initial?: SettingsSection; onClose: () => void }) {
  const [section, setSection] = useState<SettingsSection>(initial);
  const settings = useSettings();

  return (
    <Modal title="Settings" subtitle="Changes apply immediately and are saved automatically." onClose={onClose} width="max-w-4xl">
      <div className="flex h-[min(620px,calc(100vh-160px))] border-t border-white/[0.06]">
        <nav className="w-44 flex-shrink-0 p-2 space-y-0.5 border-r border-white/[0.06]">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              className={`w-full flex items-center gap-2.5 h-8 px-2.5 rounded-lg text-left text-[13px] transition-colors ${
                section === s.id ? 'bg-white/[0.08] text-gray-100' : 'text-gray-400 hover:text-gray-200 hover:bg-white/[0.04]'
              }`}
              onClick={() => setSection(s.id)}
            >
              <span className={section === s.id ? 'text-accent' : ''}>{s.icon}</span>
              {s.label}
            </button>
          ))}
        </nav>
        <div className="flex-1 min-w-0 overflow-y-auto px-6 py-5">
          {section === 'appearance' && <AppearanceSection settings={settings} />}
          {section === 'terminal' && <TerminalSection settings={settings} />}
          {section === 'highlight' && <HighlightSection settings={settings} />}
          {section === 'security' && <SecuritySection settings={settings} modKey={modKey} />}
          {section === 'sync' && <SyncSection />}
          {section === 'shortcuts' && <ShortcutsSection modKey={modKey} />}
          {section === 'about' && <AboutSection settings={settings} />}
        </div>
      </div>
    </Modal>
  );
}

// ── Appearance ────────────────────────────────────────────────────────

function AppearanceSection({ settings }: { settings: AppSettings }) {
  const a = settings.appearance;
  const set = (patch: Partial<AppSettings['appearance']>) => updateSettings((s) => ({ ...s, appearance: { ...s.appearance, ...patch } }));

  return (
    <div className="space-y-6">
      <Section
        title="Terminal theme"
        action={a.themeId !== DEFAULT_SETTINGS.appearance.themeId && <ResetButton onClick={() => set({ themeId: DEFAULT_SETTINGS.appearance.themeId })} />}
      >
        <div className="grid grid-cols-2 lg:grid-cols-3 gap-2.5">
          {THEMES.map((t) => (
            <ThemeCard key={t.id} theme={t} selected={t.id === a.themeId} onSelect={() => set({ themeId: t.id })} />
          ))}
        </div>
      </Section>

      <Section title="Preview">
        <TerminalPreview settings={settings} />
      </Section>

      <Section
        title="Font & cursor"
        action={
          (a.fontFamily !== DEFAULT_SETTINGS.appearance.fontFamily ||
            a.fontSize !== DEFAULT_SETTINGS.appearance.fontSize ||
            a.lineHeight !== DEFAULT_SETTINGS.appearance.lineHeight ||
            a.cursorStyle !== DEFAULT_SETTINGS.appearance.cursorStyle ||
            a.cursorBlink !== DEFAULT_SETTINGS.appearance.cursorBlink) && (
            <ResetButton onClick={() => set({ ...DEFAULT_SETTINGS.appearance, themeId: a.themeId })} />
          )
        }
      >
        <Row label="Font" hint="Fonts that aren't installed fall back to JetBrains Mono.">
          <Select className="w-56" value={a.fontFamily} options={FONT_OPTIONS} onChange={(v) => set({ fontFamily: v })} />
        </Row>
        <Row label="Font size">
          <Stepper value={a.fontSize} min={8} max={32} step={1} format={(v) => `${v}px`} onChange={(v) => set({ fontSize: v })} />
        </Row>
        <Row label="Line height">
          <Stepper value={a.lineHeight} min={1} max={2} step={0.05} format={(v) => v.toFixed(2)} onChange={(v) => set({ lineHeight: Math.round(v * 100) / 100 })} />
        </Row>
        <Row label="Cursor style">
          <Segmented
            value={a.cursorStyle}
            options={[
              { value: 'bar', label: 'Bar' },
              { value: 'block', label: 'Block' },
              { value: 'underline', label: 'Underline' },
            ]}
            onChange={(v) => set({ cursorStyle: v })}
          />
        </Row>
        <Row label="Blinking cursor">
          <Toggle checked={a.cursorBlink} onChange={(v) => set({ cursorBlink: v })} />
        </Row>
      </Section>
    </div>
  );
}

function ThemeCard({ theme, selected, onSelect }: { theme: TerminalTheme; selected: boolean; onSelect: () => void }) {
  const c = theme.colors;
  const swatches = [c.red, c.green, c.yellow, c.blue, c.magenta, c.cyan];
  return (
    <button
      className={`group text-left rounded-xl p-1.5 transition-all ${
        selected ? 'bg-accent/10 shadow-[inset_0_0_0_1.5px_rgb(var(--accent))]' : 'bg-white/[0.02] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.06)] hover:shadow-[inset_0_0_0_1px_rgb(255_255_255/0.14)]'
      }`}
      onClick={onSelect}
    >
      <div className="rounded-lg px-2.5 py-2 font-mono text-[11px] leading-[1.5]" style={{ background: c.background, color: c.foreground }}>
        <div className="truncate">
          <span style={{ color: c.green }}>user@host</span>:<span style={{ color: c.blue }}>~</span>$ ls
        </div>
        <div className="truncate">
          <span style={{ color: c.blue }}>src</span> <span style={{ color: c.green }}>run.sh</span> <span style={{ color: c.red }}>old.tgz</span> notes
        </div>
        <div className="flex gap-1 mt-1.5">
          {swatches.map((s, i) => (
            <span key={i} className="w-2.5 h-2.5 rounded-full" style={{ background: s }} />
          ))}
        </div>
      </div>
      <div className="flex items-center justify-between px-1 pt-1.5 pb-0.5">
        <span className={`text-[12.5px] ${selected ? 'text-gray-100 font-medium' : 'text-gray-400 group-hover:text-gray-200'}`}>{theme.name}</span>
        {selected && <Check size={13} className="text-accent" />}
        {!selected && theme.light && <span className="text-2xs text-gray-600">light</span>}
      </div>
    </button>
  );
}

// ── Terminal ──────────────────────────────────────────────────────────

const SCROLLBACK = [1000, 5000, 10000, 50000, 100000];

function TerminalSection({ settings }: { settings: AppSettings }) {
  const t = settings.terminal;
  const set = (patch: Partial<AppSettings['terminal']>) => updateSettings((s) => ({ ...s, terminal: { ...s.terminal, ...patch } }));
  const scrollOptions = (SCROLLBACK.includes(t.scrollback) ? SCROLLBACK : [...SCROLLBACK, t.scrollback].sort((x, y) => x - y)).map((n) => ({
    value: String(n),
    label: `${n.toLocaleString('en-US')} lines`,
  }));

  return (
    <div className="space-y-6">
      <Section title="Buffer">
        <Row label="Scrollback" hint="Lines kept in history for each terminal. More lines use more memory.">
          <Select className="w-44" value={String(t.scrollback)} options={scrollOptions} onChange={(v) => set({ scrollback: Number(v) })} />
        </Row>
      </Section>

      <Section title="Mouse & clipboard">
        <Row label="Copy on select" hint="Selected text is copied to the clipboard right away.">
          <Toggle checked={t.copyOnSelect} onChange={(v) => set({ copyOnSelect: v })} />
        </Row>
        <Row label="Right-click" hint="Paste mode works like PuTTY: it copies the selection, or pastes when nothing is selected. Shift+right-click still opens the menu.">
          <Segmented
            value={t.rightClick}
            options={[
              { value: 'menu', label: 'Context menu' },
              { value: 'paste', label: 'Copy / paste' },
            ]}
            onChange={(v) => set({ rightClick: v })}
          />
        </Row>
      </Section>

      <Section title="Notifications">
        <Row label="Visual bell" hint="Flash the terminal when the remote side rings the bell (e.g. Tab completion with no match).">
          <Toggle checked={t.bell} onChange={(v) => set({ bell: v })} />
        </Row>
      </Section>
      <Section title="Remote desktop">
        <Row label="Open RDP hosts in a tab" hint="Uses the built-in RDP client. Turn off to open them in the system's client (Remote Desktop Connection or Windows App) instead.">
          <Toggle checked={settings.rdp.builtin} onChange={(v) => updateSettings((s) => ({ ...s, rdp: { ...s.rdp, builtin: v } }))} />
        </Row>
      </Section>
    </div>
  );
}

// ── Highlighting ──────────────────────────────────────────────────────

function HighlightSection({ settings }: { settings: AppSettings }) {
  const h = settings.highlight;
  const theme = themeById(settings.appearance.themeId).colors as unknown as Record<string, string>;
  const [editing, setEditing] = useState<string | null>(null);
  const setH = (patch: Partial<AppSettings['highlight']>) => updateSettings((s) => ({ ...s, highlight: { ...s.highlight, ...patch } }));
  const setRules = (fn: (r: HighlightRule[]) => HighlightRule[]) => updateSettings((s) => ({ ...s, highlight: { ...s.highlight, rules: fn(s.highlight.rules) } }));
  const patchRule = (id: string, patch: Partial<HighlightRule>) => setRules((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  const move = (i: number, d: -1 | 1) =>
    setRules((rs) => {
      const j = i + d;
      if (j < 0 || j >= rs.length) return rs;
      const next = [...rs];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  const addRule = () => {
    const id = crypto.randomUUID();
    setRules((rs) => [...rs, { id, name: 'New rule', pattern: '', regex: false, caseSensitive: false, color: 'magenta', bold: false, underline: false, enabled: true }]);
    setEditing(id);
  };

  return (
    <div className="space-y-6">
      <Section title="Keyword highlighting">
        <Row
          label="Highlight keywords in output"
          hint="Colors matching words in plain terminal output. Output a program already colors itself, and full-screen apps like vim, htop or mc, are never changed."
        >
          <Toggle checked={h.enabled} onChange={(v) => setH({ enabled: v })} />
        </Row>
      </Section>

      <Section title="Preview">
        <TerminalPreview settings={settings} />
      </Section>

      <Section
        title="Rules"
        action={
          <div className="flex items-center gap-1">
            <button className="btn-ghost h-7 text-[12px]" onClick={() => setH({ rules: DEFAULT_RULES })} title="Restore the built-in rules">
              <RotateCcw size={12} /> Reset
            </button>
            <button className="btn-ghost h-7 text-[12px] text-accent hover:text-accent-hover" onClick={addRule}>
              <Plus size={13} /> Add rule
            </button>
          </div>
        }
      >
        <p className="text-2xs text-gray-500 -mt-1 mb-2">When rules overlap, the one higher in the list wins.</p>
        <div className={`space-y-1.5 ${h.enabled ? '' : 'opacity-50'}`}>
          {h.rules.map((r, i) => (
            <RuleRow
              key={r.id}
              rule={r}
              theme={theme}
              open={editing === r.id}
              first={i === 0}
              last={i === h.rules.length - 1}
              onToggleOpen={() => setEditing(editing === r.id ? null : r.id)}
              onChange={(p) => patchRule(r.id, p)}
              onMove={(d) => move(i, d)}
              onDelete={() => setRules((rs) => rs.filter((x) => x.id !== r.id))}
            />
          ))}
          {h.rules.length === 0 && <p className="text-[12.5px] text-gray-500 py-3 text-center">No rules. Add one or reset to the built-in rules.</p>}
        </div>
      </Section>
    </div>
  );
}

const colorOf = (theme: Record<string, string>, c: HighlightColor) => theme[c] ?? '#ccc';

function RuleRow({
  rule,
  theme,
  open,
  first,
  last,
  onToggleOpen,
  onChange,
  onMove,
  onDelete,
}: {
  rule: HighlightRule;
  theme: Record<string, string>;
  open: boolean;
  first: boolean;
  last: boolean;
  onToggleOpen: () => void;
  onChange: (p: Partial<HighlightRule>) => void;
  onMove: (d: -1 | 1) => void;
  onDelete: () => void;
}) {
  const valid = !rule.pattern.trim() || compileRule(rule) !== null;
  const color = colorOf(theme, rule.color);
  return (
    <div className={`rounded-xl transition-colors ${open ? 'bg-white/[0.04] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.08)]' : 'bg-white/[0.02] hover:bg-white/[0.035]'}`}>
      <div className="group flex items-center gap-3 h-11 pl-3 pr-1.5">
        <Toggle small checked={rule.enabled} onChange={(v) => onChange({ enabled: v })} />
        <button className="flex-1 min-w-0 flex items-center gap-3 text-left h-full" onClick={onToggleOpen}>
          <span
            className="flex-shrink-0 w-[92px] truncate text-[12.5px] font-mono"
            style={{ color, fontWeight: rule.bold ? 600 : 400, textDecoration: rule.underline ? 'underline' : undefined }}
          >
            {rule.name}
          </span>
          <span className={`flex-1 truncate text-2xs font-mono ${valid ? 'text-gray-500' : 'text-red-400'}`}>
            {rule.regex && <span className="text-gray-600 mr-1">regex</span>}
            {rule.pattern || <span className="italic">empty</span>}
          </span>
        </button>
        <div className={`flex items-center ${open ? '' : 'opacity-0 group-hover:opacity-100'} transition-opacity`}>
          <button className="icon-btn w-6 h-6" disabled={first} onClick={() => onMove(-1)} title="Move up">
            <ArrowUp size={12} />
          </button>
          <button className="icon-btn w-6 h-6" disabled={last} onClick={() => onMove(1)} title="Move down">
            <ArrowDown size={12} />
          </button>
          <button className="icon-btn w-6 h-6" onClick={onToggleOpen} title={open ? 'Done' : 'Edit'}>
            {open ? <X size={12} /> : <Pencil size={12} />}
          </button>
          <button className="icon-btn w-6 h-6 hover:text-red-400" onClick={onDelete} title="Delete rule">
            <Trash2 size={12} />
          </button>
        </div>
      </div>

      {open && (
        <div className="px-3 pb-3 pt-1 space-y-3 animate-fade-in">
          <div className="grid grid-cols-[160px_1fr] gap-2">
            <div>
              <label className="label">Name</label>
              <input className="input-field" value={rule.name} onChange={(e) => onChange({ name: e.target.value })} />
            </div>
            <div>
              <label className="label flex items-center justify-between">
                <span>{rule.regex ? 'Regular expression' : 'Words, comma-separated'}</span>
                {!valid && <span className="normal-case tracking-normal text-red-400">Invalid pattern</span>}
              </label>
              <input
                className={`input-field font-mono text-[12px] ${valid ? '' : 'border-red-500/60'}`}
                value={rule.pattern}
                spellCheck={false}
                autoFocus={!rule.pattern}
                placeholder={rule.regex ? String.raw`\bkernel:\s.*` : 'error, failed, denied'}
                onChange={(e) => onChange({ pattern: e.target.value })}
              />
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-x-5 gap-y-2.5">
            <Segmented
              value={rule.regex ? 'regex' : 'words'}
              options={[
                { value: 'words', label: 'Whole words' },
                { value: 'regex', label: 'Regex' },
              ]}
              onChange={(v) => onChange({ regex: v === 'regex' })}
            />
            <Check2 label="Match case" checked={rule.caseSensitive} onChange={(v) => onChange({ caseSensitive: v })} />
            <Check2 label="Bold" checked={rule.bold} onChange={(v) => onChange({ bold: v })} />
            <Check2 label="Underline" checked={rule.underline} onChange={(v) => onChange({ underline: v })} />
          </div>
          <div>
            <label className="label">Color</label>
            <div className="flex flex-wrap gap-1.5">
              {HIGHLIGHT_COLORS.map((c) => (
                <button
                  key={c}
                  className={`w-6 h-6 rounded-md transition-transform hover:scale-110 ${rule.color === c ? 'ring-2 ring-offset-2 ring-offset-surface-raised ring-white/70' : ''}`}
                  style={{ background: colorOf(theme, c) }}
                  title={c.replace(/([A-Z])/g, ' $1').toLowerCase()}
                  aria-label={c}
                  onClick={() => onChange({ color: c })}
                />
              ))}
            </div>
            <p className="text-2xs text-gray-600 mt-1.5">Colors come from the terminal theme, so rules stay readable when you switch themes.</p>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Shortcuts ─────────────────────────────────────────────────────────

function ShortcutsSection({ modKey }: { modKey: string }) {
  const groups: { title: string; items: [string, string[]][] }[] = [
    {
      title: 'App',
      items: [
        ['Command palette', [modKey, '⇧', 'P']],
        ['Settings', [modKey, ',']],
        ['New connection', [modKey, '⇧', 'T']],
        ['Close tab / pane', [modKey, '⇧', 'W']],
        ['Next / previous tab', [modKey, '⇧', ']  [']],
        ['Toggle sidebar', [modKey, '⇧', 'B']],
        ['Toggle SFTP panel', [modKey, '⇧', 'F']],
        ['Lock Terminyxx', [modKey, '⇧', 'L']],
        ['Mask secrets (presentation mode)', [modKey, '⇧', 'M']],
      ],
    },
    {
      title: 'Split panes',
      items: [
        ['Split right', [modKey, '⇧', 'D']],
        ['Split down', [modKey, '⇧', 'E']],
        ['Move focus between panes', ['Alt', '←↑→↓']],
      ],
    },
    {
      title: 'Terminal',
      items: [
        ['Copy', [modKey, '⇧', 'C']],
        ['Copy selection', [modKey, 'C']],
        ['Paste', [modKey, 'V']],
        ['Paste', ['⇧', 'Insert']],
      ],
    },
    {
      title: 'SFTP panel',
      items: [
        ['View file', ['F3']],
        ['Edit remote file', ['F4']],
        ['Copy to other pane', ['F5']],
        ['Move to other pane', ['F6']],
        ['Rename', ['⇧', 'F6']],
        ['New folder', ['F7']],
        ['Delete', ['F8']],
        ['Switch pane', ['Tab']],
        ['Refresh', [modKey, 'R']],
      ],
    },
  ];
  return (
    <div className="space-y-6">
      {groups.map((g) => (
        <Section key={g.title} title={g.title}>
          <div className="rounded-xl bg-white/[0.02] divide-y divide-white/[0.04]">
            {g.items.map(([label, keys], i) => (
              <div key={i} className="flex items-center justify-between h-9 px-3">
                <span className="text-[12.5px] text-gray-300">{label}</span>
                <span className="flex gap-1">
                  {keys.map((k, j) => (
                    <span key={j} className="kbd px-1.5">
                      {k}
                    </span>
                  ))}
                </span>
              </div>
            ))}
          </div>
        </Section>
      ))}
    </div>
  );
}

// ── About / updates ───────────────────────────────────────────────────

function AboutSection(_props: { settings: AppSettings }) {
  const u = useUpdates();
  return (
    <div className="space-y-6">
      <Section title="Terminyxx">
        <Row label="Version" hint="SSH terminal, SFTP file manager and RDP launcher.">
          <span className="font-mono text-[13px] text-gray-200">{u.version || '…'}</span>
        </Row>
      </Section>
    </div>
  );
}

// ── Building blocks ───────────────────────────────────────────────────

export function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section>
      <div className="flex items-center justify-between mb-2.5 min-h-7">
        <h3 className="text-[13px] font-semibold text-gray-200">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}

export function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6 py-2.5 border-b border-white/[0.04] last:border-b-0">
      <div className="min-w-0">
        <div className="text-[13px] text-gray-200">{label}</div>
        {hint && <div className="text-2xs text-gray-500 mt-0.5 max-w-md">{hint}</div>}
      </div>
      <div className="flex-shrink-0">{children}</div>
    </div>
  );
}

function ResetButton({ onClick }: { onClick: () => void }) {
  return (
    <button className="btn-ghost h-7 text-[12px]" onClick={onClick}>
      <RotateCcw size={12} /> Reset
    </button>
  );
}

export function Toggle({ checked, onChange, small }: { checked: boolean; onChange: (v: boolean) => void; small?: boolean }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative flex-shrink-0 rounded-full transition-colors ${small ? 'w-7 h-4' : 'w-9 h-5'} ${checked ? 'bg-accent' : 'bg-white/[0.12]'}`}
    >
      <span
        className={`absolute top-0.5 left-0.5 rounded-full bg-white shadow transition-transform ${small ? 'w-3 h-3' : 'w-4 h-4'} ${
          checked ? (small ? 'translate-x-3' : 'translate-x-4') : ''
        }`}
      />
    </button>
  );
}

function Check2({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-[12.5px] text-gray-300 cursor-pointer select-none">
      <input type="checkbox" className="accent-[rgb(var(--accent))] w-3.5 h-3.5" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

export function Segmented<T extends string>({ value, options, onChange }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex p-0.5 rounded-lg bg-black/30 border border-border">
      {options.map((o) => (
        <button
          key={o.value}
          className={`h-7 px-3 rounded-md text-[12.5px] transition-colors ${value === o.value ? 'bg-white/[0.1] text-gray-100' : 'text-gray-400 hover:text-gray-200'}`}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Stepper({
  value,
  min,
  max,
  step,
  format,
  onChange,
}: {
  value: number;
  min: number;
  max: number;
  step: number;
  format: (v: number) => string;
  onChange: (v: number) => void;
}) {
  const clamp = (v: number) => Math.min(max, Math.max(min, v));
  return (
    <div className="flex items-center gap-3">
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(clamp(Number(e.target.value)))}
        className="w-36 accent-[rgb(var(--accent))]"
      />
      <div className="inline-flex items-center rounded-lg bg-black/30 border border-border">
        <button className="w-7 h-7 text-gray-400 hover:text-gray-100" onClick={() => onChange(clamp(value - step))} aria-label="Decrease">
          −
        </button>
        <span className="w-12 text-center text-[12.5px] tabular-nums text-gray-200">{format(value)}</span>
        <button className="w-7 h-7 text-gray-400 hover:text-gray-100" onClick={() => onChange(clamp(value + step))} aria-label="Increase">
          +
        </button>
      </div>
    </div>
  );
}

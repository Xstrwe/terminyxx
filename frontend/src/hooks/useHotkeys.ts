import { useEffect, useRef } from 'react';

type HotkeyMap = Record<string, (e: KeyboardEvent) => void>;

// Builds a combo like "Ctrl+Shift+F" from the physical key (e.code), so
// shortcuts work regardless of keyboard layout (e.g. Russian).
function comboOf(e: KeyboardEvent): string | null {
  let key: string;
  if (/^Key[A-Z]$/.test(e.code)) key = e.code.slice(3);
  else if (/^Digit\d$/.test(e.code)) key = e.code.slice(5);
  else if (/^F\d{1,2}$/.test(e.code)) key = e.code;
  else if (e.code === 'BracketLeft') key = '[';
  else if (e.code === 'BracketRight') key = ']';
  else if (e.code === 'Comma') key = ',';
  else if (e.code === 'Enter' || e.code === 'NumpadEnter') key = 'Enter';
  else if (/^Arrow(Left|Right|Up|Down)$/.test(e.code)) key = e.code.slice(5);
  else return null;

  const parts: string[] = [];
  if (e.ctrlKey || e.metaKey) parts.push('Ctrl');
  if (e.shiftKey) parts.push('Shift');
  if (e.altKey) parts.push('Alt');
  parts.push(key);
  return parts.join('+');
}

/** Window-level shortcuts. The handler map may change every render. */
export function useHotkeys(hotkeys: HotkeyMap, enabled = true) {
  const ref = useRef(hotkeys);
  ref.current = hotkeys;

  useEffect(() => {
    if (!enabled) return;
    const handler = (e: KeyboardEvent) => {
      const combo = comboOf(e);
      const fn = combo ? ref.current[combo] : undefined;
      if (fn) {
        e.preventDefault();
        e.stopPropagation();
        fn(e);
      }
    };
    // Capture phase: runs before xterm swallows the key.
    window.addEventListener('keydown', handler, true);
    return () => window.removeEventListener('keydown', handler, true);
  }, [enabled]);
}

import { useSyncExternalStore } from 'react';
import { AppVersion, CheckForUpdate } from '../../wailsjs/go/main/App';
import type { updater } from '../../wailsjs/go/models';

export interface UpdateState {
  version: string;
  checking: boolean;
  /** Result of the last successful check. */
  result: updater.Result | null;
  error: string | null;
  checkedAt: number | null;
}

let state: UpdateState = { version: '', checking: false, result: null, error: null, checkedAt: null };
const listeners = new Set<() => void>();

function set(patch: Partial<UpdateState>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
}

export function loadVersion(): void {
  AppVersion().then((version) => set({ version })).catch(() => {});
}

/** Asks GitHub for the latest release. Never throws; the outcome lands in the store. */
export async function checkForUpdate(): Promise<UpdateState> {
  if (state.checking) return state;
  set({ checking: true, error: null });
  try {
    const result = await CheckForUpdate();
    set({ result, checking: false, checkedAt: Date.now() });
  } catch (err) {
    set({ checking: false, error: typeof err === 'string' ? err : err instanceof Error ? err.message : 'Update check failed' });
  }
  return state;
}

export function useUpdates(): UpdateState {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
  );
}

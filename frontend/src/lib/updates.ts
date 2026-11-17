import { useSyncExternalStore } from 'react';
import { AppVersion } from '../../wailsjs/go/main/App';

export interface UpdateState {
  version: string;
}

let state: UpdateState = { version: '' };
const listeners = new Set<() => void>();

function set(patch: Partial<UpdateState>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
}

export function loadVersion(): void {
  AppVersion().then((version) => set({ version })).catch(() => {});
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

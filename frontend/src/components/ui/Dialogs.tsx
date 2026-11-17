import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from 'lucide-react';

// Native alert/confirm/prompt don't work in every Wails webview (macOS
// WKWebView ignores them), so the app uses these promise-based dialogs.

type ConfirmOptions = {
  title: string;
  message?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  /** Focus Cancel instead of OK, so Enter can't accept a risky action. */
  focusCancel?: boolean;
  /** Modal width class (default max-w-md). */
  width?: string;
};

type PromptOptions = {
  title: string;
  label?: string;
  defaultValue?: string;
  password?: boolean;
  confirmLabel?: string;
  /** Optional checkbox under the input; onChange reports its value. */
  checkbox?: { label: string; initial?: boolean; onChange: (checked: boolean) => void };
};

type ToastKind = 'info' | 'success' | 'error';

interface DialogApi {
  confirm(opts: ConfirmOptions): Promise<boolean>;
  prompt(opts: PromptOptions): Promise<string | null>;
  toast(message: string, kind?: ToastKind): void;
}

type ActiveDialog =
  | { kind: 'confirm'; opts: ConfirmOptions; resolve: (v: boolean) => void }
  | { kind: 'prompt'; opts: PromptOptions; resolve: (v: string | null) => void };

interface Toast {
  id: number;
  message: string;
  kind: ToastKind;
}

const DialogContext = createContext<DialogApi | null>(null);

export function useDialogs(): DialogApi {
  const ctx = useContext(DialogContext);
  if (!ctx) throw new Error('useDialogs outside DialogProvider');
  return ctx;
}

export function DialogProvider({ children }: { children: ReactNode }) {
  const [dialog, setDialog] = useState<ActiveDialog | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(1);
  // Return keyboard focus (e.g. to the SFTP panel) when a dialog closes.
  const restoreFocus = useRef<HTMLElement | null>(null);

  const remember = () => {
    restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  };
  const confirm = useCallback(
    (opts: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        remember();
        setDialog({ kind: 'confirm', opts, resolve });
      }),
    []
  );
  const prompt = useCallback(
    (opts: PromptOptions) =>
      new Promise<string | null>((resolve) => {
        remember();
        setDialog({ kind: 'prompt', opts, resolve });
      }),
    []
  );
  const toast = useCallback((message: string, kind: ToastKind = 'info') => {
    const id = nextId.current++;
    setToasts((t) => [...t, { id, message, kind }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === 'error' ? 8000 : 4000);
  }, []);

  const api = useRef<DialogApi>({ confirm, prompt, toast }).current;

  const close = () => {
    setDialog(null);
    const el = restoreFocus.current;
    restoreFocus.current = null;
    if (el) setTimeout(() => el.isConnected && el.focus(), 0);
  };

  return (
    <DialogContext.Provider value={api}>
      {children}
      {dialog?.kind === 'confirm' && (
        <ConfirmDialog
          opts={dialog.opts}
          onResult={(v) => {
            dialog.resolve(v);
            close();
          }}
        />
      )}
      {dialog?.kind === 'prompt' && (
        <PromptDialog
          opts={dialog.opts}
          onResult={(v) => {
            dialog.resolve(v);
            close();
          }}
        />
      )}
      <div className="fixed bottom-10 right-4 z-[70] flex flex-col gap-2 w-[340px] pointer-events-none">
        {toasts.map((t) => (
          <ToastView key={t.id} toast={t} onClose={() => setToasts((all) => all.filter((x) => x.id !== t.id))} />
        ))}
      </div>
    </DialogContext.Provider>
  );
}

/** Set right before swapping one dialog for another so the new one appears without replaying the entrance animation. */
export const modalSwap = { pending: false };

export function Modal({
  title,
  subtitle,
  icon,
  children,
  onClose,
  width = 'max-w-md',
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  icon?: ReactNode;
  children: ReactNode;
  onClose: () => void;
  width?: string;
}) {
  const [still] = useState(() => {
    const v = modalSwap.pending;
    modalSwap.pending = false;
    return v;
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // An open dropdown (Select) closes itself first.
      if (document.querySelector('[role="listbox"]')) return;
      e.stopPropagation();
      onClose();
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center p-6 bg-black/50 backdrop-blur-[3px] ${still ? '' : 'animate-fade-in'}"
      onMouseDown={onClose}
    >
      <div
        className={`w-full ${width} max-h-full flex flex-col rounded-2xl bg-surface-raised/95 backdrop-blur-xl shadow-pop ${still ? '' : 'animate-pop-in'}`}
        onMouseDown={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div className="flex items-start gap-3 px-5 pt-5 pb-4">
          {icon && <div className="flex-shrink-0 mt-0.5">{icon}</div>}
          <div className="flex-1 min-w-0">
            <h2 className="text-[15px] font-semibold tracking-tight text-gray-100 truncate">{title}</h2>
            {subtitle && <p className="mt-0.5 text-[12.5px] text-gray-500">{subtitle}</p>}
          </div>
          <button className="icon-btn -mr-1.5 -mt-1" onClick={onClose} aria-label="Close">
            <X size={15} />
          </button>
        </div>
        <div className="min-h-0 overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}

function ConfirmDialog({ opts, onResult }: { opts: ConfirmOptions; onResult: (v: boolean) => void }) {
  const okRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => (opts.focusCancel ? cancelRef : okRef).current?.focus(), [opts.focusCancel]);
  return (
    <Modal
      title={opts.title}
      width={opts.width}
      onClose={() => onResult(false)}
      icon={
        opts.danger ? (
          <span className="flex items-center justify-center w-8 h-8 rounded-xl bg-red-500/10 text-red-400 ring-1 ring-inset ring-red-500/20">
            <AlertTriangle size={16} />
          </span>
        ) : undefined
      }
    >
      <div className="px-5 pb-1 text-[13px] text-gray-300 space-y-2 leading-relaxed">{opts.message}</div>
      <div className="flex justify-end gap-2 px-5 py-4">
        <button ref={cancelRef} className="btn-ghost h-8 px-3" onClick={() => onResult(false)}>
          {opts.cancelLabel ?? 'Cancel'}
        </button>
        <button ref={okRef} className={opts.danger ? 'btn-danger' : 'btn-primary'} onClick={() => onResult(true)}>
          {opts.confirmLabel ?? 'OK'}
        </button>
      </div>
    </Modal>
  );
}

function PromptDialog({ opts, onResult }: { opts: PromptOptions; onResult: (v: string | null) => void }) {
  const [value, setValue] = useState(opts.defaultValue ?? '');
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);
  return (
    <Modal title={opts.title} subtitle={opts.label} onClose={() => onResult(null)}>
      <form
        className="px-5 pb-4 space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          onResult(value);
        }}
      >
        <input
          ref={inputRef}
          className="input-field"
          type={opts.password ? 'password' : 'text'}
          value={value}
          spellCheck={false}
          onChange={(e) => setValue(e.target.value)}
        />
        {opts.checkbox && (
          <label className="flex items-center gap-2 text-[12.5px] text-gray-300 cursor-pointer select-none">
            <input
              type="checkbox"
              className="accent-[rgb(var(--accent))]"
              defaultChecked={opts.checkbox.initial}
              onChange={(e) => opts.checkbox!.onChange(e.target.checked)}
            />
            {opts.checkbox.label}
          </label>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn-ghost h-8 px-3" onClick={() => onResult(null)}>
            Cancel
          </button>
          <button type="submit" className="btn-primary">
            {opts.confirmLabel ?? 'OK'}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function ToastView({ toast, onClose }: { toast: Toast; onClose: () => void }) {
  const Icon = toast.kind === 'error' ? XCircle : toast.kind === 'success' ? CheckCircle2 : Info;
  const color = toast.kind === 'error' ? 'text-red-400' : toast.kind === 'success' ? 'text-emerald-400' : 'text-accent';
  return (
    <div className="pointer-events-auto flex items-start gap-2.5 rounded-xl bg-surface-raised/95 backdrop-blur-xl shadow-pop pl-3 pr-2 py-2.5 text-[12.5px] animate-slide-up">
      <Icon size={15} className={`${color} flex-shrink-0 mt-px`} />
      <span className="flex-1 break-words text-gray-200 leading-snug">{toast.message}</span>
      <button className="icon-btn w-5 h-5 -mt-0.5" onClick={onClose} aria-label="Dismiss">
        <X size={12} />
      </button>
    </div>
  );
}

export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

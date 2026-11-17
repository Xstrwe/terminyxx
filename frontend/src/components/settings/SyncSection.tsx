import { useState } from 'react';
import { Download, Loader2, Upload } from 'lucide-react';
import { Section } from './SettingsDialog';
import { Toggle } from '../terminal/ConnectDialog';
import { useDialogs } from '../ui/Dialogs';
import { ExportSync, ImportSync } from '../../../wailsjs/go/main/App';

/** Fired after an import so the sidebar reloads its hosts. */
export const LIBRARY_CHANGED = 'terminyxx:library-changed';

export function SyncSection() {
  const dialogs = useDialogs();
  const [passphrase, setPassphrase] = useState('');
  const [confirm, setConfirm] = useState('');
  const [withPasswords, setWithPasswords] = useState(false);
  const [busy, setBusy] = useState<'export' | 'import' | null>(null);
  const short = passphrase.length > 0 && passphrase.length < 8;

  const doExport = async () => {
    if (passphrase.length < 8) return dialogs.toast('Use a passphrase of at least 8 characters.', 'error');
    if (passphrase !== confirm) return dialogs.toast('The passphrases do not match.', 'error');
    setBusy('export');
    try {
      const path = await ExportSync(passphrase, withPasswords);
      if (path) dialogs.toast(`Saved the encrypted file to ${path}`, 'success');
    } catch (e) {
      dialogs.toast(String(e).replace(/^Error: /, ''), 'error');
    } finally {
      setBusy(null);
    }
  };

  const doImport = async () => {
    if (!passphrase) return dialogs.toast('Enter the passphrase of the file first.', 'error');
    setBusy('import');
    try {
      const r = await ImportSync(passphrase);
      if (r.hosts + r.folders + r.snippets === 0) return;
      window.dispatchEvent(new Event(LIBRARY_CHANGED));
      dialogs.toast(`Merged ${r.hosts} hosts, ${r.folders} folders and ${r.snippets} snippets.`, 'success');
      if (r.replaced?.length) {
        dialogs.toast(`Updated the address of existing hosts: ${r.replaced.join(', ')}. Check them before connecting.`, 'error');
      }
      if (r.flagsReset > 0) {
        dialogs.toast(`Agent forwarding and tunnel auto-start from the file were turned off on ${r.flagsReset} hosts. Turn them on again in the host settings if you trust them.`);
      }
      if (r.passwordsNotSaved > 0) {
        dialogs.toast(`${r.passwordsNotSaved} saved passwords were not imported: the OS keychain is unavailable.`, 'error');
      }
    } catch (e) {
      dialogs.toast(String(e).replace(/^Error: /, ''), 'error');
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-6">
      <Section title="Encrypted sync file">
        <p className="text-[12.5px] text-gray-400 leading-relaxed">
          Save your hosts, folders and snippets to one encrypted file and open it on another computer. Keep the file in Dropbox, OneDrive, iCloud, a USB drive or a git repository: without the passphrase it cannot be read, and the passphrase never leaves this
          computer.
        </p>
        <div className="mt-4 grid grid-cols-2 gap-2">
          <label className="block">
            <span className="label">Passphrase</span>
            <input className="input-field" type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} autoComplete="new-password" />
            {short && <span className="mt-1 block text-2xs text-amber-300/90">At least 8 characters.</span>}
          </label>
          <label className="block">
            <span className="label">Repeat (export only)</span>
            <input className="input-field" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
          </label>
        </div>
        <div className="mt-3">
          <Toggle checked={withPasswords} onChange={setWithPasswords} label="Include saved passwords" />
          <p className="mt-1 ml-[42px] text-2xs text-gray-600">Off by default. When on, the passwords are protected only by this passphrase, so choose a long one.</p>
        </div>
        <div className="mt-4 flex gap-2">
          <button className="btn-primary inline-flex items-center gap-1.5" disabled={busy !== null} onClick={() => void doExport()}>
            {busy === 'export' ? <Loader2 size={13} className="animate-spin" /> : <Upload size={13} />} Export…
          </button>
          <button className="btn-ghost h-8 px-3 inline-flex items-center gap-1.5" disabled={busy !== null} onClick={() => void doImport()}>
            {busy === 'import' ? <Loader2 size={13} className="animate-spin" /> : <Download size={13} />} Import…
          </button>
        </div>
      </Section>
      <Section title="How merging works">
        <ul className="list-disc pl-5 space-y-1 text-[12.5px] text-gray-400">
          <li>Importing matches items by ID: a host from the file replaces the local one with the same ID, new ones are added.</li>
          <li>Nothing is deleted. A host you removed on another computer stays here until you remove it too.</li>
          <li>App settings, the master password and host keys are not included.</li>
        </ul>
      </Section>
    </div>
  );
}

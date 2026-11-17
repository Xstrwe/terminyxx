# Vendored IronRDP web client

The built-in RDP client (`components/rdp/RdpPane.tsx`) uses these two packages instead of the npm releases:

- `iron-remote-desktop`: the `<iron-remote-desktop>` web component.
- `iron-remote-desktop-rdp`: the RDP backend, IronRDP compiled to WebAssembly (the `.wasm` is inlined as a `data:` URL).

They are built from [IronRDP](https://github.com/Devolutions/IronRDP) commit
`abfc97b299f04622cb7860f344a35bbea9bd4361` (2026-11-16), which uses sspi 0.23.1.

The npm releases (`@devolutions/iron-remote-desktop-rdp` 0.7.0, May 2026) embed sspi 0.21.0. With that version, Windows 11
refused NLA sign-in from Terminyxx with ERROR_LOGON_FAILURE even though Windows App accepted the same credentials, and Windows
logged no credential check at all. Several NTLM/CredSSP fixes landed in sspi between 0.21.0 and 0.23.1.

To rebuild (for example to pick up a newer IronRDP), run `./build.sh [commit]`, update the commit above, and run `npm install`
in `frontend/`. The two packages must come from the same commit: the component calls backend methods by name.

License: MIT or Apache-2.0 (`LICENSE-MIT`, `LICENSE-APACHE`).

#!/usr/bin/env bash
# Rebuilds the vendored IronRDP web packages from source.
#
# Usage: frontend/vendor/ironrdp/build.sh [commit]   (default: the commit in README.md)
# Needs: git, node/npm, rustup and `cargo install wasm-pack --version 0.15.0 --locked`.
# IronRDP's rust-toolchain.toml selects the Rust version; the wasm32 target is
# added for it below.
set -euo pipefail

COMMIT="${1:-abfc97b299f04622cb7860f344a35bbea9bd4361}"
HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git clone --quiet https://github.com/Devolutions/IronRDP.git "$WORK/ironrdp"
cd "$WORK/ironrdp"
git checkout --quiet "$COMMIT"
rustup target add wasm32-unknown-unknown

# Same flags as `cargo xtask web build` (xtask/src/web.rs).
(
  cd crates/ironrdp-web
  RUSTFLAGS='-Ctarget-feature=+simd128,+bulk-memory --cfg getrandom_backend="wasm_js" -Copt-level=s -Ccodegen-units=1 -Cllvm-args=-enable-dfa-jump-thread' \
    wasm-pack build --target web
  # Vite can't inline `new URL(url, import.meta.url)`; xtask does the same rewrite.
  node -e '
    const fs = require("fs");
    let s = fs.readFileSync("pkg/ironrdp_web.js", "utf8");
    s = "import wasmUrl from \x27./ironrdp_web_bg.wasm?url\x27;\n\n" +
        s.replace("new URL(\x27ironrdp_web_bg.wasm\x27, import.meta.url)", "wasmUrl");
    fs.writeFileSync("pkg/ironrdp_web.js", s);'
)

short="${COMMIT:0:7}"
for pkg in iron-remote-desktop iron-remote-desktop-rdp; do
  (
    cd "web-client/$pkg"
    npm ci --no-audit --no-fund
    if [ "$pkg" = iron-remote-desktop-rdp ]; then npm run build-alone; else npm run build; fi
  )
  mkdir -p "$HERE/$pkg"
  cp "web-client/$pkg/dist/"{index.d.ts,"$pkg".js,package.json} "$HERE/$pkg/"
  node -e '
    const f = process.argv[1], j = JSON.parse(require("fs").readFileSync(f));
    j.version = j.version.replace(/-ironrdp\..*$/, "") + "-ironrdp." + process.argv[2];
    require("fs").writeFileSync(f, JSON.stringify(j, null, 2) + "\n");' "$HERE/$pkg/package.json" "$short"
done
cp LICENSE-MIT LICENSE-APACHE "$HERE/"
echo "Vendored IronRDP $COMMIT. Run 'npm install' in frontend/."

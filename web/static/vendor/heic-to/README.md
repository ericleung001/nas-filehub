# heic-to 1.6.7 (CSP build)

Vendored from https://www.npmjs.com/package/heic-to (https://github.com/hoppergee/heic-to)
for in-browser HEIC/HEIF decoding in the Filehub photo editor and preview.

- File: `heic-to.min.js` is the official **CSP / wasm2js** build (`dist/csp/heic-to.min.js`).
  It does not need `unsafe-eval`. It is loaded only when a HEIC photo is previewed or edited.
- License: LGPL-3.0 (this package includes libheif). See `LICENSE` in this directory.
- The Filehub application itself remains Apache-2.0; this decoder is a replaceable
  separate script, not statically linked into the Go binary's CGO layer (`CGO_ENABLED=0`).

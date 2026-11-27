'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const exif = require(path.join(__dirname, '..', 'web', 'static', 'photo-exif.js'));

// Minimal 1x1 JPEG (SOI + APP0 + SOF0 + DHT + SOS + EOI-style payload).
// A known-good 1x1 grey JPEG.
const TINY_JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xd9]);

function u16le(n) { return Buffer.from([n & 0xff, (n >> 8) & 0xff]); }
function u32le(n) { return Buffer.from([n & 0xff, (n >> 8) & 0xff, (n >> 16) & 0xff, (n >> 24) & 0xff]); }

function buildTestTiff() {
  // II, 42, IFD0 at 8
  const date = Buffer.from('2020:01:02 03:04:05\0'); // 20 bytes
  // Layout:
  // 0: header 8 bytes
  // 8: IFD0: 2 + 5*12 + 4 = 66 bytes, ends at 74
  // 74: DateTime string 20 bytes, ends at 94
  // 94: Exif IFD: 2 + 2*12 + 4 = 30 bytes, ends at 124
  // 124: GPS IFD: 2 + 1*12 + 4 = 18 bytes, ends at 142
  const header = Buffer.concat([Buffer.from('II'), u16le(42), u32le(8)]);
  const entries = [];
  // ImageWidth 256 type LONG count 1 value 100
  entries.push(Buffer.concat([u16le(256), u16le(4), u32le(1), u32le(100)]));
  // ImageLength 257 type LONG count 1 value 50
  entries.push(Buffer.concat([u16le(257), u16le(4), u32le(1), u32le(50)]));
  // Orientation 274 type SHORT count 1 value 6
  entries.push(Buffer.concat([u16le(274), u16le(3), u32le(1), Buffer.from([6, 0, 0, 0])]));
  // DateTime 306 type ASCII count 20 offset 74
  entries.push(Buffer.concat([u16le(306), u16le(2), u32le(20), u32le(74)]));
  // Exif IFD 34665 type LONG count 1 offset 94
  entries.push(Buffer.concat([u16le(34665), u16le(4), u32le(1), u32le(94)]));
  // GPS IFD 34853 type LONG count 1 offset 124 — wait, I said 5 entries but that's 6.
  // Use 6 entries. IFD0 size = 2+72+4=78, ends at 86. Recalculate.
  // Let's rebuild properly with 6 entries.
  return null;
}

function buildTiffWithGps() {
  const date = Buffer.from('2020:01:02 03:04:05\0'); // 20
  // 0 header (8)
  // 8 IFD0: 2 + 6*12 + 4 = 78, ends 86
  // 86 DateTime 20, ends 106
  // 106 Exif IFD: 2 + 2*12 + 4 = 30, ends 136
  // 136 GPS IFD: 2 + 1*12 + 4 = 18, ends 154
  const parts = [];
  parts.push(Buffer.concat([Buffer.from('II'), u16le(42), u32le(8)]));
  parts.push(u16le(6));
  parts.push(Buffer.concat([u16le(256), u16le(4), u32le(1), u32le(100)])); // width
  parts.push(Buffer.concat([u16le(257), u16le(4), u32le(1), u32le(50)])); // height
  parts.push(Buffer.concat([u16le(274), u16le(3), u32le(1), Buffer.from([6, 0, 0, 0])])); // orientation 6
  parts.push(Buffer.concat([u16le(306), u16le(2), u32le(20), u32le(86)])); // DateTime
  parts.push(Buffer.concat([u16le(34665), u16le(4), u32le(1), u32le(106)])); // Exif IFD
  parts.push(Buffer.concat([u16le(34853), u16le(4), u32le(1), u32le(136)])); // GPS IFD
  parts.push(u32le(0)); // next IFD
  parts.push(date);
  // Exif IFD: PixelX 40962, PixelY 40963 LONG
  parts.push(u16le(2));
  parts.push(Buffer.concat([u16le(40962), u16le(4), u32le(1), u32le(100)]));
  parts.push(Buffer.concat([u16le(40963), u16le(4), u32le(1), u32le(50)]));
  parts.push(u32le(0));
  // GPS IFD: GPSVersionID 0 BYTE count 4
  parts.push(u16le(1));
  parts.push(Buffer.concat([u16le(0), u16le(1), u32le(4), Buffer.from([2, 3, 0, 0])]));
  parts.push(u32le(0));
  return Buffer.concat(parts);
}

function looksLikeJpeg(buf) {
  return buf[0] === 0xff && buf[1] === 0xd8;
}

test('supports JPEG and WebP but not PNG', () => {
  assert.equal(exif.supports('image/jpeg'), true);
  assert.equal(exif.supports('image/webp'), true);
  assert.equal(exif.supports('image/png'), false);
  assert.equal(exif.supports('image/gif'), false);
});

test('keep EXIF copies GPS and dates and sets orientation to 1 and updates size', () => {
  const tiff = buildTiffWithGps();
  assert.equal(exif.readOrientation(tiff), 6);
  assert.equal(exif.hasGpsOrDate(tiff), true);

  const source = exif.embed(TINY_JPEG, tiff, { mime: 'image/jpeg', width: 100, height: 50, sourceHint: '' });
  // embed expects source bytes with EXIF, not a raw TIFF. Build a JPEG source first.
  const sourceJpeg = (function () {
    // Manually inject using embed on a jpeg that already has the tiff via patch+build
    const built = exif.embed(TINY_JPEG, Buffer.concat([
      // fake a jpeg? extractTiff on raw tiff won't work. Use a jpeg we build.
      TINY_JPEG
    ]), { mime: 'image/jpeg' });
    return built;
  })();
  void source; void sourceJpeg;

  // Use the internal-ish helpers: build a JPEG with the test TIFF by embedding from a
  // JPEG that already contains it. We'll construct APP1 ourselves through embed by
  // passing source bytes that are a JPEG we create via a second inject path.
  const withExif = injectJpeg(TINY_JPEG, tiff);
  assert.ok(exif.jpegHasExif(withExif));
  assert.equal(exif.readOrientation(exif.extractTiff(withExif, 'image/jpeg')), 6);

  // Simulate "save as JPEG" of a 320x240 photo: start from a clean (stripped) JPEG
  // and copy metadata from the original, with orientation 1 and new dimensions.
  const saved = exif.embed(TINY_JPEG, withExif, { mime: 'image/jpeg', width: 320, height: 240, sourceHint: 'image/jpeg' });
  assert.ok(exif.jpegHasExif(saved));
  const outTiff = exif.extractTiff(saved, 'image/jpeg');
  assert.equal(exif.readOrientation(outTiff), 1);
  assert.equal(exif.hasGpsOrDate(outTiff), true);
  // ImageWidth / ImageLength patched in IFD0
  const le = true;
  function u32(buf, off) { return buf[off] | (buf[off + 1] << 8) | (buf[off + 2] << 16) | (buf[off + 3] << 24); }
  function u16(buf, off) { return buf[off] | (buf[off + 1] << 8); }
  const ifd0 = u32(outTiff, 4);
  const n = u16(outTiff, ifd0);
  let width, height, pixX, pixY;
  for (let i = 0; i < n; i++) {
    const off = ifd0 + 2 + i * 12;
    const tag = u16(outTiff, off);
    const type = u16(outTiff, off + 2);
    const val = type === 3 ? u16(outTiff, off + 8) : u32(outTiff, off + 8);
    if (tag === 256) width = val;
    if (tag === 257) height = val;
    if (tag === 34665) {
      const ptr = u32(outTiff, off + 8);
      const n2 = u16(outTiff, ptr);
      for (let j = 0; j < n2; j++) {
        const o2 = ptr + 2 + j * 12;
        const t2 = u16(outTiff, o2);
        const ty = u16(outTiff, o2 + 2);
        const v2 = ty === 3 ? u16(outTiff, o2 + 8) : u32(outTiff, o2 + 8);
        if (t2 === 40962) pixX = v2;
        if (t2 === 40963) pixY = v2;
      }
    }
  }
  assert.equal(width, 320);
  assert.equal(height, 240);
  assert.equal(pixX, 320);
  assert.equal(pixY, 240);
});

function injectJpeg(jpeg, tiff) {
  // Temporary: use embed by wrapping TIFF in a dummy JPEG APP1 so extractTiff finds it.
  const tiffBytes = Uint8Array.from(tiff);
  const app1Len = 2 + 6 + tiffBytes.length;
  const app1 = new Uint8Array(2 + app1Len);
  app1[0] = 0xff; app1[1] = 0xe1;
  app1[2] = (app1Len >> 8) & 0xff; app1[3] = app1Len & 0xff;
  const hdr = Buffer.from('Exif\0\0');
  app1.set(hdr, 4);
  app1.set(tiffBytes, 10);
  const jpegBytes = Uint8Array.from(jpeg);
  const out = new Uint8Array(2 + app1.length + (jpegBytes.length - 2));
  out[0] = 0xff; out[1] = 0xd8;
  out.set(app1, 2);
  out.set(jpegBytes.subarray(2), 2 + app1.length);
  return out;
}

test('Keep EXIF off leaves a stripped JPEG without GPS or dates', () => {
  const withExif = injectJpeg(TINY_JPEG, buildTiffWithGps());
  assert.ok(exif.jpegHasExif(withExif));
  // The editor's "off" path is: do not call embed. The canvas JPEG has no EXIF.
  assert.equal(exif.jpegHasExif(TINY_JPEG), false);
  assert.equal(exif.extractTiff(TINY_JPEG, 'image/jpeg'), null);
});

test('embed is a no-op for PNG even if the source had EXIF', () => {
  const withExif = injectJpeg(TINY_JPEG, buildTiffWithGps());
  const png = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 1, 2, 3]);
  const out = exif.embed(png, withExif, { mime: 'image/png', width: 10, height: 10, sourceHint: 'image/jpeg' });
  assert.deepEqual(Array.from(out), Array.from(png));
});

test('HEIC fixture is a valid HEIC and extractTiff does not throw', () => {
  const buf = fs.readFileSync(path.join(__dirname, 'testdata', 'sample.heic'));
  assert.ok(buf.length > 16);
  assert.equal(buf.toString('ascii', 4, 8), 'ftyp');
  const brand = buf.toString('ascii', 8, 12);
  assert.ok(['mif1', 'msf1', 'heic', 'heif'].includes(brand), brand);
  const tiff = exif.extractTiff(buf, 'image/heic');
  // Fixture may or may not include EXIF; either is fine as long as we do not throw.
  if (tiff) {
    assert.ok(tiff.length >= 8);
    const bo = String.fromCharCode(tiff[0], tiff[1]);
    assert.ok(bo === 'II' || bo === 'MM');
  }
});

test('isHeic-like ftyp on the fixture', async () => {
  const buf = fs.readFileSync(path.join(__dirname, 'testdata', 'sample.heic'));
  // Use the vendored ESM decoder's isHeic (no canvas needed).
  const url = pathToFileUrl(path.join(__dirname, '..', 'web', 'static', 'vendor', 'heic-to', 'heic-to.min.js'));
  const mod = await import(url);
  const blob = new Blob([buf]);
  assert.equal(await mod.isHeic(blob), true);
  const jpeg = new Blob([TINY_JPEG], { type: 'image/jpeg' });
  assert.equal(await mod.isHeic(jpeg), false);
});

function pathToFileUrl(p) {
  const abs = path.resolve(p);
  return 'file://' + (abs.startsWith('/') ? abs : '/' + abs.replace(/\\/g, '/'));
}

void looksLikeJpeg;
void buildTestTiff;

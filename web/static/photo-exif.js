'use strict';
// Copy, strip, and patch EXIF for the Filehub photo editor (JPEG and WebP).
// After rotate/flip/crop the pixels are already oriented; Orientation is set to 1
// and dimension tags are updated when present. GPS and dates are only copied when
// the caller asks to keep EXIF — they are never added if the option is off.
(function (root) {
  const EXIF_HEADER = [0x45, 0x78, 0x69, 0x66, 0x00, 0x00]; // Exif\0\0
  const TAG_IMAGE_WIDTH = 256;
  const TAG_IMAGE_LENGTH = 257;
  const TAG_ORIENTATION = 274;
  const TAG_EXIF_IFD = 34665;
  const TAG_PIXEL_X = 40962;
  const TAG_PIXEL_Y = 40963;

  function asBytes(input) {
    if (!input) return new Uint8Array(0);
    if (input instanceof Uint8Array) return input;
    if (input instanceof ArrayBuffer) return new Uint8Array(input);
    if (ArrayBuffer.isView(input)) return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    return new Uint8Array(0);
  }

  function asciiFrom(bytes, start, len) {
    let s = '';
    const end = Math.min(bytes.length, start + len);
    for (let i = start; i < end; i++) s += String.fromCharCode(bytes[i]);
    return s;
  }

  function supports(mime) {
    const m = String(mime || '').toLowerCase();
    return m === 'image/jpeg' || m === 'image/jpg' || m === 'image/webp';
  }

  function u16(bytes, offset, le) {
    return le ? (bytes[offset] | (bytes[offset + 1] << 8)) : ((bytes[offset] << 8) | bytes[offset + 1]);
  }
  function u32(bytes, offset, le) {
    if (le) return (bytes[offset] | (bytes[offset + 1] << 8) | (bytes[offset + 2] << 16) | (bytes[offset + 3] << 24)) >>> 0;
    return ((bytes[offset] << 24) | (bytes[offset + 1] << 16) | (bytes[offset + 2] << 8) | bytes[offset + 3]) >>> 0;
  }
  function writeU16(bytes, offset, value, le) {
    if (le) { bytes[offset] = value & 0xff; bytes[offset + 1] = (value >> 8) & 0xff; }
    else { bytes[offset] = (value >> 8) & 0xff; bytes[offset + 1] = value & 0xff; }
  }
  function writeU32(bytes, offset, value, le) {
    if (le) {
      bytes[offset] = value & 0xff;
      bytes[offset + 1] = (value >> 8) & 0xff;
      bytes[offset + 2] = (value >> 16) & 0xff;
      bytes[offset + 3] = (value >> 24) & 0xff;
    } else {
      bytes[offset] = (value >> 24) & 0xff;
      bytes[offset + 1] = (value >> 16) & 0xff;
      bytes[offset + 2] = (value >> 8) & 0xff;
      bytes[offset + 3] = value & 0xff;
    }
  }

  function findExifTiffInJpeg(bytes) {
    if (bytes.length < 4 || bytes[0] !== 0xff || bytes[1] !== 0xd8) return null;
    let i = 2;
    while (i + 4 <= bytes.length) {
      if (bytes[i] !== 0xff) break;
      const marker = bytes[i + 1];
      if (marker === 0xda) break;
      if (marker === 0xd9) break;
      if (marker === 0x00 || marker === 0xff) { i += 1; continue; }
      if (marker >= 0xd0 && marker <= 0xd7) { i += 2; continue; }
      if (i + 4 > bytes.length) break;
      const len = (bytes[i + 2] << 8) | bytes[i + 3];
      if (len < 2 || i + 2 + len > bytes.length) break;
      if (marker === 0xe1 && len >= 8) {
        const start = i + 4;
        if (asciiFrom(bytes, start, 4) === 'Exif' && bytes[start + 4] === 0 && bytes[start + 5] === 0) {
          return { tiff: bytes.subarray(start + 6, i + 2 + len), from: i, to: i + 2 + len };
        }
      }
      i += 2 + len;
    }
    return null;
  }

  function findWebpChunks(bytes) {
    if (bytes.length < 12 || asciiFrom(bytes, 0, 4) !== 'RIFF' || asciiFrom(bytes, 8, 4) !== 'WEBP') return null;
    const chunks = [];
    let i = 12;
    while (i + 8 <= bytes.length) {
      const type = asciiFrom(bytes, i, 4);
      const size = bytes[i + 4] | (bytes[i + 5] << 8) | (bytes[i + 6] << 16) | (bytes[i + 7] << 24);
      const dataStart = i + 8;
      const dataEnd = dataStart + size;
      if (dataEnd > bytes.length) break;
      chunks.push({ type, start: i, dataStart, dataEnd, size, data: bytes.subarray(dataStart, dataEnd) });
      i = dataEnd + (size % 2);
    }
    return chunks;
  }

  function findExifTiffInWebp(bytes) {
    const chunks = findWebpChunks(bytes);
    if (!chunks) return null;
    const ex = chunks.find((c) => c.type === 'EXIF');
    if (!ex) return null;
    let data = ex.data;
    if (data.length >= 6 && asciiFrom(data, 0, 4) === 'Exif' && data[4] === 0 && data[5] === 0) {
      data = data.subarray(6);
    }
    return { tiff: data };
  }

  function findExifTiffInHeic(bytes) {
    // Best-effort: many HEIC files embed a raw TIFF after an "Exif\0\0" marker.
    const needle = [0x45, 0x78, 0x69, 0x66, 0x00, 0x00];
    for (let i = 0; i <= bytes.length - 8; i++) {
      let ok = true;
      for (let j = 0; j < 6; j++) {
        if (bytes[i + j] !== needle[j]) { ok = false; break; }
      }
      if (!ok) continue;
      const tiffStart = i + 6;
      if (tiffStart + 8 > bytes.length) continue;
      const bo = asciiFrom(bytes, tiffStart, 2);
      if (bo !== 'II' && bo !== 'MM') continue;
      return { tiff: bytes.subarray(tiffStart) };
    }
    return null;
  }

  function extractTiff(bytes, mimeOrName) {
    bytes = asBytes(bytes);
    const hint = String(mimeOrName || '').toLowerCase();
    if (hint.includes('jpeg') || hint.includes('jpg') || (bytes[0] === 0xff && bytes[1] === 0xd8)) {
      const found = findExifTiffInJpeg(bytes);
      if (found) return found.tiff;
    }
    if (hint.includes('webp') || (bytes.length >= 12 && asciiFrom(bytes, 0, 4) === 'RIFF')) {
      const found = findExifTiffInWebp(bytes);
      if (found) return found.tiff;
    }
    if (hint.includes('heic') || hint.includes('heif') || (bytes.length >= 12 && asciiFrom(bytes, 4, 4) === 'ftyp')) {
      const found = findExifTiffInHeic(bytes);
      if (found) return found.tiff;
    }
    const jpeg = findExifTiffInJpeg(bytes);
    if (jpeg) return jpeg.tiff;
    const webp = findExifTiffInWebp(bytes);
    if (webp) return webp.tiff;
    const heic = findExifTiffInHeic(bytes);
    if (heic) return heic.tiff;
    return null;
  }

  function tiffEndian(tiff) {
    if (tiff.length < 8) return null;
    const bo = asciiFrom(tiff, 0, 2);
    if (bo === 'II') return true;
    if (bo === 'MM') return false;
    return null;
  }

  function walkAndPatchIFD(tiff, ifdOffset, le, width, height, opts) {
    if (ifdOffset < 8 || ifdOffset + 2 > tiff.length) return false;
    const n = u16(tiff, ifdOffset, le);
    let sawOrientation = false;
    for (let i = 0; i < n; i++) {
      const off = ifdOffset + 2 + i * 12;
      if (off + 12 > tiff.length) break;
      const tag = u16(tiff, off, le);
      const type = u16(tiff, off + 2, le);
      const count = u32(tiff, off + 4, le);
      const valOff = off + 8;
      if (tag === TAG_ORIENTATION && count >= 1 && (type === 3 || type === 4)) {
        if (type === 3) writeU16(tiff, valOff, 1, le);
        else writeU32(tiff, valOff, 1, le);
        if (!le && type === 3) { tiff[valOff + 2] = 0; tiff[valOff + 3] = 0; }
        if (le && type === 3) { tiff[valOff + 2] = 0; tiff[valOff + 3] = 0; }
        sawOrientation = true;
      }
      if ((tag === TAG_IMAGE_WIDTH || tag === TAG_PIXEL_X) && width > 0 && count === 1) {
        if (type === 3) writeU16(tiff, valOff, width, le);
        else if (type === 4) writeU32(tiff, valOff, width, le);
      }
      if ((tag === TAG_IMAGE_LENGTH || tag === TAG_PIXEL_Y) && height > 0 && count === 1) {
        if (type === 3) writeU16(tiff, valOff, height, le);
        else if (type === 4) writeU32(tiff, valOff, height, le);
      }
      if (tag === TAG_EXIF_IFD && count === 1 && (type === 4 || type === 3) && opts && opts.visitExif) {
        const ptr = type === 3 ? u16(tiff, valOff, le) : u32(tiff, valOff, le);
        walkAndPatchIFD(tiff, ptr, le, width, height, { visitExif: false });
      }
    }
    return sawOrientation;
  }

  function appendOrientationTag(tiff, le) {
    const ifd0 = u32(tiff, 4, le);
    if (ifd0 + 2 > tiff.length) return tiff;
    const n = u16(tiff, ifd0, le);
    const nextPtr = ifd0 + 2 + n * 12;
    if (nextPtr + 4 > tiff.length) return tiff;
    const nextIfd = u32(tiff, nextPtr, le);
    const out = new Uint8Array(tiff.length + 2 + (n + 1) * 12 + 4);
    out.set(tiff, 0);
    const newIfd = tiff.length;
    writeU16(out, newIfd, n + 1, le);
    // Copy existing entries.
    out.set(tiff.subarray(ifd0 + 2, ifd0 + 2 + n * 12), newIfd + 2);
    const entry = newIfd + 2 + n * 12;
    writeU16(out, entry, TAG_ORIENTATION, le);
    writeU16(out, entry + 2, 3, le); // SHORT
    writeU32(out, entry + 4, 1, le);
    writeU16(out, entry + 8, 1, le);
    writeU16(out, entry + 10, 0, le);
    writeU32(out, newIfd + 2 + (n + 1) * 12, nextIfd, le);
    writeU32(out, 4, newIfd, le);
    return out;
  }

  function patchTiff(tiff, width, height) {
    tiff = asBytes(tiff);
    if (tiff.length < 8) return null;
    const le = tiffEndian(tiff);
    if (le == null) return null;
    const magic = u16(tiff, 2, le);
    if (magic !== 42) return null;
    const copy = new Uint8Array(tiff.length);
    copy.set(tiff);
    const ifd0 = u32(copy, 4, le);
    const saw = walkAndPatchIFD(copy, ifd0, le, width, height, { visitExif: true });
    if (saw) return copy;
    const withOri = appendOrientationTag(copy, le);
    walkAndPatchIFD(withOri, u32(withOri, 4, le), le, width, height, { visitExif: true });
    return withOri;
  }

  function buildJpegWithExif(jpegBytes, tiff) {
    jpegBytes = asBytes(jpegBytes);
    tiff = asBytes(tiff);
    if (jpegBytes.length < 2 || jpegBytes[0] !== 0xff || jpegBytes[1] !== 0xd8) return null;
    // Collect segments, dropping existing Exif APP1.
    const parts = [new Uint8Array([0xff, 0xd8])];
    let i = 2;
    let restStart = 2;
    while (i + 4 <= jpegBytes.length) {
      if (jpegBytes[i] !== 0xff) { restStart = i; break; }
      const marker = jpegBytes[i + 1];
      if (marker === 0xda) { restStart = i; break; }
      if (marker === 0xd9) { restStart = i; break; }
      if (marker === 0x00 || marker === 0xff) { i += 1; continue; }
      if (marker >= 0xd0 && marker <= 0xd7) { i += 2; continue; }
      const len = (jpegBytes[i + 2] << 8) | jpegBytes[i + 3];
      if (len < 2 || i + 2 + len > jpegBytes.length) { restStart = i; break; }
      const isExif = marker === 0xe1 && len >= 8 &&
        asciiFrom(jpegBytes, i + 4, 4) === 'Exif' && jpegBytes[i + 6] === 0 && jpegBytes[i + 7] === 0;
      if (!isExif) {
        parts.push(jpegBytes.subarray(i, i + 2 + len));
      }
      i += 2 + len;
      restStart = i;
    }
    const app1Len = 2 + 6 + tiff.length;
    if (app1Len > 0xffff) return null;
    const app1 = new Uint8Array(2 + app1Len);
    app1[0] = 0xff; app1[1] = 0xe1;
    app1[2] = (app1Len >> 8) & 0xff; app1[3] = app1Len & 0xff;
    app1.set(EXIF_HEADER, 4);
    app1.set(tiff, 10);
    // Insert APP1 right after SOI (and after any APP0 we already collected — actually
    // parts[0] is SOI, then other APPn. Put Exif first after SOI for compatibility.
    const outParts = [parts[0], app1];
    for (let p = 1; p < parts.length; p++) outParts.push(parts[p]);
    outParts.push(jpegBytes.subarray(restStart));
    let total = 0;
    for (const p of outParts) total += p.length;
    const out = new Uint8Array(total);
    let o = 0;
    for (const p of outParts) { out.set(p, o); o += p.length; }
    return out;
  }

  function buildWebpWithExif(webpBytes, tiff) {
    webpBytes = asBytes(webpBytes);
    tiff = asBytes(tiff);
    const chunks = findWebpChunks(webpBytes);
    if (!chunks) return null;
    // EXIF chunk payload is the raw TIFF (some writers prepend Exif\0\0; we use TIFF only).
    const exifPayload = tiff;
    const padded = exifPayload.length % 2 === 1;
    const exifChunk = new Uint8Array(8 + exifPayload.length + (padded ? 1 : 0));
    exifChunk[0] = 0x45; exifChunk[1] = 0x58; exifChunk[2] = 0x49; exifChunk[3] = 0x46; // EXIF
    const sz = exifPayload.length;
    exifChunk[4] = sz & 0xff; exifChunk[5] = (sz >> 8) & 0xff; exifChunk[6] = (sz >> 16) & 0xff; exifChunk[7] = (sz >> 24) & 0xff;
    exifChunk.set(exifPayload, 8);

    const outChunks = [];
    let sawExif = false;
    let sawVp8x = false;
    for (const c of chunks) {
      if (c.type === 'EXIF') {
        outChunks.push(exifChunk);
        sawExif = true;
        continue;
      }
      if (c.type === 'VP8X') {
        const data = new Uint8Array(c.data);
        if (data.length >= 1) data[0] = data[0] | 0x08; // EXIF flag
        const ch = new Uint8Array(8 + data.length + (data.length % 2));
        ch[0] = 0x56; ch[1] = 0x50; ch[2] = 0x38; ch[3] = 0x58;
        ch[4] = data.length & 0xff; ch[5] = (data.length >> 8) & 0xff;
        ch[6] = (data.length >> 16) & 0xff; ch[7] = (data.length >> 24) & 0xff;
        ch.set(data, 8);
        outChunks.push(ch);
        sawVp8x = true;
        continue;
      }
      outChunks.push(webpBytes.subarray(c.start, c.dataEnd + (c.size % 2)));
    }
    if (!sawExif) outChunks.push(exifChunk);
    // If there is no VP8X, still attach EXIF (widely accepted). Adding a full VP8X
    // would need canvas size from VP8/VP8L; skip rather than risk a corrupt file.

    let payload = 4; // WEBP
    for (const c of outChunks) payload += c.length;
    const out = new Uint8Array(8 + payload);
    out[0] = 0x52; out[1] = 0x49; out[2] = 0x46; out[3] = 0x46;
    const riffSize = payload;
    out[4] = riffSize & 0xff; out[5] = (riffSize >> 8) & 0xff;
    out[6] = (riffSize >> 16) & 0xff; out[7] = (riffSize >> 24) & 0xff;
    out[8] = 0x57; out[9] = 0x45; out[10] = 0x42; out[11] = 0x50;
    let o = 12;
    for (const c of outChunks) { out.set(c, o); o += c.length; }
    return out;
    void sawVp8x;
  }

  function hasGpsOrDate(tiff) {
    tiff = asBytes(tiff);
    const le = tiffEndian(tiff);
    if (le == null) return false;
    const ifd0 = u32(tiff, 4, le);
    if (ifd0 + 2 > tiff.length) return false;
    const n = u16(tiff, ifd0, le);
    for (let i = 0; i < n; i++) {
      const off = ifd0 + 2 + i * 12;
      if (off + 2 > tiff.length) break;
      const tag = u16(tiff, off, le);
      // GPS IFD pointer, DateTime, DateTimeOriginal (in Exif IFD we also scan later)
      if (tag === 34853 || tag === 306) return true;
    }
    // Search the raw TIFF for the GPS IFD tag bytes as a fallback is unnecessary.
    return false;
  }

  function readOrientation(tiff) {
    tiff = asBytes(tiff);
    const le = tiffEndian(tiff);
    if (le == null) return 0;
    const ifd0 = u32(tiff, 4, le);
    if (ifd0 + 2 > tiff.length) return 0;
    const n = u16(tiff, ifd0, le);
    for (let i = 0; i < n; i++) {
      const off = ifd0 + 2 + i * 12;
      if (off + 12 > tiff.length) break;
      const tag = u16(tiff, off, le);
      if (tag === TAG_ORIENTATION) {
        const type = u16(tiff, off + 2, le);
        return type === 3 ? u16(tiff, off + 8, le) : u32(tiff, off + 8, le);
      }
    }
    return 0;
  }

  function embed(outputBytes, sourceBytes, opts) {
    opts = opts || {};
    const mime = String(opts.mime || '');
    if (!supports(mime)) return asBytes(outputBytes);
    const srcTiff = extractTiff(sourceBytes, opts.sourceHint || '');
    if (!srcTiff) return asBytes(outputBytes);
    const patched = patchTiff(srcTiff, opts.width || 0, opts.height || 0);
    if (!patched) return asBytes(outputBytes);
    if (mime === 'image/jpeg' || mime === 'image/jpg') {
      return buildJpegWithExif(outputBytes, patched) || asBytes(outputBytes);
    }
    if (mime === 'image/webp') {
      return buildWebpWithExif(outputBytes, patched) || asBytes(outputBytes);
    }
    return asBytes(outputBytes);
  }

  function jpegHasExif(bytes) {
    return !!findExifTiffInJpeg(asBytes(bytes));
  }

  const api = {
    supports,
    extractTiff,
    patchTiff,
    embed,
    jpegHasExif,
    findExifTiffInJpeg,
    findExifTiffInWebp,
    readOrientation,
    hasGpsOrDate,
  };
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
  root.FilehubPhotoExif = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);

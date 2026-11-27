'use strict';
// Pixel-level brightness/contrast so preview and save match on iOS Safari and desktop.
// CSS/canvas ctx.filter is not reliable in WebKit toBlob() output.
(function (root) {
  function clampByte(n) {
    return Math.max(0, Math.min(255, Math.round(n)));
  }

  // Matches CSS filter order brightness() then contrast(), in sRGB 0–255.
  // brightness(b): v * b; contrast(c): v * c + 127.5 * (1 - c).
  function makeBrightnessContrastLUT(brightnessPct, contrastPct) {
    const b = Number(brightnessPct) / 100;
    const c = Number(contrastPct) / 100;
    const intercept = 127.5 * (1 - c);
    const lut = new Uint8ClampedArray(256);
    for (let i = 0; i < 256; i++) {
      lut[i] = clampByte(i * b * c + intercept);
    }
    return lut;
  }

  function applyBrightnessContrastToPixels(data, brightnessPct, contrastPct) {
    if (!data || data.length < 4) return data;
    if (Number(brightnessPct) === 100 && Number(contrastPct) === 100) return data;
    const lut = makeBrightnessContrastLUT(brightnessPct, contrastPct);
    for (let i = 0; i < data.length; i += 4) {
      data[i] = lut[data[i]];
      data[i + 1] = lut[data[i + 1]];
      data[i + 2] = lut[data[i + 2]];
    }
    return data;
  }

  function applyBrightnessContrastToCanvas(canvas, brightnessPct, contrastPct) {
    if (!canvas || (Number(brightnessPct) === 100 && Number(contrastPct) === 100)) return canvas;
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height);
    applyBrightnessContrastToPixels(imageData.data, brightnessPct, contrastPct);
    ctx.putImageData(imageData, 0, 0);
    return canvas;
  }

  const api = {
    makeBrightnessContrastLUT,
    applyBrightnessContrastToPixels,
    applyBrightnessContrastToCanvas,
  };
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
  root.FilehubPhotoAdjust = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);

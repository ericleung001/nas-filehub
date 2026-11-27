'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const adjust = require(path.join(__dirname, '..', 'web', 'static', 'photo-adjust.js'));

test('identity LUT leaves every level unchanged', () => {
  const lut = adjust.makeBrightnessContrastLUT(100, 100);
  for (let i = 0; i < 256; i++) assert.equal(lut[i], i);
});

test('brightness 0 maps every level to black', () => {
  const lut = adjust.makeBrightnessContrastLUT(0, 100);
  for (let i = 0; i < 256; i++) assert.equal(lut[i], 0);
});

test('brightness 200% doubles then clamps', () => {
  const lut = adjust.makeBrightnessContrastLUT(200, 100);
  assert.equal(lut[0], 0);
  assert.equal(lut[10], 20);
  assert.equal(lut[100], 200);
  assert.equal(lut[200], 255);
});

test('contrast 0% collapses to mid grey', () => {
  const lut = adjust.makeBrightnessContrastLUT(100, 0);
  for (let i = 0; i < 256; i++) assert.equal(lut[i], 128);
});

test('brightness then contrast are applied in CSS filter order', () => {
  // brightness 50% then contrast 200%: out = round((i * 0.5) * 2 + 127.5 * (1 - 2))
  // = round(i - 127.5)
  const lut = adjust.makeBrightnessContrastLUT(50, 200);
  assert.equal(lut[0], 0);
  assert.equal(lut[127], 0); // 127 - 127.5 = -0.5 -> 0
  assert.equal(lut[128], 1); // 128 - 127.5 = 0.5 -> 1
  assert.equal(lut[200], 73); // 200 - 127.5 = 72.5 -> 73
  assert.equal(lut[255], 128); // 255 - 127.5 = 127.5 -> 128
});

test('pixel bake changes RGB and leaves alpha alone', () => {
  const data = new Uint8ClampedArray([40, 80, 120, 200, 255, 0, 10, 1]);
  adjust.applyBrightnessContrastToPixels(data, 150, 100);
  assert.equal(data[0], 60);
  assert.equal(data[1], 120);
  assert.equal(data[2], 180);
  assert.equal(data[3], 200);
  assert.equal(data[4], 255);
  assert.equal(data[5], 0);
  assert.equal(data[6], 15);
  assert.equal(data[7], 1);
});

test('100/100 bake is a no-op on the buffer', () => {
  const data = new Uint8ClampedArray([9, 8, 7, 6]);
  adjust.applyBrightnessContrastToPixels(data, 100, 100);
  assert.deepEqual([...data], [9, 8, 7, 6]);
});

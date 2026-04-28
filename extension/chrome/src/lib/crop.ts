import type { Rect } from "./messages";

// cropToBase64 takes the dataURL returned by tabs.captureVisibleTab and a rect
// in CSS pixels (with the source page's devicePixelRatio) and returns a base64
// PNG of the cropped region, ready for POST /clip's imageBase64 field.
export async function cropToBase64(dataURL: string, rect: Rect): Promise<string> {
  const blob = await (await fetch(dataURL)).blob();
  const bmp = await createImageBitmap(blob);

  const w = Math.max(1, Math.round(rect.w * rect.dpr));
  const h = Math.max(1, Math.round(rect.h * rect.dpr));
  const sx = Math.max(0, Math.round(rect.x * rect.dpr));
  const sy = Math.max(0, Math.round(rect.y * rect.dpr));

  const canvas = new OffscreenCanvas(w, h);
  const ctx = canvas.getContext("2d");
  if (!ctx) {
    bmp.close();
    throw new Error("OffscreenCanvas 2d context unavailable");
  }
  ctx.drawImage(bmp, sx, sy, w, h, 0, 0, w, h);
  bmp.close();

  const out = await canvas.convertToBlob({ type: "image/png" });
  return blobToBase64(out);
}

async function blobToBase64(blob: Blob): Promise<string> {
  const buf = new Uint8Array(await blob.arrayBuffer());
  // Chunked to keep the .apply argument list under JS engine limits for big
  // images.
  const CHUNK = 0x8000;
  let bin = "";
  for (let i = 0; i < buf.length; i += CHUNK) {
    bin += String.fromCharCode.apply(null, Array.from(buf.subarray(i, i + CHUNK)));
  }
  return btoa(bin);
}
